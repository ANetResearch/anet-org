package org

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ANetResearch/anet-org/internal/config"
)

func TestInfoForAnyone(t *testing.T) {
	e := newEnv(t, nil)
	m := e.ok(aidEve, "org.info", nil)
	if str(m["name"]) != "Example Lab" || str(m["invite_format"]) != "LAB-XXXX-XXXX" {
		t.Fatalf("info: %v", m)
	}
	if obj(m["caller"])["member"] != false {
		t.Fatal("stranger shown as member")
	}
	if len(list(m["capabilities"])) != 31 {
		t.Fatalf("capabilities: %v", m["capabilities"])
	}
	e.join(aidAlice, "alice")
	m = e.ok(aidAlice, "org.info", nil)
	if obj(m["caller"])["handle"] != "alice" {
		t.Fatalf("member not recognised: %v", m["caller"])
	}
}

func TestJoinHappyPathAndTolerantCode(t *testing.T) {
	e := newEnv(t, nil)
	code := e.invite(InviteOptions{Uses: 5})
	typed := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	m := e.ok(aidAlice, "org.join", map[string]any{"code": typed, "handle": "@Alice"})
	if m["joined"] != true || str(m["handle"]) != "alice" || str(m["kind"]) != KindStudent {
		t.Fatalf("join: %v", m)
	}
	me := e.ok(aidAlice, "org.me", nil)
	if str(me["handle"]) != "alice" || str(me["aid"]) != aidAlice {
		t.Fatalf("me: %v", me)
	}
	e.fail(aidAlice, "org.join", map[string]any{"code": code, "handle": "alice2"}, CodeAlreadyMember)
}

