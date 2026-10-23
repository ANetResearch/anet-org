package org

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
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
