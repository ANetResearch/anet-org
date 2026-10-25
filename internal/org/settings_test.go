package org

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ANetResearch/anet-org/internal/config"
)

func TestAgentSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "agent-profile.json")
	e := newEnv(t, func(c *config.Config) { c.AgentProfileFile = file })
	e.join(aidAlice, "alice")

	// Defaults, and administrators only.
	got := e.ok(aidAdmin, "admin.settings.get", nil)
	if str(got["brief"]) != "" || str(got["model"]) != ModelPlan {
		t.Fatalf("defaults: %v", got)
	}
	e.fail(aidAlice, "admin.settings.get", nil, CodeNotAdmin)
	e.fail(aidAlice, "admin.settings.set", map[string]any{"model": "api"}, CodeNotAdmin)

	// Validation.
	e.fail(aidAdmin, "admin.settings.set", map[string]any{}, CodeMissingField)
	e.fail(aidAdmin, "admin.settings.set", map[string]any{"model": "gpt"}, CodeInvalidValue)
	e.fail(aidAdmin, "admin.settings.set", map[string]any{"brief": strings.Repeat("x", MaxBrief+1)}, CodeTooLong)
	e.fail(aidAdmin, "admin.settings.set", map[string]any{"brief": "a\x00b"}, CodeInvalidValue)

	// A change is stored and written to the profile file, 0640.
	set := e.ok(aidAdmin, "admin.settings.set", map[string]any{"brief": "Answer in Chinese.\nCite sources.", "model": "api"})
	if str(set["brief"]) != "Answer in Chinese.\nCite sources." || str(set["model"]) != ModelAPI || str(set["updated_by"]) != aidAdmin {
		t.Fatalf("set: %v", set)
	}
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("profile mode %v", st.Mode().Perm())
	}
	var p map[string]any
	data, _ := os.ReadFile(file)
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if p["organisation"] != "Example Lab" || p["model"] != "api" || p["brief"] != "Answer in Chinese.\nCite sources." {
		t.Fatalf("profile: %v", p)
	}

	// Only the given field changes.
	e.ok(aidAdmin, "admin.settings.set", map[string]any{"model": "plan"})
	got = e.ok(aidAdmin, "admin.settings.get", nil)
	if str(got["brief"]) != "Answer in Chinese.\nCite sources." || str(got["model"]) != ModelPlan {
		t.Fatalf("partial set: %v", got)
	}

	// The agent, even acting for an administrator, cannot change its own settings.
	if _, err := e.callVia(aidAdmin, ViaAgent, "admin.settings.set", map[string]any{"model": "api"}); err == nil || err.Code != CodeAgentNotPermitted {
		t.Fatalf("the agent changed the settings: %v", err)
	}

	// WriteProfile restores a removed file.
	os.Remove(file)
	if err := e.svc.WriteProfile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
}

func TestAdminMemberList(t *testing.T) {
	e := newEnv(t, nil)
	e.join(aidAlice, "alice")
	e.join(aidBob, "bob")
	e.fail(aidAlice, "admin.member.list", nil, CodeNotAdmin)
	ms := list(e.ok(aidAdmin, "admin.member.list", nil)["members"])
	if len(ms) != 2 || str(obj(ms[0])["aid"]) != aidAlice || str(obj(ms[1])["handle"]) != "bob" {
		t.Fatalf("members: %v", ms)
	}
	q := list(e.ok(aidAdmin, "admin.member.list", map[string]any{"query": "bo"})["members"])
	if len(q) != 1 || str(obj(q[0])["aid"]) != aidBob {
		t.Fatalf("query: %v", q)
	}
	e.fail(aidAdmin, "admin.member.list", map[string]any{"query": strings.Repeat("q", 65)}, CodeTooLong)
}
