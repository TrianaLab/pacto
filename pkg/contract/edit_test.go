package contract

import (
	"strings"
	"testing"
)

func TestSetServiceVersionReplaces(t *testing.T) {
	for raw, want := range map[string]string{
		"pactoVersion: \"2.5\"\nservice:\n  name: a\n  version: 1.4.0\n":      "pactoVersion: \"2.5\"\nservice:\n  name: a\n  version: 1.5.0\n",
		"service:\n  version: \"1.4.0\"\n  name: a\n":                         "service:\n  version: \"1.5.0\"\n  name: a\n",
		"service:\n  version: '1.4.0'\n":                                      "service:\n  version: \"1.5.0\"\n",
		"service: {name: a, version: 1.4.0}\n":                                "service: {name: a, version: 1.5.0}\n",
		"service:\n  name: a\n  version: >-\n    1.4.0\n":                     "service:\n  name: a\n  version: 1.5.0\n",
		"service:\n  name: a\n  version: \"1.4.0\" # pinned\nworkload: job\n": "service:\n  name: a\n  version: \"1.5.0\" # pinned\nworkload: job\n",
	} {
		got, err := SetServiceVersion([]byte(raw), "1.5.0")
		if err != nil || string(got) != want {
			t.Errorf("%q: %v\n%s", raw, err, got)
		}
	}
	if _, err := SetServiceVersion([]byte("a: [\n"), "1.0.0"); err == nil {
		t.Fatal("malformed yaml")
	}
}

func TestSetServiceVersionInserts(t *testing.T) {
	got, err := SetServiceVersion([]byte("service:\n  name: a\n"), "0.1.0")
	if err != nil || string(got) != "service:\n  version: 0.1.0\n  name: a\n" {
		t.Fatalf("%v\n%s", err, got)
	}
}

func TestSetServiceVersionKeepsComments(t *testing.T) {
	raw := "# Written by pacto init.\nservice:\n  # the name\n  name: a\n  version: 1.0.0   # derived\n"
	got, err := SetServiceVersion([]byte(raw), "1.1.0")
	if err != nil || string(got) != strings.Replace(raw, "1.0.0", "1.1.0", 1) {
		t.Fatalf("%v\n%s", err, got)
	}
}

func TestSetCompatibility(t *testing.T) {
	raw := "dependencies:\n  # inferred\n  - name: ledger\n    compatibility: unknown   # proposed ^2.3.0\n  - name: audit\n    compatibility: unknown\n"
	got, err := SetCompatibility([]byte(raw), "ledger", "^2.3.0")
	if err != nil || string(got) != strings.Replace(raw, "unknown   #", "\"^2.3.0\"   #", 1) {
		t.Fatalf("%v\n%s", err, got)
	}
	flow := "dependencies: [{name: ledger, compatibility: unknown}]\n"
	if got, err := SetCompatibility([]byte(flow), "ledger", ">=1.0.0 <2.0.0"); err != nil || string(got) != "dependencies: [{name: ledger, compatibility: \">=1.0.0 <2.0.0\"}]\n" {
		t.Fatalf("%v\n%s", err, got)
	}
	for _, c := range []struct{ raw, name string }{
		{raw, "missing"},
		{"dependencies:\n  - name: ledger\n    compatibility: ^1.0.0\n", "ledger"},
		{"dependencies: {}\n", "ledger"},
		{"dependencies:\n  - compatibility: unknown\n", "ledger"},
		{"a: [\n", "ledger"},
	} {
		if _, err := SetCompatibility([]byte(c.raw), c.name, "^1.0.0"); err == nil {
			t.Errorf("%q accepted", c.raw)
		}
	}
}

func TestUnsetCompatibility(t *testing.T) {
	raw := "dependencies:\n  - name: ledger\n    compatibility: \"^2.3.0\"   # proposed ^2.3.0\n"
	got, err := UnsetCompatibility([]byte(raw), "ledger", "^2.3.0")
	if err != nil || string(got) != strings.Replace(raw, "\"^2.3.0\"   #", "unknown   #", 1) {
		t.Fatalf("%v\n%s", err, got)
	}
	// Only the range named is given up.
	if _, err := UnsetCompatibility([]byte(raw), "ledger", "^2.4.0"); err == nil || !strings.Contains(err.Error(), "compatibility: ^2.4.0") {
		t.Fatal(err)
	}
}

func TestSetCompatibilityNotAsWritten(t *testing.T) {
	// yaml.v3 counts columns in runes: a line with a wider rune before the
	// value does not hold it at that byte, so the contract is re-encoded.
	raw := "dependencies: [{note: \"ñññ\", name: ledger, compatibility: unknown}]\n"
	got, err := SetCompatibility([]byte(raw), "ledger", "^2.0.0")
	if err != nil || !strings.Contains(string(got), "compatibility: \"^2.0.0\"") || !strings.Contains(string(got), "ñññ") {
		t.Fatalf("%v\n%s", err, got)
	}
}

func TestRemoveServiceOwner(t *testing.T) {
	for raw, want := range map[string]string{
		"service:\n  name: a\n  owner:\n    team: shop\n# deps\nworkload: service\n": "service:\n  name: a\n# deps\nworkload: service\n",
		"service:\n  owner: {team: shop}\n  name: a\n":                               "service:\n  name: a\n",
		"service: {name: a, owner: {team: shop}}\n":                                  "service: {name: a}\n",
		"service:\n  name: a\n":                                                      "service:\n  name: a\n",
		"service:\n  name: owner\n  owner:\n    team: payments\n":                    "service:\n  name: owner\n",
		"workload: job\n": "workload: job\n",
		"":                "",
	} {
		got, err := RemoveServiceOwner([]byte(raw))
		if err != nil || string(got) != want {
			t.Errorf("%q: %v\n%s", raw, err, got)
		}
	}
	if _, err := RemoveServiceOwner([]byte("a: [\n")); err == nil {
		t.Fatal("malformed yaml")
	}
}
