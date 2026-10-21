package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fake is a local OpenAI-compatible upstream with a programmable mode.
type fake struct {
	mu       sync.Mutex
	mode     string // ok, quota, quota-reset, throttle, 5xx, slow, badkey, badreq
	calls    atomic.Int64
	inflight atomic.Int64
	peak     atomic.Int64
	hold     chan struct{}
	lastAuth atomic.Value
	srv      *httptest.Server
}

func newFake(t *testing.T, mode string) *fake {
	f := &fake{mode: mode}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) setMode(m string) { f.mu.Lock(); f.mode = m; f.mu.Unlock() }

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	n := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	f.lastAuth.Store(r.Header.Get("Authorization"))
	f.mu.Lock()
	mode, hold := f.mode, f.hold
	f.mu.Unlock()
	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return
	}
	switch mode {
	case "quota":
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":"AccountQuotaExceeded","message":"You have exceeded the quota of this account.","type":"TooManyRequests"}}`))
		return
	case "quota-reset":
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":"AccountQuotaExceeded","message":"Quota exceeded; it resets at 2026-11-01T00:00:00+08:00."}}`))
		return
	case "throttle":
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":"RateLimitExceeded.EndpointRPMExceeded","message":"Too many requests."}}`))
		return
	case "5xx":
		w.WriteHeader(502)
		return
	case "badkey":
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"code":"AuthenticationError","message":"bad key"}}`))
		return
	case "badreq":
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"InvalidParameter","message":"bad"}}`))
		return
	case "slow":
		time.Sleep(300 * time.Millisecond)
	case "hold":
		<-hold
	}
	var req chatRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello from ` + req.Model +
		`","reasoning_content":"thinking..."}}],"usage":{"prompt_tokens":10,"completion_tokens":25,"total_tokens":35}}`))
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newPool(clk *testClock, ups ...*fake) *Pool {
	var cs []UpstreamConfig
	for i, f := range ups {
		cs = append(cs, UpstreamConfig{Name: []string{"plan", "api", "third"}[i], BaseURL: f.srv.URL, Model: "model-" + []string{"plan", "api", "third"}[i],
			APIKey: "test-key-" + []string{"plan", "api", "third"}[i], MaxConcurrency: 2})
	}
	now := time.Now
	if clk != nil {
		now = clk.now
	}
	return NewPool(PoolConfig{Upstreams: cs, Timeout: 2 * time.Second, QueueSize: 2, QueueWait: 500 * time.Millisecond,
		QuotaCooldown: time.Hour, BackoffBase: 30 * time.Second, BackoffMax: 10 * time.Minute, Now: now})
}

var hi = []ChatMessage{{Role: "user", Content: "hi"}}

