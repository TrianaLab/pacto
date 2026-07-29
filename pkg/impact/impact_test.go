package impact

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/diff"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// mkBundle wraps a contract in an in-memory bundle with an (empty) MapFS. RawYAML
// is left nil so Build skips validation — the fleet topology, not validity, is
// what these tests exercise.
func mkBundle(c *contract.Contract) *contract.Bundle {
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
}

func svcContract(name, version string, owner contract.Owner, deps ...contract.Dependency) *contract.Contract {
	return &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.Service{Name: name, Version: version, Owner: owner},
		Dependencies: deps,
	}
}

// buildChain assembles frontend → api-gateway → auth-service so auth-service has a
// direct dependent (api-gateway) and a transitive one (frontend). api-gateway and
// auth-service carry a target; frontend has no owner and no target.
func buildChain(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	gateway := svcContract("api-gateway", "1.0.0", contract.Owner{Team: "gateway-team"},
		contract.Dependency{Name: "auth-service", Ref: "ghcr.io/x/auth-service:1.0.0", Required: true, Compatibility: "^1.0.0"})
	frontend := svcContract("frontend", "1.0.0", contract.Owner{},
		contract.Dependency{Name: "api-gateway", Ref: "ghcr.io/x/api-gateway:1.0.0", Compatibility: "^1.0.0"})

	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: mkBundle(auth)}, {Bundle: mkBundle(gateway)}, {Bundle: mkBundle(frontend)}},
		Targets: []fleet.RawTarget{
			{Scope: "prod", Kind: "deployment", Name: "auth", Service: "auth-service", Compliance: fleet.StatusCompliant},
			{Scope: "prod", Kind: "deployment", Name: "gw", Service: "api-gateway", Compliance: fleet.StatusCompliant},
		},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatalf("fleet.Build: %v", err)
	}
	return snap
}

func TestAnalyze(t *testing.T) {
	snap := buildChain(t)
	// An observed edge for the direct dependent, so declared+observed corroborate.
	snap.Relationships = append(snap.Relationships, fleet.Relationship{
		FromService: "api-gateway", To: "auth-service", ToService: "auth-service",
		Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved,
	})

	old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	old.Interfaces = []contract.Interface{{Name: "auth-api", Type: contract.InterfaceTypeGRPC, Ref: "auth.proto"}}
	newC := svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"}) // interface removed → Breaking

	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{IncludeObserved: true})

	gotMeta := [6]string{res.SchemaVersion, res.Service, res.OldVersion, res.NewVersion, res.Classification, res.SnapshotID}
	wantMeta := [6]string{SchemaVersion, "auth-service", "1.0.0", "2.0.0", "BREAKING", snap.SnapshotID}
	if gotMeta != wantMeta {
		t.Errorf("meta = %v, want %v", gotMeta, wantMeta)
	}
	if !res.AsOf.Equal(snap.GeneratedAt) {
		t.Errorf("asOf = %v, want %v", res.AsOf, snap.GeneratedAt)
	}
	// service.version (NonBreaking) is filtered out; interface removal (Breaking) is kept.
	if len(res.BreakingChanges) != 1 || res.BreakingChanges[0].Path != "interfaces" || res.BreakingChanges[0].Classification != diff.Breaking {
		t.Errorf("breakingChanges = %+v", res.BreakingChanges)
	}

	wantConsumers := []AffectedConsumer{
		{
			Service: "api-gateway", Depth: 1, Direct: true, Path: []string{"auth-service", "api-gateway"},
			Owner: "gateway-team", Required: true, Compatibility: "^1.0.0",
			CompatibilityVerdict: CompatibilityIncompatible, Provenance: "declared+observed",
			Confidence: ConfidenceCorroborated, Status: fleet.StatusCompliant, Targets: []string{"prod/deployment/gw"},
		},
		{
			Service: "frontend", Depth: 2, Direct: false, Path: []string{"auth-service", "api-gateway", "frontend"},
			CompatibilityVerdict: CompatibilityUnknown, Provenance: fleet.ProvenanceInferred,
			Confidence: ConfidenceInferred, Status: fleet.StatusNotEvaluated,
		},
	}
	if !reflect.DeepEqual(res.Consumers, wantConsumers) {
		t.Errorf("consumers = %+v, want %+v", res.Consumers, wantConsumers)
	}
	if !reflect.DeepEqual(res.Owners, []string{"gateway-team"}) {
		t.Errorf("owners = %v", res.Owners)
	}
	if !reflect.DeepEqual(res.ActiveTargets, []string{"prod/deployment/auth", "prod/deployment/gw"}) {
		t.Errorf("activeTargets = %v", res.ActiveTargets)
	}
}

