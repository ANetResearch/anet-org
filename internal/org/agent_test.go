package org

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ANetResearch/anet-org/internal/config"
)

// callVia dispatches as aid with the given via value.
func (e *env) callVia(aid, via, capID string, args any) (map[string]any, *Error) {
	e.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		e.t.Fatal(err)
	}
	if args == nil {
		raw = []byte("{}")
	}
	res, derr := e.svc.Dispatch(context.Background(), capID, Caller{AID: aid, Via: via}, raw)
	if derr != nil {
		oe := AsError(derr)
		if oe == nil {
			e.t.Fatalf("%s: non-coded error %v", capID, derr)
		}
		return nil, oe
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatal(err)
	}
	return m, nil
}

func (e *env) okAgent(aid, capID string, args any) map[string]any {
	e.t.Helper()
	m, err := e.callVia(aid, ViaAgent, capID, args)
	if err != nil {
		e.t.Fatalf("%s via agent as %s: %s: %s", capID, aid, err.Code, err.Message)
	}
	return m
}

func TestAskMoved(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	for _, args := range []any{map[string]any{"text": "summarise my inbox"}, map[string]any{"text": "x", "act": true}, nil, "not json"} {
		err := e.fail(aidAlice, "ask", args, CodeAskMoved)
		if !contains(err.Message, "Send the request to the organisation as a message") {
			t.Fatalf("ask_moved message: %q", err.Message)
		}
	}
	e.fail(aidEve, "ask", map[string]any{"text": "hello"}, CodeNotAMember)
}

func TestOrgAdmit(t *testing.T) {
	e := newEnv(t, nil)
	if m := e.ok(aidEve, "org.admit", nil); !reflect.DeepEqual(m, map[string]any{"admit": false}) {
		t.Fatalf("stranger: %v", m)
	}
	e.join(aidAlice, "alice")
	// The arguments are ignored and the reply carries nothing but the answer.
	for _, args := range []any{nil, `{"aid":"someone-else-000001"}`, `[1,2]`} {
		if m := e.ok(aidAlice, "org.admit", args); !reflect.DeepEqual(m, map[string]any{"admit": true}) {
			t.Fatalf("member: %v", m)
		}
	}
	// A member who leaves or is removed is no longer admitted.
	e.ok(aidAlice, "org.leave", map[string]any{"confirm": true})
	if m := e.ok(aidAlice, "org.admit", nil); m["admit"] != false {
		t.Fatalf("former member admitted: %v", m)
	}
	e.join(aidBob, "bob")
	e.ok(aidAdmin, "admin.member.remove", map[string]any{"handle": "bob", "ban": true})
	if m := e.ok(aidBob, "org.admit", nil); m["admit"] != false {
		t.Fatalf("removed member admitted: %v", m)
	}
	// A call without a caller is refused, and the agent cannot ask either.
	e.fail("", "org.admit", nil, CodeCallerRequired)
	e.join(aidCarol, "carol")
	if _, err := e.callVia(aidCarol, ViaAgent, "org.admit", nil); err == nil || err.Code != CodeAgentNotPermitted {
		t.Fatalf("admit via agent: %v", err)
	}
	// org.admit is internal: org.info does not name it.
	for _, c := range list(e.ok(aidCarol, "org.info", nil)["capabilities"]) {
		if c == "org.admit" {
			t.Fatal("org.info lists org.admit")
		}
	}
}

func TestAgentToolsOnly(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	want := []string{"board.claim", "board.comment", "board.delete", "board.done", "board.list", "board.post",
		"msg.ack", "msg.inbox", "msg.send", "org.info", "org.me", "org.members",
		"research.note.add", "research.note.delete", "research.note.list", "research.project.create", "research.project.list"}
	if got := e.svc.AgentTools(); !reflect.DeepEqual(got, want) {
		t.Fatalf("agent tools: %v", got)
	}
	for _, c := range []string{"org.join", "org.leave", "org.rename", "org.admit", "ask",
		"admin.invite.create", "admin.invite.list", "admin.invite.revoke", "admin.member.remove"} {
		if _, err := e.callVia(aidAlice, ViaAgent, c, map[string]any{"confirm": true}); err == nil || err.Code != CodeAgentNotPermitted {
			t.Fatalf("%s via agent: %v", c, err)
		}
		if _, err := e.callVia(aidAdmin, ViaAgent, c, nil); err == nil || err.Code != CodeAgentNotPermitted {
			t.Fatalf("%s via agent as admin: %v", c, err)
		}
	}
	// The membership survived the attempt to leave through the agent.
	e.ok(aidAlice, "org.me", nil)
	if _, err := e.callVia(aidAlice, "robot", "org.me", nil); err == nil || err.Code != CodeBadVia {
		t.Fatalf("unknown via: %v", err)
	}
	// The agent is the member, never more: a non-member is refused as usual.
	if _, err := e.callVia(aidEve, ViaAgent, "board.list", nil); err == nil || err.Code != CodeNotAMember {
		t.Fatalf("non-member via agent: %v", err)
	}
	if m := e.okAgent(aidAlice, "org.me", nil); m["handle"] != "alice" {
		t.Fatalf("org.me via agent: %v", m)
	}
}

