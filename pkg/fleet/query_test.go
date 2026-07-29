package fleet

import (
	"context"
	"errors"
	"sort"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
)

// -------------------- shared fixtures --------------------

// queryFleet builds a rich snapshot exercised by Search/GetService/GetTarget/
// Status/Explain tests. FreshnessWindow is 1h so old evidence is stale.
func queryFleet(t *testing.T) *Query {
	t.Helper()
	fresh := ptrTime(fixedNow().Add(-1 * time.Minute))
	old := ptrTime(fixedNow().Add(-2 * time.Hour))

	alpha := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "alpha", Version: "1.0.0", Owner: contract.Owner{Team: "team-a", DRI: "alice"}},
		Workload:     contract.WorkloadService,
		Capabilities: []contract.Capability{{Type: contract.CapabilityHealth}},
		Dependencies: []contract.Dependency{
			{Name: "leaf-svc", Ref: "oci://x/leaf", Required: true, Compatibility: "^1.0.0"},
			{Name: "ghost", Ref: "oci://x/ghost", Required: false, Compatibility: "^1.0.0"},
		},
		Readiness: &contract.Readiness{
			Expires: "2099-12-31",
			Claims:  []contract.ReadinessClaim{{ID: "dash", Type: "url", Status: contract.StatusDone, Evidence: "https://x", Weight: 10}},
		},
	}
	leaf := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "leaf-svc", Version: "1.0.0", Owner: contract.Owner{Team: "leaf-team"}},
		Interfaces:   []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"}},
	}
	leafFS := fstest.MapFS{
		"interfaces/openapi.json": {Data: []byte(smallOpenAPI)},
		"skills/deploy.md":        {Data: []byte("# deploy")},
	}
	beta := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "beta", Version: "1.0.0", Owner: contract.Owner{Team: "team-b"}},
	}

	local := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: alpha, FS: fstest.MapFS{}}, Digest: "sha256:alpha"},
			{Bundle: &contract.Bundle{Contract: leaf, FS: leafFS}, Digest: "sha256:leaf"},
			{Bundle: &contract.Bundle{Contract: mustParse(t, invalidYAML), RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
		},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "alpha-app", Service: "alpha", Digest: "sha256:alpha", Compliance: StatusCompliant, EvidenceAt: fresh, Labels: map[string]string{"tier": "gold"}},
			{Scope: "prod", Kind: "k8s", Name: "dup", Service: "dup-svc"},
			{Scope: "staging", Kind: "k8s", Name: "dup", Service: "dup-svc"},
			{Scope: "prod", Kind: "k8s", Name: "unk-app", Service: "unk-svc", Compliance: StatusUnknown},
			{Scope: "prod", Kind: "k8s", Name: "orphan-app", Service: "no-rev-svc", Digest: "sha256:none", Compliance: StatusCompliant, EvidenceAt: fresh},
		},
	})
	oci := NewMemorySource("oci", "oci", &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: beta, FS: fstest.MapFS{}}, Digest: "sha256:beta"}},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "beta-app", Service: "beta", Digest: "sha256:beta", Compliance: StatusNonCompliant, EvidenceAt: old,
				Findings: []finding.Finding{
					// Two findings share a Code so sortReasons falls back to the message tiebreak.
					{Code: "DRIFT", Severity: finding.SeverityError, Subject: finding.SubjectRef{Kind: "interface", Name: "http"}, Message: "runtime drift B"},
					{Code: "DRIFT", Severity: finding.SeverityError, Subject: finding.SubjectRef{Kind: "interface", Name: "http"}, Message: "runtime drift A"},
				}},
		},
	})

	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, local, oci)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// twoDomainSnap builds a fleet with the SAME service name "shared" in two
