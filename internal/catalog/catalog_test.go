package catalog

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/org"
	"github.com/ANetResearch/anet-org/internal/store"
)

func TestCatalogFitsCardLimits(t *testing.T) {
	es := Entries("Example Lab", "LAB")
	if err := Check(es); err != nil {
		t.Fatal(err)
	}
	if len(es) != 29 {
		t.Fatalf("%d entries", len(es))
	}
	for _, e := range es {
		if !strings.Contains(e.Description, "Arguments") && !strings.HasPrefix(e.ID, "admin.") {
			t.Errorf("%s: description does not state its arguments", e.ID)
		}
		if !strings.Contains(strings.Join(e.Examples, " "), "--capability "+e.ID) {
			t.Errorf("%s: no anet delegate example", e.ID)
		}
	}
}

func TestBlocks(t *testing.T) {
	es := Entries("Example Lab", "LAB")
	b := Blocks(es, Options{Socket: "/run/user/1000/anet-org/backend.sock", TokenFile: "/home/org/.config/anet-org/token"})
	raw, _ := json.Marshal(b)
	var v struct {
		Inbound struct {
			Policy string           `json:"policy"`
			Public []map[string]any `json:"public_capabilities"`
			Admit  any              `json:"admit"`
		} `json:"inbound"`
		Modules struct {
			Service struct {
				TokenFile string           `json:"token_file"`
				Caps      []map[string]any `json:"capabilities"`
			} `json:"service"`
		} `json:"modules"`
		AutoReply any `json:"auto_reply"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	// org.admit is declared to the service module but is no public capability.
	if v.Inbound.Policy != "closed" || len(v.Inbound.Public) != len(es)-1 || len(v.Modules.Service.Caps) != len(es) {
		t.Fatalf("blocks: %s", raw)
	}
	if v.Inbound.Admit != nil || v.AutoReply != nil {
		t.Fatal("agent blocks without an agent")
	}
	urls := map[string]string{}
	for _, c := range v.Modules.Service.Caps {
		urls[c["id"].(string)] = c["url"].(string)
	}
	for _, p := range v.Inbound.Public {
		id := p["id"].(string)
		if id == "org.admit" {
			t.Fatal("org.admit is public")
		}
		if strings.HasPrefix(id, "admin.") != (p["allow_only"] == true) {
			t.Errorf("%s allow_only %v", id, p["allow_only"])
		}
		if urls[id] != "unix:///run/user/1000/anet-org/backend.sock:/v1/"+id {
			t.Errorf("url %s", urls[id])
		}
	}
	pub := Blocks(es, Options{Socket: "/s", PublicAdmin: true})
	raw, _ = json.Marshal(pub)
	if strings.Contains(string(raw), "allow_only") {
		t.Fatal("public-admin still allow_only")
	}
}

func TestDiscardDeclared(t *testing.T) {
	for _, e := range Entries("Example Lab", "LAB") {
		want := e.ID == "org.join" || e.ID == "ask" || strings.HasPrefix(e.ID, "msg.") ||
			strings.HasPrefix(e.ID, "board.") || strings.HasPrefix(e.ID, "research.")
		if e.Discard != want {
			t.Errorf("%s: discard %v", e.ID, e.Discard)
		}
	}
}

func newService(t *testing.T) *org.Service {
	t.Helper()
	cfg, err := config.Parse([]byte(`{"org":{"name":"Example Lab","prefix":"LAB"},"socket":"/tmp/x/b.sock",
		"token_file":"/tmp/x/t","database":"` + filepath.Join(t.TempDir(), "org.db") + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return org.New(cfg, db, org.Options{})
}

// The capabilities declared agent_tool are exactly those the service lets
// the organisation's agent call, and the internal one is org.admit.
func TestAgentToolsDeclared(t *testing.T) {
	svc := newService(t)
	es := Entries("Example Lab", "LAB")
	var tools, ids []string
	for _, e := range es {
		ids = append(ids, e.ID)
		if e.AgentTool {
			tools = append(tools, e.ID)
		}
		if e.Internal != org.Internal(e.ID) {
			t.Errorf("%s: internal %v", e.ID, e.Internal)
		}
	}
	sort.Strings(tools)
	sort.Strings(ids)
	if !reflect.DeepEqual(tools, svc.AgentTools()) {
		t.Fatalf("agent tools: catalog %v, service %v", tools, svc.AgentTools())
	}
	if !reflect.DeepEqual(ids, svc.Capabilities()) {
		t.Fatalf("catalog %v, service %v", ids, svc.Capabilities())
	}
	for _, id := range tools {
		if strings.HasPrefix(id, "admin.") || id == "org.join" || id == "org.leave" || id == "org.rename" || id == "ask" {
			t.Errorf("%s must not be an agent tool", id)
		}
	}
	b := Blocks(es, Options{Socket: "/s"})
	raw, _ := json.Marshal(b)
	var v struct {
		Modules struct {
			Service struct {
				Caps []struct {
					ID          string          `json:"id"`
					Description string          `json:"description"`
					AgentTool   bool            `json:"agent_tool"`
					Internal    bool            `json:"internal"`
					InputSchema json.RawMessage `json:"input_schema"`
				} `json:"capabilities"`
			} `json:"service"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range v.Modules.Service.Caps {
		if c.Internal != (c.ID == "org.admit") {
			t.Errorf("%s: internal %v", c.ID, c.Internal)
		}
		if !c.AgentTool {
			if c.InputSchema != nil {
				t.Errorf("%s: input schema without agent_tool", c.ID)
			}
			continue
		}
		n++
		var sch struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Additional *bool                      `json:"additionalProperties"`
		}
		if err := json.Unmarshal(c.InputSchema, &sch); err != nil || sch.Type != "object" || sch.Properties == nil ||
			sch.Additional == nil || *sch.Additional || c.Description == "" {
			t.Errorf("%s: schema %s", c.ID, c.InputSchema)
		}
	}
	if n != len(tools) {
		t.Fatalf("%d agent tools in the blocks, %d in the catalog", n, len(tools))
	}
}

// Every property an input schema names is an argument the capability
// accepts: a call with all of them set is never refused as unknown_field
// or for a wrong type.
func TestInputSchemasMatchTheArguments(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	const aid = "aid-alice-0000000001"
	codes, _, err := svc.CreateInvites(ctx, "operator", org.InviteOptions{Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Dispatch(ctx, "org.join", org.Caller{AID: aid}, json.RawMessage(`{"code":"`+codes[0]+`","handle":"alice"}`)); err != nil {
		t.Fatal(err)
	}
	sample := func(p map[string]any) any {
		if en, ok := p["enum"].([]any); ok {
			return en[0]
		}
		switch ty := p["type"].(type) {
		case string:
			switch ty {
			case "string":
				return "graph-learning"
			case "integer":
				return 1
			case "boolean":
				return true
			case "array":
				it, _ := p["items"].(map[string]any)
				if it["type"] == "integer" {
					return []int{1}
				}
				return []string{"cuda"}
			}
		case []any:
			return 1
		}
		t.Fatalf("no sample for %v", p)
		return nil
	}
	for _, e := range Entries("Example Lab", "LAB") {
		if !e.AgentTool {
			continue
		}
		var sch struct {
			Properties map[string]map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(e.InputSchema, &sch); err != nil {
			t.Fatal(err)
		}
		args := map[string]any{}
		for k, p := range sch.Properties {
			args[k] = sample(p)
		}
		raw, _ := json.Marshal(args)
		_, err := svc.Dispatch(ctx, e.ID, org.Caller{AID: aid, Via: org.ViaAgent}, raw)
		if oe := org.AsError(err); oe != nil && (oe.Code == org.CodeUnknownField || oe.Code == org.CodeBadArgs ||
			oe.Code == org.CodeAgentNotPermitted || oe.Code == org.CodeInternal) {
			t.Errorf("%s %s: %s: %s", e.ID, raw, oe.Code, oe.Message)
		}
	}
}

func TestAgentBlocks(t *testing.T) {
	es := Entries("Example Lab", "LAB")
	a := &Agent{Name: "hermes", Admitted: "direct", AdmitCacheSeconds: 30, RunnerSocket: "/run/anet-runner/runner.sock",
		RunnerUser: "anet-runner", ProxyListen: "127.0.0.1:47811", SystemPrompt: "prompt",
		Upstreams: []Upstream{{Name: "plan", APIBase: "https://model.example/plan/v3", APIKeyFile: "/k/plan"},
			{Name: "api", APIBase: "https://model.example/v3", APIKeyFile: "/k/api"}}}
	raw, _ := json.Marshal(Blocks(es, Options{Socket: "/s", Agent: a}))
	var v map[string]map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if adm, _ := json.Marshal(v["inbound"]["admit"]); string(adm) != `{"cache_seconds":30,"capability":"org.admit"}` {
		t.Fatalf("admit: %s", adm)
	}
	ar, _ := json.Marshal(v["auto_reply"])
	want := `{"admitted":"direct","agent":"hermes","backend":"exec","model_proxy":{"listen":"127.0.0.1:47811","upstreams":[` +
		`{"api_base":"https://model.example/plan/v3","api_key_file":"/k/plan","name":"plan"},` +
		`{"api_base":"https://model.example/v3","api_key_file":"/k/api","name":"api"}]},` +
		`"runner":{"expected_user":"anet-runner","socket":"/run/anet-runner/runner.sock"},"system_prompt":"prompt","untrusted":"off"}`
	if string(ar) != want {
		t.Fatalf("auto_reply:\n%s\nwant\n%s", ar, want)
	}
}