// Writes made through the organisation's agent are stored with via=agent
// and shown with the mark; the member's own writes are not marked.
func TestViaAgentMarking(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")

	if m := e.okAgent(aidAlice, "msg.send", map[string]any{"to": "bob", "text": "from the agent"}); m["via"] != viaShown {
		t.Fatalf("send via agent: %v", m)
	}
	e.ok(aidAlice, "msg.send", map[string]any{"to": "bob", "text": "by hand"})
	msgs := list(e.ok(aidBob, "msg.inbox", nil)["messages"])
	if len(msgs) != 2 || obj(msgs[0])["via"] != viaShown || obj(msgs[0])["from"] != "alice" || obj(msgs[1])["via"] != nil {
		t.Fatalf("inbox: %v", msgs)
	}

	item := obj(e.okAgent(aidAlice, "board.post", map[string]any{"title": "Need a GPU profile", "tags": []string{"gpu"}})["item"])
	if item["via"] != viaShown {
		t.Fatalf("post via agent: %v", item)
	}
	id := num(item["id"])
	e.okAgent(aidBob, "board.claim", map[string]any{"id": id})
	e.okAgent(aidBob, "board.comment", map[string]any{"id": id, "text": "On it."})
	e.ok(aidAlice, "board.comment", map[string]any{"id": id, "text": "Thanks."})
	e.okAgent(aidBob, "board.done", map[string]any{"id": id, "note": "Profile attached to my notes."})
	got := obj(list(e.ok(aidAlice, "board.list", map[string]any{"id": id})["items"])[0])
	if got["via"] != viaShown || got["claim_via"] != viaShown || got["close_via"] != viaShown {
		t.Fatalf("item marks: %v", got)
	}
	cs := list(got["comments"])
	if obj(cs[0])["via"] != viaShown || obj(cs[1])["via"] != nil {
		t.Fatalf("comment marks: %v", cs)
	}
	// A claim made by hand after a release through the agent is not marked.
	other := obj(e.ok(aidAlice, "board.post", map[string]any{"title": "by hand"})["item"])
	if other["via"] != nil {
		t.Fatalf("own post marked: %v", other)
	}
	oid := num(other["id"])
	e.okAgent(aidBob, "board.claim", map[string]any{"id": oid})
	e.okAgent(aidBob, "board.claim", map[string]any{"id": oid, "release": true})
	e.ok(aidBob, "board.claim", map[string]any{"id": oid})
	if it := obj(list(e.ok(aidAlice, "board.list", map[string]any{"id": oid})["items"])[0]); it["claim_via"] != nil {
		t.Fatalf("hand claim marked: %v", it)
	}

	p := obj(e.okAgent(aidAlice, "research.project.create", map[string]any{"name": "gpu-profiles"})["project"])
	if p["via"] != viaShown {
		t.Fatalf("project via agent: %v", p)
	}
	n := obj(e.okAgent(aidAlice, "research.note.add", map[string]any{"project": "gpu-profiles", "text": "a note"})["note"])
	if n["via"] != viaShown {
		t.Fatalf("note via agent: %v", n)
	}
	e.ok(aidBob, "research.note.add", map[string]any{"project": "gpu-profiles", "text": "by hand"})
	notes := list(e.ok(aidBob, "research.note.list", map[string]any{"project": "gpu-profiles"})["notes"])
	if obj(notes[0])["via"] != nil || obj(notes[1])["via"] != viaShown {
		t.Fatalf("note marks: %v", notes)
	}
	if pr := obj(list(e.ok(aidBob, "research.project.list", nil)["projects"])[0]); pr["via"] != viaShown {
		t.Fatalf("project list mark: %v", pr)
	}
	// Stored as the value "agent", not as the sentence.
	var stored string
	e.svc.db.QueryRow(`SELECT via FROM board_items WHERE id = ?`, id).Scan(&stored)
	if stored != ViaAgent {
		t.Fatalf("stored via %q", stored)
	}
}

// The agent acts as the member: the member's rate limits, quotas and
// permissions apply, shared with the member's own calls.
func TestViaAgentLimitsAsMember(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.Rates["msg"] = config.Rate{PerMinute: 2, PerDay: 100}
		c.MemberContentBytes = 300
	})
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.ok(aidAlice, "msg.send", map[string]any{"to": "bob", "text": "one"})
	e.okAgent(aidAlice, "msg.send", map[string]any{"to": "bob", "text": "two"})
	if _, err := e.callVia(aidAlice, ViaAgent, "msg.send", map[string]any{"to": "bob", "text": "three"}); err == nil || err.Code != CodeRateLimited {
		t.Fatalf("rate limit via agent: %v", err)
	}
	e.ok(aidAlice, "board.post", map[string]any{"title": strings.Repeat("x", 200)})
	if _, err := e.callVia(aidAlice, ViaAgent, "board.post", map[string]any{"title": strings.Repeat("y", 150)}); err == nil || err.Code != CodeQuotaExceeded {
		t.Fatalf("quota via agent: %v", err)
	}
	// Another member's item cannot be deleted through the agent either.
	it := obj(e.ok(aidBob, "board.post", map[string]any{"title": "bob's"})["item"])
	if _, err := e.callVia(aidAlice, ViaAgent, "board.delete", map[string]any{"id": num(it["id"])}); err == nil || err.Code != CodeNotPermitted {
		t.Fatalf("delete other's item via agent: %v", err)
	}
}

