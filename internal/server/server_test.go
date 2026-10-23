package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/org"
	"github.com/ANetResearch/anet-org/internal/store"
)

const token = "test-token-0123456789abcdef"

func newServer(t *testing.T, mut func(*config.Config)) *Server {
	t.Helper()
	raw := `{"org":{"name":"Example Lab","prefix":"LAB"},"socket":"/tmp/x/b.sock","token_file":"/tmp/x/t",
		"database":"` + filepath.Join(t.TempDir(), "org.db") + `","admins":["aid-admin-0000000001"]}`
	cfg, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if mut != nil {
		mut(cfg)
	}
	db, err := store.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(org.New(cfg, db, org.Options{}), token, 4, nil)
}

func do(t *testing.T, s *Server, method, path, auth, caller, body string, hdr map[string]string) (int, map[string]any, http.Header) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if caller != "" {
		req.Header.Set(HeaderCaller, caller)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m, rec.Header()
}

const bearer = "Bearer " + token

func TestTokenCheckedBeforeRouting(t *testing.T) {
	s := newServer(t, nil)
	for _, auth := range []string{"", "Bearer", "Bearer ", "Bearer wrong-token-0123456789abcdef", "Basic " + token, bearer + "x", "bearer " + token} {
		for _, path := range []string{"/v1/org.info", "/v1/no.such", "/other"} {
			code, m, _ := do(t, s, "POST", path, auth, "aid-eve-00000000001", "{}", nil)
			if code != 401 || m["error"] != "unauthorized" {
				t.Fatalf("auth %q path %s: %d %v", auth, path, code, m)
			}
		}
	}
	code, m, _ := do(t, s, "POST", "/v1/org.info", bearer, "aid-eve-00000000001", "{}", nil)
	if code != 200 || m["name"] != "Example Lab" {
		t.Fatalf("good token: %d %v", code, m)
	}
}

func TestCallerRequired(t *testing.T) {
	s := newServer(t, nil)
	code, m, _ := do(t, s, "POST", "/v1/org.info", bearer, "", "{}", nil)
	if code != 403 || m["error"] != "caller_required" {
		t.Fatalf("missing caller: %d %v", code, m)
	}
	code, m, _ = do(t, s, "POST", "/v1/org.info", bearer, "short", "{}", nil)
	if code != 400 || m["error"] != "bad_caller" {
		t.Fatalf("bad caller: %d %v", code, m)
	}
	l := newServer(t, func(c *config.Config) { c.AllowLocalAdmin = true })
	code, m, _ = do(t, l, "POST", "/v1/admin.invite.create", bearer, "", "{}", nil)
	if code != 200 || !strings.HasPrefix(m["code"].(string), "LAB-") {
		t.Fatalf("local admin: %d %v", code, m)
	}
}

func TestRoutingAndBody(t *testing.T) {
	s := newServer(t, nil)
	eve := "aid-eve-00000000001"
	cases := []struct {
		method, path, body string
		hdr                map[string]string
		status             int
		code               string
	}{
		{"GET", "/v1/org.info", "", nil, 404, "unknown_capability"},
		{"POST", "/v1/no.such", "{}", nil, 404, "unknown_capability"},
		{"POST", "/v1/org.info", "{}", map[string]string{HeaderCapability: "org.join"}, 400, "capability_mismatch"},
		{"POST", "/v1/org.info", "[1]", nil, 400, "bad_json"},
		{"POST", "/v1/org.info", "{", nil, 400, "bad_json"},
		{"POST", "/v1/org.join", `{"args":{"code":"x"},"files":[{"name":"a","mime":"text/plain","data_b64":""}]}`, nil, 400, "files_not_accepted"},
		{"POST", "/v1/org.join", strings.Repeat(" ", MaxBodyBytes+1), nil, 400, "too_long"},
		{"POST", "/v1/msg.inbox", "{}", nil, 403, "not_a_member"},
		{"POST", "/v1/org.join", `{"code":"LAB-0000-0000","handle":"eve"}`, nil, 404, "invite_unknown"},
		{"POST", "/v1/org.join", `{"code":"LAB-0000-0000","handle":"eve"}`, map[string]string{HeaderVia: "agent"}, 403, "agent_not_permitted"},
		{"POST", "/v1/org.info", "{}", map[string]string{HeaderVia: "robot"}, 400, "bad_via"},
		{"POST", "/v1/org.admit", "{}", map[string]string{HeaderVia: "agent"}, 403, "agent_not_permitted"},
		{"POST", "/v1/ask", `{"text":"hello"}`, nil, 403, "not_a_member"},
	}
	for _, c := range cases {
		code, m, _ := do(t, s, c.method, c.path, bearer, eve, c.body, c.hdr)
		if code != c.status || m["error"] != c.code {
			t.Errorf("%s %s %q: %d %v, want %d %s", c.method, c.path, c.body, code, m, c.status, c.code)
		}
		if code >= 400 && (code == 429 || code == 503) {
			t.Errorf("%s: status %d loses the code", c.path, code)
		}
	}
	code, m, _ := do(t, s, "POST", "/v1/org.admit", bearer, eve, "{}", nil)
	if code != 200 || len(m) != 1 || m["admit"] != false {
		t.Fatalf("org.admit for a stranger: %d %v", code, m)
	}
	code, m, _ = do(t, s, "POST", "/v1/org.info", bearer, eve, "{}", map[string]string{HeaderVia: "agent"})
	if code != 200 || m["name"] != "Example Lab" {
		t.Fatalf("org.info via agent: %d %v", code, m)
	}
	// Empty body is an empty object.
	code, _, _ = do(t, s, "POST", "/v1/org.info", bearer, eve, "", nil)
	if code != 200 {
		t.Fatalf("empty body: %d", code)
	}
	code, m, _ = do(t, s, "GET", "/healthz", "", "", "", nil)
	if code != 401 {
		t.Fatalf("healthz without the token: %d %v", code, m)
	}
	code, m, _ = do(t, s, "GET", "/healthz", bearer, "", "", nil)
	if code != 200 || m["status"] != "ok" {
		t.Fatalf("healthz: %d %v", code, m)
	}
}

