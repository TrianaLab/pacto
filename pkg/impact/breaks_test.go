package impact

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// reportsRev is one revision of reports in a break fixture: its version,
// digest, its range on ledger and whether a target runs it.
type reportsRev struct {
	version, digest, compat string
	running                 bool
}

// breakSnap is a fleet holding ledger 2.0.0 and the given revisions of
// reports, which depends on ledger.
func breakSnap(t *testing.T, revs ...reportsRev) *fleet.FleetSnapshot {
	t.Helper()
	col := &fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: mkBundle(svcContract("ledger", "2.0.0", contract.Owner{Team: "core"}))}}}
	for _, r := range revs {
		c := svcContract("reports", r.version, contract.Owner{DRI: "ana@acme.com"},
			contract.Dependency{Name: "ledger", Ref: "ghcr.io/x/ledger:2.0.0", Required: true, Compatibility: r.compat})
		col.Revisions = append(col.Revisions, fleet.RawRevision{Bundle: mkBundle(c), Digest: r.digest})
		if r.running {
			col.Targets = append(col.Targets, fleet.RawTarget{Scope: "payments", Kind: "deployment", Name: "reports", Service: "reports",
				Digest: r.digest, Compliance: fleet.StatusCompliant, EvidenceAt: fresh()})
		}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// breakLedger analyses ledger 2.0.0 -> 3.0.0 (an interface removed) with in.
func breakLedger(snap *fleet.FleetSnapshot, in *BreakInput) *Result {
	old := svcContract("ledger", "2.0.0", contract.Owner{Team: "core"})
	old.Interfaces = []contract.Interface{{Name: "api", Type: contract.InterfaceTypeGRPC, Ref: "api.proto"}}
	return Analyze(context.Background(), old, svcContract("ledger", "3.0.0", contract.Owner{Team: "core"}), fstest.MapFS{}, fstest.MapFS{}, snap, Options{Breaks: in})
}

const announcement = "sha256:ann"

// announced is ledger's break for reports, expiring in the future.
func announced(expires string, accs ...Acceptance) *BreakInput {
	return &BreakInput{Announced: map[string]BreakAnnouncement{"reports": {Reason: "the v1 API is removed", Announcement: announcement, Expires: expires}},
		Acceptances: func(fleet.ServiceKey) ([]Acceptance, bool) { return accs, true }}
}

// counted is ana's counted acceptance of ledger 3.0.0's break.
func counted() Acceptance {
	return Acceptance{State: AcceptCounted, By: "ana@acme.com", Record: "ghcr.io/x/reports@sha256:rec",
		Break: &BreakRef{Provider: "ledger", Version: "3.0.0", Announcement: announcement}}
}

const future = "2999-01-01"

var running14 = reportsRev{"1.4.0", "sha256:r14", "^2.0.0", true}

func reportsOf(t *testing.T, res *Result) AffectedConsumer {
	t.Helper()
	for _, c := range res.Consumers {
		if c.Service == "reports" {
			return c
		}
	}
	t.Fatalf("no reports in %+v", res.Consumers)
	return AffectedConsumer{}
}

func TestBreakAnnouncedIncompatibleRunningBlocks(t *testing.T) {
	res := breakLedger(breakSnap(t, running14), announced(future))
	if c := reportsOf(t, res); res.Gate != GateBlocked || c.Break == nil || c.Break.State != BreakAnnounced || c.Break.Expires != future {
		t.Fatalf("gate %s, break %+v", res.Gate, c.Break)
	}
}

func TestBreakAcceptedDoesNotBlock(t *testing.T) {
	res := breakLedger(breakSnap(t, running14), announced(future, Acceptance{State: AcceptRouted, Reason: "waiting for the owner of reports: ana@acme.com",
		Break: &BreakRef{Provider: "ledger", Version: "3.0.0", Announcement: announcement}}, counted()))
	c := reportsOf(t, res)
	if res.Blocking() != 0 || c.Break.State != BreakAccepted || c.Break.By != "ana@acme.com" || c.Break.Record != "ghcr.io/x/reports@sha256:rec" {
		t.Fatalf("blocking %d, break %+v", res.Blocking(), c.Break)
	}
}

func TestAllConsumersAcceptedClears(t *testing.T) {
	res := breakLedger(breakSnap(t, running14), announced(future, counted()))
	if res.Gate != GateClear || res.GateCode() != GateCodeBreakingAccepted || !strings.Contains(res.GateReason, "every consumer accepted it: reports (signed by ana@acme.com)") {
		t.Fatalf("gate %s %s: %s", res.Gate, res.GateCode(), res.GateReason)
	}
}

func TestBreakAcceptedByMatchingRange(t *testing.T) {
	res := breakLedger(breakSnap(t, reportsRev{"1.6.0", "sha256:r16", "^3.0.0", true}), announced(future))
	if c := reportsOf(t, res); c.Break.State != BreakAccepted || c.Break.Reason != "reports's ranges admit 3.0.0" || res.Gate != GateClear {
		t.Fatalf("gate %s, break %+v", res.Gate, c.Break)
	}
}

func TestBreakRangePathIgnoredWhileIncompatibleRevisionRuns(t *testing.T) {
	res := breakLedger(breakSnap(t, reportsRev{"1.6.0", "sha256:r16", "^3.0.0", false}, running14), announced(future))
	c := reportsOf(t, res)
	if c.Break.State != BreakAnnounced || !strings.HasPrefix(c.Break.Reason, "reports 1.6.0 admits 3.0.0; running 1.4.0 (") || !strings.HasSuffix(c.Break.Reason, ") does not") {
		t.Fatalf("break %+v", c.Break)
	}
	// A running target no revision is linked to is unknown, never admitting.
	snap := breakSnap(t, reportsRev{"1.6.0", "sha256:r16", "^3.0.0", false}, running14)
	for _, tr := range snap.Targets {
		tr.ContractRevision = ""
	}
	if c := reportsOf(t, breakLedger(snap, announced(future))); !strings.Contains(c.Break.Reason, "running an unknown revision") {
		t.Fatalf("break %+v", c.Break)
	}
}

func TestExpiredBreakBlocks(t *testing.T) {
	res := breakLedger(breakSnap(t, running14), announced("2020-01-01", counted()))
	if c := reportsOf(t, res); res.Gate != GateBlocked || c.Break.State != BreakExpired || c.Break.Reason != "the break expired on 2020-01-01" {
		t.Fatalf("gate %s, break %+v", res.Gate, c.Break)
	}
}

func TestBreakWithoutExpiryNeverAccepts(t *testing.T) {
	for expires, why := range map[string]string{"": "expiry unknown: run pacto ci or declare expires", "soon": "expiry soon is not a YYYY-MM-DD date"} {
		res := breakLedger(breakSnap(t, running14), announced(expires, counted()))
		if c := reportsOf(t, res); res.Gate != GateBlocked || c.Break.State != BreakExpired || c.Break.Reason != why {
			t.Errorf("%q: gate %s, break %+v", expires, res.Gate, c.Break)
		}
	}
}

func TestBreakAcceptanceForOtherBreakIgnored(t *testing.T) {
	other, version := counted(), counted()
	other.Break.Announcement = "sha256:other"
	version.Break.Version = "3.1.0"
	res := breakLedger(breakSnap(t, running14), announced(future, other, version))
	if c := reportsOf(t, res); res.Gate != GateBlocked || c.Break.State != BreakAnnounced {
		t.Fatalf("gate %s, break %+v", res.Gate, c.Break)
	}
}

func TestAcceptanceListingTruncatedIsUnverified(t *testing.T) {
	in := announced(future)
	in.Acceptances = func(fleet.ServiceKey) ([]Acceptance, bool) { return nil, false }
	res := breakLedger(breakSnap(t, reportsRev{"1.4.0", "sha256:r14", "^2.0.0", false}), in)
	if c := reportsOf(t, res); res.Gate != GateUnverified || c.Break.State != BreakAnnounced || !hasCode(res.Limitations, LimitationAcceptancesTruncated) {
		t.Fatalf("gate %s, break %+v, limitations %+v", res.Gate, c.Break, res.Limitations)
	}
	// No acceptance reader reads none.
	in.Acceptances = nil
	if c := reportsOf(t, breakLedger(breakSnap(t, running14), in)); c.Break.State != BreakAnnounced {
		t.Fatalf("break %+v", c.Break)
	}
}

func TestUnannouncedConsumerUnchanged(t *testing.T) {
	in := announced(future, counted())
	in.Announced = map[string]BreakAnnouncement{"billing": in.Announced["reports"]}
	res := breakLedger(breakSnap(t, running14), in)
	if c := reportsOf(t, res); res.Gate != GateBlocked || c.Break != nil {
		t.Fatalf("gate %s, break %+v", res.Gate, c.Break)
	}
	// A change that is not BREAKING reads no break.
	r := &Result{Classification: "NON_BREAKING", Consumers: []AffectedConsumer{{Service: "reports", Direct: true}}}
	r.applyBreaks(nil, in, "ledger")
	if r.Consumers[0].Break != nil {
		t.Fatal("a NON_BREAKING change read a break")
	}
}

func TestEventAnswerIgnoresBreaks(t *testing.T) {
	res := Analyze(context.Background(), nil, nil, nil, nil, breakSnap(t, running14),
		Options{Event: &Event{Kind: EventUnavailable, Service: "ledger"}, Breaks: announced(future, counted())})
	for _, c := range res.Consumers {
		if c.Break != nil {
			t.Fatalf("event answer read a break: %+v", c)
		}
	}
}

func TestExpiredBreakBoundary(t *testing.T) {
	day := time.Date(2026, 12, 14, 23, 0, 0, 0, time.UTC)
	if expiredBreak("2026-12-14", day) != "" || expiredBreak("2026-12-13", day) == "" {
		t.Fatal("an expiry applies through its whole day")
	}
}

func TestNewerRevisionTieBreaksOnKey(t *testing.T) {
	a, b := &fleet.ContractRevision{Key: "b", Version: "1.0.0"}, &fleet.ContractRevision{Key: "a", Version: "1.0.0"}
	if !newerRevision(a, b) || newerRevision(b, a) {
		t.Fatal("an equal version orders by key")
	}
}

func TestRangeAdmitsSkipsOtherServicesTargets(t *testing.T) {
	snap := breakSnap(t, reportsRev{"1.6.0", "sha256:r16", "^3.0.0", true})
	snap.Targets["other"] = &fleet.TargetRecord{Key: "other", ServiceKey: "ledger"}
	if ok, why := rangeAdmits(snap, "reports", "ledger", "3.0.0"); !ok || why != "" {
		t.Fatalf("%v %q", ok, why)
	}
}
