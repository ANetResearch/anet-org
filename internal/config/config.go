// Package config reads the anet-org configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ANetResearch/anet-org/internal/invite"
)

// Config is the whole file. Every key is described in docs/configuration.rst.
type Config struct {
	Org Org `json:"org"`

	// Socket is the path of the Unix socket the server listens on.
	Socket string `json:"socket"`
	// SocketGroup, when set, gives the socket to this group with mode 0660;
	// otherwise the socket is 0600 and only this account can connect.
	SocketGroup string `json:"socket_group,omitempty"`
	// PeerUsers lists further accounts (names or uids) whose processes may
	// connect to the socket. This account is always allowed.
	PeerUsers []string `json:"peer_users,omitempty"`
	// TokenFile holds the bearer token the anet daemon presents (the same
	// file as the node's modules.service token_file).
	TokenFile string `json:"token_file"`
	// Database is the SQLite file.
	Database string `json:"database"`

	// Admins are the AIDs that may call admin.* capabilities.
	Admins []string `json:"admins,omitempty"`
	// Staff are AIDs treated as staff (moderators) whatever their stored kind.
	Staff []string `json:"staff,omitempty"`

	// AllowLocalAdmin admits calls without X-ANet-Caller (a local call
	// through the daemon, or a direct call with the token) as the operator,
	// for admin.* and org.info only. Meant for tests; off by default.
	AllowLocalAdmin bool `json:"allow_local_admin,omitempty"`

	// MailboxMax is the number of unread messages a mailbox holds. A full
	// mailbox refuses new messages (mailbox_full); nothing is evicted.
	MailboxMax int `json:"mailbox_max,omitempty"`
	// MailboxPerSender bounds the unread messages one sender may have in one
	// recipient's mailbox (too_many_unread).
	MailboxPerSender int `json:"mailbox_per_sender,omitempty"`
	// FailedJoinsPerCaller and FailedJoinsGlobal bound failed org.join
	// attempts in a ten-minute window, per caller and for everyone.
	FailedJoinsPerCaller int `json:"failed_joins_per_caller,omitempty"`
	FailedJoinsGlobal    int `json:"failed_joins_global,omitempty"`
	// GlobalDaily caps the calls of a family by all members together per UTC
	// day: "msg" (msg.send) and "write" (board and research writes).
	GlobalDaily map[string]int `json:"global_daily,omitempty"`
	// MemberContentBytes bounds what one member has stored on the board and
	// in the research space (titles, bodies, comments, notes).
	MemberContentBytes int64 `json:"member_content_bytes,omitempty"`
	// Rates overrides the per-member rate limits by capability family.
	Rates map[string]Rate `json:"rates,omitempty"`
	// JoinsPerMinutePerCode bounds joins with one code.
	JoinsPerMinutePerCode int `json:"joins_per_minute_per_code,omitempty"`
	// MaxConcurrent bounds the calls the server handles at once.
	MaxConcurrent int `json:"max_concurrent,omitempty"`
	// AgentProfileFile, when set, is where the server writes the settings
	// of the organisation's agent (admin.settings.set) as JSON, mode 0640,
	// for the account that runs the agent to read before each run.
	AgentProfileFile string `json:"agent_profile_file,omitempty"`

	// OpenJoin, when enabled, lets an identity join without an invite code
	// once the hub has known it for long enough (docs/configuration.rst).
	OpenJoin OpenJoin `json:"open_join,omitempty"`
}

// OpenJoin admits members without an invite code. The server asks the hub
// for the caller's registration (GET <hub>/agents/<aid>) and admits an
// identity that the hub has known for at least MinHubAgeHours.
type OpenJoin struct {
	Enabled bool `json:"enabled"`
	// Hub is the https URL of the hub whose registrations count.
	Hub string `json:"hub,omitempty"`
	// HubAddress, when set, is the host:port the server connects to for the
	// hub, instead of resolving the hub's name; TLS is still checked against
	// the name in Hub. It lets the server run with network access to the
	// hub's address only.
	HubAddress string `json:"hub_address,omitempty"`
	// MinHubAgeHours is the least time since the hub first registered the
	// identity (default 72, the hub's probation).
	MinHubAgeHours int `json:"min_hub_age_hours,omitempty"`
	// DailyJoins caps the joins without a code per UTC day (default 300),
	// HourlyJoins per clock hour (default 30), so that a burst cannot take a
	// whole day's places. Only successful joins count.
	DailyJoins  int `json:"daily_joins,omitempty"`
	HourlyJoins int `json:"hourly_joins,omitempty"`
}

// Org describes the organisation.
type Org struct {
	Name             string `json:"name"`
	Prefix           string `json:"prefix"`
	Description      string `json:"description,omitempty"`
	JoinInstructions string `json:"join_instructions,omitempty"`
	Contact          string `json:"contact,omitempty"`
}