func TestSuccessUsesContentOnly(t *testing.T) {
	a := newFake(t, "ok")
	p := newPool(nil, a)
	c, err := p.Complete(context.Background(), hi, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if c.Content != "hello from model-plan" || c.Usage.Tokens() != 35 || c.Upstream != "plan" {
		t.Fatalf("completion: %+v", c)
	}
	if a.lastAuth.Load() != "Bearer test-key-plan" {
		t.Fatal("key not sent")
	}
}

func TestQuotaFailoverAndLongCooldown(t *testing.T) {
	clk := &testClock{t: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	plan, api := newFake(t, "quota"), newFake(t, "ok")
	p := newPool(clk, plan, api)
	c, err := p.Complete(context.Background(), hi, nil, 100)
	if err != nil || c.Upstream != "api" {
		t.Fatalf("failover: %+v %v", c, err)
	}
	if d := p.CoolingUntil("plan").Sub(clk.now()); d != time.Hour {
		t.Fatalf("quota cool-down %v", d)
	}
	// While cooling, plan is not tried at all.
	before := plan.calls.Load()
	for i := 0; i < 3; i++ {
		if _, err := p.Complete(context.Background(), hi, nil, 100); err != nil {
			t.Fatal(err)
		}
	}
	if plan.calls.Load() != before {
		t.Fatal("cooling upstream was tried")
	}
	// After the cool-down and a recovery, plan is preferred again.
	plan.setMode("ok")
	clk.add(time.Hour + time.Second)
	c, _ = p.Complete(context.Background(), hi, nil, 100)
	if c.Upstream != "plan" {
		t.Fatalf("plan not preferred after cool-down: %s", c.Upstream)
	}
}

func TestQuotaResetTimeFromMessage(t *testing.T) {
	clk := &testClock{t: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	plan, api := newFake(t, "quota-reset"), newFake(t, "ok")
	p := newPool(clk, plan, api)
	if _, err := p.Complete(context.Background(), hi, nil, 100); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC)
	if got := p.CoolingUntil("plan"); !got.Equal(want) {
		t.Fatalf("cooling until %v, want %v", got, want)
	}
	if tt := p.resetTime("resets at 2026-11-01 00:00:00"); !tt.Equal(want) {
		t.Fatalf("plain reset time %v", tt)
	}
}

func TestTransientBackoffGrows(t *testing.T) {
	clk := &testClock{t: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	plan, api := newFake(t, "5xx"), newFake(t, "ok")
	p := newPool(clk, plan, api)
	var got []time.Duration
	for i := 0; i < 4; i++ {
		c, err := p.Complete(context.Background(), hi, nil, 100)
		if err != nil || c.Upstream != "api" {
			t.Fatalf("round %d: %+v %v", i, c, err)
		}
		got = append(got, p.CoolingUntil("plan").Sub(clk.now()))
		clk.add(got[len(got)-1] + time.Second)
	}
	want := []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second, 240 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff %v, want %v", got, want)
		}
	}
	plan.setMode("throttle")
	clk.add(time.Hour)
	p.Complete(context.Background(), hi, nil, 100)
	if d := p.CoolingUntil("plan").Sub(clk.now()); d != 8*time.Minute {
		t.Fatalf("throttle is treated as transient with growing backoff: %v", d)
	}
	plan.setMode("ok")
	clk.add(time.Hour)
	if c, _ := p.Complete(context.Background(), hi, nil, 100); c.Upstream != "plan" {
		t.Fatal("recovered upstream not used")
	}
	clk.add(time.Second)
	plan.setMode("5xx")
	p.Complete(context.Background(), hi, nil, 100)
	if d := p.CoolingUntil("plan").Sub(clk.now()); d != 30*time.Second {
		t.Fatalf("backoff not reset after success: %v", d)
	}
}

func TestTimeoutFailsOver(t *testing.T) {
	plan, api := newFake(t, "slow"), newFake(t, "ok")
	p := newPool(nil, plan, api)
	p.cfg.Timeout = 100 * time.Millisecond
	c, err := p.Complete(context.Background(), hi, nil, 100)
	if err != nil || c.Upstream != "api" {
		t.Fatalf("timeout failover: %+v %v", c, err)
	}
	if !p.CoolingUntil("plan").After(time.Now()) {
		t.Fatal("timed-out upstream not cooling")
	}
}

func TestAllDownAndRejected(t *testing.T) {
	plan, api := newFake(t, "quota"), newFake(t, "badkey")
	p := newPool(nil, plan, api)
	if _, err := p.Complete(context.Background(), hi, nil, 100); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("all down: %v", err)
	}
	bad := newFake(t, "badreq")
	p = newPool(nil, bad, newFake(t, "ok"))
	if _, err := p.Complete(context.Background(), hi, nil, 100); !errors.Is(err, ErrRejected) {
		t.Fatalf("400 should not fail over: %v", err)
	}
	if (&Pool{}).Configured() {
		t.Fatal("empty pool configured")
	}
}