// The task board as the way to find helpers: claims, releases, comments
// and closing notify the other party by message, never the actor.
func TestBoardNotifications(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.join(aidCarol, "carol")
	notes := func(aid string) []map[string]any {
		var out []map[string]any
		for _, m := range list(e.ok(aid, "msg.inbox", nil)["messages"]) {
			if obj(m)["from_kind"] == "notification" {
				out = append(out, obj(m))
			}
		}
		e.ok(aid, "msg.ack", map[string]any{"up_to": 1 << 40})
		return out
	}
	it := obj(e.okAgent(aidAlice, "board.post", map[string]any{"title": "Help wanted: CUDA build",
		"body": "Needs someone who knows nvcc.", "tags": []string{"help-wanted", "cuda"}})["item"])
	id := num(it["id"])
	if ts := it["tags"]; len(list(ts)) != 2 {
		t.Fatalf("tags: %v", ts)
	}
	if got := list(e.ok(aidBob, "board.list", map[string]any{"tag": "help-wanted", "status": "open"})["items"]); len(got) != 1 {
		t.Fatalf("help requests: %v", got)
	}

	res := e.ok(aidBob, "board.claim", map[string]any{"id": id})
	if fmt.Sprint(res["notified"]) != "[alice]" {
		t.Fatalf("claim notified: %v", res["notified"])
	}
	n := notes(aidAlice)
	if len(n) != 1 || n[0]["from"] != "board" || num(n[0]["board_item"]) != id ||
		!contains(str(n[0]["untrusted_text"]), "@bob claimed your board item") || n[0]["via"] != nil {
		t.Fatalf("claim notification: %v", n)
	}
	if len(notes(aidBob)) != 0 {
		t.Fatal("the claimer was notified of its own claim")
	}

	e.okAgent(aidBob, "board.claim", map[string]any{"id": id, "release": true})
	if n := notes(aidAlice); len(n) != 1 || !contains(str(n[0]["untrusted_text"]), "released") || n[0]["via"] != viaShown {
		t.Fatalf("release notification: %v", n)
	}
	e.ok(aidCarol, "board.claim", map[string]any{"id": id})
	notes(aidAlice)
	e.ok(aidCarol, "board.comment", map[string]any{"id": id, "text": "I can do it on Friday."})
	if n := notes(aidAlice); len(n) != 1 || !contains(str(n[0]["untrusted_text"]), "I can do it on Friday.") {
		t.Fatalf("comment notification: %v", n)
	}
	e.ok(aidAlice, "board.comment", map[string]any{"id": id, "text": "Thank you."})
	if len(notes(aidAlice)) != 0 {
		t.Fatal("the author was notified of its own comment")
	}
	res = e.ok(aidCarol, "board.done", map[string]any{"id": id, "note": "Built with CUDA 13."})
	if fmt.Sprint(res["notified"]) != "[alice]" {
		t.Fatalf("done notified: %v", res["notified"])
	}
	if n := notes(aidAlice); len(n) != 1 || !contains(str(n[0]["untrusted_text"]), "done. Note: Built with CUDA 13.") {
		t.Fatalf("done notification: %v", n)
	}
	if len(notes(aidCarol)) != 0 {
		t.Fatal("the closer was notified")
	}

	// The author closing a claimed item tells the claimer.
	it2 := obj(e.ok(aidAlice, "board.post", map[string]any{"title": "second"})["item"])
	e.ok(aidBob, "board.claim", map[string]any{"id": num(it2["id"])})
	notes(aidAlice)
	e.ok(aidAlice, "board.done", map[string]any{"id": num(it2["id"]), "withdraw": true})
	if n := notes(aidBob); len(n) != 1 || !contains(str(n[0]["untrusted_text"]), "@alice withdrew board item") {
		t.Fatalf("withdraw notification: %v", n)
	}

	// A notification never blocks the action: a full mailbox is skipped.
	e.cfg.MailboxMax = 1
	e.ok(aidCarol, "msg.send", map[string]any{"to": "alice", "text": "fills the mailbox"})
	it3 := obj(e.ok(aidAlice, "board.post", map[string]any{"title": "third"})["item"])
	res = e.ok(aidBob, "board.claim", map[string]any{"id": num(it3["id"])})
	if len(list(res["notified"])) != 0 {
		t.Fatalf("notified into a full mailbox: %v", res)
	}
	// Nobody can write as the board: the handle is reserved.
	e.fail(aidEve, "org.join", map[string]any{"code": e.invite(InviteOptions{}), "handle": "board"}, CodeHandleReserved)
}