// Rate is a pair of limits for one family.
type Rate struct {
	PerMinute int `json:"per_minute"`
	PerDay    int `json:"per_day"`
}

// Defaults.
const (
	DefaultMailboxMax        = 500
	DefaultMailboxPerSender  = 50
	DefaultFailedJoinsCaller = 10
	DefaultFailedJoinsGlobal = 300
	DefaultContentBytes      = 5 << 20
	DefaultJoinsPerCode      = 30
	DefaultMaxConcurrent     = 64
	DefaultMinHubAgeHours    = 72
	DefaultOpenDailyJoins    = 300
	DefaultOpenHourlyJoins   = 30
)

// DefaultRates are the per-member limits of each capability family.
var DefaultRates = map[string]Rate{
	"join":     {PerMinute: 10, PerDay: 50},
	"read":     {PerMinute: 120, PerDay: 5000},
	"msg":      {PerMinute: 20, PerDay: 300},
	"write":    {PerMinute: 30, PerDay: 1000},
	"ack":      {PerMinute: 60, PerDay: 5000},
	"ask":      {PerMinute: 6, PerDay: 100},
	"admit":    {PerMinute: 600, PerDay: 50000},
	"admin":    {PerMinute: 60, PerDay: 2000},
	"identity": {PerMinute: 10, PerDay: 50},
	"report":   {PerMinute: 3, PerDay: 20},
}

// DefaultGlobalDaily are the organisation-wide daily caps.
var DefaultGlobalDaily = map[string]int{"msg": 20000, "write": 20000}

// maxConfigBytes bounds the configuration file.
const maxConfigBytes = 1 << 20

// Load reads, defaults and checks a configuration file.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("config %s: larger than %d bytes", path, maxConfigBytes)
	}
	return Parse(raw)
}