func TestConcurrencyLimitAndQueue(t *testing.T) {
	up := newFake(t, "hold")
	up.hold = make(chan struct{})
	p := newPool(nil, up) // max_concurrency 2, queue 2, wait 500ms
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Complete(context.Background(), hi, nil, 10)
			results <- err
		}()
	}
	time.Sleep(150 * time.Millisecond)
	if up.inflight.Load() != 2 {
		t.Fatalf("in flight %d, want the limit 2", up.inflight.Load())
	}
	// The admission queue (2 slots + 2 waiting) is full: refused at once.
	if _, err := p.Complete(context.Background(), hi, nil, 10); !errors.Is(err, ErrBusy) {
		t.Fatalf("full queue: %v", err)
	}
	// The two waiting ones give up after the queue wait.
	time.Sleep(600 * time.Millisecond)
	close(up.hold)
	wg.Wait()
	close(results)
	busy, ok := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrBusy):
			busy++
		default:
			t.Fatalf("unexpected %v", err)
		}
	}
	if ok != 2 || busy != 2 || up.peak.Load() != 2 {
		t.Fatalf("ok %d busy %d peak %d", ok, busy, up.peak.Load())
	}
}

func TestBusyUpstreamSpillsToNext(t *testing.T) {
	plan := newFake(t, "hold")
	plan.hold = make(chan struct{})
	api := newFake(t, "ok")
	p := newPool(nil, plan, api)
	go p.Complete(context.Background(), hi, nil, 10)
	go p.Complete(context.Background(), hi, nil, 10)
	time.Sleep(100 * time.Millisecond)
	c, err := p.Complete(context.Background(), hi, nil, 10)
	if err != nil || c.Upstream != "api" {
		t.Fatalf("spill: %+v %v", c, err)
	}
	close(plan.hold)
}

type budget struct {
	used, limit int64
}

func (b *budget) Check(context.Context) error {
	if b.limit > 0 && b.used >= b.limit {
		return errors.New("budget")
	}
	return nil
}
func (b *budget) Charge(_ context.Context, n int64) error { b.used += n; return nil }

func TestRunToolLoop(t *testing.T) {
	step := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		step++
		w.Header().Set("Content-Type", "application/json")
		if step == 1 {
			if len(req.Tools) != 1 {
				t.Errorf("tools not offered: %d", len(req.Tools))
			}
			w.Write([]byte(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"echo","arguments":"{\"x\":1}"}},{"id":"c2","type":"function","function":{"name":"shell","arguments":"{}"}}]}}],"usage":{"total_tokens":100}}`))
			return
		}
		last := req.Messages[len(req.Messages)-1]
		prev := req.Messages[len(req.Messages)-2]
		if prev.Content != `{"got":{"x":1}}` || last.Content == "" || last.ToolCallID != "c2" {
			t.Errorf("tool results not returned: %+v %+v", prev, last)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"done"}}],"usage":{"prompt_tokens":5,"completion_tokens":7}}`))
	}))
	defer srv.Close()
	p := NewPool(PoolConfig{Upstreams: []UpstreamConfig{{Name: "u", BaseURL: srv.URL, Model: "m", APIKey: "k"}}})
	b := &budget{}
	res, err := p.Run(context.Background(), Request{System: "s", User: "u", MaxSteps: 4, MaxTokens: 100, Budget: b,
		Tools: []Tool{{Name: "echo", Parameters: map[string]any{"type": "object"},
			Run: func(_ context.Context, a json.RawMessage) (any, error) {
				return map[string]json.RawMessage{"got": a}, nil
			}}}})
	if err != nil || res.Answer != "done" || res.Steps != 2 || res.Tokens != 112 || b.used != 112 {
		t.Fatalf("run: %+v %v used %d", res, err, b.used)
	}
	b2 := &budget{limit: 10, used: 10}
	if _, err := p.Run(context.Background(), Request{System: "s", User: "u", MaxSteps: 4, Budget: b2}); err == nil {
		t.Fatal("exhausted budget not refused")
	}
}
