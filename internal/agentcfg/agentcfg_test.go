package agentcfg

import (
	"strings"
	"testing"
)

func TestHermesDefaults(t *testing.T) {
	c := Hermes(HermesOptions{})
	if strings.Count(c, `"`+DefaultModel+`"`) != 4 || !strings.Contains(c, `command: "anet"`) {
		t.Fatalf("defaults:\n%s", c)
	}
	// Nothing in the fragment is a key: the key reaches the agent only as
	// the run's ANET_MODEL_KEY.
	if strings.Contains(c, "api_key") || strings.Count(c, "key_env: ANET_MODEL_KEY") != 2 {
		t.Fatalf("keys:\n%s", c)
	}
}

func TestPromptNamesTheOrganisation(t *testing.T) {
	p := Prompt("USTC")
	if !strings.HasPrefix(p, "You are the organisation agent of USTC ") || strings.Count(p, "USTC") < 3 {
		t.Fatal(p)
	}
}