// domains plus a domain-unique "eastonly", to exercise domain-qualified identity
// and Query.resolveService.
func twoDomainSnap(t *testing.T) *FleetSnapshot {
	t.Helper()
	east := NewMemorySource("east", "oci", &Collection{Revisions: []RawRevision{
		{Bundle: bundleFor(t, "shared"), Domain: "east", Digest: "sha256:east-shared"},
		{Bundle: bundleFor(t, "eastonly"), Domain: "east", Digest: "sha256:east-only"},
	}})
	west := NewMemorySource("west", "oci", &Collection{Revisions: []RawRevision{
		{Bundle: bundleFor(t, "shared"), Domain: "west", Digest: "sha256:west-shared"},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, east, west)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// -------------------- domain-qualified resolveService --------------------

func TestDomainResolveService_GetService(t *testing.T) {
	q := NewQuery(twoDomainSnap(t))
	// A qualified "domain/name" key resolves the exact domained service.
	east, err := q.GetService("east/shared")
	if err != nil {
		t.Fatalf("qualified: %v", err)
	}
	if east.Service.Domain != "east" || east.Service.Key != NewServiceKeyDomain("east", "shared") {
		t.Errorf("qualified resolved wrong service: %+v", east.Service)
	}
	// A bare name unique across domains resolves (non-default key, matched by name).
	if only, err := q.GetService("eastonly"); err != nil || only.Service.Domain != "east" {
		t.Errorf("bare-unique resolve: svc=%+v err=%v", only, err)
	}
	// An absent name is a NotFoundError.
	if _, err := q.GetService("ghost"); err == nil {
		t.Fatal("expected NotFoundError")
	} else if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("want NotFoundError, got %T", err)
	}
}

func TestDomainResolveService_Ambiguous(t *testing.T) {
	q := NewQuery(twoDomainSnap(t))
	assertAmbiguous := func(who string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected AmbiguousError", who)
		}
		ae, ok := err.(*AmbiguousError)
		if !ok {
			t.Fatalf("%s: want AmbiguousError, got %T", who, err)
		}
		// Matches are the sorted qualified keys of every domain the name lives in.
		if len(ae.Matches) != 2 || ae.Matches[0] != "east/shared" || ae.Matches[1] != "west/shared" {
			t.Errorf("%s: matches = %v, want [east/shared west/shared]", who, ae.Matches)
		}
	}
	_, gsErr := q.GetService("shared")
	assertAmbiguous("GetService", gsErr)
	_, gErr := q.Graph(GraphQuery{Service: "shared"})
	assertAmbiguous("Graph", gErr)
	_, eErr := q.Explain("shared")
	assertAmbiguous("Explain", eErr)
}

func TestDomainSearch_SameNameAcrossDomains(t *testing.T) {
	q := NewQuery(twoDomainSnap(t))
	res, err := q.Search(SearchFilter{Text: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Fatalf("Total = %d, want 2 (both domains appear)", res.Total)
	}
	// Deterministic order by service key: east/shared before west/shared.
	if len(res.Services) != 2 ||
		!containsStr(res.Services[0].Sources, "east") ||
		!containsStr(res.Services[1].Sources, "west") {
		t.Errorf("results not deterministically ordered by key: %+v", res.Services)
	}
}

// -------------------- NewQuery / meta / Snapshot --------------------

func TestQueryMetaAndSnapshot(t *testing.T) {
	q := queryFleet(t)
	if q.Snapshot() == nil {
		t.Fatal("Snapshot() nil")
	}
	m := q.meta()
	if !m.AsOf.Equal(fixedNow()) {
		t.Errorf("meta AsOf = %v", m.AsOf)
	}
	if m.Completeness != CompletenessComplete {
		t.Errorf("expected complete, got %q", m.Completeness)
	}
}

// -------------------- Search --------------------

func TestSearch_Filters(t *testing.T) {
	q := queryFleet(t)
	tests := []struct {
		name   string
		filter SearchFilter
		want   string // a service name that MUST appear
		absent string // a service name that must NOT appear ("" to skip)
	}{
		{"text name", SearchFilter{Text: "alpha"}, "alpha", "beta"},
		{"text owner dri", SearchFilter{Text: "alice"}, "alpha", ""},
		{"owner", SearchFilter{Owner: "team-a"}, "alpha", "beta"},
		{"labels", SearchFilter{Labels: map[string]string{"tier": "gold"}}, "alpha", "beta"},
		{"status", SearchFilter{Status: StatusNonCompliant}, "beta", "alpha"},
		{"compliance", SearchFilter{Compliance: StatusNonCompliant}, "beta", "alpha"},
		{"source oci", SearchFilter{Source: "oci"}, "beta", "alpha"},
		{"source local", SearchFilter{Source: "local"}, "alpha", "beta"},
		{"workload", SearchFilter{Workload: contract.WorkloadService}, "alpha", "beta"},
		{"has capability", SearchFilter{HasCapability: true}, "alpha", "beta"},
		{"has dependency", SearchFilter{HasDependency: true}, "alpha", "beta"},
		{"ready only", SearchFilter{ReadyOnly: true}, "alpha", "beta"},
		{"not ready", SearchFilter{NotReady: true}, "beta", "alpha"},
		// Correlated: a prod target on an UNLINKED revision is not-ready (nil
		// revision), so no-rev-svc matches; alpha's prod target links a ready
		// revision so alpha is excluded.
		{"scope+notready", SearchFilter{Scope: "prod", NotReady: true}, "no-rev-svc", "alpha"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := q.Search(tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if !hasService(res.Services, tt.want) {
				t.Errorf("want %q present in %v", tt.want, names(res.Services))
			}
			if tt.absent != "" && hasService(res.Services, tt.absent) {
				t.Errorf("%q should be absent from %v", tt.absent, names(res.Services))
			}
		})
	}
}

// TestSearch_CorrelatedTargetsNegative is the §7.1 negative case: a service with a
// PRODUCTION target on a READY revision and a STAGING target on a NOT-READY
// revision must NOT match "production AND not-ready" — the two conditions come from
// different targets and are never satisfied by correlating across them.
func TestSearch_CorrelatedTargetsNegative(t *testing.T) {
	q := correlationFleet(t)

	// Negative: production + not-ready must not match (prod target is ready).
	for _, f := range []SearchFilter{
		{Scope: "production", NotReady: true},
		{Labels: map[string]string{"env": "production"}, NotReady: true},
	} {
		res, err := q.Search(f)
		if err != nil {
			t.Fatal(err)
		}
		if hasService(res.Services, "corr") {
			t.Errorf("filter %+v must NOT match corr (conditions span two different targets)", f)
		}
	}

	// Positive controls: each condition holds for its OWN target.
	positives := []struct {
		name string
		f    SearchFilter
	}{
		{"prod+ready", SearchFilter{Scope: "production", ReadyOnly: true}},
		{"staging+notready", SearchFilter{Scope: "staging", NotReady: true}},
		{"prod label+ready", SearchFilter{Labels: map[string]string{"env": "production"}, ReadyOnly: true}},
	}
	for _, tt := range positives {
		t.Run(tt.name, func(t *testing.T) {
			res, err := q.Search(tt.f)
			if err != nil {
				t.Fatal(err)
			}
			if !hasService(res.Services, "corr") {
				t.Errorf("filter %+v should match corr", tt.f)
			}
		})
	}
}

// correlationFleet builds a single service "corr" with two revisions (one ready,
// one not) each linked to a target in a different scope/label.
func correlationFleet(t *testing.T) *Query {
	t.Helper()
	ready := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "corr", Version: "1.0.0"},
		Readiness: &contract.Readiness{
			Expires: "2099-12-31",
			Claims:  []contract.ReadinessClaim{{ID: "d", Type: "url", Status: contract.StatusDone, Evidence: "https://x", Weight: 10}},
		},
	}
	notReady := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "corr", Version: "2.0.0"}}
	col := &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: ready, FS: fstest.MapFS{}}, Digest: "sha256:ready"},
			{Bundle: &contract.Bundle{Contract: notReady, FS: fstest.MapFS{}}, Digest: "sha256:notready"},
		},
		Targets: []RawTarget{
			{Scope: "production", Kind: "k8s", Name: "corr-prod", Service: "corr", Digest: "sha256:ready", Labels: map[string]string{"env": "production"}, Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())},
			{Scope: "staging", Kind: "k8s", Name: "corr-stg", Service: "corr", Digest: "sha256:notready", Labels: map[string]string{"env": "staging"}, Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())},
		},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// TestSearch_ValidationErrors asserts a malformed filter returns a typed
