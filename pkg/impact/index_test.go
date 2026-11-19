package impact

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// indexDomain is the OCI domain every published service in these fixtures shares.
const indexDomain = "ghcr.io/acme"

// Target modes of indexSnap.
const (
	noTarget = iota
	liveTarget
	deadTarget
)

// indexSnap builds ledger with two declared direct consumers in indexDomain:
// checkout accepts ^1.0.0 (incompatible with 2.0.0) and runs when a target
// source answers; billing accepts ^2.0.0. With no consumers it holds ledger only.
func indexSnap(t *testing.T, target int, consumers bool) *fleet.FleetSnapshot {
	t.Helper()
	rev := func(name, rng string) fleet.RawRevision {
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0"}}
		if rng != "" {
			c.Dependencies = []contract.Dependency{{Name: "ledger", Ref: "oci://ghcr.io/acme/ledger", Required: true, Compatibility: rng}}
		}
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: indexDomain, Digest: "sha256:" + name}
	}
	col := &fleet.Collection{Revisions: []fleet.RawRevision{rev("ledger", "")}}
	if consumers {
		col.Revisions = append(col.Revisions, rev("checkout", "^1.0.0"), rev("billing", "^2.0.0"))
	}
	srcs := []fleet.Source{fleet.NewMemorySource("oci", "oci", col)}
	switch target {
	case liveTarget:
		srcs = append(srcs, fleet.NewMemorySource("ts", fleet.SourceKindTargetState, &fleet.Collection{Targets: []fleet.RawTarget{
			{Scope: "prod", Kind: "deployment", Name: "checkout", Service: "checkout", Domain: indexDomain, Compliance: fleet.StatusCompliant, EvidenceAt: fresh()},
		}}))
	case deadTarget:
		srcs = append(srcs, fleet.NewFailingSource("ts", fleet.SourceKindTargetState, errors.New("unreachable")))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, srcs...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// analyzeIndexed runs a BREAKING ledger 1.0.0 -> 2.0.0 change with an index
// summary and the given set of indexed service names.
func analyzeIndexed(t *testing.T, snap *fleet.FleetSnapshot, indexed ...string) *Result {
	t.Helper()
	old := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "ledger", Version: "1.0.0"},
		Interfaces: []contract.Interface{{Name: "api", Type: contract.InterfaceTypeGRPC, Ref: "api.proto"}}}
	newC := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "ledger", Version: "2.0.0"}}
	set := map[fleet.ServiceKey]bool{}
	for _, n := range indexed {
		set[fleet.NewServiceKeyDomain(indexDomain, n)] = true
	}
	idx := &ConsumerIndex{Repository: "ghcr.io/acme/pacto-index", Status: IndexRead, Services: 3, Consumers: len(indexed), Completeness: fleet.CompletenessComplete}
	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{
		Domain: indexDomain, Index: idx, Indexed: func(k fleet.ServiceKey) bool { return set[k] },
	})
	if res.Classification != "BREAKING" {
		t.Fatalf("classification = %s, want BREAKING", res.Classification)
	}
	return res
}

func TestConsumerNotIndexedIsPartial(t *testing.T) {
	res := analyzeIndexed(t, indexSnap(t, liveTarget, true), "billing")
	l := limitation(res, LimitationConsumerNotIndexed)
	if l == nil || !strings.Contains(l.Message, "1 consumer the fleet holds has no organization index entry: checkout;") {
		t.Fatalf("limitation = %+v", l)
	}
	if res.Completeness != fleet.CompletenessPartial || res.ConsumerIndex.Completeness != fleet.CompletenessPartial {
		t.Errorf("completeness = %s / %s, want partial", res.Completeness, res.ConsumerIndex.Completeness)
	}
	if limitation(res, LimitationConsumerIndexUnchecked) != nil || !res.ConsumerIndex.Checked {
		t.Errorf("a live target source checks the index: %+v", res.ConsumerIndex)
	}
}

