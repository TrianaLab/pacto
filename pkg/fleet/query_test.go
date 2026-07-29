package fleet

import (
	"context"
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := q.Search(tt.filter)
			if !hasService(res.Services, tt.want) {
				t.Errorf("want %q present in %v", tt.want, names(res.Services))
			}
			if tt.absent != "" && hasService(res.Services, tt.absent) {
				t.Errorf("%q should be absent from %v", tt.absent, names(res.Services))
			}
		})
	}
}

func TestSearch_LimitOffsetAndCap(t *testing.T) {
	q := queryFleet(t)
	all := q.Search(SearchFilter{})
	total := all.Total
	if all.Count != total {
		t.Fatalf("default search should return all %d, got %d", total, all.Count)
	}

	// Limit above MaxSearchLimit is capped but still returns everything here.
	capped := q.Search(SearchFilter{Limit: MaxSearchLimit + 1000})
	if capped.Count != total {
		t.Errorf("capped search count = %d, want %d", capped.Count, total)
	}

	// Offset paging: skip first, take one.
	page := q.Search(SearchFilter{Offset: 1, Limit: 1})
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
	if len(leaf.Tools) == 0 {
		t.Error("leaf-svc representative should expose tools")
	}
	if len(leaf.Skills) == 0 {
		t.Error("leaf-svc representative should expose skills")
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
	if !containsStr(res.Unresolved, "g-missing") {
		t.Errorf("unresolved should list g-missing, got %v", res.Unresolved)
	}
	if len(res.Edges) == 0 {
		t.Error("dependency edges should be populated")
	}
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
	// edgesFor dependents: the g-b→g-c edge resolves to g-c.
	found := false
	for _, e := range res.Edges {
		if e.From == "g-b" && e.ResolvedService == "g-c" {
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

func joinCycle(c []string) string {
	out := ""
	for _, s := range c {
		out += s + "\x00"
	}
	return out
}

func TestGraph_NotFound(t *testing.T) {
	q := graphFleet(t)
	if _, err := q.Graph(GraphQuery{Service: "nope"}); err == nil {
		t.Fatal("unknown root should error")
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
}