func TestJoinInviteFailures(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()

	e.fail(aidAlice, "org.join", map[string]any{"code": "LAB-0000-0000", "handle": "alice"}, CodeInviteUnknown)
	e.fail(aidAlice, "org.join", map[string]any{"code": "not a code at all", "handle": "alice"}, CodeInviteUnknown)
	e.fail(aidAlice, "org.join", map[string]any{"code": "", "handle": "alice"}, CodeMissingField)
	// A code of another organisation (other prefix) is unknown here.
	other := strings.Replace(e.invite(InviteOptions{}), "LAB-", "ORG-", 1)
	e.fail(aidAlice, "org.join", map[string]any{"code": other, "handle": "alice"}, CodeInviteUnknown)

	expiring := e.invite(InviteOptions{ExpiresDays: 1})
	e.clk.add(25 * time.Hour)
	e.fail(aidAlice, "org.join", map[string]any{"code": expiring, "handle": "alice"}, CodeInviteExpired)

	revoked, v, err := e.svc.CreateInvite(ctx, "operator", InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RevokeInvite(ctx, "operator", v.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.fail(aidAlice, "org.join", map[string]any{"code": revoked, "handle": "alice"}, CodeInviteRevoked)

	single := e.invite(InviteOptions{Uses: 1})
	e.ok(aidBob, "org.join", map[string]any{"code": single, "handle": "bob"})
	e.fail(aidAlice, "org.join", map[string]any{"code": single, "handle": "alice"}, CodeInviteUsedUp)
}

func TestJoinInviteOptionsBounds(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	for _, o := range []InviteOptions{{ExpiresDays: 91}, {ExpiresDays: -1}, {Uses: 10001}, {Kind: "god"},
		{Note: strings.Repeat("n", 201)}} {
		if _, _, err := e.svc.CreateInvite(ctx, "operator", o); err == nil {
			t.Errorf("invite %+v accepted", o)
		}
	}
	_, v, err := e.svc.CreateInvite(ctx, "operator", InviteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v.MaxUses != DefaultInviteUses {
		t.Fatalf("default uses %d", v.MaxUses)
	}
	exp, _ := time.Parse(time.RFC3339, v.ExpiresAt)
	if got := exp.Sub(e.clk.now()); got != 14*24*time.Hour {
		t.Fatalf("default expiry %v", got)
	}
}

func TestInviteStoresOnlyHash(t *testing.T) {
	e := newEnv(t, nil)
	code := e.invite(InviteOptions{})
	body := strings.ReplaceAll(strings.TrimPrefix(code, "LAB-"), "-", "")
	var n int
	if err := e.svc.db.QueryRow(`SELECT COUNT(*) FROM invites WHERE lookup LIKE ? OR note LIKE ? OR CAST(hash AS TEXT) LIKE ?`,
		"%"+body+"%", "%"+body+"%", "%"+body+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the code appears in the database")
	}
}

func TestJoinRatePerCode(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.JoinsPerMinutePerCode = 3 })
	code := e.invite(InviteOptions{})
	for i := 0; i < 3; i++ {
		aid := "aid-student-000000" + string(rune('a'+i))
		e.ok(aid, "org.join", map[string]any{"code": code, "handle": "student" + string(rune('a'+i))})
	}
	err := e.fail("aid-student-000000z", "org.join", map[string]any{"code": code, "handle": "studentz"}, CodeRateLimited)
	if err.RetryAfter <= 0 {
		t.Fatal("no retry-after")
	}
	e.clk.add(time.Minute)
	e.ok("aid-student-000000z", "org.join", map[string]any{"code": code, "handle": "studentz"})
}

func TestJoinAttemptsPerCallerLimited(t *testing.T) {
	e := newEnv(t, nil)
	for i := 0; i < 10; i++ {
		e.fail(aidEve, "org.join", map[string]any{"code": "LAB-0000-000" + string(rune('0'+i)), "handle": "eve"}, CodeInviteUnknown)
	}
	e.fail(aidEve, "org.join", map[string]any{"code": "LAB-0000-0000", "handle": "eve"}, CodeRateLimited)
}

func TestHandleRules(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.Rates["join"] = config.Rate{PerMinute: 1000, PerDay: 1000}
		c.FailedJoinsPerCaller = 1000
	})
	code := e.invite(InviteOptions{})
	for _, h := range []string{"a", "1abc", "ab cd", "ab!c", "x" + strings.Repeat("y", 24), "aliсe", "_abc", ""} {
		_, err := e.call(aidAlice, "org.join", map[string]any{"code": code, "handle": h})
		if err == nil || (err.Code != CodeInvalidHandle && err.Code != CodeMissingField) {
			t.Errorf("handle %q: %v", h, err)
		}
	}
	for _, h := range []string{"admin", "staff", "helpdesk", "taskboard", "research", "anet", "system", "root", "lab",
		"lab-office", "example-lab", "administrator2", "official-bot", "all", "anet-bot"} {
		e.fail(aidAlice, "org.join", map[string]any{"code": code, "handle": h}, CodeHandleReserved)
	}
	e.ok(aidAlice, "org.join", map[string]any{"code": code, "handle": "alice"})
	e.fail(aidBob, "org.join", map[string]any{"code": code, "handle": "ALICE"}, CodeHandleTaken)
	e.ok(aidBob, "org.join", map[string]any{"code": code, "handle": "b0b_the-builder"})
}

func TestRenameOncePerWeekAndHold(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.fail(aidAlice, "org.rename", map[string]any{"handle": "bob"}, CodeHandleTaken)
	e.fail(aidAlice, "org.rename", map[string]any{"handle": "staff"}, CodeHandleReserved)
	e.fail(aidAlice, "org.rename", map[string]any{"handle": "alice"}, CodeInvalidValue)
	m := e.ok(aidAlice, "org.rename", map[string]any{"handle": "alice-w"})
	if str(m["previous"]) != "alice" || str(m["handle"]) != "alice-w" {
		t.Fatalf("rename: %v", m)
	}
	e.fail(aidAlice, "org.rename", map[string]any{"handle": "alice-x"}, CodeRenameTooSoon)
	// The old handle is held for its former holder.
	e.fail(aidCarol, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "alice"}, CodeHandleTaken)
	e.clk.add(7*24*time.Hour + time.Second)
	e.ok(aidAlice, "org.rename", map[string]any{"handle": "alice"})
	e.clk.add(31 * 24 * time.Hour)
	e.ok(aidCarol, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "alice-w"})
}