func TestConsumerNotIndexedKeepsTheConsumer(t *testing.T) {
	res := analyzeIndexed(t, indexSnap(t, liveTarget, true))
	if len(res.Consumers) != 2 {
		t.Fatalf("consumers = %+v, want checkout and billing kept", res.Consumers)
	}
	if l := limitation(res, LimitationConsumerNotIndexed); l == nil || !strings.HasPrefix(l.Message, "2 consumers the fleet holds have no organization index entry: ") {
		t.Errorf("limitation = %+v", l)
	}
}

func TestUnindexedCheckSkipsLocalAndObservedConsumers(t *testing.T) {
	r := &Result{Completeness: fleet.CompletenessComplete, Consumers: []AffectedConsumer{
		{Service: "local", Direct: true, Provenance: fleet.ProvenanceDeclared},
		{Service: "seen", Domain: indexDomain, Direct: true, Provenance: fleet.ProvenanceObserved},
		{Service: "guess", Domain: indexDomain, Direct: true, Provenance: fleet.ProvenanceInferred},
		{Service: "far", Domain: indexDomain, Depth: 2, Provenance: fleet.ProvenanceInferred},
	}}
	r.noteUnindexed(func(fleet.ServiceKey) bool { return false })
	if len(r.Limitations) != 0 || r.Completeness != fleet.CompletenessComplete {
		t.Fatalf("flagged a consumer the index could not hold: %+v", r.Limitations)
	}
	// declared+observed is declared; more than ten are counted, not named.
	for i := range 12 {
		r.Consumers = append(r.Consumers, AffectedConsumer{Service: "c" + strconv.Itoa(i), Domain: indexDomain, Direct: true, Provenance: "declared+observed"})
	}
	r.noteUnindexed(func(fleet.ServiceKey) bool { return false })
	if l := limitation(r, LimitationConsumerNotIndexed); l == nil || !strings.Contains(l.Message, "12 consumers") ||
		!strings.Contains(l.Message, "c8 and c9 (and 2 more);") || strings.Contains(l.Message, "c10") {
		t.Errorf("limitation = %+v", l)
	}
	if r.Completeness != fleet.CompletenessPartial || r.ConsumerIndex != nil {
		t.Errorf("result = %+v", r)
	}
}

func TestNoIndexNoUnindexedCheck(t *testing.T) {
	snap := indexSnap(t, noTarget, true)
	old := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "ledger", Version: "1.0.0"}}
	res := Analyze(context.Background(), old, old, fstest.MapFS{}, fstest.MapFS{}, snap, Options{
		Domain: indexDomain, Index: &ConsumerIndex{Status: IndexSkipped, Reason: "--root names the consumers"},
	})
	if limitation(res, LimitationConsumerNotIndexed) != nil || limitation(res, LimitationConsumerIndexUnchecked) != nil {
		t.Fatalf("limitations = %+v", res.Limitations)
	}
	if res.ConsumerIndex == nil || res.ConsumerIndex.Status != IndexSkipped || res.Completeness != fleet.CompletenessComplete {
		t.Errorf("result = %+v", res)
	}
}

func TestConsumerIndexCopiedToResult(t *testing.T) {
	idx := &ConsumerIndex{Repository: "ghcr.io/acme/pacto-index", Status: IndexRead, Services: 3}
	old := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "ledger", Version: "1.0.0"}}
	res := Analyze(context.Background(), old, old, fstest.MapFS{}, fstest.MapFS{}, indexSnap(t, noTarget, false), Options{Domain: indexDomain, Index: idx})
	if res.ConsumerIndex == idx || *res.ConsumerIndex != *idx {
		t.Fatalf("ConsumerIndex = %+v, want a copy of %+v", res.ConsumerIndex, idx)
	}
	if res2 := Analyze(context.Background(), old, old, fstest.MapFS{}, fstest.MapFS{}, indexSnap(t, noTarget, false), Options{}); res2.ConsumerIndex != nil {
		t.Errorf("no index read, ConsumerIndex = %+v", res2.ConsumerIndex)
	}
}

