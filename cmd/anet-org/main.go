// Command anet-org is the organisation program that runs behind an anet node.
//
//	anet-org serve      --config FILE
//	anet-org gen-config --config FILE [--token-file PATH] [--public-admin] [agent flags | --no-agent]
//	anet-org gen-agent  --config FILE [--agent hermes] [--model NAME] [--part all|config|prompt]
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
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ANetResearch/anet-org/internal/agentcfg"
	"github.com/ANetResearch/anet-org/internal/catalog"
	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/org"
	"github.com/ANetResearch/anet-org/internal/server"
	"github.com/ANetResearch/anet-org/internal/store"
)

const usage = `anet-org: the organisation program behind an anet node.

Usage:
  anet-org serve      --config FILE
  anet-org gen-config --config FILE [--token-file PATH] [--public-admin] [--no-agent]
                      [--agent hermes] [--admitted direct|sandbox] [--admit-cache-seconds N]
                      [--runner-socket PATH] [--runner-user NAME]
                      [--model-proxy-listen HOST:PORT]
                      [--model-plan-base URL] [--model-plan-key-file PATH]
                      [--model-api-base URL] [--model-api-key-file PATH]
  anet-org gen-agent  --config FILE [--agent hermes] [--model NAME] [--anet-command PATH]
                      [--part all|config|prompt]
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
	case "gen-agent":
		err = cmdGenAgent(os.Args[2:], os.Stdout)
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

// openService opens the database and builds the service.
func openService(cfg *config.Config, log *slog.Logger) (*org.Service, *sql.DB, error) {
	dbPath, err := config.ExpandPath(cfg.Database)
	if err != nil {
		return nil, nil, fmt.Errorf("database: %w", err)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	return org.New(cfg, db, org.Options{Logger: log}), db, nil
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
	svc, db, err := openService(cfg, log)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := svc.WriteProfile(context.Background()); err != nil {
		return fmt.Errorf("agent_profile_file: %w", err)
	}
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
		"admins", len(cfg.Admins))
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

// Defaults of the node blocks for the organisation's agent.
const (
	defaultRunnerSocket = "/run/anet-runner/runner.sock"
	defaultRunnerUser   = "anet-runner"
	defaultProxyListen  = "127.0.0.1:47811"
	defaultPlanBase     = "https://ark.cn-beijing.volces.com/api/plan/v3"
	defaultAPIBase      = "https://ark.cn-beijing.volces.com/api/v3"
	defaultPlanKeyFile  = "${HOME}/.config/anet-org/ark-plan.key"
	defaultAPIKeyFile   = "${HOME}/.config/anet-org/ark-api.key"
)

func cmdGenConfig(args []string, out io.Writer) error {
	fs, cfgPath := flags("gen-config")
	tokenFile := fs.String("token-file", "", "token file path as the anet daemon reads it (default: the config's token_file)")
	publicAdmin := fs.Bool("public-admin", false, "list admin.* as ordinary public capabilities instead of allow_only")
	noAgent := fs.Bool("no-agent", false, "omit inbound.admit and auto_reply (no organisation agent)")
	agent := fs.String("agent", "hermes", "the exec agent the node runs for members")
	admitted := fs.String("admitted", "direct", "how an admitted member's turn runs: direct (through the runner) or sandbox")
	cache := fs.Int("admit-cache-seconds", 30, "how long the node keeps an org.admit answer (1 to 300)")
	runnerSocket := fs.String("runner-socket", defaultRunnerSocket, "the anet runner's socket")
	runnerUser := fs.String("runner-user", defaultRunnerUser, "the account the anet runner runs as")
	listen := fs.String("model-proxy-listen", defaultProxyListen, "loopback address of the node's model proxy")
	planBase := fs.String("model-plan-base", defaultPlanBase, "API base of the upstream plan")
	planKey := fs.String("model-plan-key-file", defaultPlanKeyFile, "key file of the upstream plan, read by the node")
	apiBase := fs.String("model-api-base", defaultAPIBase, "API base of the upstream api")
	apiKey := fs.String("model-api-key-file", defaultAPIKeyFile, "key file of the upstream api, read by the node")
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
	o := catalog.Options{Socket: sock, TokenFile: tf, PublicAdmin: *publicAdmin}
	if !*noAgent {
		a, err := agentBlocks(cfg.Org.Name, *agent, *admitted, *cache, *runnerSocket, *runnerUser, *listen,
			[]catalog.Upstream{{Name: "plan", APIBase: *planBase, APIKeyFile: *planKey}, {Name: "api", APIBase: *apiBase, APIKeyFile: *apiKey}})
		if err != nil {
			return err
		}
		o.Agent = a
	}
	b := catalog.Blocks(es, o)
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(b)
}

// agentBlocks checks the agent flags of gen-config. Paths are written as
// this environment resolves them, like the socket.
func agentBlocks(org, agent, admitted string, cache int, socket, user, listen string, ups []catalog.Upstream) (*catalog.Agent, error) {
	if !knownAgent(agent) {
		return nil, usageErr(fmt.Sprintf("--agent %q: use one of %v", agent, agentcfg.Agents))
	}
	if admitted != "direct" && admitted != "sandbox" {
		return nil, usageErr("--admitted is direct or sandbox")
	}
	if cache < 1 || cache > 300 {
		return nil, usageErr("--admit-cache-seconds is 1 to 300")
	}
	sock, err := config.ExpandPath(socket)
	if err != nil {
		return nil, fmt.Errorf("--runner-socket: %w", err)
	}
	if strings.TrimSpace(user) == "" {
		return nil, usageErr("--runner-user is required")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil || !(host == "127.0.0.1" || host == "::1" || host == "localhost") {
		return nil, usageErr("--model-proxy-listen is a loopback HOST:PORT, for example " + defaultProxyListen)
	}
	for i := range ups {
		u := &ups[i]
		if !strings.HasPrefix(u.APIBase, "https://") {
			return nil, usageErr(fmt.Sprintf("--model-%s-base must be an https URL", u.Name))
		}
		u.APIBase = strings.TrimRight(u.APIBase, "/")
		p, err := config.ExpandPath(u.APIKeyFile)
		if err != nil {
			return nil, fmt.Errorf("--model-%s-key-file: %w", u.Name, err)
		}
		u.APIKeyFile = p
	}
	return &catalog.Agent{Name: agent, Admitted: admitted, AdmitCacheSeconds: cache, RunnerSocket: sock, RunnerUser: user,
		ProxyListen: listen, Upstreams: ups, SystemPrompt: agentcfg.Prompt(org)}, nil
}

func knownAgent(name string) bool {
	for _, a := range agentcfg.Agents {
		if a == name {
			return true
		}
	}
	return false
}

var modelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,127}$`)

