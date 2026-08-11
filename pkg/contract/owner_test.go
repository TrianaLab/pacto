package contract

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOwner_IsEmpty(t *testing.T) {
	if !(Owner{}).IsEmpty() {
		t.Fatal("zero Owner should be empty")
	}
	if (Owner{Team: "team/x"}).IsEmpty() {
		t.Fatal("owner with team should not be empty")
	}
	if (Owner{Contacts: []OwnerContact{{Type: "email", Value: "a@b.c"}}}).IsEmpty() {
		t.Fatal("owner with contacts should not be empty")
	}
}

func TestOwner_DisplayString(t *testing.T) {
	if got := (Owner{Team: "team/x", DRI: "ed"}).DisplayString(); got != "team/x" {
		t.Fatalf("want team, got %q", got)
	}
	if got := (Owner{DRI: "ed"}).DisplayString(); got != "ed" {
		t.Fatalf("want dri fallback, got %q", got)
	}
	if got := (Owner{}).DisplayString(); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestOwner_Equal(t *testing.T) {
	a := Owner{Team: "t", DRI: "d", Contacts: []OwnerContact{{Type: "email", Value: "x"}}}
	b := Owner{Team: "t", DRI: "d", Contacts: []OwnerContact{{Type: "email", Value: "x"}}}
	if !a.Equal(b) {
		t.Fatal("identical owners should be equal")
	}
	if a.Equal(Owner{Team: "t"}) {
		t.Fatal("different contacts should not be equal")
	}
	// Same length contacts but different values
	c := Owner{Team: "t", DRI: "d", Contacts: []OwnerContact{{Type: "email", Value: "y"}}}
	if a.Equal(c) {
		t.Fatal("owners with same length but different contact values should not be equal")
	}
}

func TestOwner_MatchesFilter(t *testing.T) {
	o := Owner{Team: "Payments", DRI: "Eduardo", Contacts: []OwnerContact{{Type: "chat", Value: "#pay"}}}
	for _, q := range []string{"pay", "eduardo", "#pay"} {
		if !o.MatchesFilter(q) {
			t.Fatalf("expected match for %q", q)
		}
	}
	if o.MatchesFilter("nope") {
		t.Fatal("unexpected match")
	}
}

// IsKey and MatchesFilter answer two different questions, and the pair `team-a` /
// `team-a-platform` is where the difference stops being academic: a substring
// query matches both, and only one of them IS `team-a`.
func TestOwner_IsKeyIsExactCanonicalIdentity(t *testing.T) {
	a := Owner{Team: "team-a"}
	collider := Owner{Team: "team-a-platform"}
	if !a.IsKey("team-a") {
		t.Fatal("an owner must be its own canonical key")
	}
	if collider.IsKey("team-a") {
		t.Fatal("team-a-platform is a different owner from team-a")
	}
	if !collider.MatchesFilter("team-a") {
		t.Fatal("free-text owner search must still discover the substring collider")
	}
	// Case and contacts belong to search, never to identity: a key is compared as
	// the snapshot spells it, and a contact value is not an owner name.
	if a.IsKey("Team-A") {
		t.Fatal("canonical identity is not case-insensitive")
	}
	paged := Owner{Contacts: []OwnerContact{{Type: "chat", Value: "#team-a"}}}
	if paged.IsKey("#team-a") {
		t.Fatal("a contact value is not a canonical owner key")
	}
	if (Owner{}).IsKey("") {
		t.Fatal("an undeclared owner has no canonical key, so it matches none")
	}
	// The DRI fallback is the canonical key when no team is declared, exactly as
	// DisplayString reports it.
	if !(Owner{DRI: "alice"}).IsKey("alice") {
		t.Fatal("the DRI fallback is the canonical key when no team is declared")
	}
}

func TestOwner_YAMLRoundTrip(t *testing.T) {
	in := []byte("team: team/payments\ndri: eduardo.diaz\ncontacts:\n  - type: email\n    value: pay@acme.com\n    purpose: ownership\n")
	var o Owner
	if err := yaml.Unmarshal(in, &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if o.Team != "team/payments" || o.DRI != "eduardo.diaz" || len(o.Contacts) != 1 {
		t.Fatalf("bad parse: %+v", o)
	}
	out, err := yaml.Marshal(o)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt Owner
	if err := yaml.Unmarshal(out, &rt); err != nil || !o.Equal(rt) {
		t.Fatalf("round trip mismatch: %v / %+v", err, rt)
	}
}

func TestOwner_StringRejected(t *testing.T) {
	var o Owner
	err := yaml.Unmarshal([]byte("just-a-string\n"), &o)
	if err == nil {
		t.Fatal("string owner must no longer unmarshal into Owner")
	}
}
