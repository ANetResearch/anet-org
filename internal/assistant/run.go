package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Tool is one operation the model may call. Run receives the model's JSON
// arguments and returns a value that is sent back to the model as JSON.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	Run         func(ctx context.Context, args json.RawMessage) (any, error)
}

// Budget is reserved before every model call and settled after it.
type Budget interface {
	// Reserve charges an estimate of a call before it is made, or returns
	// an error when the budget cannot cover it.
	Reserve(ctx context.Context, estimate int64) error
	// Settle replaces a reserved estimate by the tokens actually used.
	Settle(ctx context.Context, estimate, actual int64) error
}

// Estimate is what a call is reserved at: half a token per character of
// the conversation (a generous count for most text) plus the most the
// completion may use.
func Estimate(msgs []ChatMessage, maxTokens int) int64 {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return int64(n/2 + maxTokens)
}

// Request is one ask.
type Request struct {
	System    string
	User      string
	Tools     []Tool
	MaxSteps  int
	MaxTokens int
	Budget    Budget
}

// Result is what the loop produced.
type Result struct {
	Answer    string
	Steps     int
	Tokens    int64
	Upstreams []string
	// Stopped names why the loop ended early: "", "max_steps" or "budget".
	Stopped string
}

// maxToolResultBytes bounds what one tool result adds to the conversation.
const maxToolResultBytes = 16 << 10

// maxToolCallsPerStep bounds the tool calls of one model turn.
const maxToolCallsPerStep = 8

// Run executes the tool loop: the model is called, the tools it asks for
// are run, and their results are returned to it, until it answers without
// a tool call, MaxSteps is reached or the budget runs out. A budget that is
// already exhausted before the first call is returned as the Check error.
func (p *Pool) Run(ctx context.Context, r Request) (Result, error) {
	var res Result
	specs := make([]ToolSpec, 0, len(r.Tools))
	byName := map[string]Tool{}
	for _, t := range r.Tools {
		var s ToolSpec
		s.Type = "function"
		s.Function.Name = t.Name
		s.Function.Description = t.Description
		s.Function.Parameters = t.Parameters
		specs = append(specs, s)
		byName[t.Name] = t
	}
	msgs := []ChatMessage{{Role: "system", Content: r.System}, {Role: "user", Content: r.User}}
	// Budget writes outlive a cancelled caller: what an upstream served is
	// charged whether or not anyone waits for the answer.
	bctx := context.WithoutCancel(ctx)
	for step := 0; step < r.MaxSteps; step++ {
		tools := specs
		if step == r.MaxSteps-1 {
			tools = nil // the last turn must answer
		}
		est := Estimate(msgs, r.MaxTokens)
		if err := r.Budget.Reserve(bctx, est); err != nil {
			if step == 0 {
				return res, err
			}
			res.Stopped = "budget"
			return res, nil
		}
		c, err := p.Complete(ctx, msgs, tools, r.MaxTokens)
		if err != nil {
			kept := int64(0)
			if c.MaybeBilled {
				kept = est
			}
			if serr := r.Budget.Settle(bctx, est, kept); serr != nil {
				return res, serr
			}
			res.Tokens += kept
			return res, err
		}
		res.Steps++
		res.Tokens += c.Usage.Tokens()
		res.Upstreams = append(res.Upstreams, c.Upstream)
		if err := r.Budget.Settle(bctx, est, c.Usage.Tokens()); err != nil {
			return res, err
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if len(c.ToolCalls) == 0 || tools == nil {
			res.Answer = strings.TrimSpace(c.Content)
			return res, nil
		}
		calls := c.ToolCalls
		if len(calls) > maxToolCallsPerStep {
			calls = calls[:maxToolCallsPerStep]
		}
		msgs = append(msgs, ChatMessage{Role: "assistant", Content: c.Content, ToolCalls: calls})
		for _, tc := range calls {
			out := runTool(ctx, byName, tc)
			msgs = append(msgs, ChatMessage{Role: "tool", ToolCallID: tc.ID, Content: out})
		}
	}
	res.Stopped = "max_steps"
	return res, nil
}

func runTool(ctx context.Context, byName map[string]Tool, tc ToolCall) string {
	t, ok := byName[tc.Function.Name]
	if !ok {
		return `{"error":"unknown_tool","message":"No tool has this name."}`
	}
	args := json.RawMessage(tc.Function.Arguments)
	if strings.TrimSpace(tc.Function.Arguments) == "" {
		args = json.RawMessage("{}")
	}
	if !json.Valid(args) {
		return `{"error":"bad_args","message":"The arguments are not valid JSON."}`
	}
	v, err := t.Run(ctx, args)
	if err != nil {
		var coded interface{ ErrorCode() (string, string) }
		if errors.As(err, &coded) {
			code, msg := coded.ErrorCode()
			b, _ := json.Marshal(map[string]string{"error": code, "message": msg})
			return string(b)
		}
		return `{"error":"tool_failed","message":"The operation failed."}`
	}
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"tool_failed","message":"The result could not be encoded."}`
	}
	if len(b) > maxToolResultBytes {
		b, _ = json.Marshal(map[string]any{"truncated": true,
			"partial": string(b[:maxToolResultBytes])})
	}
	return string(b)
}