func TestAnalyze_ObservedEdgesSurfaceShadowAndCorroborate(t *testing.T) {
	snap := buildChain(t)
	old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	old.Interfaces = []contract.Interface{{Name: "auth-api", Type: contract.InterfaceTypeGRPC, Ref: "auth.proto"}}
	newC := svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"})

	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{
		IncludeObserved: true,
		ObservedEdges: []ObservedEdge{
			{Consumer: "api-gateway", Provider: "auth-service"}, // corroborates declared direct consumer
			{Consumer: "shadow-svc", Provider: "auth-service"},  // observed-only (shadow) consumer
			{Consumer: "billing", Provider: "other-service"},    // different provider -> skipped
		},
	})

	got := map[string]AffectedConsumer{}
	for _, c := range res.Consumers {
		got[c.Service] = c
	}
	if len(res.Consumers) != 3 {
		t.Fatalf("expected 3 consumers (api-gateway, frontend, shadow-svc), got %+v", res.Consumers)
	}
	if got["api-gateway"].Confidence != ConfidenceCorroborated || got["api-gateway"].Provenance != "declared+observed" {
		t.Errorf("api-gateway not corroborated: %+v", got["api-gateway"])
	}
	sh := got["shadow-svc"]
	wantShadow := AffectedConsumer{
		Service: "shadow-svc", Depth: 1, Direct: true, Path: []string{"auth-service", "shadow-svc"},
		CompatibilityVerdict: CompatibilityUnknown, Provenance: fleet.ProvenanceObserved, Confidence: ConfidenceObserved,
	}
	if !reflect.DeepEqual(sh, wantShadow) {
		t.Errorf("shadow consumer = %+v, want %+v", sh, wantShadow)
	}
	if _, ok := got["billing"]; ok {
		t.Error("billing (different provider) should not be a consumer")
	}
}

func TestAnalyze_ObservedEdgesIgnoredWhenNotIncluded(t *testing.T) {
	snap := buildChain(t)
	old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	newC := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{
		ObservedEdges: []ObservedEdge{{Consumer: "shadow-svc", Provider: "auth-service"}},
	})
	for _, c := range res.Consumers {
		if c.Service == "shadow-svc" {
			t.Error("shadow-svc should not appear without IncludeObserved")
		}
	}
}

func TestAnalyzeServiceNotInFleet(t *testing.T) {
	snap := buildChain(t)
	old := svcContract("orphan", "1.0.0", contract.Owner{})
	newC := svcContract("orphan", "2.0.0", contract.Owner{})

	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{})

	if len(res.Consumers) != 0 {
		t.Errorf("expected no consumers, got %+v", res.Consumers)
	}
	if res.Owners != nil || res.ActiveTargets != nil {
		t.Errorf("expected nil owners/targets, got %v / %v", res.Owners, res.ActiveTargets)
	}
	found := false
	for _, l := range res.Limitations {
		if l.Code == "SERVICE_NOT_IN_FLEET" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SERVICE_NOT_IN_FLEET limitation, got %+v", res.Limitations)
	}
}

func TestConsumerImpactServiceAbsent(t *testing.T) {
	snap := &fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{}}
	node := consumerNode{key: fleet.NewServiceKey("ghost"), name: "ghost", depth: 2, path: []fleet.ServiceKey{"auth-service", "ghost"}}
	got := consumerImpact(snap, fleet.NewServiceKey("auth-service"), node, "2.0.0", Options{})
	want := AffectedConsumer{
		Service: "ghost", Depth: 2, Path: []string{"auth-service", "ghost"},
		CompatibilityVerdict: CompatibilityUnknown, Provenance: fleet.ProvenanceInferred, Confidence: ConfidenceInferred,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("consumer = %+v, want %+v", got, want)
	}
}

