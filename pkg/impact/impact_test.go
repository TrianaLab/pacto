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
			Service: "api-gateway", Depth: 1, Direct: true, Path: []string{"api-gateway", "auth-service"},
			Owner: "gateway-team", Required: true, Compatibility: "^1.0.0",
			CompatibilityVerdict: CompatibilityIncompatible, Provenance: "declared+observed",
			Confidence: ConfidenceCorroborated, Status: fleet.StatusCompliant, Targets: []string{"prod/deployment/gw"},
		},
		{
			Service: "frontend", Depth: 2, Direct: false, Path: []string{"frontend", "api-gateway", "auth-service"},
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
	// shadow-svc is a REGISTERED fleet service that calls auth-service without
	// declaring the dependency — a domain-qualified observed-only (shadow) consumer.
	// Only registered services can be resolved to a unique domain-qualified key;
	// an unknown OTel name is preserved as an unresolved limitation instead.
	auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	auth.Interfaces = []contract.Interface{{Name: "auth-api", Type: contract.InterfaceTypeGRPC, Ref: "auth.proto"}}
	gateway := svcContract("api-gateway", "1.0.0", contract.Owner{Team: "gateway-team"},
		contract.Dependency{Name: "auth-service", Ref: "ghcr.io/x/auth-service:1.0.0", Required: true, Compatibility: "^1.0.0"})
	shadow := svcContract("shadow-svc", "1.0.0", contract.Owner{Team: "ops"})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory",
		&fleet.Collection{Revisions: []fleet.RawRevision{{Bundle: mkBundle(auth)}, {Bundle: mkBundle(gateway)}, {Bundle: mkBundle(shadow)}}}))
	if err != nil {
		t.Fatalf("fleet.Build: %v", err)
	}
	newC := svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"})

	res := Analyze(context.Background(), auth, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{
		IncludeObserved: true,
		ObservedEdges: []ObservedEdge{
			{Consumer: "api-gateway", Provider: "auth-service"}, // corroborates declared direct consumer
			{Consumer: "shadow-svc", Provider: "auth-service"},  // observed-only (shadow) consumer
			{Consumer: "api-gateway", Provider: "shadow-svc"},   // resolved, but provider != changed -> skipped
			{Consumer: "billing", Provider: "other-service"},    // unknown endpoints -> unresolved, dropped
		},
	})

	got := map[string]AffectedConsumer{}
	for _, c := range res.Consumers {
		got[c.Service] = c
	}
	if len(res.Consumers) != 2 {
		t.Fatalf("expected 2 consumers (api-gateway, shadow-svc), got %+v", res.Consumers)
	}
	if got["api-gateway"].Confidence != ConfidenceCorroborated || got["api-gateway"].Provenance != "declared+observed" {
		t.Errorf("api-gateway not corroborated: %+v", got["api-gateway"])
	}
	sh := got["shadow-svc"]
	if sh.Provenance != fleet.ProvenanceObserved || sh.Confidence != ConfidenceObserved || !sh.Direct || sh.Depth != 1 || sh.Owner != "ops" {
		t.Errorf("shadow consumer not surfaced as domain-qualified observed-only: %+v", sh)
	}
	if _, ok := got["billing"]; ok {
		t.Error("billing (unknown endpoint) must not become a phantom default-domain consumer")
	}
	// billing and other-service are unknown → structured unresolved limitations.
	unresolved := 0
	for _, l := range res.Limitations {
		if l.Code == ObservedIdentityUnresolved {
			unresolved++
		}
	}
	if unresolved != 2 {
		t.Errorf("expected 2 OBSERVED_IDENTITY_UNRESOLVED limitations (billing, other-service), got %d: %+v", unresolved, res.Limitations)
	}
}

// TestAnalyze_ObservedEdgesCrossDomainIsolation proves observed evidence never
// corroborates or affects a same-named service in a DIFFERENT domain: an
// ambiguous OTel name (two "payments" services) resolves to neither, and is
// preserved as an unresolved limitation instead of defaulting a domain.
func TestAnalyze_ObservedEdgesCrossDomainIsolation(t *testing.T) {
	payEU := svcContract("payments", "1.0.0", contract.Owner{Team: "eu"})
	payUS := svcContract("payments", "1.0.0", contract.Owner{Team: "us"})
	checkout := svcContract("checkout", "1.0.0", contract.Owner{Team: "eu"},
		contract.Dependency{Name: "payments", Ref: "ghcr.io/x/payments:1.0.0", Required: true, Compatibility: "^1.0.0"})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory",
		&fleet.Collection{Revisions: []fleet.RawRevision{
			{Domain: "eu", Bundle: mkBundle(payEU)},
			{Domain: "us", Bundle: mkBundle(payUS)},
			{Domain: "eu", Bundle: mkBundle(checkout)},
		}}))
	if err != nil {
		t.Fatalf("fleet.Build: %v", err)
	}
	old := svcContract("payments", "1.0.0", contract.Owner{Team: "eu"})
	newC := svcContract("payments", "2.0.0", contract.Owner{Team: "eu"})

	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{
		Domain:          "eu",
		IncludeObserved: true,
		// "payments" is ambiguous across eu/us; "checkout" is unique. The ambiguous
		// endpoint must not corroborate the eu/payments declared edge.
		ObservedEdges: []ObservedEdge{{Consumer: "checkout", Provider: "payments"}},
	})

	for _, c := range res.Consumers {
		if c.Service == "checkout" && c.Provenance == "declared+observed" {
			t.Errorf("ambiguous observed edge wrongly corroborated cross-domain: %+v", c)
		}
	}
	found := false
	for _, l := range res.Limitations {
		if l.Code == ObservedIdentityUnresolved {
			found = true
		}
	}
	if !found {
		t.Errorf("expected OBSERVED_IDENTITY_UNRESOLVED for ambiguous 'payments', got %+v", res.Limitations)
	}
}

