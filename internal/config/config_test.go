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
	if c.Rates["msg"].PerMinute != 20 || c.Rates["admit"].PerMinute == 0 {
		t.Fatal("rate defaults")
	}
}

// A configuration of an earlier version with the assistant block is refused
// with a sentence that says what to do, not merely as an unknown key.
func TestRemovedAssistantBlockRefused(t *testing.T) {
	raw := strings.Replace(minimal, `"database"`, `"assistant":{"upstreams":[
		{"name":"plan","base_url":"https://model.example/v3","api_key_file":"/k/plan"}]},"database"`, 1)
	_, err := Parse([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "removed in 0.3.9") || !strings.Contains(err.Error(), "model_proxy") {
		t.Fatalf("assistant block: %v", err)
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
		strings.Replace(minimal, `"database"`, `"assistant":{},"database"`, 1),
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
