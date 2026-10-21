package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimal = `{"org":{"name":"Example Lab","prefix":"lab"},"socket":"/run/x/b.sock","token_file":"/etc/x/token","database":"/var/lib/x/org.db"}`

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if c.Org.Prefix != "LAB" || c.MailboxMax != DefaultMailboxMax || c.JoinsPerMinutePerCode != 30 {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Rates["msg"].PerMinute != 20 || c.Assistant.Configured() {
		t.Fatal("rates or assistant defaults")
	}
	if c.Assistant.MemberBudget() != 2_000_000 || c.Assistant.GlobalBudget() != 200_000_000 {
		t.Fatal("budget defaults")
	}
}

func TestBudgetZeroIsUnlimitedAndUpstreams(t *testing.T) {
	raw := strings.Replace(minimal, `"database"`, `"assistant":{"member_daily_tokens":0,"upstreams":[
		{"name":"plan","base_url":"https://ark.example/api/plan/v3/","api_key_file":"/k/plan"},
		{"name":"api","base_url":"https://ark.example/api/v3","api_key_file":"/k/api","max_concurrency":3}]},"database"`, 1)
	c, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if c.Assistant.MemberBudget() != 0 || c.Assistant.GlobalBudget() != DefaultGlobalDailyTokens {
		t.Fatal("budgets")
	}
	u := c.Assistant.Upstreams
	if u[0].BaseURL != "https://ark.example/api/plan/v3" || u[0].Model != DefaultModel || u[0].MaxConcurrency != 8 || u[1].MaxConcurrency != 3 {
		t.Fatalf("upstreams: %+v", u)
	}
}

func TestRejects(t *testing.T) {
	bad := []string{
		`{}`,
		strings.Replace(minimal, `"lab"`, `"l"`, 1),
		strings.Replace(minimal, `"/run/x/b.sock"`, `"relative.sock"`, 1),
		strings.Replace(minimal, `"token_file":"/etc/x/token",`, ``, 1),
		strings.Replace(minimal, `"database"`, `"unknown_key":1,"database"`, 1),
		strings.Replace(minimal, `"database"`, `"admins":["short"],"database"`, 1),
		strings.Replace(minimal, `"database"`, `"rates":{"nope":{"per_minute":1,"per_day":1}},"database"`, 1),
		strings.Replace(minimal, `"database"`, `"assistant":{"upstreams":[{"name":"a","base_url":"http://example.com","api_key_file":"/k"}]},"database"`, 1),
		strings.Replace(minimal, `"database"`, `"assistant":{"upstreams":[{"name":"a","base_url":"https://example.com"}]},"database"`, 1),
		strings.Replace(minimal, `"database"`, `"assistant":{"global_daily_tokens":-1},"database"`, 1),
		minimal + `{}`,
	}
	for _, b := range bad {
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("accepted: %s", b)
		}
	}
}

func TestReadSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	os.WriteFile(p, []byte("abcdefghijklmnopqrstuvwxyz\nsecond line\n"), 0o600)
	s, err := ReadSecret(p, 16)
	if err != nil || s != "abcdefghijklmnopqrstuvwxyz" {
		t.Fatalf("%q %v", s, err)
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o610} {
		os.Chmod(p, mode)
		if _, err := ReadSecret(p, 16); err == nil {
			t.Fatalf("secret with mode %04o accepted", mode)
		}
	}
	os.WriteFile(p, []byte("short"), 0o600)
	os.Chmod(p, 0o600)
	if _, err := ReadSecret(p, 16); err == nil {
		t.Fatal("short secret accepted")
	}
	t.Setenv("ORG_TEST_DIR", dir)
	os.WriteFile(p, []byte("abcdefghijklmnopqrstuvwxyz"), 0o600)
	if _, err := ReadSecret("${ORG_TEST_DIR}/tok", 16); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecret("${ORG_UNSET_VAR}/tok", 16); err == nil {
		t.Fatal("unset variable accepted")
	}
}
