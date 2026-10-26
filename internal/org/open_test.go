package org

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/hubage"
	"github.com/ANetResearch/anet-org/internal/store"
)

// fakeHub answers KnownSince from a table; an AID missing from it is not
// registered, and down makes every lookup fail.
type fakeHub struct {
	since map[string]time.Time
	down  bool
	calls int
}

func (f *fakeHub) KnownSince(_ context.Context, aid string) (time.Time, error) {
	f.calls++
	if f.down {
		return time.Time{}, errors.New("connection refused")
	}
	t, ok := f.since[aid]
	if !ok {
		return time.Time{}, hubage.ErrNotRegistered
	}
	return t, nil
}

func newOpenEnv(t *testing.T, hub *fakeHub) *env {
	t.Helper()
	cfg := baseConfig(t)
	cfg.OpenJoin = config.OpenJoin{Enabled: true, Hub: "https://hub.example.org", MinHubAgeHours: 72, DailyJoins: 3}
	db, err := store.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &clock{t: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	var h HubAge
	if hub != nil {
		h = hub
	}
	svc := New(cfg, db, Options{Now: clk.now, HubAge: h})
	return &env{t: t, svc: svc, cfg: cfg, clk: clk}
}

func TestOpenJoin(t *testing.T) {
	now := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)
	hub := &fakeHub{since: map[string]time.Time{
		aidAlice: now.Add(-100 * time.Hour),
		aidBob:   now.Add(-10 * time.Hour),
	}}
	e := newOpenEnv(t, hub)

	info := e.ok(aidEve, "org.info", nil)
	if str(obj(info["join"])["mode"]) != "open" || !contains(str(info["how_to_join"]), "no invite code") {
		t.Fatalf("info: %v", info)
	}
	m := e.ok(aidAlice, "org.join", map[string]any{"handle": "alice"})
	if str(m["kind"]) != KindMember || m["joined"] != true {
		t.Fatalf("join: %v", m)
	}
	e.fail(aidAlice, "org.join", map[string]any{"handle": "alice2"}, CodeAlreadyMember)

	early := e.ok(aidBob, "org.join", map[string]any{"handle": "bob"})
	if early["joined"] != false || str(early["status"]) != CodeIdentityTooNew || num(early["retry_after_seconds"]) != 62*3600 ||
		str(early["join_from"]) != "2026-10-23T22:00:00Z" {
		t.Fatalf("too new: %v", early)
	}
	e.fail(aidCarol, "org.join", map[string]any{"handle": "carol"}, CodeNotOnHub)

	// Cheap refusals do not reach the hub.
	before := hub.calls
	e.fail(aidCarol, "org.join", map[string]any{"handle": "admin"}, CodeHandleReserved)
	e.fail(aidCarol, "org.join", map[string]any{"handle": "a1ice"}, CodeHandleTaken)
	if hub.calls != before {
		t.Fatal("the hub was asked for a refused handle")
	}

	// Once the identity is old enough it joins.
	e.clk.add(62 * time.Hour)
	e.ok(aidBob, "org.join", map[string]any{"handle": "bob"})

	// An invite code still works besides open joining.
	code := e.invite(InviteOptions{Kind: KindStaff})
	e.ok(aidCarol, "org.join", map[string]any{"code": code, "handle": "carol"})
	if str(e.ok(aidCarol, "org.me", nil)["kind"]) != KindStaff {
		t.Fatal("code join kind")
	}

	// Banned identities stay out.
	if _, err := e.svc.RemoveMember(context.Background(), "operator", "bob", true); err != nil {
		t.Fatal(err)
	}
	e.fail(aidBob, "org.join", map[string]any{"handle": "bob2"}, CodeJoinRefused)

	// The hub being unreachable is its own code, with a status that carries it.
	hub.down = true
	er := e.fail(aidEve, "org.join", map[string]any{"handle": "eve"}, CodeHubUnavailable)
	if er.Status >= 500 {
		t.Fatalf("status %d", er.Status)
	}
}

func TestOpenJoinDailyCap(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hub := &fakeHub{since: map[string]time.Time{}}
	e := newOpenEnv(t, hub)
	aids := []string{"aid-open-0000000001", "aid-open-0000000002", "aid-open-0000000003", "aid-open-0000000004"}
	for _, a := range aids {
		hub.since[a] = old
	}
	for i, a := range aids[:3] {
		e.ok(a, "org.join", map[string]any{"handle": "member-" + string(rune('a'+i))})
	}
	e.fail(aids[3], "org.join", map[string]any{"handle": "member-d"}, CodeRateLimited)
}

