package org

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ANetResearch/anet-org/internal/assistant"
	"github.com/ANetResearch/anet-org/internal/config"
)

// scriptedModel answers each completion with the next scripted reply and
// records the requests.
type scriptedModel struct {
	mu      sync.Mutex
	replies []string
	reqs    []map[string]any
}

func (s *scriptedModel) handler(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	reply := `{"choices":[{"message":{"content":"(no script)"}}],"usage":{"total_tokens":1}}`
	if len(s.replies) > 0 {
		reply, s.replies = s.replies[0], s.replies[1:]
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(reply))
}

func toolCall(name, args string, tokens int) string {
	b, _ := json.Marshal(args)
	return `{"choices":[{"message":{"content":"","tool_calls":[{"id":"t1","type":"function","function":{"name":"` + name +
		`","arguments":` + string(b) + `}}]}}],"usage":{"total_tokens":` + itoa(int64(tokens)) + `}}`
}

func answer(text string, tokens int) string {
	b, _ := json.Marshal(text)
	return `{"choices":[{"message":{"content":` + string(b) + `,"reasoning_content":"secret reasoning"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":` + itoa(int64(tokens)) + `}}`
}

func askEnv(t *testing.T, sm *scriptedModel, mut func(*config.Config)) *env {
	srv := httptest.NewServer(http.HandlerFunc(sm.handler))
	t.Cleanup(srv.Close)
	pool := assistant.NewPool(assistant.PoolConfig{Upstreams: []assistant.UpstreamConfig{{Name: "fake", BaseURL: srv.URL, Model: "m", APIKey: "k"}}})
	return newEnv(t, mut, pool)
}

func TestAskNotConfigured(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.join(aidAlice, "alice")
	m := e.ok(aidAlice, "ask", map[string]any{"text": "hello"})
	if m["configured"] != false || !contains(str(m["answer"]), "not configured") {
		t.Fatalf("ask: %v", m)
	}
	e.fail(aidAlice, "ask", map[string]any{"text": ""}, CodeMissingField)
	e.fail(aidAlice, "ask", map[string]any{"text": strings.Repeat("a", maxAskText+1)}, CodeTooLong)
}

func TestAskReadsOnlyCallersData(t *testing.T) {
	sm := &scriptedModel{replies: []string{toolCall("read_inbox", `{}`, 50), answer("You have one message from bob.", 30)}}
	e := askEnv(t, sm, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.join(aidCarol, "carol")
	e.ok(aidBob, "msg.send", map[string]any{"to": "alice", "text": "for alice only"})
	e.ok(aidBob, "msg.send", map[string]any{"to": "carol", "text": "for carol only"})
	m := e.ok(aidAlice, "ask", map[string]any{"text": "what is in my inbox?"})
	if str(m["answer"]) != "You have one message from bob." || len(list(m["actions"])) != 0 {
		t.Fatalf("ask: %v", m)
	}
	if strings.Contains(str(m["answer"]), "secret reasoning") {
		t.Fatal("reasoning_content leaked into the answer")
	}
	if num(obj(m["usage"])["tokens"]) != 80 {
		t.Fatalf("usage: %v", m["usage"])
	}
	// Read-only: no write tools offered without act.
	tools := list(sm.reqs[0]["tools"])
	for _, x := range tools {
		n := str(obj(obj(x)["function"])["name"])
		if n == "send_message" || n == "post_board_item" {
			t.Fatal("write tool offered without act")
		}
	}
	// The tool result the model saw holds Alice's mail, not Carol's.
	msgs := list(sm.reqs[1]["messages"])
	toolOut := str(obj(msgs[len(msgs)-1])["content"])
	if !strings.Contains(toolOut, "for alice only") || strings.Contains(toolOut, "for carol only") {
		t.Fatalf("tool output: %s", toolOut)
	}
	// The read did not acknowledge anything.
	if len(list(e.ok(aidAlice, "msg.inbox", nil)["messages"])) != 1 {
		t.Fatal("ask consumed the inbox")
	}
}

func TestAskActSendsOnBehalf(t *testing.T) {
	sm := &scriptedModel{replies: []string{
		toolCall("send_message", `{"to":"bob","text":"Meeting moves to 3 pm."}`, 10),
		answer("Sent.", 10),
	}}
	e := askEnv(t, sm, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	m := e.ok(aidAlice, "ask", map[string]any{"text": "tell bob the meeting moves to 3 pm", "act": true})
	acts := list(m["actions"])
	if len(acts) != 1 || str(obj(acts[0])["tool"]) != "send_message" {
		t.Fatalf("actions: %v", m)
	}
	in := list(e.ok(aidBob, "msg.inbox", nil)["messages"])
	if len(in) != 1 || str(obj(in[0])["from"]) != "alice" {
		t.Fatalf("bob's inbox: %v", in)
	}
}

func TestAskWriteToolRefusedWithoutAct(t *testing.T) {
	// A model that calls a write tool it was not given gets unknown_tool.
	sm := &scriptedModel{replies: []string{toolCall("send_message", `{"to":"bob","text":"x"}`, 10), answer("ok", 1)}}
	e := askEnv(t, sm, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.ok(aidAlice, "ask", map[string]any{"text": "read the inbox of bob"})
	if len(list(e.ok(aidBob, "msg.inbox", nil)["messages"])) != 0 {
		t.Fatal("message sent without act")
	}
	msgs := list(sm.reqs[1]["messages"])
	if !strings.Contains(str(obj(msgs[len(msgs)-1])["content"]), "unknown_tool") {
		t.Fatal("unknown tool not reported")
	}
}

func TestAskBudgets(t *testing.T) {
	member, global := int64(100), int64(150)
	sm := &scriptedModel{replies: []string{answer("a", 120), answer("b", 40), answer("c", 40)}}
	e := askEnv(t, sm, func(c *config.Config) {
		c.Assistant.MemberDailyTokens = &member
		c.Assistant.GlobalDailyTokens = &global
	})
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.ok(aidAlice, "ask", map[string]any{"text": "1"}) // 120 used, over the member budget afterwards
	e.fail(aidAlice, "ask", map[string]any{"text": "2"}, CodeBudgetExhausted)
	e.ok(aidBob, "ask", map[string]any{"text": "3"}) // global 160 after this
	e.fail(aidBob, "ask", map[string]any{"text": "4"}, CodeBudgetExhausted)
	// A new UTC day resets both.
	e.clk.add(24 * 60 * 60 * 1e9)
	e.ok(aidAlice, "ask", map[string]any{"text": "5"})

	zero := int64(0)
	u := askEnv(t, &scriptedModel{replies: []string{answer("x", 1<<30)}}, func(c *config.Config) {
		c.Assistant.MemberDailyTokens = &zero
		c.Assistant.GlobalDailyTokens = &zero
	})
	u.join(aidAlice, "alice")
	u.ok(aidAlice, "ask", map[string]any{"text": "1"})
	u.ok(aidAlice, "ask", map[string]any{"text": "2"})
}

func TestAskUpstreamFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":"AccountQuotaExceeded","message":"quota"}}`))
	}))
	defer srv.Close()
	pool := assistant.NewPool(assistant.PoolConfig{Upstreams: []assistant.UpstreamConfig{{Name: "plan", BaseURL: srv.URL, Model: "m", APIKey: "k"}}})
	e := newEnv(t, nil, pool)
	e.join(aidAlice, "alice")
	e.fail(aidAlice, "ask", map[string]any{"text": "hi"}, CodeAssistantOff)
}
