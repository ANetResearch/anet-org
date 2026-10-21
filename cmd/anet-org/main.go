// Command anet-org is the organisation agent that runs behind an anet node.
//
//	anet-org serve      --config FILE
//	anet-org gen-config --config FILE [--token-file PATH] [--public-admin]
//	anet-org gen-token  PATH
//	anet-org admin      --config FILE invite create [--count N] [--expires-days N] [--uses N] [--kind K] [--note TEXT]
//	anet-org admin      --config FILE invite list [--all]
//	anet-org admin      --config FILE invite revoke ID [--remove-joined-after TIME]
//	anet-org admin      --config FILE member list [--query Q]
//	anet-org admin      --config FILE member remove HANDLE [--ban]
//	anet-org admin      --config FILE member kind HANDLE student|service|staff
//	anet-org backup     --config FILE [--replace] PATH
//	anet-org version
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ANetResearch/anet-org/internal/assistant"
	"github.com/ANetResearch/anet-org/internal/catalog"
	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/org"
	"github.com/ANetResearch/anet-org/internal/server"
	"github.com/ANetResearch/anet-org/internal/store"
)

const usage = `anet-org: the organisation agent behind an anet node.

Usage:
  anet-org serve      --config FILE
  anet-org gen-config --config FILE [--token-file PATH] [--public-admin]
  anet-org gen-token  PATH
  anet-org admin      --config FILE invite create [--count N] [--expires-days N] [--uses N] [--kind K] [--note TEXT]
  anet-org admin      --config FILE invite list [--all]
  anet-org admin      --config FILE invite revoke ID [--remove-joined-after RFC3339]
  anet-org admin      --config FILE member list [--query Q]
  anet-org admin      --config FILE member remove HANDLE [--ban]
  anet-org admin      --config FILE member kind HANDLE student|service|staff
  anet-org backup     --config FILE [--replace] PATH
  anet-org version

The configuration file may also be named by ANET_ORG_CONFIG.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "gen-config":
		err = cmdGenConfig(os.Args[2:], os.Stdout)
	case "gen-token":
		err = cmdGenToken(os.Args[2:])
	case "admin":
		err = cmdAdmin(os.Args[2:], os.Stdout)
	case "backup":
		err = cmdBackup(os.Args[2:])
	case "version", "--version":
		fmt.Println("anet-org", server.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "anet-org: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "anet-org:", err)
		os.Exit(1)
	}
}

type usageErr string

func (u usageErr) Error() string { return string(u) }

// flags makes a flag set with --config.
func flags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg := fs.String("config", os.Getenv("ANET_ORG_CONFIG"), "configuration file")
	return fs, cfg
}

// parse parses flags that may appear before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, usageErr(fmt.Sprintf("%s: %v", fs.Name(), err))
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func load(path string) (*config.Config, error) {
	if path == "" {
		return nil, usageErr("--config is required (or set ANET_ORG_CONFIG)")
	}
	return config.Load(path)
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// openService opens the database and builds the service; with assistant it
// also reads the upstream keys.
func openService(cfg *config.Config, log *slog.Logger, withAssistant bool) (*org.Service, *sql.DB, error) {
	dbPath, err := config.ExpandPath(cfg.Database)
	if err != nil {
		return nil, nil, fmt.Errorf("database: %w", err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	var pool *assistant.Pool
	if withAssistant {
		pool, err = buildPool(cfg, log)
		if err != nil {
			db.Close()
			return nil, nil, err
		}
	}
	return org.New(cfg, db, org.Options{Assistant: pool, Logger: log}), db, nil
}

func buildPool(cfg *config.Config, log *slog.Logger) (*assistant.Pool, error) {
	a := cfg.Assistant
	var ups []assistant.UpstreamConfig
	for _, u := range a.Upstreams {
		key, err := config.ReadSecret(u.APIKeyFile, 8)
		if err != nil {
			return nil, fmt.Errorf("assistant upstream %s: api_key_file: %w", u.Name, err)
		}
		ups = append(ups, assistant.UpstreamConfig{Name: u.Name, BaseURL: u.BaseURL, Model: u.Model, APIKey: key,
			MaxConcurrency: u.MaxConcurrency})
	}
	return assistant.NewPool(assistant.PoolConfig{
		Upstreams:     ups,
		Timeout:       time.Duration(a.TimeoutSeconds) * time.Second,
		QueueSize:     a.QueueSize,
		QueueWait:     time.Duration(a.QueueWaitSeconds) * time.Second,
		QuotaCooldown: time.Duration(a.QuotaCooldownSeconds) * time.Second,
		BackoffBase:   time.Duration(a.BackoffBaseSeconds) * time.Second,
		BackoffMax:    time.Duration(a.BackoffMaxSeconds) * time.Second,
		Logger:        log,
	}), nil
}

func cmdServe(args []string) error {
	fs, cfgPath := flags("serve")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := load(*cfgPath)
	if err != nil {
		return err
	}
	log := newLogger()
	token, err := config.ReadSecret(cfg.TokenFile, 16)
	if err != nil {
		return fmt.Errorf("token_file: %w", err)
	}
	svc, db, err := openService(cfg, log, true)
	if err != nil {
		return err
	}
	defer db.Close()
	srv := server.New(svc, token, cfg.MaxConcurrent, log)
	peers := []int{os.Getuid()}
	extra, err := server.ResolveUsers(cfg.PeerUsers)
	if err != nil {
		return err
	}
	srv.SetPeerUIDs(append(peers, extra...))
	if !server.PeerCheckSupported {
		srv.SetPeerUIDs(nil)
		log.Warn("peer credentials cannot be checked on this platform; the socket mode is the only access control")
	}
	sock, err := config.ExpandPath(cfg.Socket)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	l, err := server.Listen(sock, cfg.SocketGroup)
	if err != nil {
		return err
	}
	hs := srv.HTTPServer()
	if cfg.AllowLocalAdmin {
		log.Warn("allow_local_admin is on: calls without a caller are treated as the operator for admin.* and org.info")
	}
	log.Info("serving", "socket", sock, "org", cfg.Org.Name, "version", server.Version,
		"assistant", cfg.Assistant.Configured(), "admins", len(cfg.Admins))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(l) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
		_ = os.Remove(sock)
		log.Info("stopped")
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func cmdGenConfig(args []string, out io.Writer) error {
	fs, cfgPath := flags("gen-config")
	tokenFile := fs.String("token-file", "", "token file path as the anet daemon reads it (default: the config's token_file)")
	publicAdmin := fs.Bool("public-admin", false, "list admin.* as ordinary public capabilities instead of allow_only")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := load(*cfgPath)
	if err != nil {
		return err
	}
	es := catalog.Entries(cfg.Org.Name, cfg.Org.Prefix)
	if err := catalog.Check(es); err != nil {
		return err
	}
	tf := *tokenFile
	if tf == "" {
		tf = cfg.TokenFile
	}
	// The daemon does not expand variables in a URL: the socket path is
	// written as this environment resolves it.
	sock, err := config.ExpandPath(cfg.Socket)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	b := catalog.Blocks(es, catalog.Options{Socket: sock, TokenFile: tf, PublicAdmin: *publicAdmin})
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(b)
}

func cmdGenToken(args []string) error {
	if len(args) != 1 {
		return usageErr("gen-token PATH")
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	tok := base64.RawURLEncoding.EncodeToString(b[:])
	if err := os.MkdirAll(filepath.Dir(args[0]), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(args[0], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, tok); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "A new token was written to %s (mode 0600).\n", args[0])
	return nil
}

func cmdBackup(args []string) error {
	fs, cfgPath := flags("backup")
	replace := fs.Bool("replace", false, "replace an existing file")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErr("backup --config FILE [--replace] PATH")
	}
	cfg, err := load(*cfgPath)
	if err != nil {
		return err
	}
	dbPath, err := config.ExpandPath(cfg.Database)
	if err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Backup(context.Background(), db, pos[0], *replace); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "The database was copied to %s.\n", pos[0])
	return nil
}

func cmdAdmin(args []string, out io.Writer) error {
	fs, cfgPath := flags("admin")
	expires := fs.Int("expires-days", 0, "invite validity in days (default 14, at most 90)")
	uses := fs.Int("uses", 0, "invite uses (default 200)")
	count := fs.Int("count", 1, "number of codes to create (at most 500)")
	kind := fs.String("kind", "", "member kind for an invite: student, service or staff")
	note := fs.String("note", "", "invite note")
	all := fs.Bool("all", false, "include inactive invites")
	removeAfter := fs.String("remove-joined-after", "", "with revoke: remove members who joined with the invite at or after this RFC 3339 time")
	query := fs.String("query", "", "filter by part of a handle")
	ban := fs.Bool("ban", false, "with remove: refuse the identity if it tries to join again")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return usageErr("admin: give a subject and an action, for example: admin --config FILE invite create")
	}
	cfg, err := load(*cfgPath)
	if err != nil {
		return err
	}
	svc, db, err := openService(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)), false)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	const by = "operator"
	emit := func(v any) error {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	switch pos[0] + " " + pos[1] {
	case "invite create":
		codes, vs, err := svc.CreateInvites(ctx, by, org.InviteOptions{Count: *count, ExpiresDays: *expires, Uses: *uses, Kind: *kind, Note: *note})
		if err != nil && len(codes) == 0 {
			return err
		}
		if *asJSON {
			out := map[string]any{"codes": codes, "invites": vs}
			if len(codes) == 1 {
				out["code"], out["invite"] = codes[0], vs[0]
			}
			if eerr := emit(out); eerr != nil {
				return eerr
			}
			return err
		}
		if len(codes) == 1 {
			fmt.Fprintf(out, "Invite code: %s\n", codes[0])
			fmt.Fprintf(out, "Id %d, kind %s, %d uses, expires %s.\n", vs[0].ID, vs[0].Kind, vs[0].MaxUses, vs[0].ExpiresAt)
			fmt.Fprintln(out, "The code is shown only once; only its hash is stored.")
			return err
		}
		fmt.Fprintf(out, "%d invite codes, kind %s, %d uses each, expiring %s:\n", len(codes), vs[0].Kind, vs[0].MaxUses, vs[0].ExpiresAt)
		for _, c := range codes {
			fmt.Fprintln(out, c)
		}
		fmt.Fprintln(out, "The codes are shown only once; only their hashes are stored. Give each person one code.")
		return err
	case "invite list":
		vs, err := svc.ListInvites(ctx, *all)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(vs)
		}
		fmt.Fprintln(tw, "ID\tSTATE\tKIND\tUSES\tEXPIRES\tNOTE")
		for _, v := range vs {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%d/%d\t%s\t%s\n", v.ID, v.State, v.Kind, v.Uses, v.MaxUses, v.ExpiresAt, v.Note)
		}
		return tw.Flush()
	case "invite revoke":
		if len(pos) != 3 {
			return usageErr("admin invite revoke ID [--remove-joined-after TIME]")
		}
		id, err := strconv.ParseInt(pos[2], 10, 64)
		if err != nil || id <= 0 {
			return usageErr("admin invite revoke: ID is a positive number (see invite list)")
		}
		var after *time.Time
		if *removeAfter != "" {
			t, err := time.Parse(time.RFC3339, *removeAfter)
			if err != nil {
				return usageErr("--remove-joined-after takes an RFC 3339 time such as 2026-10-21T08:00:00Z")
			}
			after = &t
		}
		n, err := svc.RevokeInvite(ctx, by, id, after)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Invite %d is revoked. Members removed: %d.\n", id, n)
		return nil
	case "member list":
		ms, err := svc.ListMembers(ctx, *query)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(ms)
		}
		fmt.Fprintln(tw, "HANDLE\tKIND\tJOINED\tINVITE\tAID")
		for _, m := range ms {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", m.Handle, m.Kind, m.JoinedAt, m.InviteID, m.AID)
		}
		return tw.Flush()
	case "member remove":
		if len(pos) != 3 {
			return usageErr("admin member remove HANDLE [--ban]")
		}
		m, err := svc.RemoveMember(ctx, by, pos[2], *ban)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Member %s was removed.", m.Handle)
		if *ban {
			fmt.Fprint(out, " The identity cannot join again.")
		}
		fmt.Fprintln(out)
		return nil
	case "member kind":
		if len(pos) != 4 {
			return usageErr("admin member kind HANDLE student|service|staff")
		}
		if err := svc.SetKind(ctx, pos[2], pos[3]); err != nil {
			return err
		}
		fmt.Fprintf(out, "Member %s is now %s.\n", pos[2], pos[3])
		return nil
	}
	return usageErr("admin: unknown action " + pos[0] + " " + pos[1])
}