// Parse decodes a configuration, applies defaults and checks it.
func Parse(raw []byte) (*Config, error) {
	if err := checkRemoved(raw); err != nil {
		return nil, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if dec.More() {
		return nil, errors.New("config: trailing data after the JSON object")
	}
	c.applyDefaults()
	if err := c.check(); err != nil {
		return nil, err
	}
	return &c, nil
}

// removedKeys are top-level keys of earlier versions that this one no
// longer reads. A file that still has one is refused with what to do, so
// that an operator does not believe a removed setting still applies.
var removedKeys = map[string]string{
	"assistant": "the built-in assistant was removed in 0.3.9: the organisation's agent, run by the node, answers requests now. " +
		"Delete the assistant block; the model key files belong to the node's auto_reply.model_proxy (see anet-org gen-config)",
}

func checkRemoved(raw []byte) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return nil // the strict decode reports the problem
	}
	for k, why := range removedKeys {
		if _, ok := top[k]; ok {
			return fmt.Errorf("config: %s: %s", k, why)
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	c.Org.Prefix = strings.ToUpper(strings.TrimSpace(c.Org.Prefix))
	if c.MailboxMax <= 0 {
		c.MailboxMax = DefaultMailboxMax
	}
	setDefault(&c.MailboxPerSender, DefaultMailboxPerSender)
	setDefault(&c.FailedJoinsPerCaller, DefaultFailedJoinsCaller)
	setDefault(&c.FailedJoinsGlobal, DefaultFailedJoinsGlobal)
	if c.MemberContentBytes <= 0 {
		c.MemberContentBytes = DefaultContentBytes
	}
	gd := map[string]int{}
	for k, v := range DefaultGlobalDaily {
		gd[k] = v
	}
	for k, v := range c.GlobalDaily {
		gd[k] = v
	}
	c.GlobalDaily = gd
	if c.JoinsPerMinutePerCode <= 0 {
		c.JoinsPerMinutePerCode = DefaultJoinsPerCode
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = DefaultMaxConcurrent
	}
	if c.OpenJoin.Enabled {
		setDefault(&c.OpenJoin.MinHubAgeHours, DefaultMinHubAgeHours)
		setDefault(&c.OpenJoin.DailyJoins, DefaultOpenDailyJoins)
		setDefault(&c.OpenJoin.HourlyJoins, DefaultOpenHourlyJoins)
	}
	rates := map[string]Rate{}
	for k, v := range DefaultRates {
		rates[k] = v
	}
	for k, v := range c.Rates {
		rates[k] = v
	}
	c.Rates = rates
}

func setDefault(p *int, def int) {
	if *p <= 0 {
		*p = def
	}
}

func (c *Config) check() error {
	if strings.TrimSpace(c.Org.Name) == "" {
		return errors.New("config: org.name is required")
	}
	if len(c.Org.Name) > 64 {
		return errors.New("config: org.name is longer than 64 bytes")
	}
	if !invite.ValidPrefix(c.Org.Prefix) {
		return fmt.Errorf("config: org.prefix %q must be 2 to 10 letters and digits, starting with a letter", c.Org.Prefix)
	}
	if len(c.Org.Description) > 1000 || len(c.Org.JoinInstructions) > 1000 || len(c.Org.Contact) > 200 {
		return errors.New("config: org.description and org.join_instructions are limited to 1000 bytes, org.contact to 200")
	}
	if c.Socket == "" || !(filepath.IsAbs(c.Socket) || strings.HasPrefix(c.Socket, "$")) {
		return errors.New("config: socket must be an absolute path (it may start with a variable such as ${XDG_RUNTIME_DIR})")
	}
	if len(c.Socket) > 100 {
		return errors.New("config: socket path is longer than 100 bytes")
	}
	if c.TokenFile == "" {
		return errors.New("config: token_file is required; the server refuses to run without a token")
	}
	if c.Database == "" {
		return errors.New("config: database is required")
	}
	if c.AgentProfileFile != "" && !filepath.IsAbs(c.AgentProfileFile) {
		return errors.New("config: agent_profile_file must be an absolute path")
	}
	for _, list := range [][]string{c.Admins, c.Staff} {
		for _, a := range list {
			if !ValidAID(a) {
				return fmt.Errorf("config: %q is not an AID", a)
			}
		}
	}
	for k, r := range c.Rates {
		if _, ok := DefaultRates[k]; !ok {
			return fmt.Errorf("config: rates: unknown family %q", k)
		}
		if r.PerMinute <= 0 || r.PerDay <= 0 {
			return fmt.Errorf("config: rates.%s: per_minute and per_day must be positive", k)
		}
	}
	for k, v := range c.GlobalDaily {
		if _, ok := DefaultGlobalDaily[k]; !ok {
			return fmt.Errorf("config: global_daily: unknown family %q (msg or write)", k)
		}
		if v <= 0 {
			return fmt.Errorf("config: global_daily.%s must be positive", k)
		}
	}
	if c.MailboxPerSender > c.MailboxMax {
		c.MailboxPerSender = c.MailboxMax
	}
	if c.MailboxMax > 100000 {
		return errors.New("config: mailbox_max is above 100000")
	}
	if c.JoinsPerMinutePerCode > 1000 {
		return errors.New("config: joins_per_minute_per_code is above 1000")
	}
	if oj := c.OpenJoin; oj.Enabled {
		u, err := url.Parse(oj.Hub)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return errors.New("config: open_join.hub must be the https URL of a hub, such as https://hub.example.org")
		}
		if p, ok := strings.CutPrefix(oj.HubAddress, "unix:"); ok {
			if !filepath.IsAbs(p) || len(p) > 100 {
				return errors.New("config: open_join.hub_address unix:<path> needs an absolute path of at most 100 bytes")
			}
		} else if oj.HubAddress != "" {
			if _, port, err := net.SplitHostPort(oj.HubAddress); err != nil || port == "" {
				return errors.New("config: open_join.hub_address must be host:port or unix:<path>")
			}
		}
		if oj.MinHubAgeHours > 24*365 {
			return errors.New("config: open_join.min_hub_age_hours is above one year")
		}
		if oj.DailyJoins > 100000 || oj.HourlyJoins > 100000 {
			return errors.New("config: open_join.daily_joins and hourly_joins are limited to 100000")
		}
	}
	return nil
}

// ValidAID is a loose syntax check for an AID: 8 to 256 printable ASCII
// characters without spaces or '#'.
func ValidAID(s string) bool {
	if len(s) < 8 || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c > '~' || c == '#' {
			return false
		}
	}
	return true
}

// ExpandPath expands $VAR and ${VAR} and requires an absolute result. An
// unset variable is an error.
func ExpandPath(raw string) (string, error) {
	var unset []string
	p := os.Expand(raw, func(k string) string {
		v, ok := os.LookupEnv(k)
		if !ok || v == "" {
			unset = append(unset, k)
		}
		return v
	})
	if len(unset) > 0 {
		return "", fmt.Errorf("%q: %s is not set", raw, strings.Join(unset, ", "))
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q must be an absolute path", raw)
	}
	return p, nil
}

// ReadSecret reads the first line of a secret file. The file must be a
// regular file that other users cannot access, and the secret at least min
// bytes without whitespace. Any group or other permission bit is refused.
func ReadSecret(raw string, min int) (string, error) {
	p, err := ExpandPath(raw)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", p)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is accessible to its group or other users (mode %04o); chmod 0600", p, fi.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", err
	}
	s := string(b)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) < min {
		return "", fmt.Errorf("%s: the secret is %d bytes, at least %d required", p, len(s), min)
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return "", fmt.Errorf("%s: the secret contains whitespace or control characters", p)
		}
	}
	return s, nil
}
