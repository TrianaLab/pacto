package discover

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	indexSrc    = "index:ghcr.io/acme/pacto-index:0123456789abcdef-2.4.0"
	snapSrc     = "snapshot:ghcr.io/acme/pacto-target-snapshots@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	commit      = "0123456789abcdef0123"
	epoch141126 = "1794614400" // 2026-11-14T00:00:00Z
)

func parsed(t *testing.T, yaml string) *contract.Contract {
	t.Helper()
	c, err := contract.Parse(strings.NewReader("pactoVersion: \"2.5\"\nservice: {name: reports, version: 1.0.0}\n" + yaml))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func proposalOf(rec *contract.InferredRecord, key string) (contract.Inferred, bool) {
	if rec == nil {
		return contract.Inferred{}, false
	}
	for _, f := range rec.Fields {
		if f.Field == key {
			return f, true
		}
	}
	return contract.Inferred{}, false
}

const byName = "dependencies:\n  - name: ledger\n    required: true\n"

func TestProposeRefFromIndex(t *testing.T) {
	c := parsed(t, byName)
	rec := Propose(nil, c, ProposeInput{Refs: map[string]Sourced{"ledger": {Value: "oci://ghcr.io/acme/ledger", Source: indexSrc}}})
	f, ok := proposalOf(rec, "dependencies[ledger].ref")
	if !ok || f.Value != "oci://ghcr.io/acme/ledger" || f.Source != indexSrc || f.Declare != "ref: oci://ghcr.io/acme/ledger" ||
		f.Confidence != contract.ConfidenceLikely || rec.Format != contract.InferredRecordFormat {
		t.Fatalf("record = %+v", rec)
	}
	// An index that names nothing for ledger proposes nothing.
	if rec := Propose(nil, c, ProposeInput{Refs: map[string]Sourced{"other": {Value: "oci://x/y", Source: indexSrc}}}); rec != nil {
		t.Fatalf("record = %+v", rec)
	}
}

func TestProposeRefSkipsDeclaredAndExternal(t *testing.T) {
	c := parsed(t, "dependencies:\n  - name: ledger\n    ref: oci://ghcr.io/acme/ledger\n    required: true\n    compatibility: \"^2.0.0\"\n"+
		"  - name: stripe\n    external: true\n    version: 2.0.0\n    required: true\n")
	refs := map[string]Sourced{"ledger": {Value: "oci://x/ledger", Source: indexSrc}, "stripe": {Value: "oci://x/stripe", Source: indexSrc}}
	if rec := Propose(nil, c, ProposeInput{Refs: refs, Running: map[string]Sourced{"stripe": {Value: "2.0.0", Source: snapSrc}}}); rec != nil {
		t.Fatalf("record = %+v", rec)
	}
}

func TestBreakExpiry(t *testing.T) {
	date, src := BreakExpiry(1794614400, commit)
	if date != "2026-12-14" || src != "commit:0123456789ab +30d" {
		t.Fatalf("got %q %q", date, src)
	}
	for _, c := range []struct {
		epoch  int64
		commit string
	}{{0, commit}, {1794614400, ""}, {1794614400, "abc"}} {
		if d, s := BreakExpiry(c.epoch, c.commit); d != "" || s != "" {
			t.Errorf("%+v: %q %q", c, d, s)
		}
	}
}

func TestProposeRangeFromSnapshot(t *testing.T) {
	rec := Propose(nil, parsed(t, byName), ProposeInput{Running: map[string]Sourced{"ledger": {Value: "2.4.0, 2.3.1", Source: snapSrc}}})
	f, ok := proposalOf(rec, "dependencies[ledger].compatibility")
	if !ok || f.Proposed != "^2.3.0" || f.Value != "compatibility unknown" || f.Source != snapSrc || f.Declare != `compatibility: "^2.3.0"` {
		t.Fatalf("record = %+v", rec)
	}
}

// TestProposeRangeNeedsRunningConsumer: with no provider running beside this
// service, or no strict version, nothing is proposed.
func TestProposeRangeNeedsRunningConsumer(t *testing.T) {
	c := parsed(t, byName)
	for _, in := range []ProposeInput{{}, {Running: map[string]Sourced{"ledger": {Value: "latest", Source: snapSrc}}}} {
		if rec := Propose(nil, c, in); rec != nil {
			t.Errorf("%+v: record = %+v", in, rec)
		}
	}
}

func TestProposeRangeSkipsDeclaredRange(t *testing.T) {
	c := parsed(t, "dependencies:\n  - name: ledger\n    compatibility: \"^2.0.0\"\n    required: true\n")
	if rec := Propose(nil, c, ProposeInput{Running: map[string]Sourced{"ledger": {Value: "2.3.1", Source: snapSrc}}}); rec != nil {
		t.Fatalf("record = %+v", rec)
	}
	// An explicit unknown is proposed for.
	c = parsed(t, "dependencies:\n  - name: ledger\n    compatibility: unknown\n    required: true\n")
	if _, ok := proposalOf(Propose(nil, c, ProposeInput{Running: map[string]Sourced{"ledger": {Value: "2.3.1", Source: snapSrc}}}), "dependencies[ledger].compatibility"); !ok {
		t.Fatal("no proposal for compatibility: unknown")
	}
}

const twoBreaks = "breaks:\n  - consumers: [web]\n    reason: drop v1\n  - consumers: [api]\n    reason: drop v2\n    expires: \"2027-01-01\"\n"

func TestProposeBreakExpiryFromEpoch(t *testing.T) {
	rec := Propose(nil, parsed(t, twoBreaks), ProposeInput{Commit: commit, Epoch: epoch141126})
	f, ok := proposalOf(rec, "breaks[0].expires")
	if !ok || f.Value != "2026-12-14" || f.Source != "commit:0123456789ab +30d" || f.Declare != `expires: "2026-12-14"` {
		t.Fatalf("record = %+v", rec)
	}
}

func TestProposeNoEpochWritesNoExpiry(t *testing.T) {
	for _, in := range []ProposeInput{{Commit: commit}, {Commit: commit, Epoch: "soon"}, {Epoch: epoch141126}} {
		if rec := Propose(nil, parsed(t, twoBreaks), in); rec != nil {
			t.Errorf("%+v: record = %+v", in, rec)
		}
	}
}

func TestProposeDeclaredExpiryNotProposed(t *testing.T) {
	rec := Propose(nil, parsed(t, twoBreaks), ProposeInput{Commit: commit, Epoch: epoch141126})
	if _, ok := proposalOf(rec, "breaks[1].expires"); ok || len(rec.Fields) != 1 {
		t.Fatalf("record = %+v", rec)
	}
}

// TestProposeIsDeterministic: the same inputs give the same record bytes,
// the input record is never changed and a field it holds is never replaced.
func TestProposeIsDeterministic(t *testing.T) {
	c := parsed(t, byName+twoBreaks)
	in := ProposeInput{Refs: map[string]Sourced{"ledger": {Value: "oci://ghcr.io/acme/ledger", Source: indexSrc}},
		Running: map[string]Sourced{"ledger": {Value: "2.3.1", Source: snapSrc}}, Commit: commit, Epoch: epoch141126}
	base := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "t", Discovery: "1"},
		Fields: []contract.Inferred{{Field: "dependencies[ledger].ref", Value: "kept", Source: "x"}}}
	a, b := Propose(base, c, in).Marshal(), Propose(base, c, in).Marshal()
	if !bytes.Equal(a, b) || len(base.Fields) != 1 {
		t.Fatalf("not deterministic or input changed:\n%s\n%s", a, b)
	}
	if f, _ := proposalOf(Propose(base, c, in), "dependencies[ledger].ref"); f.Value != "kept" {
		t.Fatalf("ref = %+v", f)
	}
	if _, err := contract.ParseInferredRecord(Propose(&contract.InferredRecord{Format: contract.InferredRecordFormat}, c, in).Marshal()); err != nil {
		t.Fatal(err)
	}
}