func TestEdgeEvidence(t *testing.T) {
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{
		{Type: fleet.RelationshipConfigRef, FromService: "a", ToService: "b"},                                                                                 // wrong type
		{Type: fleet.RelationshipDependency, FromService: "x", ToService: "b"},                                                                                // wrong from
		{Type: fleet.RelationshipDependency, FromService: "a", ToService: "y"},                                                                                // wrong to
		{Type: fleet.RelationshipDependency, FromService: "a", ToService: "b", Provenance: fleet.ProvenanceObserved},                                          // observed
		{Type: fleet.RelationshipDependency, FromService: "a", ToService: "b", Provenance: fleet.ProvenanceDeclared, Required: true, Compatibility: "^1.0.0"}, // declared
	}}
	rel, declared, observed := edgeEvidence(snap, "a", "b", nil)
	if !declared || !observed || !rel.Required || rel.Compatibility != "^1.0.0" {
		t.Errorf("edgeEvidence = rel:%+v declared:%v observed:%v", rel, declared, observed)
	}

	// An observed edge supplied via telemetry (not in the graph) also counts.
	relT, declaredT, observedT := edgeEvidence(
		&fleet.FleetSnapshot{Relationships: []fleet.Relationship{
			{Type: fleet.RelationshipDependency, FromService: "a", ToService: "b", Provenance: fleet.ProvenanceDeclared, Required: true},
		}},
		"a", "b", []ObservedEdge{{Consumer: "a", Provider: "b"}, {Consumer: "other", Provider: "b"}})
	if !declaredT || !observedT || !relT.Required {
		t.Errorf("telemetry edge not counted: rel:%+v declared:%v observed:%v", relT, declaredT, observedT)
	}
}

func TestProvenance(t *testing.T) {
	tests := []struct {
		declared, observed bool
		want               string
	}{
		{true, true, "declared+observed"},
		{false, true, fleet.ProvenanceObserved},
		{true, false, fleet.ProvenanceDeclared},
		{false, false, fleet.ProvenanceInferred}, // no counted evidence → inferred
	}
	for _, tc := range tests {
		if got := provenance(tc.declared, tc.observed); got != tc.want {
			t.Errorf("provenance(%v,%v) = %q, want %q", tc.declared, tc.observed, got, tc.want)
		}
	}
}

func TestConfidence(t *testing.T) {
	tests := []struct {
		depth              int
		declared, observed bool
		want               Confidence
	}{
		{2, false, false, ConfidenceInferred},
		{1, true, true, ConfidenceCorroborated},
		{1, false, true, ConfidenceObserved},
		{1, true, false, ConfidenceContractual},
		{1, false, false, ConfidenceUnknown},
	}
	for _, tc := range tests {
		if got := confidence(tc.depth, tc.declared, tc.observed); got != tc.want {
			t.Errorf("confidence(%d,%v,%v) = %q, want %q", tc.depth, tc.declared, tc.observed, got, tc.want)
		}
	}
}

func TestCompatibilityVerdict(t *testing.T) {
	tests := []struct {
		constraint, version string
		declared            bool
		want                string
	}{
		{"^1.0.0", "2.0.0", false, CompatibilityUnknown}, // not declared
		{"", "2.0.0", true, CompatibilityUnknown},        // empty constraint
		{"^1.0.0", "", true, CompatibilityUnknown},       // empty version
		{"abc", "2.0.0", true, CompatibilityUnknown},     // unparseable constraint
		{"^1.0.0", "abc", true, CompatibilityUnknown},    // unparseable version
		{"^1.0.0", "2.0.0", true, CompatibilityIncompatible},
		{"^2.0.0", "2.0.0", true, CompatibilityCompatible},
	}
	for _, tc := range tests {
		if got := compatibilityVerdict(tc.constraint, tc.version, tc.declared); got != tc.want {
			t.Errorf("compatibilityVerdict(%q,%q,%v) = %q, want %q", tc.constraint, tc.version, tc.declared, got, tc.want)
		}
	}
}

func TestServiceTargets(t *testing.T) {
	snap := &fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{
		fleet.NewServiceKey("svc"): {Name: "svc", Targets: []fleet.TargetKey{"b", "a"}},
	}}
	if got := serviceTargets(snap, "svc"); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("serviceTargets(svc) = %v", got)
	}
	if got := serviceTargets(snap, "absent"); got != nil {
		t.Errorf("serviceTargets(absent) = %v, want nil", got)
	}
}

func TestSortedKeys(t *testing.T) {
	if got := sortedKeys(map[string]bool{}); got != nil {
		t.Errorf("sortedKeys(empty) = %v, want nil", got)
	}
	if got := sortedKeys(map[string]bool{"b": true, "a": true}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("sortedKeys = %v", got)
	}
}
