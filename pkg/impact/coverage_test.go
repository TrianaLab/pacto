package impact

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// coverageSnap holds ledger 2.0.0 and one consumer per range: "^2.0.0"
// declared, "unknown" unknown. accepted names the consumers whose range a
// counted acceptance accepts.
func coverageSnap(t *testing.T, ranges map[string]string) *fleet.FleetSnapshot {
	t.Helper()
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: mkBundle(svcContract("ledger", "2.0.0", contract.Owner{Team: "core"}))}}}
	for name, compat := range ranges {
		c := svcContract(name, "1.0.0", contract.Owner{DRI: "ana@acme.com"},
			contract.Dependency{Name: "ledger", Ref: "ghcr.io/x/ledger:2.0.0", Required: true, Compatibility: compat})
		col.Revisions = append(col.Revisions, fleet.RawRevision{Bundle: mkBundle(c), Digest: "sha256:" + name})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// changeLedger analyses ledger 2.0.0 -> 3.0.0 (BREAKING) with opts.
func changeLedger(snap *fleet.FleetSnapshot, opts Options) *Result {
	old := svcContract("ledger", "2.0.0", contract.Owner{Team: "core"})
	old.Interfaces = []contract.Interface{{Name: "api", Type: contract.InterfaceTypeGRPC, Ref: "api.proto"}}
	return Analyze(context.Background(), old, svcContract("ledger", "3.0.0", contract.Owner{Team: "core"}), fstest.MapFS{}, fstest.MapFS{}, snap, opts)
}

// acceptsRange is a counted acceptance of the ledger range for consumer.
func acceptsRange(consumer string) func(fleet.ServiceKey) ([]Acceptance, bool) {
	return func(k fleet.ServiceKey) ([]Acceptance, bool) {
		if _, name := fleet.ParseServiceKey(k); name != consumer {
			return []Acceptance{{State: AcceptRouted, Fields: []string{"dependencies[ledger].compatibility"}}}, true
		}
		return []Acceptance{{State: AcceptCounted, By: "ana@acme.com", Fields: []string{"dependencies[ledger].compatibility"}}}, true
	}
}

func TestCoverageCountsDeclaredAndAccepted(t *testing.T) {
	snap := coverageSnap(t, map[string]string{"reports": "^2.0.0", "billing": "unknown", "audit": "unknown"})
	res := changeLedger(snap, Options{Acceptances: acceptsRange("billing")})
	cov := res.Ranges
	if cov == nil || cov.Edges != 3 || cov.Declared != 1 || cov.Accepted != 1 || cov.UnknownTotal != 1 ||
		len(cov.Unknown) != 1 || cov.Unknown[0] != (UnknownRange{From: "audit", To: "ledger"}) {
		t.Fatalf("ranges = %+v", cov)
	}
}

func TestCoverageListsUnknownBounded(t *testing.T) {
	ranges := map[string]string{}
	for i := range MaxUnknownRanges + 3 {
		ranges["svc"+strconv.Itoa(i)] = "unknown"
	}
	cov := changeLedger(coverageSnap(t, ranges), Options{}).Ranges
	if cov.Edges != MaxUnknownRanges+3 || len(cov.Unknown) != MaxUnknownRanges || cov.UnknownTotal != MaxUnknownRanges+3 {
		t.Fatalf("ranges: %d edges, %d listed of %d", cov.Edges, len(cov.Unknown), cov.UnknownTotal)
	}
}

// TestCoverageProposedFromRecord: an unknown range names the range the
// consumer's newest record proposes; the proposal stays unknown.
func TestCoverageProposedFromRecord(t *testing.T) {
	rec := (&contract.InferredRecord{Format: contract.InferredRecordFormat, Fields: []contract.Inferred{
		{Field: "dependencies[notification-worker].compatibility", Value: "compatibility unknown", Proposed: "^1.0.0", Confidence: contract.ConfidenceLikely,
			Source: "snapshot:ghcr.io/acme/pacto-targets@sha256:" + strings.Repeat("a", 64)},
	}}).Marshal()
	byName := contract.Dependency{Name: "notification-worker", Ref: "oci://x", Required: true, Compatibility: contract.CompatibilityUnknown}
	snap := workerFleet(t, newsletterRev("sha256:news", rec, byName))
	res := breakWorker(snap)
	if got := proposedRange(snap, "newsletter", "dependencies[other].compatibility"); got != "" {
		t.Errorf("another field proposes %q", got)
	}
	if cov := res.Ranges; cov.Accepted != 0 || len(cov.Unknown) != 1 || cov.Unknown[0].Proposed != "^1.0.0" {
		t.Fatalf("ranges = %+v", cov)
	}
	if got := proposedRange(&fleet.FleetSnapshot{}, "x", "f"); got != "" {
		t.Errorf("no revision proposes %q", got)
	}
}

func TestCoverageNoEdges(t *testing.T) {
	cov := changeLedger(coverageSnap(t, nil), Options{}).Ranges
	if cov == nil || cov.Edges != 0 || cov.Scope != "consumers of ledger" {
		t.Fatalf("ranges = %+v", cov)
	}
}

func TestResultNamesCoverageScope(t *testing.T) {
	res := changeLedger(coverageSnap(t, map[string]string{"reports": "^2.0.0"}), Options{})
	if res.Ranges.Scope != "consumers of ledger" || res.Policy == nil || *res.Policy != (Policy{UnknownRange: PolicyWarn, Uncontracted: PolicyWarn}) {
		t.Fatalf("ranges %+v, policy %+v", res.Ranges, res.Policy)
	}
	ev := event(buildAt(t, &fleet.Collection{}), EventUnavailable, "redis")
	if ev.Ranges != nil || ev.Policy != nil {
		t.Errorf("an event answer carries ranges %+v, policy %+v", ev.Ranges, ev.Policy)
	}
}

func TestPolicyUnknownRangeFailBlocks(t *testing.T) {
	snap := coverageSnap(t, map[string]string{"reports": "unknown"})
	before := changeLedger(snap, Options{})
	res := changeLedger(snap, Options{Policy: Policy{UnknownRange: PolicyFail}})
	if before.Gate != GateUnverified || res.Gate != GateBlocked || res.GateCode() != GateCodePolicyUnknownRange ||
		!strings.Contains(res.GateReason, "ledger -> reports range unknown, 1 consumer FAIL (policy unknown-range=fail)") {
		t.Fatalf("before %s; gate %s %s (%s)", before.Gate, res.Gate, res.GateCode(), res.GateReason)
	}
	// An accepted range or an accepted break leaves nothing unknown.
	if res := changeLedger(snap, Options{Policy: Policy{UnknownRange: PolicyFail}, Acceptances: acceptsRange("reports")}); res.Gate == GateBlocked {
		t.Errorf("accepted range: gate %s (%s)", res.Gate, res.GateReason)
	}
	brk := &BreakInput{Announced: map[string]BreakAnnouncement{"reports": {Announcement: announcement, Expires: future}},
		Acceptances: func(fleet.ServiceKey) ([]Acceptance, bool) { return []Acceptance{counted()}, true }}
	if res := changeLedger(snap, Options{Policy: Policy{UnknownRange: PolicyFail}, Breaks: brk}); res.Gate == GateBlocked {
		t.Errorf("accepted break: gate %s (%s)", res.Gate, res.GateReason)
	}
}

func TestPolicyUnknownRangeWarnOnlyNotes(t *testing.T) {
	snap := coverageSnap(t, map[string]string{"reports": "unknown"})
	none := func(fleet.ServiceKey) ([]Acceptance, bool) { return nil, true }
	res := changeLedger(snap, Options{Policy: Policy{UnknownRange: PolicyWarn}, Acceptances: none})
	if res.Gate != GateUnverified || len(res.Notes) != 1 ||
		res.Notes[0] != "POLICY_WARNING: ledger -> reports range unknown, 1 consumer (policy unknown-range=warn)" {
		t.Fatalf("gate %s, notes %q", res.Gate, res.Notes)
	}
	for _, l := range res.Limitations {
		if strings.Contains(l.Code, "POLICY_WARNING") {
			t.Errorf("a warning is a limitation: %+v", l)
		}
	}
}

// uncontracted is a BREAKING answer with one uncontracted caller seen at
// at (nil: no time).
func uncontracted(p Policy, at *time.Time) *Result {
	asOf := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	r := &Result{Classification: "BREAKING", Service: "ledger", AsOf: asOf, Completeness: fleet.CompletenessComplete,
		Consumers: []AffectedConsumer{{Service: "k8s:prod/deployment/batch", Direct: true, Uncontracted: true,
			Evidence: []Evidence{{Kind: EvidenceTarget}, {Kind: EvidenceObservedCall, At: at}}}}}
	r.applyPolicy(p)
	r.Gate, r.GateReason = r.gate()
	return r
}

func TestPolicyUncontractedFailWithinWindow(t *testing.T) {
	p, err := ParsePolicy([]string{"uncontracted=fail:7d"})
	if err != nil {
		t.Fatal(err)
	}
	recent := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	for _, at := range []*time.Time{&recent, nil} {
		r := uncontracted(p, at)
		if r.Gate != GateBlocked || r.GateCode() != GateCodePolicyUncontracted ||
			!strings.Contains(r.GateReason, "1 caller no contract describes calls ledger: k8s:prod/deployment/batch FAIL (policy uncontracted=fail:168h0m0s)") {
			t.Errorf("at %v: gate %s %s (%s)", at, r.Gate, r.GateCode(), r.GateReason)
		}
	}
}

func TestPolicyUncontractedOutsideWindowWarns(t *testing.T) {
	old := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if r := uncontracted(Policy{Uncontracted: PolicyFail, UncontractedWindow: "168h"}, &old); r.Gate == GateBlocked {
		t.Errorf("an old call blocks: %s", r.GateReason)
	}
	r := uncontracted(Policy{}, nil)
	if r.Gate == GateBlocked || len(r.Notes) != 1 || !strings.HasPrefix(r.Notes[0], "POLICY_WARNING: 1 caller no contract describes calls ledger") {
		t.Errorf("warn: gate %s, notes %q", r.Gate, r.Notes)
	}
}

// TestPolicyNeverClears: a fail policy on a change the gate does not hold
// leaves it clear, and a warn never changes a gate.
func TestPolicyNeverClears(t *testing.T) {
	fail := Policy{UnknownRange: PolicyFail, Uncontracted: PolicyFail, UncontractedWindow: "24h"}
	r := &Result{Classification: "NON_BREAKING", Service: "ledger", unknownRanges: []string{"reports"},
		Consumers: []AffectedConsumer{{Service: "reports", Direct: true}}}
	r.applyPolicy(fail)
	if gate, _ := r.gate(); gate != GateClear || len(r.Notes) != 0 {
		t.Errorf("NON_BREAKING with fail: gate %s, notes %q", gate, r.Notes)
	}
	for _, tt := range gateCases() {
		r := tt.r
		r.Consumers = append(r.Consumers, AffectedConsumer{Service: "x", Uncontracted: true, Evidence: []Evidence{{Kind: EvidenceObservedCall}}})
		r.unknownRanges = []string{"x"}
		r.applyPolicy(Policy{})
		if gate, _ := r.gate(); gate == GateBlocked && tt.gate != GateBlocked {
			t.Errorf("%s: warn blocked a %s gate", tt.name, tt.gate)
		}
	}
}

func TestParsePolicy(t *testing.T) {
	p, err := ParsePolicy([]string{"unknown-range=fail", "uncontracted=fail:12h", "uncontracted=warn"})
	if err != nil || p != (Policy{UnknownRange: PolicyFail, Uncontracted: PolicyWarn}) {
		t.Fatalf("ParsePolicy = %+v, %v", p, err)
	}
	for _, bad := range []string{"unknown-range=maybe", "uncontracted=fail", "uncontracted=fail:0d", "uncontracted=fail:-1h", "uncontracted=fail:xd",
		"uncontracted=warn:7d", "coverage=80", "unknown-range"} {
		if _, err := ParsePolicy([]string{bad}); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// TestRangeCoverageNotesUnreadAcceptances: an unknown range counted with no
// acceptance read says so, so "0 accepted" never reads as checked.
func TestRangeCoverageNotesUnreadAcceptances(t *testing.T) {
	res := changeLedger(coverageSnap(t, map[string]string{"reports": "unknown"}), Options{})
	if res.Ranges.Accepted != 0 || len(res.Notes) != 2 || !strings.HasPrefix(res.Notes[0], NoteAcceptancesNotRead+": ") {
		t.Fatalf("ranges %+v, notes %q", res.Ranges, res.Notes)
	}
	// A declared range needs no acceptance: nothing to note.
	if res := changeLedger(coverageSnap(t, map[string]string{"reports": "^2.0.0"}), Options{}); len(res.Notes) != 0 {
		t.Fatalf("notes %q", res.Notes)
	}
}
