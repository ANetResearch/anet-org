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
	var out bytes.Buffer
	if err := cmdGenConfig([]string{"--config", cfg, "--token-file", "${CREDENTIALS_DIRECTORY}/token"}, &out); err != nil {
		t.Fatal(err)
	}
	var v map[string]map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	svc := v["modules"]["service"].(map[string]any)
	if svc["token_file"] != "${CREDENTIALS_DIRECTORY}/token" || len(svc["capabilities"].([]any)) != 23 {
		t.Fatalf("gen-config: %s", out.String())
	}
	if !strings.Contains(out.String(), `"url": "unix:///run/user/1000/anet-org/backend.sock:/v1/org.join"`) {
		t.Fatal("socket URL")
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