func TestLeaveNeedsConfirmAndCleansUp(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.ok(aidBob, "msg.send", map[string]any{"to": "alice", "text": "hello"})
	item := obj(e.ok(aidBob, "board.post", map[string]any{"title": "task"})["item"])
	e.ok(aidAlice, "board.claim", map[string]any{"id": num(item["id"])})
	e.fail(aidAlice, "org.leave", nil, CodeConfirmRequired)
	e.ok(aidAlice, "org.leave", map[string]any{"confirm": true})
	e.fail(aidAlice, "org.me", nil, CodeNotAMember)
	var n int
	_ = e.svc.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE to_aid = ?`, aidAlice).Scan(&n)
	if n != 0 {
		t.Fatal("mailbox kept after leaving")
	}
	it := list(e.ok(aidBob, "board.list", map[string]any{"id": num(item["id"])})["items"])
	if str(obj(it[0])["status"]) != statusOpen {
		t.Fatal("claimed item not reopened")
	}
}

func TestNonMemberRefused(t *testing.T) {
	e := newEnv(t, nil)
	for _, c := range []string{"org.me", "org.leave", "org.rename", "org.members", "msg.send", "msg.inbox", "msg.ack",
		"board.post", "board.list", "board.claim", "board.done", "board.comment", "research.project.create",
		"research.project.list", "research.note.add", "research.note.list", "ask", "board.delete", "research.note.delete"} {
		e.fail(aidEve, c, nil, CodeNotAMember)
	}
}

func TestMissingCallerAndLocalAdmin(t *testing.T) {
	e := newEnv(t, nil)
	e.fail("", "org.info", nil, CodeCallerRequired)
	e.fail("", "admin.invite.list", nil, CodeCallerRequired)
	e.fail("", "org.join", map[string]any{"code": "x", "handle": "alice"}, CodeCallerRequired)

	l := newEnv(t, func(c *config.Config) { c.AllowLocalAdmin = true })
	l.ok("", "org.info", nil)
	m := l.ok("", "admin.invite.create", map[string]any{"uses": 2})
	if !strings.HasPrefix(str(m["code"]), "LAB-") {
		t.Fatalf("local admin invite: %v", m)
	}
	l.fail("", "org.join", map[string]any{"code": str(m["code"]), "handle": "alice"}, CodeCallerRequired)
	l.fail("", "msg.inbox", nil, CodeCallerRequired)
}

func TestMembersDirectory(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.join(aidStaff, "prof-li")
	m := e.ok(aidAlice, "org.members", nil)
	ms := list(m["members"])
	if len(ms) != 3 {
		t.Fatalf("members: %v", ms)
	}
	for _, x := range ms {
		if _, ok := obj(x)["aid"]; ok {
			t.Fatal("AID shown without include_aid")
		}
	}
	st := list(e.ok(aidAlice, "org.members", map[string]any{"kind": "staff"})["members"])
	if len(st) != 1 || str(obj(st[0])["handle"]) != "prof-li" {
		t.Fatalf("staff: %v", st)
	}
	// Members do not learn each other's AIDs; administrators do.
	e.fail(aidAlice, "org.members", map[string]any{"query": "bo", "include_aid": true}, CodeNotPermitted)
	e.join(aidAdmin, "chair")
	q := list(e.ok(aidAdmin, "org.members", map[string]any{"query": "bo", "include_aid": true})["members"])
	if len(q) != 1 || str(obj(q[0])["aid"]) != aidBob {
		t.Fatalf("query: %v", q)
	}
	e.fail(aidAlice, "org.members", map[string]any{"query": strings.Repeat("q", 65)}, CodeTooLong)
	e.fail(aidAlice, "org.members", map[string]any{"kind": "boss"}, CodeInvalidValue)
}

func TestArgumentDecoding(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.fail(aidAlice, "org.me", map[string]any{"as": aidBob}, CodeUnknownField)
	e.fail(aidAlice, "msg.send", `{"to": 5, "text": "x"}`, CodeBadArgs)
	e.fail(aidAlice, "msg.send", `{"to": "bob", "text": "x"} {}`, CodeBadJSON)
	e.fail(aidAlice, "nope.cap", nil, CodeUnknownCapability)
	// No argument can change the caller.
	e.fail(aidAlice, "msg.inbox", map[string]any{"aid": aidBob}, CodeUnknownField)
}