// *InvalidQueryError rather than silently defaulting.
func TestSearch_ValidationErrors(t *testing.T) {
	q := queryFleet(t)
	for _, f := range []SearchFilter{
		{Offset: -1},
		{Limit: -1},
		{Status: "Bogus"},
		{Compliance: "Bogus"},
	} {
		_, err := q.Search(f)
		var iqe *InvalidQueryError
		if !errors.As(err, &iqe) {
			t.Errorf("filter %+v: want *InvalidQueryError, got %v", f, err)
		}
	}
	// A well-formed filter validates.
	if _, err := q.Search(SearchFilter{Status: StatusCompliant, Compliance: StatusNonCompliant}); err != nil {
		t.Fatalf("valid filter should not error: %v", err)
	}
}

func TestSearch_LimitOffsetAndCap(t *testing.T) {
	q := queryFleet(t)
	all, err := q.Search(SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	total := all.Total
	if all.Count != total {
		t.Fatalf("default search should return all %d, got %d", total, all.Count)
	}

	// Limit above MaxSearchLimit is capped but still returns everything here.
	capped, err := q.Search(SearchFilter{Limit: MaxSearchLimit + 1000})
	if err != nil {
		t.Fatal(err)
	}
	if capped.Count != total {
		t.Errorf("capped search count = %d, want %d", capped.Count, total)
	}

	// Offset paging: skip first, take one.
	page, err := q.Search(SearchFilter{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 1 {
		t.Errorf("paged count = %d, want 1", page.Count)
	}
	if page.Total != total {
		t.Errorf("paged total = %d, want %d", page.Total, total)
	}
	// The paged service is the second in sorted order.
	sortedNames := names(all.Services)
	sort.Strings(sortedNames)
	if page.Services[0].Name != sortedNames[1] {
		t.Errorf("paged first = %q, want %q", page.Services[0].Name, sortedNames[1])
	}
	// Hit-projection carries counts.
	for _, h := range all.Services {
		if h.Name == "alpha" && h.TargetCount != 1 {
			t.Errorf("alpha target count = %d", h.TargetCount)
		}
	}
}

func hasService(hits []ServiceHit, name string) bool {
	for _, h := range hits {
		if h.Name == name {
			return true
		}
	}
	return false
}

func names(hits []ServiceHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Name
	}
	return out
}

// -------------------- GetService --------------------

func TestGetService(t *testing.T) {
	q := queryFleet(t)

	leaf, err := q.GetService("leaf-svc")
	if err != nil {
		t.Fatal(err)
	}
	// Capabilities are attributed PER REVISION (never flattened to one
	// "representative"): leaf-svc has one revision carrying tools + skills.
	if len(leaf.Capabilities) != 1 {
		t.Fatalf("leaf-svc should expose one revision's capabilities, got %d", len(leaf.Capabilities))
	}
	capA := leaf.Capabilities[0]
	if capA.Revision != leaf.Revisions[0].Key {
		t.Errorf("capability must reference its exact revision, got %q vs %q", capA.Revision, leaf.Revisions[0].Key)
	}
	if capA.Version != "1.0.0" {
		t.Errorf("capability version = %q, want 1.0.0", capA.Version)
	}
	if len(capA.Tools) == 0 {
		t.Error("leaf-svc revision should expose tools")
	}
	if len(capA.Skills) == 0 {
		t.Error("leaf-svc revision should expose skills")
	}
	if !containsStr(leaf.Dependents, "alpha") {
		t.Errorf("leaf-svc dependents should include alpha, got %v", leaf.Dependents)
	}
	if len(leaf.Revisions) != 1 {
		t.Errorf("leaf-svc revisions = %d", len(leaf.Revisions))
	}

	alpha, err := q.GetService("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha.Dependencies) != 2 {
		t.Errorf("alpha should declare 2 dependency edges, got %d", len(alpha.Dependencies))
	}
	// alpha's revision carries no tools/skills (empty FS) → no capabilities entry.
	if len(alpha.Capabilities) != 0 {
		t.Errorf("alpha should expose no capabilities, got %+v", alpha.Capabilities)
	}

	if _, err := q.GetService("does-not-exist"); err == nil {
		t.Fatal("missing service should error")
	} else if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("want NotFoundError, got %T", err)
	}
}

// -------------------- GetTarget --------------------

func TestGetTarget(t *testing.T) {
	q := queryFleet(t)

	// exact key + linked revision.
	byKey, err := q.GetTarget(string(NewTargetKey("prod", "k8s", "alpha-app")))
	if err != nil {
		t.Fatal(err)
	}
	if byKey.Revision == nil {
		t.Error("alpha-app should link to a revision")
	}

	// unique name.
	byName, err := q.GetTarget("beta-app")
	if err != nil {
		t.Fatal(err)
	}
	if byName.Target.Name != "beta-app" {
		t.Errorf("got %q", byName.Target.Name)
	}

	// unmatched revision → nil Revision.
	orphan, err := q.GetTarget("orphan-app")
	if err != nil {
		t.Fatal(err)
	}
	if orphan.Revision != nil {
		t.Error("orphan-app should not link to any revision")
	}

	// ambiguous name.
	if _, err := q.GetTarget("dup"); err == nil {
		t.Fatal("dup should be ambiguous")
	} else if ae, ok := err.(*AmbiguousError); !ok {
		t.Errorf("want AmbiguousError, got %T", err)
	} else if len(ae.Matches) != 2 {
		t.Errorf("ambiguous matches = %v", ae.Matches)
	}

	// not found.
	if _, err := q.GetTarget("nope"); err == nil {
		t.Fatal("missing target should error")
	} else if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("want NotFoundError, got %T", err)
	}
}