func TestIndexWithoutTargetSourceIsPartial(t *testing.T) {
	res := analyzeIndexed(t, indexSnap(t, noTarget, true), "checkout", "billing")
	l := limitation(res, LimitationConsumerIndexUnchecked)
	if l == nil || l.Message != "the index lists 3 services; without a target source Pacto cannot tell whether a consumer is missing from it" {
		t.Fatalf("limitation = %+v", l)
	}
	if res.ConsumerIndex.Checked || res.Completeness != fleet.CompletenessPartial || res.ConsumerIndex.Completeness != fleet.CompletenessPartial {
		t.Errorf("result = %+v / %+v", res.Completeness, res.ConsumerIndex)
	}
	if limitation(res, LimitationConsumerNotIndexed) != nil {
		t.Errorf("every consumer is indexed: %+v", res.Limitations)
	}
}

func TestIndexWithUnavailableTargetSourceIsUnchecked(t *testing.T) {
	res := analyzeIndexed(t, indexSnap(t, deadTarget, true), "checkout", "billing")
	if limitation(res, LimitationConsumerIndexUnchecked) == nil || res.ConsumerIndex.Checked {
		t.Fatalf("an unavailable target source checks nothing: %+v", res.ConsumerIndex)
	}
}

func TestIndexWithTargetSourceIsChecked(t *testing.T) {
	res := analyzeIndexed(t, indexSnap(t, liveTarget, true), "checkout", "billing")
	if limitation(res, LimitationConsumerIndexUnchecked) != nil || limitation(res, LimitationConsumerNotIndexed) != nil {
		t.Fatalf("limitations = %+v", res.Limitations)
	}
	if !res.ConsumerIndex.Checked || res.ConsumerIndex.Completeness != fleet.CompletenessComplete {
		t.Errorf("ConsumerIndex = %+v", res.ConsumerIndex)
	}
}

func TestBreakingIndexOnlyNeverClears(t *testing.T) {
	// No service depends on ledger and the index agrees, but with no target
	// source the index alone cannot show it lists every consumer.
	res := analyzeIndexed(t, indexSnap(t, noTarget, false))
	if res.Gate != GateUnverified || !strings.Contains(res.GateReason, "partial") {
		t.Fatalf("gate = %s: %s", res.Gate, res.GateReason)
	}
	// The same answer checked against a target source clears.
	if res := analyzeIndexed(t, indexSnap(t, liveTarget, false)); res.Gate != GateClear {
		t.Errorf("checked gate = %s: %s", res.Gate, res.GateReason)
	}
}

func TestUnindexedBreakingNeverClears(t *testing.T) {
	res := analyzeIndexed(t, indexSnap(t, liveTarget, true), "checkout")
	if res.Gate == GateClear {
		t.Fatalf("gate = %s: %s", res.Gate, res.GateReason)
	}
	if limitation(res, LimitationConsumerNotIndexed) == nil {
		t.Errorf("limitations = %+v", res.Limitations)
	}
}

func TestUnindexedIncompatibleOnTargetStillBlocks(t *testing.T) {
	res := analyzeIndexed(t, indexSnap(t, liveTarget, true), "billing")
	if res.Gate != GateBlocked {
		t.Fatalf("gate = %s: %s", res.Gate, res.GateReason)
	}
}

func TestEventAnswerIgnoresIndex(t *testing.T) {
	snap := indexSnap(t, noTarget, true)
	res := Analyze(context.Background(), nil, nil, nil, nil, snap, Options{
		Event:   &Event{Kind: EventUnavailable, Service: string(fleet.NewServiceKeyDomain(indexDomain, "ledger"))},
		Index:   &ConsumerIndex{Status: IndexRead},
		Indexed: func(fleet.ServiceKey) bool { return false },
	})
	if res.ConsumerIndex != nil || limitation(res, LimitationConsumerNotIndexed) != nil || limitation(res, LimitationConsumerIndexUnchecked) != nil {
		t.Fatalf("event answer read the index: %+v %+v", res.ConsumerIndex, res.Limitations)
	}
}
