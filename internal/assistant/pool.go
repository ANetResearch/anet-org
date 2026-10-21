// Package assistant talks to OpenAI-compatible chat-completions endpoints
// and runs the tool loop of the ask capability.
//
// The package has no shell, no file access and no network access beyond the
// configured upstream URLs: its HTTP client uses no proxy, follows no
// redirect, and is only ever handed an upstream's own URL. The tools it runs
// are closures the caller supplies, already scoped to one member.
//
// Load handling. Upstreams are tried in the configured order. A 429, a 5xx
// or a timeout fails the request over to the next upstream and puts the
// failing one on a cool-down: an exhausted quota (a 429 whose error names a
// quota) for QuotaCooldown, or until the reset time the message states; any
// other failure for an exponential back-off from BackoffBase. Each upstream
// has its own concurrency limit, and a global admission queue bounds the
// requests that wait for a slot; a request that cannot be admitted, or
// waits longer than QueueWait, is refused with ErrBusy.
package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors returned by Complete.
var (
	// ErrBusy: the admission queue is full, or no upstream slot became free
	// within the queue wait.
	ErrBusy = errors.New("assistant: busy")
	// ErrUnavailable: every upstream is cooling down or failed this request.
	ErrUnavailable = errors.New("assistant: no upstream available")
	// ErrRejected: an upstream refused the request itself (a 4xx other than
	// 429), which another upstream would refuse too.
	ErrRejected = errors.New("assistant: request rejected by the upstream")
)

// UpstreamConfig is one endpoint with its key already read.
type UpstreamConfig struct {
	Name           string
	BaseURL        string
	Model          string
	APIKey         string
	MaxConcurrency int
}

// PoolConfig configures a Pool.
type PoolConfig struct {
	Upstreams     []UpstreamConfig
	Timeout       time.Duration // one HTTP request
	QueueSize     int           // requests that may wait beyond the upstreams' slots
	QueueWait     time.Duration // the longest wait for a slot
	QuotaCooldown time.Duration
	BackoffBase   time.Duration
	BackoffMax    time.Duration
	Now           func() time.Time
	Logger        *slog.Logger
	// ResetZone is the time zone of a reset time stated without one.
	ResetZone *time.Location
}

// Pool is the set of upstreams with their health and limits.
type Pool struct {
	cfg   PoolConfig
	ups   []*upstream
	admit chan struct{}
	freed chan struct{}
	http  *http.Client
	now   func() time.Time
	log   *slog.Logger
}

type upstream struct {
	UpstreamConfig
	sem chan struct{}

	mu        sync.Mutex
	coolUntil time.Time
	failures  int
}

