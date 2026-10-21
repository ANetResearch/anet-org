package org

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ANetResearch/anet-org/internal/assistant"
	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/store"
)

const (
	aidAdmin = "aid-admin-0000000001"
	aidAlice = "aid-alice-0000000001"
	aidBob   = "aid-bob-00000000001"
	aidCarol = "aid-carol-0000000001"
	aidEve   = "aid-eve-00000000001"
	aidStaff = "aid-staff-0000000001"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t   testing.TB
	svc *Service
	cfg *config.Config
	clk *clock
}

func baseConfig(t testing.TB) *config.Config {
	raw := `{"org":{"name":"Example Lab","prefix":"LAB"},"socket":"/tmp/x/backend.sock","token_file":"/tmp/x/token",
		"database":"` + filepath.Join(t.TempDir(), "org.db") + `","admins":["` + aidAdmin + `"],"staff":["` + aidStaff + `"]}`
	cfg, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newEnv(t testing.TB, mut func(*config.Config), pool *assistant.Pool) *env {
	t.Helper()
	cfg := baseConfig(t)
	if mut != nil {
		mut(cfg)
	}
	db, err := store.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &clock{t: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	svc := New(cfg, db, Options{Now: clk.now, Assistant: pool})
	return &env{t: t, svc: svc, cfg: cfg, clk: clk}
}

// call dispatches as aid ("" for a local call) and returns the reply as a
// generic map, or the error.
func (e *env) call(aid, capID string, args any) (map[string]any, *Error) {
	e.t.Helper()
	var raw []byte
	switch v := args.(type) {
	case string:
		raw = []byte(v)
	case nil:
		raw = []byte("{}")
	default:
		var err error
		raw, err = json.Marshal(v)
		if err != nil {
			e.t.Fatal(err)
		}
	}
	c := Caller{AID: aid, Local: aid == ""}
	res, err := e.svc.Dispatch(context.Background(), capID, c, raw)
	if err != nil {
		oe := AsError(err)
		if oe == nil {
			e.t.Fatalf("%s: non-coded error %v", capID, err)
		}
		return nil, oe
	}
	b, err := json.Marshal(res)
	if err != nil {
		e.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatal(err)
	}
	return m, nil
}

func (e *env) ok(aid, capID string, args any) map[string]any {
	e.t.Helper()
	m, err := e.call(aid, capID, args)
	if err != nil {
		e.t.Fatalf("%s as %s: %s: %s", capID, aid, err.Code, err.Message)
	}
	return m
}

func (e *env) fail(aid, capID string, args any, code string) *Error {
	e.t.Helper()
	m, err := e.call(aid, capID, args)
	if err == nil {
		e.t.Fatalf("%s as %s: succeeded (%v), want %s", capID, aid, m, code)
	}
	if err.Code != code {
		e.t.Fatalf("%s as %s: code %s (%s), want %s", capID, aid, err.Code, err.Message, code)
	}
	if err.Status < 400 || err.Status == 429 {
		e.t.Fatalf("%s: status %d would not carry the code", capID, err.Status)
	}
	return err
}

func (e *env) invite(o InviteOptions) string {
	e.t.Helper()
	code, _, err := e.svc.CreateInvite(context.Background(), "operator", o)
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

// join makes aid a member with handle using a fresh invite.
func (e *env) join(aid, handle string) {
	e.t.Helper()
	code := e.invite(InviteOptions{})
	e.ok(aid, "org.join", map[string]any{"code": code, "handle": handle})
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int64 { f, _ := v.(float64); return int64(f) }

func list(v any) []any { l, _ := v.([]any); return l }

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

func contains(s, sub string) bool { return strings.Contains(s, sub) }