func cmdGenAgent(args []string, out io.Writer) error {
	fs, cfgPath := flags("gen-agent")
	agent := fs.String("agent", "hermes", "the agent program to configure")
	model := fs.String("model", agentcfg.DefaultModel, "the model the agent asks for")
	anetCmd := fs.String("anet-command", "anet", "how the runner account starts anet (for anet turn-mcp)")
	part := fs.String("part", "all", "what to print: all, config (the agent's configuration) or prompt (the system prompt)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if !knownAgent(*agent) {
		return usageErr(fmt.Sprintf("--agent %q: use one of %v", *agent, agentcfg.Agents))
	}
	if !modelNameRe.MatchString(*model) {
		return usageErr("--model: a model name has 1 to 128 letters, digits and ._:/@+-")
	}
	if strings.ContainsAny(*anetCmd, "\"\n\r") || strings.TrimSpace(*anetCmd) == "" {
		return usageErr("--anet-command: a command name or an absolute path")
	}
	cfg, err := load(*cfgPath)
	if err != nil {
		return err
	}
	conf := agentcfg.Hermes(agentcfg.HermesOptions{Model: *model, AnetCommand: *anetCmd})
	prompt := agentcfg.Prompt(cfg.Org.Name)
	switch *part {
	case "config":
		_, err = io.WriteString(out, conf)
	case "prompt":
		_, err = io.WriteString(out, prompt+"\n")
	case "all":
		_, err = fmt.Fprintf(out, "%s\n# System prompt of the organisation agent. anet-org gen-config writes the same text into\n"+
			"# the node's auto_reply.system_prompt, and the node passes it with every run; it is not part\n"+
			"# of config.yaml. Print it alone with --part prompt.\n#\n%s\n", conf, commentLines(prompt))
	default:
		return usageErr("--part is all, config or prompt")
	}
	return err
}

func commentLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = "#"
		} else {
			lines[i] = "# " + l
		}
	}
	return strings.Join(lines, "\n")
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
	svc, db, err := openService(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
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
