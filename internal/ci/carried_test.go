package ci

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCarriedRedacted(t *testing.T) {
	const secret = "https://u:tok@h.example/x?token=s"
	p := &Plan{Rows: []Row{{Impact: Impact{Gate: "clear"}, Carried: []Carried{{Field: "service.owner", Value: secret, Reason: secret, Signer: secret, Record: secret}}}}}
	p.Settle("repo")
	c := p.Rows[0].Carried[0]
	for _, s := range []string{c.Value, c.Reason, c.Signer, c.Record} {
		if strings.Contains(s, "tok") || !strings.Contains(s, "https://h.example/x (redacted)") {
			t.Errorf("not redacted: %q", s)
		}
	}
}

func TestCarriedJSON(t *testing.T) {
	out, _ := json.Marshal(Row{Carried: []Carried{{Field: "service.owner", Value: "billing", From: "registry", Kind: "declared"}}})
	if !strings.Contains(string(out), `"carried":[{"field":"service.owner","value":"billing","from":"registry","kind":"declared"}]`) {
		t.Fatalf("%s", out)
	}
	if out, _ := json.Marshal(Row{}); strings.Contains(string(out), "carried") {
		t.Fatalf("%s", out)
	}
}

// declRef is a declaration bundle whose rev prints as sha256:7c1d2e3f4a5b.
const declRef = "r.io/acme/invoices@sha256:7c1d2e3f4a5b" + "00000000000000000000000000000000000000000000000000000"

func carriedRow(cs ...Carried) Row {
	return Row{Service: "invoices", Next: "0.2.0", Baseline: "0.1.0", BaselineDigest: "sha256:7c1d2e3f4a5b99", Carried: cs}
}

func TestCarriedLinesCarried(t *testing.T) {
	r := carriedRow(
		Carried{Field: "service.owner", Value: "billing", From: "registry", Kind: "declared", Record: declRef, State: CarriedCarried},
		Carried{Field: "dependencies[ledger].compatibility", Value: "^2.3.0", From: "registry", Kind: "declared", Record: declRef, State: CarriedCarried})
	r.Notes = []string{"a note"}
	got := footer(&Plan{Rows: []Row{r}})
	want := []string{
		"invoices: a note",
		"invoices: owner: billing, declared in registry (rev sha256:7c1d2e3f4a5b)",
		"invoices: dependencies.ledger.compatibility: ^2.3.0, accepted in registry (rev sha256:7c1d2e3f4a5b)",
		"invoices: service.version: 0.2.0, from pacto.yaml",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestCarriedLinesWholeEntry(t *testing.T) {
	got := carriedLines(carriedRow(Carried{Field: "dependencies[ledger]", Value: "^2.3.0 (oci://ghcr.io/acme/ledger)", Record: declRef, State: CarriedCarried}))
	if got[0] != "dependencies.ledger: ^2.3.0 (oci://ghcr.io/acme/ledger), accepted in registry (rev sha256:7c1d2e3f4a5b)" {
		t.Fatalf("%q", got)
	}
}

func TestCarriedLinesOverridden(t *testing.T) {
	got := carriedLines(carriedRow(Carried{Field: "service.owner", Value: "payments", From: "pacto.yaml", Record: declRef, State: CarriedOverridden, Reason: "registry declares billing"}))
	if got[0] != "owner: payments, from pacto.yaml (registry declares billing, rev sha256:7c1d2e3f4a5b)" {
		t.Fatalf("%q", got)
	}
}

func TestCarriedLinesDropped(t *testing.T) {
	got := carriedLines(carriedRow(Carried{Field: "service.owner", Value: "billing", From: "registry", Record: declRef, State: CarriedDropped, Reason: "no owner policy readable from CI"}))
	if got[0] != "owner: billing, declared in registry (rev sha256:7c1d2e3f4a5b), not carried: no owner policy readable from CI" {
		t.Fatalf("%q", got)
	}
}

func TestCarriedLinesDerivedVersion(t *testing.T) {
	r := carriedRow(Carried{Field: "service.owner", Value: "billing", Record: declRef, State: CarriedCarried})
	r.Derived = true
	if got := carriedLines(r); got[1] != "service.version: 0.2.0, derived from 0.1.0 (rev sha256:7c1d2e3f4a5b)" {
		t.Fatalf("%q", got)
	}
	r.Next, r.To = "", "0.3.0" // publish mode names the version published
	if got := carriedLines(r); got[1] != "service.version: 0.3.0, derived from 0.1.0 (rev sha256:7c1d2e3f4a5b)" {
		t.Fatalf("%q", got)
	}
	r.Next, r.To = "", ""
	if got := carriedLines(r); len(got) != 1 {
		t.Fatalf("%q", got)
	}
}

func TestCarriedLinesNoneNoVersionLine(t *testing.T) {
	if got := carriedLines(carriedRow()); got != nil {
		t.Fatalf("%q", got)
	}
	// A record with no digest prints the record itself.
	if got := carriedLines(carriedRow(Carried{Field: "service.owner", Value: "billing", Record: "acc-1", State: CarriedCarried})); got[0] != "owner: billing, declared in registry (rev acc-1)" {
		t.Fatalf("%q", got)
	}
}

func TestCarriedLinesMarkdownAreCodeSpans(t *testing.T) {
	p := &Plan{Rows: []Row{carriedRow(Carried{Field: "service.owner", Value: "bill`ing|x", Record: declRef, State: CarriedCarried})}}
	md := Markdown(p)
	if !strings.Contains(md, "- `invoices: owner: bill'ing|x, declared in registry (rev sha256:7c1d2e3f4a5b)`\n") {
		t.Fatalf("%s", md)
	}
}
