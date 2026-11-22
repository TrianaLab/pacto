package fleet

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const carriedDigest = "sha256:7c1d2e3f4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455"

// carriedRev is fieldsRev as a CI publish that carried fields: no
// declaration block or cluster read of its own, its record citing what cite
// sets.
func carriedRev(t *testing.T, cite func(*contract.InferredInputs)) RawRevision {
	t.Helper()
	raw := fieldsRev(t, fieldsOpts{})
	rec, err := contract.ReadInferredRecord(raw.Bundle.FS)
	if err != nil {
		t.Fatal(err)
	}
	// A CI publish reads no cluster: only the folder's facts stay.
	rec.Declaration, rec.Inputs.Cluster = nil, nil
	rec.Fields = slices.DeleteFunc(rec.Fields, func(f contract.Inferred) bool { return clusterFact(f.Source) })
	cite(&rec.Inputs)
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatalf("citation does not parse: %v", err)
	}
	raw.Bundle.FS.(fstest.MapFS)[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	return raw
}

func declarationCite(in *contract.InferredInputs) {
	in.Declaration = &contract.CitedDeclaration{Record: "ghcr.io/acme/invoices@" + carriedDigest, Signer: "ana@acme.com (https://accounts.google.com)",
		Fields: "dependencies[ledger].compatibility,service.owner"}
}

func TestFieldsCarriedFromDeclaration(t *testing.T) {
	f := fieldsOfInvoices(t, NewQuery(handSnap(t, []RawRevision{carriedRev(t, declarationCite)}, nil, false)))
	want := ", carried from ghcr.io/acme/invoices@sha256:7c1d2e3f4a5b"
	for _, field := range []string{"service.owner", "dependencies[ledger].compatibility"} {
		r := fieldNamed(t, f, field)
		if r.Provenance != FieldDeclared || r.By != "" || !strings.HasPrefix(r.Source, "pacto.yaml of ") || !strings.HasSuffix(r.Source, want) {
			t.Errorf("%s: %+v", field, r)
		}
	}
	if r := fieldNamed(t, f, "dependencies[audit].compatibility"); strings.Contains(r.Source, "carried") {
		t.Errorf("audit was not carried: %+v", r)
	}
}

func TestFieldsCarriedFromAcceptance(t *testing.T) {
	f := fieldsOfInvoices(t, NewQuery(handSnap(t, []RawRevision{carriedRev(t, func(in *contract.InferredInputs) {
		in.Acceptances = []contract.CitedAcceptance{
			{Record: "ghcr.io/acme/invoices@sha256:" + strings.Repeat("0", 64), Signer: "ana@acme.com", Fields: "break ledger@3.0.0"},
			{Record: "ghcr.io/acme/invoices@" + carriedDigest, Signer: "ana@acme.com", Fields: "dependencies[audit].ref, dependencies[ledger]"},
		}
	})}, nil, false)))
	r := fieldNamed(t, f, "dependencies[ledger].compatibility")
	if r.Provenance != FieldDeclared || !strings.HasSuffix(r.Source, ", carried from ghcr.io/acme/invoices@sha256:7c1d2e3f4a5b") {
		t.Errorf("ledger %+v", r)
	}
	if r := fieldNamed(t, f, "service.owner"); strings.Contains(r.Source, "carried") {
		t.Errorf("owner was not carried: %+v", r)
	}
}

func TestFieldsNotCarriedUnchanged(t *testing.T) {
	f := fieldsOfInvoices(t, NewQuery(handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: anaSigned()})}, nil, false)))
	for _, r := range f.Items {
		if r.Provenance == FieldDeclared && r.Proposed == "" && r.Source != "pacto.yaml of invoices@sha256:invoices-0.1.0" {
			t.Errorf("source %+v", r)
		}
	}
	if carriedFrom(nil, "service.owner") != "" {
		t.Error("a nil record carried a field")
	}
}

func TestFieldsCarriedRestricted(t *testing.T) {
	full := NewQuery(handSnap(t, []RawRevision{carriedRev(t, declarationCite)}, []RawTarget{workload("billing", "invoices")}, true))
	want := fieldNamed(t, fieldsOfInvoices(t, full), "service.owner").Source
	f := fieldsOfInvoices(t, full.Restrict(inBilling, noDeployments))
	for _, r := range f.Items {
		if clusterFact(r.Source) {
			t.Errorf("a restricted viewer sees %+v", r)
		}
	}
	if got := fieldNamed(t, f, "service.owner").Source; got != want {
		t.Errorf("restricted source %q, want %q", got, want)
	}
}
