package invite

import (
	"strings"
	"testing"
)

func TestNewAndParseRoundTrip(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := New("LAB")
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != len("LAB-XXXX-XXXX") || !strings.HasPrefix(c, "LAB-") || c[8] != '-' {
			t.Fatalf("bad display form %q", c)
		}
		body, err := Parse("LAB", c)
		if err != nil {
			t.Fatalf("parse %q: %v", c, err)
		}
		if Display("LAB", body) != c {
			t.Fatalf("display %q != %q", Display("LAB", body), c)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestFormatKnownValue(t *testing.T) {
	if got := Format("LAB", [5]byte{0, 0, 0, 0, 0}); got != "LAB-0000-0000" {
		t.Fatal(got)
	}
	if got := Format("LAB", [5]byte{0xff, 0xff, 0xff, 0xff, 0xff}); got != "LAB-ZZZZ-ZZZZ" {
		t.Fatal(got)
	}
}

func TestParseTolerant(t *testing.T) {
	want := "7K2M9XQD"
	for _, in := range []string{
		"LAB-7K2M-9XQD", "lab-7k2m-9xqd", "LAB 7K2M 9XQD", "  LAB_7K2M_9XQD ",
		"LAB7K2M9XQD", "7K2M-9XQD", "7k2m9xqd", "LAB-7K2M-9XQD\n", "lab.7k2m.9xqd",
	} {
		got, err := Parse("LAB", in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v", in, got, err)
		}
	}
	// Crockford aliases.
	got, err := Parse("LAB", "LAB-OIL0-0000")
	if err != nil || got != "01100000" {
		t.Errorf("aliases: %q %v", got, err)
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"", "LAB", "LAB-7K2M", "LAB-7K2M-9XQDX", "LAB-7K2M-9XQU", "OTHER-7K2M-9XQD",
		"LAB-7K2M-9XQ!", strings.Repeat("A", 100), "LAB-7K2M-9XQD-0000",
	} {
		if _, err := Parse("LAB", in); err == nil {
			t.Errorf("Parse(%q) accepted", in)
		}
	}
}

func TestHashSeparatesPrefixes(t *testing.T) {
	a := Hash("LAB", "7K2M9XQD")
	b := Hash("ORG", "7K2M9XQD")
	if Equal(a.Hash, b.Hash) || a.Lookup == b.Lookup {
		t.Fatal("prefix not bound into hash")
	}
	if !Equal(a.Hash, Hash("LAB", "7K2M9XQD").Hash) {
		t.Fatal("hash not stable")
	}
	if Equal(a.Hash, a.Hash[:10]) {
		t.Fatal("short digest compared equal")
	}
}

func TestValidPrefix(t *testing.T) {
	for _, p := range []string{"LAB", "ORG", "A1", "ABCDEFGHIJ"} {
		if !ValidPrefix(p) {
			t.Errorf("%q rejected", p)
		}
	}
	for _, p := range []string{"", "A", "1AB", "lab", "ABCDEFGHIJK", "A-B"} {
		if ValidPrefix(p) {
			t.Errorf("%q accepted", p)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"LAB-7K2M-9XQD", "lab7k2m9xqd", "", "LAB--", "ÄÖÜ", "LAB-OIL0-0000"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		body, err := Parse("LAB", in)
		if err != nil {
			return
		}
		if len(body) != BodyLen {
			t.Fatalf("body %q has length %d", body, len(body))
		}
		for i := 0; i < len(body); i++ {
			if !strings.ContainsRune(alphabet, rune(body[i])) {
				t.Fatalf("body %q has non-alphabet char", body)
			}
		}
		// The canonical form parses to itself.
		again, err := Parse("LAB", Display("LAB", body))
		if err != nil || again != body {
			t.Fatalf("canonical form %q did not round-trip: %q %v", body, again, err)
		}
	})
}
