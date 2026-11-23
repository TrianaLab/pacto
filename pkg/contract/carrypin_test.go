package contract

import (
	"strings"
	"testing"
)

func TestPinServiceOwnerAddsBlock(t *testing.T) {
	raw := "pactoVersion: \"2.5\"\nservice:\n  name: invoices\n"
	out, err := PinServiceOwner([]byte(raw), Owner{Team: "billing", DRI: "ana@acme.com", Contacts: []OwnerContact{{Type: "email", Value: "b@acme.com"}}})
	want := "pactoVersion: \"2.5\"\nservice:\n  owner:\n    team: billing\n    dri: ana@acme.com\n    contacts:\n      - type: email\n        value: b@acme.com\n  name: invoices\n"
	if err != nil || string(out) != want {
		t.Fatalf("%v\n%s", err, out)
	}
	if !Declares(out, "service.owner") {
		t.Fatal("not declared after pin")
	}
}

func TestPinServiceOwnerCreatesService(t *testing.T) {
	for raw, want := range map[string]string{
		"pactoVersion: \"2.5\"\n": "service:\n  owner:\n    team: billing\npactoVersion: \"2.5\"\n",
		"":                        "service:\n  owner:\n    team: billing\n",
		"{pactoVersion: \"2.5\"}": "{pactoVersion: \"2.5\", service: {owner: {team: billing}}}\n",
		"service: {name: a}\n":    "service: {name: a, owner: {team: billing}}\n",
	} {
		out, err := PinServiceOwner([]byte(raw), Owner{Team: "billing"})
		if err != nil || string(out) != want {
			t.Errorf("%q: %v\n%s", raw, err, out)
		}
	}
}

func TestPinServiceOwnerRefusesExisting(t *testing.T) {
	for _, raw := range []string{"service:\n  owner: {team: x}\n", "[1]\n", "service: 3\n", "a: [\n"} {
		if _, err := PinServiceOwner([]byte(raw), Owner{Team: "billing"}); err == nil {
			t.Errorf("%q pinned", raw)
		}
	}
}

func TestPinServiceOwnerKeepsComments(t *testing.T) {
	raw := "# the invoices service\nservice:\n  # its name\n  name: invoices # inline\n"
	out, err := PinServiceOwner([]byte(raw), Owner{Team: "billing"})
	if err != nil || !strings.Contains(string(out), "# the invoices service\n") || !strings.Contains(string(out), "  name: invoices # inline\n") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestAddDependencyAppends(t *testing.T) {
	raw := "service:\n  name: invoices\ndependencies:\n  - name: tax\n"
	out, err := AddDependency([]byte(raw), Dependency{Name: "ledger", Ref: "oci://r.io/ledger", Compatibility: "^2.3.0"})
	if err != nil || !Declares(out, "dependencies[ledger].compatibility") || !Declares(out, "dependencies[tax]") ||
		!strings.Contains(string(out), `ref: "oci://r.io/ledger"`) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestAddDependencyCreatesSequence(t *testing.T) {
	raw := "service:\n  name: invoices\n"
	out, err := AddDependency([]byte(raw), Dependency{Name: "ledger", Compatibility: "^2.3.0"})
	want := raw + "dependencies:\n  - name: \"ledger\"\n    compatibility: \"^2.3.0\"\n"
	if err != nil || string(out) != want {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = AddDependency([]byte("{service: {name: a}}"), Dependency{Name: "ledger", Compatibility: "^2"})
	if err != nil || !Declares(out, "dependencies[ledger].compatibility") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestAddDependencyRefusesDuplicate(t *testing.T) {
	for _, raw := range []string{"dependencies:\n  - name: ledger\n", "[1]\n", "dependencies: 3\n", "a: [\n"} {
		if _, err := AddDependency([]byte(raw), Dependency{Name: "ledger", Compatibility: "^2"}); err == nil {
			t.Errorf("%q added", raw)
		}
	}
}

func TestDeclaresKeyForms(t *testing.T) {
	raw := []byte("service:\n  owner: {team: a}\ndependencies:\n  - name: ledger\n    compatibility: unknown\n  - name: tax\n")
	for field, want := range map[string]bool{
		"service.owner": true, "dependencies[ledger]": true, "dependencies[ledger].compatibility": true,
		"dependencies[tax]": true, "dependencies[tax].compatibility": false, "dependencies[gone]": false,
		"dependencies[ledger].ref": false, "service.name": false,
	} {
		if Declares(raw, field) != want {
			t.Errorf("%s: want %v", field, want)
		}
	}
	if Declares([]byte("a: [\n"), "service.owner") || Declares([]byte("[1]"), "service.owner") {
		t.Fatal("a broken or non-mapping contract declares nothing")
	}
}

func TestDeclaresIgnoresParseDefault(t *testing.T) {
	raw := []byte("pactoVersion: \"2.5\"\nservice:\n  name: invoices\ndependencies:\n  - name: ledger\n")
	c, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Dependencies[0].Compatibility != CompatibilityUnknown || Declares(raw, "dependencies[ledger].compatibility") {
		t.Fatalf("%+v", c.Dependencies)
	}
}
