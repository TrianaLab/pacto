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