// NewPool builds a pool. With no upstream it is a pool that is not
// configured.
func NewPool(c PoolConfig) *Pool {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if c.Timeout <= 0 {
		c.Timeout = 90 * time.Second
	}
	if c.QueueWait <= 0 {
		c.QueueWait = 30 * time.Second
	}
	if c.QuotaCooldown <= 0 {
		c.QuotaCooldown = time.Hour
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 30 * time.Second
	}
	if c.BackoffMax <= 0 {
		c.BackoffMax = 15 * time.Minute
	}
	if c.ResetZone == nil {
		c.ResetZone = time.FixedZone("UTC+8", 8*3600)
	}
	p := &Pool{cfg: c, now: c.Now, log: c.Logger, freed: make(chan struct{}, 1)}
	total := 0
	for _, u := range c.Upstreams {
		if u.MaxConcurrency <= 0 {
			u.MaxConcurrency = 8
		}
		total += u.MaxConcurrency
		p.ups = append(p.ups, &upstream{UpstreamConfig: u, sem: make(chan struct{}, u.MaxConcurrency)})
	}
	p.admit = make(chan struct{}, total+max(c.QueueSize, 0))
	p.http = &http.Client{
		Transport: &http.Transport{
			Proxy:               nil, // never through a proxy: only the configured URLs
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 15 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are not followed") },
	}
	return p
}

// Configured reports whether the pool has an upstream.
func (p *Pool) Configured() bool { return p != nil && len(p.ups) > 0 }

// ChatMessage is one message of the conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a call the model asks for.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolSpec is a tool as the API sees it.
type ToolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

// Usage is what the API reports for one completion.
type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// Tokens is the number counted against budgets: the total, which includes
// the completion and its reasoning; prompt plus completion when the API
// gives no total.
func (u Usage) Tokens() int64 {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.PromptTokens + u.CompletionTokens
}

// Completion is the part of a reply the loop uses. reasoning_content is
// not read.
type Completion struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
	Upstream  string
	// MaybeBilled is set on an error when an attempt may have been served
	// and billed by an upstream without its usage reaching us (a timeout
	// or a reply that broke off), so that a reserved budget is kept.
	MaybeBilled bool
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Tools     []ToolSpec    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   *string    `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

const maxResponseBytes = 4 << 20

// Complete sends one chat completion to the first healthy upstream with a
// free slot, failing over as described in the package comment.
func (p *Pool) Complete(ctx context.Context, msgs []ChatMessage, tools []ToolSpec, maxTokens int) (Completion, error) {
	if !p.Configured() {
		return Completion{}, ErrUnavailable
	}
	select {
	case p.admit <- struct{}{}:
	default:
		p.log.Warn("assistant queue full")
		return Completion{}, ErrBusy
	}
	defer func() { <-p.admit }()

	deadline := p.now().Add(p.cfg.QueueWait)
	tried := map[*upstream]bool{}
	billed := false
	fail := func(err error) (Completion, error) { return Completion{MaybeBilled: billed}, err }
	for {
		anyBusy := false
		for _, u := range p.ups {
			if tried[u] || u.cooling(p.now()) {
				continue
			}
			select {
			case u.sem <- struct{}{}:
			default:
				anyBusy = true
				continue
			}
			tried[u] = true
			c, maybe, err := p.try(ctx, u, msgs, tools, maxTokens)
			billed = billed || maybe
			<-u.sem
			select {
			case p.freed <- struct{}{}:
			default:
			}
			if err == nil {
				return c, nil
			}
			if errors.Is(err, ErrRejected) {
				return fail(err)
			}
			if ctx.Err() != nil {
				return fail(ctx.Err())
			}
			// A failure that may be the upstream's: the next one is tried.
		}
		if !anyBusy {
			return fail(ErrUnavailable)
		}
		wait := deadline.Sub(p.now())
		if wait <= 0 {
			p.log.Warn("assistant queue wait exceeded")
			return fail(ErrBusy)
		}
		t := time.NewTimer(min(wait, 250*time.Millisecond))
		select {
		case <-ctx.Done():
			t.Stop()
			return fail(ctx.Err())
		case <-p.freed:
			t.Stop()
		case <-t.C:
		}
	}
}

func (u *upstream) cooling(now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return now.Before(u.coolUntil)
}

// CoolingUntil reports an upstream's cool-down end (for tests and status).
func (p *Pool) CoolingUntil(name string) time.Time {
	for _, u := range p.ups {
		if u.Name == name {
			u.mu.Lock()
			defer u.mu.Unlock()
			return u.coolUntil
		}
	}
	return time.Time{}
}

func (p *Pool) markOK(u *upstream) {
	u.mu.Lock()
	u.failures = 0
	u.coolUntil = time.Time{}
	u.mu.Unlock()
}

// markTransient applies exponential back-off, at least retryAfter.
func (p *Pool) markTransient(u *upstream, retryAfter time.Duration) time.Duration {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.failures++
	d := p.cfg.BackoffBase
	for i := 1; i < u.failures && d < p.cfg.BackoffMax; i++ {
		d *= 2
	}
	d = min(d, p.cfg.BackoffMax)
	if retryAfter > d {
		d = min(retryAfter, p.cfg.BackoffMax)
	}
	u.coolUntil = p.now().Add(d)
	return d
}

func (p *Pool) markQuota(u *upstream, until time.Time) time.Duration {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := p.now()
	d := p.cfg.QuotaCooldown
	if !until.IsZero() && until.After(now) && until.Sub(now) < 31*24*time.Hour {
		d = until.Sub(now)
	}
	u.coolUntil = now.Add(d)
	u.failures++
	return d
}

// try makes one request to u. maybeBilled reports a failure after which the
// upstream may have served (and billed) the request.
//
// The request runs under its own deadline, detached from the caller's
// context: a caller who gives up while the model works does not stop the
// request, so its usage is still read and charged.
func (p *Pool) try(ctx context.Context, u *upstream, msgs []ChatMessage, tools []ToolSpec, maxTokens int) (c Completion, maybeBilled bool, err error) {
	body, err := json.Marshal(chatRequest{Model: u.Model, Messages: msgs, Tools: tools, MaxTokens: maxTokens})
	if err != nil {
		return Completion{}, false, err
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, u.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Completion{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+u.APIKey)
	started := p.now()
	resp, err := p.http.Do(req)
	if err != nil {
		reason := transportReason(err)
		d := p.markTransient(u, 0)
		p.log.Warn("assistant upstream failed", "upstream", u.Name, "status", 0, "reason", reason,
			"cooldown_s", int(d.Seconds()), "ms", time.Since(started).Milliseconds())
		return Completion{}, reason == "timeout", fmt.Errorf("upstream %s: transport error", u.Name)
	}
	defer resp.Body.Close()
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	ms := time.Since(started).Milliseconds()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		code, msg := errorOf(raw)
		if quotaExhausted(code, msg) {
			d := p.markQuota(u, p.resetTime(msg))
			p.log.Warn("assistant upstream quota exhausted", "upstream", u.Name, "status", 429, "code", code,
				"cooldown_s", int(d.Seconds()), "ms", ms)
		} else {
			d := p.markTransient(u, retryAfter(resp.Header.Get("Retry-After")))
			p.log.Warn("assistant upstream throttled", "upstream", u.Name, "status", 429, "code", code,
				"cooldown_s", int(d.Seconds()), "ms", ms)
		}
		return Completion{}, false, fmt.Errorf("upstream %s: HTTP 429", u.Name)
	case resp.StatusCode >= 500:
		d := p.markTransient(u, 0)
		p.log.Warn("assistant upstream error", "upstream", u.Name, "status", resp.StatusCode,
			"cooldown_s", int(d.Seconds()), "ms", ms)
		return Completion{}, false, fmt.Errorf("upstream %s: HTTP %d", u.Name, resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// A key the upstream does not accept: this upstream is unusable for
		// now, the next may not be.
		code, _ := errorOf(raw)
		d := p.markQuota(u, time.Time{})
		p.log.Error("assistant upstream refused the key", "upstream", u.Name, "status", resp.StatusCode, "code", code,
			"cooldown_s", int(d.Seconds()))
		return Completion{}, false, fmt.Errorf("upstream %s: HTTP %d", u.Name, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		code, _ := errorOf(raw)
		p.log.Warn("assistant upstream rejected the request", "upstream", u.Name, "status", resp.StatusCode, "code", code, "ms", ms)
		return Completion{}, false, fmt.Errorf("upstream %s: HTTP %d: %w", u.Name, resp.StatusCode, ErrRejected)
	}
	if rerr != nil {
		d := p.markTransient(u, 0)
		p.log.Warn("assistant upstream reply broke off", "upstream", u.Name, "status", resp.StatusCode, "cooldown_s", int(d.Seconds()))
		return Completion{}, true, fmt.Errorf("upstream %s: reply broke off", u.Name)
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil || len(cr.Choices) == 0 {
		d := p.markTransient(u, 0)
		p.log.Warn("assistant upstream reply unreadable", "upstream", u.Name, "status", resp.StatusCode, "cooldown_s", int(d.Seconds()))
		return Completion{}, true, fmt.Errorf("upstream %s: unreadable reply", u.Name)
	}
	p.markOK(u)
	m := cr.Choices[0].Message
	c = Completion{ToolCalls: m.ToolCalls, Usage: cr.Usage, Upstream: u.Name}
	if m.Content != nil {
		c.Content = *m.Content
	}
	p.log.Info("assistant upstream answered", "upstream", u.Name, "status", resp.StatusCode, "ms", ms,
		"tokens", cr.Usage.Tokens(), "tool_calls", len(m.ToolCalls))
	return c, false, nil
}

func transportReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
		return "timeout"
	}
	return "connection"
}

// errorOf reads {"error": {"code", "message"}} (or a string error).
func errorOf(raw []byte) (code, msg string) {
	var v struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &v) != nil || len(v.Error) == 0 {
		return "", ""
	}
	var o struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	if json.Unmarshal(v.Error, &o) == nil {
		switch c := o.Code.(type) {
		case string:
			code = c
		case float64:
			code = strconv.FormatFloat(c, 'f', -1, 64)
		}
		if code == "" {
			code = o.Type
		}
		return sanitizeCode(code), o.Message
	}
	var s string
	if json.Unmarshal(v.Error, &s) == nil {
		return "", s
	}
	return "", ""
}

// sanitizeCode keeps an upstream's error code loggable.
func sanitizeCode(c string) string {
	var b strings.Builder
	for _, r := range c {
		if b.Len() >= 64 {
			break
		}
		if r == '_' || r == '.' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func quotaExhausted(code, msg string) bool {
	c := strings.ToLower(code)
	if strings.Contains(c, "quota") || strings.Contains(c, "insufficient") || strings.Contains(c, "exceeded") && !strings.Contains(c, "rate") {
		return true
	}
	m := strings.ToLower(msg)
	return strings.Contains(m, "quota") && !strings.Contains(m, "rate limit")
}

var (
	resetRFC3339 = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
	resetPlain   = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}`)
)

// resetTime finds a reset time stated in an error message: an RFC 3339
// time, or a "YYYY-MM-DD hh:mm:ss" time in ResetZone.
func (p *Pool) resetTime(msg string) time.Time {
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	if s := resetRFC3339.FindString(msg); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	if s := resetPlain.FindString(msg); s != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.Replace(s, "T", " ", 1), p.cfg.ResetZone); err == nil {
			return t
		}
	}
	return time.Time{}
}

func retryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if n, err := strconv.Atoi(h); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		return time.Until(t)
	}
	return 0
}