// -------------------- Graph --------------------

// graphFleet builds a snapshot with a chain, a cycle, a diamond, and an
// unresolved dependency for graph-traversal tests.
func graphFleet(t *testing.T) *Query {
	t.Helper()
	dep := func(name string, deps ...string) RawRevision {
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0"}}
		for _, d := range deps {
			c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, Ref: "oci://x/" + d, Required: true, Compatibility: "^1.0.0"})
		}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name}
	}
	col := &Collection{Revisions: []RawRevision{
		dep("g-a", "g-b", "g-missing"),
		dep("g-b", "g-c"),
		dep("g-c"),
		dep("g-x", "g-y"),
		dep("g-y", "g-x"),
		dep("d-a", "d-b", "d-c"),
		dep("d-b", "d-e"),
		dep("d-c", "d-e"),
		dep("d-e"),
		// m participates in two distinct cycles (m↔n and m↔p) so the cycle sort
		// comparator is exercised.
		dep("m", "n", "p"),
		dep("n", "m"),
		dep("p", "m"),
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestGraph_Dependencies(t *testing.T) {
	q := graphFleet(t)
	// transitive dependencies from g-a.
	res, err := q.Graph(GraphQuery{Service: "g-a", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Direction != DirectionDependencies {
		t.Errorf("default direction = %q", res.Direction)
	}
	got := nodeDepths(res)
	if got["g-b"] != 1 || got["g-c"] != 2 {
		t.Errorf("transitive depths = %v", got)
	}
	if !hasUnresolvedTo(res.Unresolved, "g-missing") {
		t.Errorf("unresolved should list a g-missing edge, got %v", res.Unresolved)
	}
	if len(res.Edges) == 0 {
		t.Error("dependency edges should be populated")
	}
}

// hasUnresolvedTo reports whether an unresolved relationship edge points at to.
func hasUnresolvedTo(rels []Relationship, to string) bool {
	for _, r := range rels {
		if r.To == to && !r.Resolved {
			return true
		}
	}
	return false
}

func TestGraph_Direct(t *testing.T) {
	q := graphFleet(t)
	res, err := q.Graph(GraphQuery{Service: "g-a", Transitive: false})
	if err != nil {
		t.Fatal(err)
	}
	got := nodeDepths(res)
	if _, ok := got["g-b"]; !ok {
		t.Error("direct neighbor g-b missing")
	}
	if _, ok := got["g-c"]; ok {
		t.Error("direct-only traversal must not reach g-c")
	}
}

func TestGraph_MaxDepth(t *testing.T) {
	q := graphFleet(t)
	res, err := q.Graph(GraphQuery{Service: "g-a", Transitive: true, MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := nodeDepths(res)
	if _, ok := got["g-c"]; ok {
		t.Errorf("MaxDepth 1 must not reach g-c: %v", got)
	}
}

func TestGraph_Dependents(t *testing.T) {
	q := graphFleet(t)
	res, err := q.Graph(GraphQuery{Service: "g-c", Direction: DirectionDependents, Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	got := nodeDepths(res)
	if got["g-b"] != 1 || got["g-a"] != 2 {
		t.Errorf("dependents depths = %v", got)
	}
	// dependents edges: the g-b→g-c edge resolves to g-c.
	found := false
	for _, e := range res.Edges {
		if e.FromService == "g-b" && e.ToService == "g-c" {
			found = true
		}
	}
	if !found {
		t.Errorf("dependents edges should include g-b→g-c, got %v", res.Edges)
	}
}

func TestGraph_Cycle(t *testing.T) {
	q := graphFleet(t)
	res, err := q.Graph(GraphQuery{Service: "g-x", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cycles) == 0 {
		t.Fatal("cycle g-x↔g-y should be recorded")
	}
	last := res.Cycles[0][len(res.Cycles[0])-1]
	if last != "g-x" {
		t.Errorf("cycle should close on g-x, got %v", res.Cycles[0])
	}
}

func TestGraph_Diamond(t *testing.T) {
	q := graphFleet(t)
	// d-e is reachable via both d-b and d-c; it must appear exactly once.
	res, err := q.Graph(GraphQuery{Service: "d-a", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, n := range res.Nodes {
		if n.Name == "d-e" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("d-e should be visited once, got %d", count)
	}
}

func TestGraph_MultipleCycles(t *testing.T) {
	q := graphFleet(t)
	res, err := q.Graph(GraphQuery{Service: "m", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cycles) < 2 {
		t.Fatalf("expected >=2 cycles from m, got %d: %v", len(res.Cycles), res.Cycles)
	}
	// Deterministically ordered.
	for i := 1; i < len(res.Cycles); i++ {
		if joinCycle(res.Cycles[i-1]) > joinCycle(res.Cycles[i]) {
			t.Errorf("cycles not sorted: %v", res.Cycles)
		}
	}
}

func joinCycle(c []ServiceKey) string {
	out := ""
	for _, s := range c {
		out += string(s) + "\x00"
	}
	return out
}

func TestGraph_NotFound(t *testing.T) {
	q := graphFleet(t)
	if _, err := q.Graph(GraphQuery{Service: "nope"}); err == nil {
		t.Fatal("unknown root should error")
	}
}

func TestGraph_RevisionScope(t *testing.T) {
	q := NewQuery(twoRevSnapshot(t))
	rev1 := NewRevisionKey(NewServiceKey("svc"), "sha256:1")
	rev2 := NewRevisionKey(NewServiceKey("svc"), "sha256:2")

	// A revision-scoped graph uses THAT revision's exact dependencies only.
	r1, err := q.Graph(GraphQuery{Revision: rev1, Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Root != "svc" || r1.Revision != rev1 || r1.Aggregated {
		t.Errorf("rev1 scope: root/revision/aggregated wrong: %+v", r1)
	}
	d1 := nodeDepths(r1)
	if _, ok := d1["old-dep"]; !ok {
		t.Errorf("rev1 graph should reach old-dep: %v", d1)
	}
	if _, ok := d1["new-dep"]; ok {
		t.Errorf("rev1 graph must NOT reach rev2's new-dep: %v", d1)
	}
	// Root edges are scoped to rev1 only.
	for _, e := range r1.Edges {
		if e.FromService == "svc" && e.FromRevision != rev1 {
			t.Errorf("rev1 graph leaked a non-rev1 root edge: %+v", e)
		}
	}

	r2, err := q.Graph(GraphQuery{Revision: rev2, Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	d2 := nodeDepths(r2)
	if _, ok := d2["new-dep"]; !ok {
		t.Errorf("rev2 graph should reach new-dep: %v", d2)
	}
	if _, ok := d2["old-dep"]; ok {
		t.Errorf("rev2 graph must NOT reach old-dep: %v", d2)
	}
}

func TestGraph_TargetScope(t *testing.T) {
	// A target-scoped graph uses the target's LINKED revision (svc-app pins rev1).
	q := NewQuery(twoRevSnapshot(t))
	res, err := q.Graph(GraphQuery{Target: "svc-app", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Aggregated {
		t.Error("a target-scoped graph is exact, not aggregated")
	}
	got := nodeDepths(res)
	if _, ok := got["old-dep"]; !ok {
		t.Errorf("target graph should follow the linked (rev1) deps: %v", got)
	}
	if _, ok := got["new-dep"]; ok {
		t.Errorf("target graph must not follow rev2's deps: %v", got)
	}
}

func TestGraph_ServiceAggregated(t *testing.T) {
	// A bare service query over a multi-revision service aggregates every revision's
	// deps and flags Aggregated.
	q := NewQuery(twoRevSnapshot(t))
	res, err := q.Graph(GraphQuery{Service: "svc", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Aggregated {
		t.Error("multi-revision service graph should be Aggregated")
	}
	if res.Revision != "" {
		t.Errorf("aggregated graph carries no single revision, got %q", res.Revision)
	}
	got := nodeDepths(res)
	if _, ok := got["old-dep"]; !ok {
		t.Errorf("aggregated graph should reach old-dep: %v", got)
	}
	if _, ok := got["new-dep"]; !ok {
		t.Errorf("aggregated graph should reach new-dep: %v", got)
	}
}

func TestGraph_UnknownRevision(t *testing.T) {
	q := NewQuery(twoRevSnapshot(t))
	_, err := q.Graph(GraphQuery{Revision: "svc@bogus"})
	var nf *NotFoundError
	if !errors.As(err, &nf) || nf.Kind != "revision" {
		t.Errorf("unknown revision should yield a revision NotFoundError, got %v", err)
	}
}

func TestGraph_TargetNotFound(t *testing.T) {
	q := NewQuery(twoRevSnapshot(t))
	if _, err := q.Graph(GraphQuery{Target: "no-such-target"}); err == nil {
		t.Fatal("unknown target should error (propagated from GetTarget)")
	}
}

// TestGraph_SkipsReferenceEdges asserts the graph traversal only follows and
// collects dependency edges — config/policy reference edges are skipped by
// graphEdges even when the declaring service is the root.
func TestGraph_SkipsReferenceEdges(t *testing.T) {
	gc := &contract.Contract{
		PactoVersion:   "2.0",
		Service:        contract.Service{Name: "gc", Version: "1.0.0"},
		Dependencies:   []contract.Dependency{{Name: "gd", Ref: "oci://x/gd", Required: true, Compatibility: "^1.0.0"}},
		Configurations: []contract.Configuration{{Name: "gcfg", Ref: "oci://x/gcfg"}},
	}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: gc, FS: fstest.MapFS{}}, Digest: "sha256:gc"},
		{Bundle: bundleFor(t, "gd"), Digest: "sha256:gd"},
		{Bundle: bundleFor(t, "gcfg"), Digest: "sha256:gcfg"},
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewQuery(snap).Graph(GraphQuery{Service: "gc", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeDepths(res)["gcfg"]; ok {
		t.Error("graph must not traverse a configuration reference edge")
	}
	for _, e := range res.Edges {
		if e.Type != RelationshipDependency {
			t.Errorf("graph edges must be dependency-only, got %+v", e)
		}
	}
}

func TestGraph_InvalidDirection(t *testing.T) {
	q := graphFleet(t)
	_, err := q.Graph(GraphQuery{Service: "g-a", Direction: Direction("sideways")})
	var iqe *InvalidQueryError
	if !errors.As(err, &iqe) || iqe.Field != "direction" {
		t.Errorf("bad direction should yield a direction InvalidQueryError, got %v", err)
	}
}

func TestGraph_NegativeMaxDepth(t *testing.T) {
	q := graphFleet(t)
	_, err := q.Graph(GraphQuery{Service: "g-a", MaxDepth: -1})
	var iqe *InvalidQueryError
	if !errors.As(err, &iqe) || iqe.Field != "maxDepth" {
		t.Errorf("negative MaxDepth should yield a maxDepth InvalidQueryError, got %v", err)
	}
}

func nodeDepths(res *GraphResult) map[string]int {
	out := map[string]int{}
	for _, n := range res.Nodes {
		out[n.Name] = n.Depth
	}
	return out
}

// -------------------- Status --------------------

func TestStatus_Categories(t *testing.T) {
	q := queryFleet(t)
	tests := []struct {
		name  string
		query StatusQuery
		code  string
	}{
		{"noncompliant", StatusQuery{NonCompliant: true}, "NON_COMPLIANT"},
		{"unknown", StatusQuery{Unknown: true}, "UNKNOWN"},
		{"stale", StatusQuery{StaleEvidence: true}, "STALE_EVIDENCE"},
		{"invalid", StatusQuery{Invalid: true}, "INVALID_CONTRACT"},
		{"missing readiness", StatusQuery{MissingReadiness: true}, "MISSING_READINESS"},
		{"unresolved deps", StatusQuery{UnresolvedDeps: true}, "UNRESOLVED_DEPENDENCY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := q.Status(tt.query)
			if !hasCode(res.Items, tt.code) {
				t.Errorf("expected code %q in %+v", tt.code, res.Items)
			}
		})
	}
}

func TestStatus_NeedsAttentionAndOrderAndLimit(t *testing.T) {
	q := queryFleet(t)
	all := q.Status(StatusQuery{NeedsAttention: true})
	if len(all.Items) < 5 {
		t.Errorf("NeedsAttention should aggregate every category, got %d", len(all.Items))
	}
	// Deterministic order: sorted by Code, then Name.
	for i := 1; i < len(all.Items); i++ {
		a, b := all.Items[i-1], all.Items[i]
		if a.Code > b.Code || (a.Code == b.Code && a.Name > b.Name) {
			t.Errorf("items not sorted at %d: %+v then %+v", i, a, b)
		}
	}
	// Limit cap.
	limited := q.Status(StatusQuery{NeedsAttention: true, Limit: 1})
	if len(limited.Items) != 1 {
		t.Errorf("limit 1 should truncate, got %d", len(limited.Items))
	}
}

func hasCode(items []StatusItem, code string) bool {
	for _, it := range items {
		if it.Code == code {
			return true
		}
	}
	return false
}

// -------------------- Explain --------------------

func TestExplain_Service(t *testing.T) {
	q := queryFleet(t)
	res, err := q.Explain("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "service" || res.Subject != "alpha" {
		t.Errorf("unexpected subject: %+v", res)
	}
	if !hasReason(res.Reasons, LimitationUnresolvedDep) {
		t.Errorf("alpha explain should cite the unresolved ghost dependency: %+v", res.Reasons)
	}
}

func TestExplain_TargetFindingsAndStale(t *testing.T) {
	q := queryFleet(t)
	res, err := q.Explain("beta-app")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "target" {
		t.Errorf("kind = %q", res.Kind)
	}
	if !hasReason(res.Reasons, "DRIFT") {
		t.Error("target findings should surface as reasons")
	}
	if !hasReason(res.Reasons, LimitationSourceStale) {
		t.Error("stale evidence should surface as a reason")
	}
}

func TestExplain_TargetMissingEvidence(t *testing.T) {
	q := queryFleet(t)
	res, err := q.Explain("unk-app")
	if err != nil {
		t.Fatal(err)
	}
	if !hasReason(res.Reasons, LimitationEvidenceMissing) {
		t.Errorf("missing-evidence reason expected: %+v", res.Reasons)
	}
}

func TestExplain_NotFound(t *testing.T) {
	q := queryFleet(t)
	if _, err := q.Explain("nothing-here"); err == nil {
		t.Fatal("unknown subject should error")
	} else if nf, ok := err.(*NotFoundError); !ok || nf.Kind != "subject" {
		t.Errorf("want subject NotFoundError, got %T %v", err, err)
	}
}

func TestExplain_AmbiguousPropagates(t *testing.T) {
	q := queryFleet(t)
	if _, err := q.Explain("dup"); err == nil {
		t.Fatal("ambiguous subject should error")
	} else if _, ok := err.(*AmbiguousError); !ok {
		t.Errorf("ambiguous error should propagate, got %T", err)
	}
}

func hasReason(rs []Reason, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}

// -------------------- error types --------------------

func TestErrorMessages(t *testing.T) {
	nf := &NotFoundError{Kind: "service", ID: "x"}
	if nf.Error() == "" {
		t.Error("NotFoundError message empty")
	}
	ae := &AmbiguousError{Kind: "target", ID: "dup", Matches: []string{"a", "b"}}
	if ae.Error() == "" {
		t.Error("AmbiguousError message empty")
	}
	iqe := &InvalidQueryError{Field: "direction", Value: "sideways", Reason: "must be dependencies or dependents"}
	if iqe.Error() == "" {
		t.Error("InvalidQueryError message empty")
	}
}
