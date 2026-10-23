package org

// Regression tests for the findings of the security review of c055cb3. Each
// one started as a proof of concept that succeeded against that commit and
// now has to fail closed.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/store"
)

// Finding 4: failed joins with a known code no longer lock the code.
func TestReviewJoinLockoutPerCode(t *testing.T) {
	e := newEnv(t, nil)
	code := e.invite(InviteOptions{Uses: 100})
	e.ok(aidAlice, "org.join", map[string]any{"code": code, "handle": "alice"})
	for i := 0; i < 3; i++ {
		aid := "aid-sybil-000000000" + string(rune('a'+i))
		for j := 0; j < 10; j++ {
			e.call(aid, "org.join", map[string]any{"code": code, "handle": "alice"})
		}
		// The caller's own failures are bounded.
		e.fail(aid, "org.join", map[string]any{"code": code, "handle": "free-name"}, CodeRateLimited)
	}
	e.ok(aidBob, "org.join", map[string]any{"code": code, "handle": "bob"})
}

func TestReviewFailedJoinsGlobalLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.FailedJoinsGlobal = 3 })
	for i := 0; i < 3; i++ {
		e.fail("aid-guess-000000000"+string(rune('a'+i)), "org.join", map[string]any{"code": "LAB-0000-0000", "handle": "x" + string(rune('a'+i))}, CodeInviteUnknown)
	}
	e.fail(aidBob, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "bob"}, CodeRateLimited)
	e.clk.add(10 * time.Minute)
	e.ok(aidBob, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "bob"})
}

// Finding 3: flooding a mailbox no longer evicts anyone's mail.
func TestReviewMailboxNoEviction(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.MailboxMax = 20
		c.Rates["msg"] = config.Rate{PerMinute: 1000, PerDay: 10000}
	})
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.join(aidEve, "eve")
	e.ok(aidAlice, "msg.send", map[string]any{"to": "bob", "text": "IMPORTANT: exam moved to Monday"})
	refused := 0
	for i := 0; i < 25; i++ {
		if _, err := e.call(aidEve, "msg.send", map[string]any{"to": "bob", "text": "spam"}); err != nil {
			if err.Code != CodeMailboxFull && err.Code != CodeTooManyUnread {
				t.Fatalf("unexpected %s", err.Code)
			}
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("the flood was never refused")
	}
	found := false
	for _, m := range list(e.ok(aidBob, "msg.inbox", map[string]any{"limit": 200})["messages"]) {
		if strings.Contains(str(obj(m)["untrusted_text"]), "IMPORTANT") {
			found = true
		}
	}
	if !found {
		t.Fatal("alice's message was evicted")
	}
}

// Sound in c055cb3 and kept: concurrent joins never exceed max uses.
func TestReviewConcurrentJoinsRespectUses(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.JoinsPerMinutePerCode = 1000 })
	code := e.invite(InviteOptions{Uses: 5})
	var wg sync.WaitGroup
	var mu sync.Mutex
	joined := 0
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			aid := "aid-race-" + strings.Repeat("0", 10) + string(rune('a'+i%26)) + string(rune('a'+i/26))
			_, err := e.call(aid, "org.join", map[string]any{"code": code, "handle": "u" + strings.Repeat("x", i%5+1) + string(rune('a'+i%26)) + string(rune('a'+i/26))})
			if err == nil {
				mu.Lock()
				joined++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if joined != 5 {
		t.Fatalf("joined %d, want 5", joined)
	}
}

// Finding 7: confusable handles count as taken.
func TestReviewConfusableHandles(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.FailedJoinsPerCaller = 100 })
	e.join(aidAlice, "alice")
	for _, h := range []string{"a1ice", "alice_", "al1ce", "a-l-i-c-e", "ALICE"} {
		e.fail(aidEve, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": h}, CodeHandleTaken)
		e.clk.add(time.Minute)
	}
	for _, h := range []string{"adm1n", "offic1al", "r00t", "iab-news", "ane7-x", "sy5tem"} {
		_, err := e.call(aidEve, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": h})
		if h == "ane7-x" { // 7 is not folded: an ordinary handle
			if err != nil {
				t.Fatalf("%s refused: %v", h, err)
			}
			e.ok(aidEve, "org.leave", map[string]any{"confirm": true})
			e.clk.add(31 * 24 * time.Hour)
			continue
		}
		if err == nil || err.Code != CodeHandleReserved {
			t.Fatalf("%s: %v", h, err)
		}
		e.clk.add(time.Minute)
	}
	e.ok(aidEve, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "label"})
	// A rename may not take a look-alike of another member either.
	e.fail(aidEve, "org.rename", map[string]any{"handle": "a1ice"}, CodeHandleTaken)
	if Skeleton("Al1ce_0-5") != "aiiceos" {
		t.Fatal(Skeleton("Al1ce_0-5"))
	}
}

func TestReviewSkeletonBackfill(t *testing.T) {
	cfg := baseConfig(t)
	db, err := store.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`INSERT INTO members(aid, handle, kind, joined_at) VALUES(?, 'alice', 'student', 1)`, aidAlice)
	s := New(cfg, db, Options{})
	var sk string
	db.QueryRow(`SELECT skeleton FROM members WHERE aid = ?`, aidAlice).Scan(&sk)
	if sk != "aiice" {
		t.Fatalf("skeleton %q", sk)
	}
	_ = s
}

