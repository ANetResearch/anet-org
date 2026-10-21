package org

import (
	"context"
	"testing"

	"github.com/ANetResearch/anet-org/internal/config"
)

// FuzzDispatchArgs feeds arbitrary argument bytes to every capability: no
// input may panic or produce an error without a code.
func FuzzDispatchArgs(f *testing.F) {
	for _, s := range []string{"{}", `{"code":"LAB-0000-0000","handle":"alice"}`, `{"to":"bob","text":"x"}`,
		`{"since":"2026-10-21T00:00:00Z"}`, `{"ids":[1,2,3]}`, `{"id":1,"release":true}`, `{"tags":["a"]}`, "null", `{"x":1}`} {
		f.Add(s, uint8(0))
	}
	e := newEnv(f, func(c *config.Config) {
		for k := range c.Rates {
			c.Rates[k] = config.Rate{PerMinute: 1 << 30, PerDay: 1 << 30}
		}
		c.JoinsPerMinutePerCode = 1000
	}, nil)
	ctx := context.Background()
	if _, _, err := e.svc.CreateInvite(ctx, "operator", InviteOptions{}); err != nil {
		f.Fatal(err)
	}
	e.svc.db.Exec(`INSERT INTO members(aid, handle, kind, joined_at) VALUES(?, 'alice', 'student', 1)`, aidAlice)
	caps := e.svc.Capabilities()
	f.Fuzz(func(t *testing.T, args string, which uint8) {
		c := caps[int(which)%len(caps)]
		if c == "ask" || c == "org.leave" {
			return
		}
		for _, aid := range []string{aidAlice, aidAdmin} {
			_, err := e.svc.Dispatch(ctx, c, Caller{AID: aid}, []byte(args))
			if err != nil {
				oe := AsError(err)
				if oe == nil || oe.Code == "" || oe.Status < 400 {
					t.Fatalf("%s %q: %v", c, args, err)
				}
				if oe.Code == CodeInternal {
					t.Fatalf("%s %q: internal error", c, args)
				}
			}
		}
	})
}
