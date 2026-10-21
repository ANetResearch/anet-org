package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogFitsCardLimits(t *testing.T) {
	es := Entries("Example Lab", "LAB")
	if err := Check(es); err != nil {
		t.Fatal(err)
	}
	if len(es) != 25 {
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
		} `json:"inbound"`
		Modules struct {
			Service struct {
				TokenFile string           `json:"token_file"`
				Caps      []map[string]any `json:"capabilities"`
			} `json:"service"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Inbound.Policy != "closed" || len(v.Inbound.Public) != len(es) || len(v.Modules.Service.Caps) != len(es) {
		t.Fatalf("blocks: %s", raw)
	}
	for i, p := range v.Inbound.Public {
		id := p["id"].(string)
		if strings.HasPrefix(id, "admin.") != (p["allow_only"] == true) {
			t.Errorf("%s allow_only %v", id, p["allow_only"])
		}
		u := v.Modules.Service.Caps[i]["url"].(string)
		if u != "unix:///run/user/1000/anet-org/backend.sock:/v1/"+id {
			t.Errorf("url %s", u)
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