func TestOpenJoinWithoutHubChecker(t *testing.T) {
	e := newOpenEnv(t, nil)
	e.fail(aidAlice, "org.join", map[string]any{"handle": "alice"}, CodeHubUnavailable)
}

func TestInviteOnlyNeedsCode(t *testing.T) {
	e := newEnv(t, nil)
	e.fail(aidAlice, "org.join", map[string]any{"handle": "alice"}, CodeMissingField)
	if str(obj(e.ok(aidEve, "org.info", nil)["join"])["mode"]) != "invite" {
		t.Fatal("join mode")
	}
}

func TestReports(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.join(aidCarol, "carol")

	// A message in the reporter's own mailbox.
	sent := e.ok(aidBob, "msg.send", map[string]any{"to": "alice", "text": "Buy cheap credits at example.com"})
	mid := num(sent["message_id"])
	r := e.ok(aidAlice, "org.report", map[string]any{"message_id": mid, "reason": "spam"})
	rid := num(r["report_id"])
	if rid == 0 {
		t.Fatalf("report: %v", r)
	}
	// The same report again is not a second one.
	if r2 := e.ok(aidAlice, "org.report", map[string]any{"message_id": mid, "reason": "spam"}); num(r2["report_id"]) != rid || r2["duplicate"] != true {
		t.Fatalf("duplicate: %v", r2)
	}
	// A message in someone else's mailbox cannot be reported (or read) by id.
	e.fail(aidCarol, "org.report", map[string]any{"message_id": mid, "reason": "spam"}, CodeInvalidValue)

	// A board item, then the item is deleted: the evidence stays.
	item := e.ok(aidBob, "board.post", map[string]any{"title": "Free GPUs", "body": "Send me your key"})
	iid := num(obj(item["item"])["id"])
	e.ok(aidCarol, "org.report", map[string]any{"board_item": iid, "reason": "phishing"})
	e.ok(aidBob, "board.delete", map[string]any{"id": iid})

	e.clk.add(time.Minute) // three reports a minute
	e.fail(aidCarol, "org.report", map[string]any{"handle": "carol", "reason": "x"}, CodeInvalidValue)
	e.clk.add(time.Minute)
	e.fail(aidCarol, "org.report", map[string]any{"handle": "bob", "board_item": 3, "reason": "x"}, CodeBadArgs)
	e.fail(aidCarol, "org.report", map[string]any{"handle": "bob"}, CodeMissingField)
	e.fail(aidEve, "org.report", map[string]any{"handle": "bob", "reason": "x"}, CodeNotAMember)
	e.fail(aidAlice, "admin.report.list", nil, CodeNotAdmin)

	l := e.ok(aidAdmin, "admin.report.list", nil)
	reps := list(l["reports"])
	if len(reps) != 2 {
		t.Fatalf("reports: %v", l)
	}
	newest := obj(reps[0])
	if str(newest["subject"]) != subjectBoardItem || str(newest["target"]) != "bob" || str(newest["target_aid"]) != aidBob ||
		!contains(str(newest["untrusted_evidence"]), "Send me your key") || num(newest["target_open_reports"]) != 2 {
		t.Fatalf("report view: %v", newest)
	}
	if str(newest["reporter"]) != "carol" {
		t.Fatalf("reporter: %v", newest)
	}

	e.ok(aidAdmin, "admin.report.close", map[string]any{"id": rid, "note": "warned"})
	if c := e.ok(aidAdmin, "admin.report.close", map[string]any{"id": rid}); c["already_closed"] != true {
		t.Fatalf("close twice: %v", c)
	}
	e.fail(aidAdmin, "admin.report.close", map[string]any{"id": 999}, CodeReportUnknown)
	if n := len(list(e.ok(aidAdmin, "admin.report.list", nil)["reports"])); n != 1 {
		t.Fatalf("open reports: %d", n)
	}
	if n := len(list(e.ok(aidAdmin, "admin.report.list", map[string]any{"status": "all"})["reports"])); n != 2 {
		t.Fatalf("all reports: %d", n)
	}
	// The reported member, once removed, still shows by its stored handle.
	if _, err := e.svc.RemoveMember(context.Background(), "operator", "bob", true); err != nil {
		t.Fatal(err)
	}
	v := obj(list(e.ok(aidAdmin, "admin.report.list", nil)["reports"])[0])
	if str(v["target"]) != "bob" || v["target_is_member"] != false {
		t.Fatalf("after removal: %v", v)
	}
}

func TestReportsOpenLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Rates["report"] = config.Rate{PerMinute: 1000, PerDay: 1000} })
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	for i := 0; i < maxOpenReports; i++ {
		it := e.ok(aidBob, "board.post", map[string]any{"title": "item"})
		e.ok(aidAlice, "org.report", map[string]any{"board_item": num(obj(it["item"])["id"]), "reason": "spam"})
	}
	it := e.ok(aidBob, "board.post", map[string]any{"title": "item"})
	e.fail(aidAlice, "org.report", map[string]any{"board_item": num(obj(it["item"])["id"]), "reason": "spam"}, CodeLimitReached)
}

// Refusals of joins without a code count against their caller only: many
// callers cannot use them to close joining for everybody.
func TestOpenJoinRefusalsDoNotLockOthersOut(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hub := &fakeHub{since: map[string]time.Time{aidAlice: old}}
	e := newOpenEnv(t, hub)
	e.cfg.FailedJoinsGlobal = 5
	for i := 0; i < 20; i++ {
		aid := "aid-sybil-" + strings.Repeat("0", 8) + string(rune('a'+i))
		e.fail(aid, "org.join", map[string]any{"handle": "admin"}, CodeHandleReserved)
	}
	e.ok(aidAlice, "org.join", map[string]any{"handle": "alice"})
	code := e.invite(InviteOptions{})
	e.ok(aidBob, "org.join", map[string]any{"code": code, "handle": "bob"})
}

func TestOpenJoinHourlyCapCountsSuccessOnly(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hub := &fakeHub{since: map[string]time.Time{}}
	e := newOpenEnv(t, hub)
	e.cfg.OpenJoin.DailyJoins, e.cfg.OpenJoin.HourlyJoins = 100, 2
	aids := []string{"aid-open-0000000001", "aid-open-0000000002", "aid-open-0000000003"}
	for _, a := range aids {
		hub.since[a] = old
	}
	e.join(aidCarol, "taken")
	e.fail(aids[0], "org.join", map[string]any{"handle": "taken"}, CodeHandleTaken)
	e.ok(aids[0], "org.join", map[string]any{"handle": "first"})
	e.ok(aids[1], "org.join", map[string]any{"handle": "second"})
	e.fail(aids[2], "org.join", map[string]any{"handle": "third"}, CodeRateLimited)
	e.clk.add(time.Hour)
	e.ok(aids[2], "org.join", map[string]any{"handle": "third"})
}

func TestOpenJoinPause(t *testing.T) {
	hub := &fakeHub{since: map[string]time.Time{aidAlice: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
	e := newOpenEnv(t, hub)
	s := e.ok(aidAdmin, "admin.settings.set", map[string]any{"open_join_paused": true})
	if s["open_join_paused"] != true {
		t.Fatalf("settings: %v", s)
	}
	e.fail(aidAlice, "org.join", map[string]any{"handle": "alice"}, CodeJoinPaused)
	code := e.invite(InviteOptions{})
	e.ok(aidBob, "org.join", map[string]any{"code": code, "handle": "bob"})
	e.ok(aidAdmin, "admin.settings.set", map[string]any{"open_join_paused": false})
	e.ok(aidAlice, "org.join", map[string]any{"handle": "alice"})

	inv := newEnv(t, nil)
	inv.fail(aidAdmin, "admin.settings.set", map[string]any{"open_join_paused": true}, CodeInvalidValue)
	if _, ok := inv.ok(aidAdmin, "admin.settings.get", nil)["open_join_paused"]; ok {
		t.Fatal("open_join_paused shown in an invite-only organisation")
	}
}

func TestReportRetention(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	r := e.ok(aidAlice, "org.report", map[string]any{"handle": "bob", "reason": "spam"})
	e.ok(aidAdmin, "admin.report.close", map[string]any{"id": num(r["report_id"])})
	e.clk.add(ReportRetention - time.Hour)
	if n := len(list(e.ok(aidAdmin, "admin.report.list", map[string]any{"status": "all"})["reports"])); n != 1 {
		t.Fatalf("kept: %d", n)
	}
	e.clk.add(2 * time.Hour)
	if n := len(list(e.ok(aidAdmin, "admin.report.list", map[string]any{"status": "all"})["reports"])); n != 0 {
		t.Fatalf("pruned: %d", n)
	}
}