// Finding 1a: single-use codes in bulk.
func TestReviewBulkSingleUseInvites(t *testing.T) {
	e := newEnv(t, nil)
	r := e.ok(aidAdmin, "admin.invite.create", map[string]any{"count": 3, "uses": 1})
	codes := list(r["codes"])
	if len(codes) != 3 || len(list(r["invites"])) != 3 || str(codes[0]) == str(codes[1]) {
		t.Fatalf("bulk: %v", r)
	}
	e.ok(aidAlice, "org.join", map[string]any{"code": str(codes[0]), "handle": "alice"})
	e.fail(aidBob, "org.join", map[string]any{"code": str(codes[0]), "handle": "bob"}, CodeInviteUsedUp)
	e.ok(aidBob, "org.join", map[string]any{"code": str(codes[1]), "handle": "bob"})
	e.fail(aidAdmin, "admin.invite.create", map[string]any{"count": MaxInviteBatch + 1}, CodeInvalidValue)
}

// Finding 1b: organisation-wide daily caps on messages and writes.
func TestReviewGlobalDailyCaps(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.GlobalDaily = map[string]int{"msg": 2, "write": 1} })
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.join(aidCarol, "carol")
	e.ok(aidAlice, "msg.send", map[string]any{"to": "bob", "text": "1"})
	e.ok(aidCarol, "msg.send", map[string]any{"to": "bob", "text": "2"})
	e.fail(aidBob, "msg.send", map[string]any{"to": "alice", "text": "3"}, CodeRateLimited)
	e.ok(aidAlice, "board.post", map[string]any{"title": "one"})
	e.fail(aidCarol, "research.project.create", map[string]any{"name": "proj"}, CodeRateLimited)
	// Reading and acknowledging are not capped.
	e.ok(aidBob, "msg.ack", map[string]any{"up_to": 100})
	e.clk.add(24 * time.Hour)
	e.ok(aidBob, "msg.send", map[string]any{"to": "alice", "text": "3"})
}

// Finding 1c: an administrator's removal releases the handle at once;
// leaving keeps it on hold.
func TestReviewRemovalReleasesHold(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.ok(aidAdmin, "admin.member.remove", map[string]any{"handle": "alice", "ban": true})
	e.ok(aidCarol, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "alice"})
	e.ok(aidBob, "org.leave", map[string]any{"confirm": true})
	e.fail(aidEve, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "bob"}, CodeHandleTaken)
}

// Finding 8: per-member content quota and owner-only deletion.
func TestReviewContentQuotaAndDelete(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.MemberContentBytes = 3000 })
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	id := num(obj(e.ok(aidAlice, "board.post", map[string]any{"title": "big", "body": strings.Repeat("b", 2500)})["item"])["id"])
	e.fail(aidAlice, "board.post", map[string]any{"title": "more", "body": strings.Repeat("b", 600)}, CodeQuotaExceeded)
	e.ok(aidAlice, "research.project.create", map[string]any{"name": "proj"})
	e.fail(aidAlice, "research.note.add", map[string]any{"project": "proj", "text": strings.Repeat("n", 600)}, CodeQuotaExceeded)
	e.ok(aidBob, "board.comment", map[string]any{"id": id, "text": "nice"})
	e.fail(aidBob, "board.delete", map[string]any{"id": id}, CodeNotPermitted)
	e.ok(aidAlice, "board.delete", map[string]any{"id": id})
	e.fail(aidBob, "board.list", map[string]any{"id": id}, CodeItemUnknown)
	e.fail(aidBob, "board.claim", map[string]any{"id": id}, CodeItemUnknown)
	if len(list(e.ok(aidBob, "board.list", map[string]any{"status": "all"})["items"])) != 0 {
		t.Fatal("deleted item listed")
	}
	var n int
	e.svc.db.QueryRow(`SELECT COUNT(*) FROM board_comments WHERE item_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("comments kept")
	}
	note := num(obj(e.ok(aidAlice, "research.note.add", map[string]any{"project": "proj", "text": strings.Repeat("n", 600)})["note"])["id"])
	e.fail(aidBob, "research.note.delete", map[string]any{"id": note}, CodeNotPermitted)
	e.ok(aidAlice, "research.note.delete", map[string]any{"id": note})
	e.fail(aidAlice, "research.note.delete", map[string]any{"id": note}, CodeNoteUnknown)
}

// Finding 9: every comment up to the cap is returned.
func TestReviewAllCommentsReturned(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.Rates["write"] = config.Rate{PerMinute: 10000, PerDay: 10000}
		c.GlobalDaily = map[string]int{"write": 10000}
	})
	e.join(aidAlice, "alice")
	id := num(obj(e.ok(aidAlice, "board.post", map[string]any{"title": "t"})["item"])["id"])
	for i := 0; i < 250; i++ {
		e.ok(aidAlice, "board.comment", map[string]any{"id": id, "text": "c"})
	}
	it := obj(list(e.ok(aidAlice, "board.list", map[string]any{"id": id})["items"])[0])
	if len(list(it["comments"])) != 250 {
		t.Fatalf("comments %d", len(list(it["comments"])))
	}
}
