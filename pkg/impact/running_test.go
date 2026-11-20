package impact

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ledgerSnap is a fleet where ledger depends on payments with rng and, when
// running, runs on a target; extra are more sources.
func ledgerSnap(t *testing.T, rng string, running bool, extra ...fleet.Source) *fleet.FleetSnapshot {
	t.Helper()
	ledger := svcContract("ledger", "1.0.0", contract.Owner{Team: "money"},
		contract.Dependency{Name: "payments", Ref: "ghcr.io/x/payments:1.0.0", Required: true, Compatibility: rng})
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: mkBundle(svcContract("payments", "1.0.0", contract.Owner{})), Digest: "sha256:p1"}, {Bundle: mkBundle(ledger), Digest: "sha256:l1"}}}
	if running {
		col.Targets = []fleet.RawTarget{{Scope: "prod", Kind: "deployment", Name: "ledger", Service: "ledger", Compliance: fleet.StatusCompliant, EvidenceAt: fresh()}}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, append([]fleet.Source{fleet.NewMemorySource("test", "memory", col)}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// firstVersion analyses payments at version against itself over snap.
func firstVersion(snap *fleet.FleetSnapshot, version string, unread ...string) *Result {
	c := svcContract("payments", version, contract.Owner{})
	return Analyze(context.Background(), c, c, nil, nil, snap, Options{FirstVersion: true, UnreadBaselines: unread})
}

func TestFirstVersionBlocksIncompatibleRunningConsumer(t *testing.T) {
	res := firstVersion(ledgerSnap(t, "^1", true), "2.0.0")
	if res.Gate != GateBlocked || !strings.Contains(res.GateReason, "first version of payments on a known target: running consumers do not accept payments 2.0.0: ledger") ||
		res.GateCode() != GateCodeConsumersIncompatible {
		t.Fatalf("gate %s %q %s", res.Gate, res.GateReason, res.GateCode())
	}
}

func TestFirstVersionClearWhenCompleteAndAccepted(t *testing.T) {
	res := firstVersion(ledgerSnap(t, "^2", true), "2.0.0")
	if res.Gate != GateClear || !strings.Contains(res.GateReason, "every consumer that runs accepts 2.0.0") || res.GateCode() != GateCodeFirstVersionAccepted {
		t.Fatalf("gate %s %q %s", res.Gate, res.GateReason, res.GateCode())
	}
}

func TestFirstVersionNoConsumerClear(t *testing.T) {
	res := firstVersion(ledgerSnap(t, "^1", false), "2.0.0")
	if res.Gate != GateClear || !strings.Contains(res.GateReason, "no consumer runs") {
		t.Fatalf("gate %s %q", res.Gate, res.GateReason)
	}
}

func TestFirstVersionPartialIsUnverified(t *testing.T) {
	stale := fleet.NewFailingSource("down", "memory", context.DeadlineExceeded)
	res := firstVersion(ledgerSnap(t, "^2", true, stale), "2.0.0")
	if res.Gate != GateUnverified || !strings.Contains(res.GateReason, "the answer is partial") || res.GateCode() != GateCodeFirstVersionUnchecked {
		t.Fatalf("gate %s %q %s", res.Gate, res.GateReason, res.GateCode())
	}
	// A running consumer that rejects it still blocks on a partial answer.
	if res := firstVersion(ledgerSnap(t, "^1", true, stale), "2.0.0"); res.Gate != GateBlocked {
		t.Fatalf("partial, incompatible: %s", res.Gate)
	}
}

func TestFirstVersionUnknownRangeIsUnverified(t *testing.T) {
	res := firstVersion(ledgerSnap(t, "", true), "2.0.0")
	if res.Gate != GateUnverified || !strings.Contains(res.GateReason, "running consumers accept 2.0.0: ledger") || res.GateCode() != GateCodeFirstVersionUnchecked {
		t.Fatalf("gate %s %q %s", res.Gate, res.GateReason, res.GateCode())
	}
}

func TestUnreadBaselineNeverClears(t *testing.T) {
	old := svcContract("payments", "1.0.0", contract.Owner{})
	c := svcContract("payments", "1.1.0", contract.Owner{})
	res := Analyze(context.Background(), old, c, nil, nil, ledgerSnap(t, "^1", false), Options{UnreadBaselines: []string{"a", "b", "c", "prod/x runs a revision Pacto could not link"}})
	if res.Classification == "BREAKING" || res.Gate != GateUnverified || !strings.Contains(res.GateReason, "a; b; c and 1 more") || res.GateCode() != GateCodeRunningUnread {
		t.Fatalf("%s gate %s %q %s", res.Classification, res.Gate, res.GateReason, res.GateCode())
	}
}

func TestUnreadBaselineKeepsBlocked(t *testing.T) {
	res := firstVersion(ledgerSnap(t, "^1", true), "2.0.0", "prod/x runs a revision Pacto could not link")
	if res.Gate != GateBlocked || res.GateCode() != GateCodeConsumersIncompatible {
		t.Fatalf("gate %s %s", res.Gate, res.GateCode())
	}
}

func TestFirstVersionPolicyUncheckedIsUnverified(t *testing.T) {
	r := Result{Service: "api", Completeness: fleet.CompletenessComplete, firstVersion: true, hiddenReferrers: 1}
	if g, why := r.gate(); g != GateUnverified || !strings.Contains(why, "first version of api on a known target; ") {
		t.Fatalf("gate %s %q", g, why)
	}
}

func TestFirstVersionNameInferredIsUnverified(t *testing.T) {
	r := Result{Service: "api", Completeness: fleet.CompletenessComplete, firstVersion: true,
		Limitations: []fleet.Limitation{{Code: LimitationServiceNameInferred, Message: "service.name is inferred"}}}
	if g, why := r.gate(); g != GateUnverified || !strings.Contains(why, "service.name is inferred") {
		t.Fatalf("gate %s %q", g, why)
	}
}

func TestResultJSONRunningOmittedWithoutFlag(t *testing.T) {
	b, err := json.Marshal(Result{})
	if err != nil || strings.Contains(string(b), `"running"`) {
		t.Fatalf("%s %v", b, err)
	}
}

func TestResultJSONRunningRefsIsEmptyArray(t *testing.T) {
	b, err := json.Marshal(Result{Running: &RunningBaseline{Refs: []string{}, FirstVersion: true}})
	if err != nil || !strings.Contains(string(b), `"running":{"refs":[],"firstVersion":true}`) {
		t.Fatalf("%s %v", b, err)
	}
}