func TestRateLimitedCarriesRetryAfter(t *testing.T) {
	s := newServer(t, func(c *config.Config) { c.Rates["read"] = config.Rate{PerMinute: 1, PerDay: 10} })
	eve := "aid-eve-00000000001"
	do(t, s, "POST", "/v1/org.info", bearer, eve, "{}", nil)
	code, m, h := do(t, s, "POST", "/v1/org.info", bearer, eve, "{}", nil)
	if code != 403 || m["error"] != "rate_limited" || h.Get("Retry-After") == "" {
		t.Fatalf("rate: %d %v %v", code, m, h)
	}
}

func TestBusy(t *testing.T) {
	s := newServer(t, nil)
	for i := 0; i < cap(s.sem); i++ {
		s.sem <- struct{}{}
	}
	code, _, h := do(t, s, "POST", "/v1/org.info", bearer, "aid-eve-00000000001", "{}", nil)
	if code != 503 || h.Get("Retry-After") == "" {
		t.Fatalf("busy: %d", code)
	}
}

func TestPeerCheck(t *testing.T) {
	s := newServer(t, nil)
	s.SetPeerUIDs([]int{os.Getuid() + 1})
	req := httptest.NewRequest("POST", "/v1/org.info", strings.NewReader("{}"))
	req = req.WithContext(context.WithValue(req.Context(), peerKey, os.Getuid()))
	req.Header.Set("Authorization", bearer)
	req.Header.Set(HeaderCaller, "aid-eve-00000000001")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("foreign uid admitted: %d", rec.Code)
	}
}

func TestListenOverUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "anet-org-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "run", "b.sock")
	s := newServer(t, nil)
	s.SetPeerUIDs([]int{os.Getuid()})
	l, err := Listen(sock, "")
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(sock)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", fi.Mode().Perm())
	}
	hs := s.HTTPServer()
	go hs.Serve(l)
	defer hs.Close()
	if _, err := Listen(sock, ""); err == nil || !strings.Contains(err.Error(), "another process") {
		t.Fatalf("second listen: %v", err)
	}
	cli := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}, Timeout: 5 * time.Second}
	req, _ := http.NewRequest("POST", "http://localhost/v1/org.info", strings.NewReader("{}"))
	req.Header.Set("Authorization", bearer)
	req.Header.Set(HeaderCaller, "aid-eve-00000000001")
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("over the socket: %d", resp.StatusCode)
	}
	// A world-writable socket directory is refused.
	open := filepath.Join(dir, "open")
	os.Mkdir(open, 0o777)
	os.Chmod(open, 0o777)
	if _, err := Listen(filepath.Join(open, "b.sock"), ""); err == nil {
		t.Fatal("world-writable directory accepted")
	}
}

func FuzzDecodeBody(f *testing.F) {
	for _, s := range []string{"", "{}", `{"code":"LAB-1234-5678"}`, `{"args":{},"files":[]}`, "[]", "{", "null", `{"a":` + strings.Repeat("[", 100)} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		raw, err := DecodeBody(b)
		if err != nil {
			if err.Status < 400 || err.Status >= 500 || err.Code == "" {
				t.Fatalf("bad error %+v", err)
			}
			return
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			t.Fatalf("accepted non-object %q", raw)
		}
	})
}
