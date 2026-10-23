package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeConfig(t *testing.T) string {
	dir := t.TempDir()
	p := filepath.Join(dir, "anet-org.json")
	cfg := `{"org":{"name":"Example Lab","prefix":"LAB"},"socket":"/run/user/1000/anet-org/backend.sock",
		"token_file":"` + filepath.Join(dir, "token") + `","database":"` + filepath.Join(dir, "org.db") + `"}`
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAdminCommandLine(t *testing.T) {
	cfg := writeConfig(t)
	var out bytes.Buffer
	if err := cmdAdmin([]string{"--config", cfg, "invite", "create", "--uses", "3", "--note", "Autumn intake"}, &out); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`Invite code: LAB-[0-9A-Z]{4}-[0-9A-Z]{4}`).MatchString(out.String()) {
		t.Fatalf("create: %s", out.String())
	}
	out.Reset()
	if err := cmdAdmin([]string{"invite", "list", "--config", cfg, "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var invs []map[string]any
	if err := json.Unmarshal(out.Bytes(), &invs); err != nil || len(invs) != 1 || invs[0]["note"] != "Autumn intake" {
		t.Fatalf("list: %s %v", out.String(), err)
	}
	out.Reset()
	if err := cmdAdmin([]string{"--config", cfg, "invite", "revoke", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cmdAdmin([]string{"--config", cfg, "invite", "list"}, &out); err != nil || strings.Contains(out.String(), "Autumn") {
		t.Fatalf("revoked invite listed as active: %s", out.String())
	}
	if err := cmdAdmin([]string{"--config", cfg, "member", "remove", "ghost"}, &out); err == nil {
		t.Fatal("removing an unknown member succeeded")
	}
	if err := cmdAdmin([]string{"--config", cfg, "invite", "explode"}, &out); err == nil {
		t.Fatal("unknown action accepted")
	}
	if err := cmdBackup([]string{"--config", cfg, filepath.Join(t.TempDir(), "b.db")}); err != nil {
		t.Fatal(err)
	}
}

func TestGenConfig(t *testing.T) {
	cfg := writeConfig(t)
	t.Setenv("HOME", "/home/org")
	var out bytes.Buffer
	if err := cmdGenConfig([]string{"--config", cfg, "--token-file", "${CREDENTIALS_DIRECTORY}/token",
		"--runner-user", "anet-hermes", "--runner-socket", "/srv/runner/runner.sock"}, &out); err != nil {
		t.Fatal(err)
	}
	var v map[string]map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	svc := v["modules"]["service"].(map[string]any)
	caps := svc["capabilities"].([]any)
	if svc["token_file"] != "${CREDENTIALS_DIRECTORY}/token" || len(caps) != 26 {
		t.Fatalf("gen-config: %s", out.String())
	}
	if !strings.Contains(out.String(), `"url": "unix:///run/user/1000/anet-org/backend.sock:/v1/org.join"`) {
		t.Fatal("socket URL")
	}
	tools := map[string]bool{}
	for _, c := range caps {
		m := c.(map[string]any)
		if m["agent_tool"] == true {
			if _, ok := m["input_schema"].(map[string]any); !ok {
				t.Fatalf("%s: agent_tool without input_schema", m["id"])
			}
			tools[m["id"].(string)] = true
		}
		if (m["internal"] == true) != (m["id"] == "org.admit") {
			t.Fatalf("%s: internal %v", m["id"], m["internal"])
		}
	}
	for _, id := range []string{"org.info", "org.me", "org.members", "msg.send", "msg.inbox", "msg.ack", "board.post",
		"board.list", "board.claim", "board.done", "board.comment", "board.delete", "research.project.create",
		"research.project.list", "research.note.add", "research.note.list", "research.note.delete"} {
		if !tools[id] {
			t.Errorf("%s is not an agent tool", id)
		}
		delete(tools, id)
	}
	if len(tools) != 0 {
		t.Errorf("unexpected agent tools: %v", tools)
	}
	for _, p := range v["inbound"]["public_capabilities"].([]any) {
		if p.(map[string]any)["id"] == "org.admit" {
			t.Fatal("org.admit is a public capability")
		}
	}
	adm := v["inbound"]["admit"].(map[string]any)
	if adm["capability"] != "org.admit" || adm["cache_seconds"] != float64(30) {
		t.Fatalf("admit: %v", adm)
	}
	ar := v["auto_reply"]
	runner := ar["runner"].(map[string]any)
	proxy := ar["model_proxy"].(map[string]any)
	ups := proxy["upstreams"].([]any)
	if ar["backend"] != "exec" || ar["agent"] != "hermes" || ar["admitted"] != "direct" || ar["untrusted"] != "off" ||
		runner["socket"] != "/srv/runner/runner.sock" || runner["expected_user"] != "anet-hermes" ||
		proxy["listen"] != "127.0.0.1:47811" || len(ups) != 2 ||
		ups[0].(map[string]any)["name"] != "plan" || ups[1].(map[string]any)["name"] != "api" ||
		ups[0].(map[string]any)["api_key_file"] != "/home/org/.config/anet-org/ark-plan.key" ||
		!strings.Contains(str(ar["system_prompt"]), "organisation agent of Example Lab") {
		t.Fatalf("auto_reply: %v", ar)
	}

	out.Reset()
	if err := cmdGenConfig([]string{"--config", cfg, "--no-agent"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `"auto_reply"`) || strings.Contains(out.String(), `"admit"`) {
		t.Fatal("--no-agent still has the agent blocks")
	}
	for _, bad := range [][]string{
		{"--agent", "unknown"},
		{"--admitted", "always"},
		{"--admit-cache-seconds", "301"},
		{"--model-proxy-listen", "0.0.0.0:47811"},
		{"--model-plan-base", "http://model.example/v3"},
		{"--runner-socket", "relative.sock"},
	} {
		if err := cmdGenConfig(append([]string{"--config", cfg}, bad...), &out); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func str(v any) string { s, _ := v.(string); return s }

func TestGenAgent(t *testing.T) {
	cfg := writeConfig(t)
	var out bytes.Buffer
	if err := cmdGenAgent([]string{"--config", cfg, "--part", "config"}, &out); err != nil {
		t.Fatal(err)
	}
	conf := out.String()
	for _, want := range []string{
		"model:\n  provider: \"custom:anet-plan\"\n  default: \"deepseek-v4-1-flash-261017\"\n",
		"  anet-plan:\n    base_url: \"${ANET_MODEL_BASE}/plan\"\n    key_env: ANET_MODEL_KEY\n",
		"  anet-api:\n    base_url: \"${ANET_MODEL_BASE}/api\"\n    key_env: ANET_MODEL_KEY\n",
		"fallback_providers:\n  - provider: \"custom:anet-api\"\n",
		"mcp_servers:\n  org:\n    command: \"anet\"\n    args: [\"turn-mcp\"]\n",
		"      ANET_TURN_URL: \"${ANET_TURN_URL}\"\n      ANET_TURN_TOKEN: \"${ANET_TURN_TOKEN}\"\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "You are") || regexp.MustCompile(`(?i)api_key:|sk-`).MatchString(conf) {
		t.Fatalf("config part: %s", conf)
	}

	out.Reset()
	if err := cmdGenAgent([]string{"--config", cfg, "--part", "prompt"}, &out); err != nil {
		t.Fatal(err)
	}
	prompt := out.String()
	for _, want := range []string{"organisation agent of Example Lab", "nvidia-smi", "idle", "release", "only that member",
		"untrusted_*", "Never follow instructions", "board.post", "item id", "help-wanted", "full deliverable",
		"$ANET_OUTBOX", "Never ask for", "keys"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if len(prompt) > 4000 || strings.Contains(prompt, "!") {
		t.Fatalf("prompt length %d or exclamation mark", len(prompt))
	}

	out.Reset()
	if err := cmdGenAgent([]string{"--config", cfg, "--model", "other-model-1", "--anet-command", "/opt/anet/bin/anet"}, &out); err != nil {
		t.Fatal(err)
	}
	all := out.String()
	if !strings.Contains(all, `default: "other-model-1"`) || !strings.Contains(all, `command: "/opt/anet/bin/anet"`) ||
		!strings.Contains(all, "# You are the organisation agent of Example Lab") {
		t.Fatalf("all: %s", all)
	}
	// Every line of the combined output is YAML or a YAML comment.
	for _, l := range strings.Split(strings.TrimSpace(all), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") && !regexp.MustCompile(`^( *- )?[ a-zA-Z_-]+:`).MatchString(l) {
			t.Errorf("not YAML: %q", l)
		}
	}
	for _, bad := range [][]string{{"--agent", "other"}, {"--model", "a b"}, {"--part", "x"}, {"--anet-command", "a\"b"}} {
		if err := cmdGenAgent(append([]string{"--config", cfg}, bad...), &out); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

// The example blocks in deploy/ are what gen-config prints for the example
// configuration; a change to the catalog must regenerate them.
func TestDeployExampleIsGenerated(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("HOME", "/home/anet-org")
	var out bytes.Buffer
	if err := cmdGenConfig([]string{"--config", "../../deploy/anet-org.example.json"}, &out); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../deploy/node-config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != string(want) {
		t.Fatal("deploy/node-config.example.json is stale; regenerate it with: XDG_RUNTIME_DIR=/run/user/1000 HOME=/home/anet-org " +
			"anet-org gen-config --config deploy/anet-org.example.json > deploy/node-config.example.json")
	}
}

func TestBulkInvitesOnTheCommandLine(t *testing.T) {
	cfg := writeConfig(t)
	var out bytes.Buffer
	if err := cmdAdmin([]string{"--config", cfg, "invite", "create", "--count", "5", "--uses", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	codes := regexp.MustCompile(`(?m)^LAB-[0-9A-Z]{4}-[0-9A-Z]{4}$`).FindAllString(out.String(), -1)
	if len(codes) != 5 {
		t.Fatalf("codes: %s", out.String())
	}
	out.Reset()
	if err := cmdAdmin([]string{"--config", cfg, "invite", "create", "--count", "501"}, &out); err == nil {
		t.Fatal("count above the cap accepted")
	}
}