// TestAnalyze_ObservedEdgesFromSnapshot proves the real pipeline: an observed
// relationship folded into the snapshot by Build surfaces the observed consumer
// with no ad-hoc --traces input, and only when observed evidence is opted in.
func TestAnalyze_ObservedEdgesFromSnapshot(t *testing.T) {
	auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	caller := svcContract("caller", "1.0.0", contract.Owner{Team: "team-c"})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("obs", "observation",
		&fleet.Collection{
			Revisions: []fleet.RawRevision{{Bundle: mkBundle(auth)}, {Bundle: mkBundle(caller)}},
			Observed:  []fleet.ObservedEdge{{From: "caller", To: "auth-service", Count: 7}},
		}))
	if err != nil {
		t.Fatal(err)
	}
	newC := svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"})

	res := Analyze(context.Background(), auth, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{IncludeObserved: true})
	found := false
	for _, c := range res.Consumers {
		if c.Service == "caller" && c.Provenance == fleet.ProvenanceObserved {
			found = true
		}
	}
	if !found {
		t.Errorf("expected observed consumer 'caller' from the snapshot pipeline, got %+v", res.Consumers)
	}

	res2 := Analyze(context.Background(), auth, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{})
	for _, c := range res2.Consumers {
		if c.Service == "caller" {
			t.Error("a snapshot observed edge must not surface a consumer without IncludeObserved")
		}
	}
}

func TestResolveObservedEdges(t *testing.T) {
	snap := buildChain(t)
	// unique endpoints resolve; unknown/ambiguous do not.
	edges := []ObservedEdge{
		{Consumer: "api-gateway", Provider: "auth-service"}, // both unique -> resolved
		{Consumer: "ghost", Provider: "auth-service"},       // ghost unknown -> dropped + limitation
	}
	resolved, lims := resolveObservedEdges(snap, edges, true)
	if len(resolved) != 1 || resolved[0].consumer != fleet.NewServiceKey("api-gateway") || resolved[0].provider != fleet.NewServiceKey("auth-service") {
		t.Fatalf("resolved = %+v", resolved)
	}
	if len(lims) != 1 || lims[0].Code != ObservedIdentityUnresolved {
		t.Fatalf("lims = %+v", lims)
	}
	// not opted in -> nothing.
	if r, l := resolveObservedEdges(snap, edges, false); r != nil || l != nil {
		t.Errorf("expected nil when not included, got %+v / %+v", r, l)
	}
	// no edges -> nothing.
	if r, l := resolveObservedEdges(snap, nil, true); r != nil || l != nil {
		t.Errorf("expected nil for no edges, got %+v / %+v", r, l)
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
	got := consumerImpact(snap, fleet.NewServiceKey("auth-service"), node, "2.0.0", nil)
	want := AffectedConsumer{
		Service: "ghost", Depth: 2, Path: []string{"ghost", "auth-service"},
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
		"a", "b", []resolvedObservedEdge{{consumer: "a", provider: "b"}, {consumer: "other", provider: "b"}})
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
		depth                        int
		declared, hasRange, observed bool
		want                         Confidence
	}{
		{2, false, false, false, ConfidenceInferred},
		{1, true, true, true, ConfidenceCorroborated},
		{1, false, false, true, ConfidenceObserved},
		{1, true, true, false, ConfidenceContractual}, // declared WITH range
		{1, true, false, false, ConfidenceDeclared},   // declared WITHOUT range
		{1, false, false, false, ConfidenceUnknown},
	}
	for _, tc := range tests {
		if got := confidence(tc.depth, tc.declared, tc.hasRange, tc.observed); got != tc.want {
			t.Errorf("confidence(%d,%v,%v,%v) = %q, want %q", tc.depth, tc.declared, tc.hasRange, tc.observed, got, tc.want)
		}
	}
}

func TestAnalyze_PotentialBreakingSeparated(t *testing.T) {
	dep := func(comp string) contract.Dependency {
		return contract.Dependency{Name: "redis", Ref: "oci://x/redis", Required: true, Compatibility: comp}
	}
	old := svcContract("svc", "1.0.0", contract.Owner{}, dep("^1.0.0"))
	newC := svcContract("svc", "1.0.0", contract.Owner{}, dep("^2.0.0")) // compatibility modified -> PotentialBreaking
	snap := &fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{}}

	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap, Options{})

	// A potentially-breaking change lands in its own bucket, never counted as a
	// confirmed break.
	if len(res.PotentiallyBreakingChanges) == 0 {
		t.Fatalf("dependency compatibility change should be potentially-breaking; breaking=%+v potential=%+v", res.BreakingChanges, res.PotentiallyBreakingChanges)
	}
	for _, ch := range res.PotentiallyBreakingChanges {
		if ch.Classification != diff.PotentialBreaking {
			t.Errorf("wrong class in PotentiallyBreakingChanges: %+v", ch)
		}
	}
	for _, ch := range res.BreakingChanges {
		if ch.Classification == diff.PotentialBreaking {
			t.Errorf("POTENTIAL_BREAKING leaked into BreakingChanges: %+v", ch)
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
