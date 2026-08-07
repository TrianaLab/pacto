package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

// richFleetQuery builds a fleet exercising every product-detail surface: two
// services (app depends on lib and on a non-existent "ghost", with an observed
// app->lib edge), and one app deployment carrying findings, coverage, observed
// runtime, evidence and a limitation. It is the substrate for the transport
// (href) and immutability tests.
func richFleetQuery(t *testing.T) *fleet.Query {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev := now.Add(-time.Hour)
	appBundle := &contract.Bundle{Contract: &contract.Contract{
		PactoVersion:   "2.0",
		Service:        contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "team-a"}},
		Interfaces:     []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI}},
		Configurations: []contract.Configuration{{Name: "cfg"}},
		Policies:       []contract.Policy{{Name: "pol"}},
		Capabilities:   []contract.Capability{{Type: "health"}},
		Dependencies:   []contract.Dependency{{Name: "lib", Ref: "oci://x/lib"}, {Name: "ghost", Ref: "oci://x/ghost"}},
	}, RawYAML: []byte("app"), FS: fstest.MapFS{}}
	libBundle := &contract.Bundle{Contract: &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "lib", Version: "1.0.0", Owner: contract.Owner{Team: "team-b"}},
	}, RawYAML: []byte("lib"), FS: fstest.MapFS{}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{
				{Bundle: appBundle, ResolvedRef: "oci://x/app@sha256:app", Digest: "sha256:app"},
				{Bundle: libBundle, ResolvedRef: "oci://x/lib@sha256:lib", Digest: "sha256:lib"},
			},
			Targets: []fleet.RawTarget{{
				Scope: "prod", Kind: "k8s", Name: "app-1", Service: "app",
				ResolvedRef: "oci://x/app@sha256:app", Digest: "sha256:app",
				Compliance:      fleet.StatusNonCompliant,
				Findings:        []finding.Finding{{Message: "boom"}},
				Coverage:        &fleet.Coverage{Evaluated: 1, Required: 2},
				ObservedRuntime: map[string]any{"k": "v"},
				EvidenceAt:      &ev,
				Limitations:     []fleet.Limitation{{Code: "X", Message: "m"}},
			}},
			Observed: []fleet.ObservedEdge{{From: "app", To: "lib", Count: 5, FirstSeen: ev, LastSeen: ev}},
		}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func appRevKey(t *testing.T, q *fleet.Query) string {
	t.Helper()
	v, err := q.GetService("app")
	if err != nil {
		t.Fatal(err)
	}
	return string(v.Revisions[0].Key)
}

// walkHrefs collects every "href" leaf in a JSON tree and reports whether any is
// empty (so a missing transport href anywhere is caught).
func walkHrefs(v any) (count, empty int) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "href" {
				count++
				if s, _ := val.(string); s == "" {
					empty++
				}
			}
			c, e := walkHrefs(val)
			count, empty = count+c, empty+e
		}
	case []any:
		for _, val := range t {
			c, e := walkHrefs(val)
			count, empty = count+c, empty+e
		}
	}
	return count, empty
}

// productPayloadCount reports how many kind payloads a transport detail populated.
func productPayloadCount(pd *ProductEntityDetail) int {
	n := 0
	for _, ok := range []bool{pd.Service != nil, pd.Revision != nil, pd.Target != nil, pd.Owner != nil, pd.Source != nil} {
		if ok {
			n++
		}
	}
	return n
}

// assertNoEmptyHrefs marshals v and fails if any "href" leaf is empty or absent.
func assertNoEmptyHrefs(t *testing.T, label string, v any) {
	t.Helper()
	blob, _ := json.Marshal(v)
	var tree any
	_ = json.Unmarshal(blob, &tree)
	cnt, empty := walkHrefs(tree)
	if cnt == 0 {
		t.Errorf("%s: must contain at least one href", label)
	}
	if empty != 0 {
		t.Errorf("%s: %d empty href(s) in transport output", label, empty)
	}
}

// TestProductDetail_AllKindsHrefs converts every entity-detail kind through the
// transport and proves exactly one payload is populated and every reference
// carries a non-empty href.
func TestProductDetail_AllKindsHrefs(t *testing.T) {
	q := richFleetQuery(t)
	cases := []struct{ kind, key string }{
		{"service", "app"},
		{"revision", appRevKey(t, q)},
		{"target", string(fleet.NewTargetKey("prod", "k8s", "app-1"))},
		{"owner", "team-a"},
		{"source", "local"},
	}
	for _, c := range cases {
		d, err := q.EntityDetail(fleet.EntityKind(c.kind), c.key)
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		pd := toProductEntityDetail(d)
		if productPayloadCount(pd) != 1 {
			t.Errorf("%s: exactly one payload must be populated, got %d", c.kind, productPayloadCount(pd))
		}
		if pd.Entity.Href == "" {
			t.Errorf("%s: entity ref must carry an href", c.kind)
		}
		assertNoEmptyHrefs(t, c.kind, pd)
	}
}

// TestProductOverviewAndNeighborhoodHrefs exercises the overview and neighborhood
// converters (entry points, edges, unresolved deps) for href completeness.
func TestProductOverviewAndNeighborhoodHrefs(t *testing.T) {
	q := richFleetQuery(t)
	ov := toProductOverview(q.Overview())
	if len(ov.EntryPoints) == 0 {
		t.Error("overview must have entry points")
	}
	for _, ep := range ov.EntryPoints {
		if ep.Href == "" {
			t.Errorf("entry point %q must carry an href", ep.Label)
		}
	}
	nb, err := q.Neighborhood(fleet.NeighborhoodQuery{Kind: fleet.KindService, Key: "app", Direction: fleet.DirectionDependencies, Views: []fleet.KnowledgeView{fleet.ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	pnb := toProductNeighborhood(nb)
	if len(pnb.Edges) == 0 || pnb.Edges[0].Href == "" {
		t.Errorf("neighborhood edge must carry an href: %+v", pnb.Edges)
	}
	if pnb.UnresolvedDependencies.Count == 0 || pnb.UnresolvedDependencies.Items[0].From.Href == "" {
		t.Errorf("unresolved dependency must carry a navigable source: %+v", pnb.UnresolvedDependencies)
	}
	assertNoEmptyHrefs(t, "neighborhood", pnb)
}

// TestProductRef_DeepLinkHrefs proves an href is generated from the EXACT
// canonical key (never a label) and round-trips through URL escaping for keys
// containing domain separators, escaped slashes, percent characters and
// owner/source strings.
func TestProductRef_DeepLinkHrefs(t *testing.T) {
	keys := []struct {
		kind fleet.EntityKind
		key  string
	}{
		{fleet.KindService, "domain-a/payment-service"},
		{fleet.KindTarget, string(fleet.NewTargetKey("prod/eu", "k8s", "pay/svc"))},
		{fleet.KindOwner, "team a & co"},
		{fleet.KindSource, "oci://registry:5000/x"},
		{fleet.KindRevision, "domain-a/svc@sha256:abc"},
		{fleet.KindService, "weird%2Fkey"},
	}
	for _, k := range keys {
		r := productRef(fleet.EntityRef{Kind: k.kind, Key: k.key, Label: "ignore-me"})
		if r.Href == "" {
			t.Fatalf("no href for %q", k.key)
		}
		// The last path segment decodes back to the exact key.
		seg := r.Href[strings.LastIndex(r.Href, "/")+1:]
		got, err := url.PathUnescape(seg)
		if err != nil {
			t.Fatalf("href segment for %q is not valid escaping: %q", k.key, seg)
		}
		if got != k.key {
			t.Errorf("href for %q did not round-trip: segment %q -> %q", k.key, seg, got)
		}
		// The href never contains the display label.
		if strings.Contains(r.Href, "ignore-me") {
			t.Errorf("href must be built from the key, not the label: %q", r.Href)
		}
	}
}

// impactWithRes builds a ProductImpact for the app service from a crafted result.
func impactWithRes(t *testing.T, q *fleet.Query, res *impact.Result, limit, offset int) *ProductImpact {
	t.Helper()
	snap := q.Snapshot()
	v, err := q.GetService("app")
	if err != nil {
		t.Fatal(err)
	}
	rev := v.Revisions[0]
	return buildProductImpact(q.ProductMeta(), snap, rev.ServiceKey, rev, rev, res, limit, offset)
}

// adversarialImpactResult builds an impact result whose every list is above its
// bound, for the bound/paging tests. It returns the result, the consumer count
// and the (over-cap) path length.
func adversarialImpactResult() (*impact.Result, int, int) {
	const nConsumers = 250
	longPath := make([]string, MaxImpactPath+10)
	for i := range longPath {
		longPath[i] = fmt.Sprintf("svc-%03d", i)
	}
	consumers := make([]impact.AffectedConsumer, nConsumers)
	for i := range consumers {
		consumers[i] = impact.AffectedConsumer{Service: fmt.Sprintf("consumer-%03d", i), Path: longPath}
	}
	owners := make([]string, MaxImpactOwners+7)
	for i := range owners {
		owners[i] = fmt.Sprintf("owner-%03d", i)
	}
	targets := make([]string, MaxImpactActiveTargets+9)
	for i := range targets {
		targets[i] = fmt.Sprintf("prod/k8s/t-%03d", i)
	}
	lims := make([]fleet.Limitation, MaxImpactLimitations+11)
	for i := range lims {
		lims[i] = fleet.Limitation{Code: fmt.Sprintf("L%03d", i)}
	}
	return &impact.Result{Consumers: consumers, Owners: owners, ActiveTargets: targets, Limitations: lims}, nConsumers, len(longPath)
}

// TestProductImpact_ListsBounded proves owners, active targets, limitations and
// per-consumer path are all bounded above their maxima with honest metadata.
func TestProductImpact_ListsBounded(t *testing.T) {
	q := richFleetQuery(t)
	res, _, pathLen := adversarialImpactResult()
	p := impactWithRes(t, q, res, 20, 0)
	if p.Owners.Count != MaxImpactOwners || !p.Owners.Truncated || p.Owners.Total != len(res.Owners) {
		t.Errorf("owners not bounded: %+v", p.Owners)
	}
	if p.ActiveTargets.Count != MaxImpactActiveTargets || !p.ActiveTargets.Truncated {
		t.Errorf("active targets not bounded: %+v", p.ActiveTargets)
	}
	if p.Limitations.Count != MaxImpactLimitations || !p.Limitations.Truncated {
		t.Errorf("limitations not bounded: %+v", p.Limitations)
	}
	c0 := p.Consumers.Items[0]
	if !c0.PathTruncated || c0.PathTotal != pathLen || len(c0.Path) != MaxImpactPath {
		t.Errorf("consumer path not bounded: %+v", c0)
	}
}

// walkImpactConsumers walks every consumer page and returns the set of distinct
// consumer keys seen (asserting no page overlaps or fails to terminate).
func walkImpactConsumers(t *testing.T, q *fleet.Query, res *impact.Result, limit, total int) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	offset, guard := 0, 0
	for {
		page := impactWithRes(t, q, res, limit, offset)
		for _, c := range page.Consumers.Items {
			if seen[c.Service.Key] {
				t.Errorf("consumer %q seen twice across pages", c.Service.Key)
			}
			seen[c.Service.Key] = true
		}
		if page.Consumers.NextOffset == nil {
			return seen
		}
		offset = *page.Consumers.NextOffset
		if guard++; guard > total {
			t.Fatal("consumer paging did not terminate")
		}
	}
}

// TestProductImpact_ConsumerPaging proves consumers are stably offset-pageable
// (page 1 truncated, a full walk reconstructs every consumer once), the limit is
// capped and defaulted, and an offset beyond total is an empty final page.
func TestProductImpact_ConsumerPaging(t *testing.T) {
	q := richFleetQuery(t)
	res, n, _ := adversarialImpactResult()
	limit := 20
	p1 := impactWithRes(t, q, res, limit, 0)
	if p1.Consumers.Total != n || p1.Consumers.Count != limit || !p1.Consumers.Truncated || p1.Consumers.NextOffset == nil {
		t.Fatalf("consumer page 1 wrong: %+v", p1.Consumers)
	}
	if seen := walkImpactConsumers(t, q, res, limit, n); len(seen) != n {
		t.Errorf("paged walk saw %d consumers, want %d", len(seen), n)
	}
	if big := impactWithRes(t, q, res, MaxImpactConsumers+100, 0); big.Consumers.Limit != MaxImpactConsumers {
		t.Errorf("excessive consumer limit not capped: %d", big.Consumers.Limit)
	}
	zero := impactWithRes(t, q, &impact.Result{Consumers: res.Consumers[:5]}, 0, 0)
	if zero.Consumers.Limit != DefaultImpactConsumers || zero.Consumers.Truncated {
		t.Errorf("default limit / small result wrong: %+v", zero.Consumers)
	}
	if beyond := impactWithRes(t, q, res, limit, 100000); beyond.Consumers.Count != 0 || beyond.Consumers.NextOffset != nil {
		t.Errorf("offset beyond total must be an empty final page: %+v", beyond.Consumers)
	}
}

// TestProductImpact_Immutable proves a ProductImpact is fully detached from the
// raw impact result and the snapshot: deep mutation of the answer leaves a second
// identical build unchanged (item 7).
func TestProductImpact_Immutable(t *testing.T) {
	q := richFleetQuery(t)
	targetKey := string(fleet.NewTargetKey("prod", "k8s", "app-1"))
	res := &impact.Result{
		Service: "app", Classification: "BREAKING",
		Consumers: []impact.AffectedConsumer{{
			Service: "lib", Depth: 1, Direct: true, Path: []string{"app", "lib"},
			Owner: "team-b", Confidence: impact.ConfidenceContractual, CompatibilityVerdict: "incompatible",
		}},
		Owners: []string{"team-a"}, ActiveTargets: []string{targetKey},
		Limitations: []fleet.Limitation{{Code: "L1", Message: "m"}},
	}
	first := impactWithRes(t, q, res, 0, 0)
	want := mustJSONStr(t, first)
	snapBefore := mustJSONStr(t, q.Snapshot())

	// Deep-mutate every reachable field of the answer.
	first.SnapshotID = "hacked"
	first.Classification = "hacked"
	first.Service.Label = "hacked"
	first.Service.Href = "hacked"
	first.OldRevision.Key = "hacked"
	first.NewRevision.Secondary = "hacked"
	for i := range first.Consumers.Items {
		first.Consumers.Items[i].Service.Key = "hacked"
		first.Consumers.Items[i].Owner = "hacked"
		for j := range first.Consumers.Items[i].Path {
			first.Consumers.Items[i].Path[j].Href = "hacked"
		}
	}
	for i := range first.Owners.Items {
		first.Owners.Items[i].Label = "hacked"
	}
	for i := range first.ActiveTargets.Items {
		first.ActiveTargets.Items[i].Key = "hacked"
	}
	for i := range first.Limitations.Items {
		first.Limitations.Items[i].Code = "hacked"
		first.Limitations.Items[i].Message = "hacked"
	}

	if after := mustJSONStr(t, q.Snapshot()); after != snapBefore {
		t.Error("mutating a ProductImpact changed the snapshot")
	}
	second := impactWithRes(t, q, res, 0, 0)
	if got := mustJSONStr(t, second); got != want {
		t.Error("a second identical ProductImpact differs from the first (shared state leaked)")
	}
}

func mustJSONStr(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRouteBuilder covers the entry-point and unknown-kind branches of the
// canonical route builder.
func TestRouteBuilder(t *testing.T) {
	if got := hrefForEntity("bogus", "x"); got != fleetRouteRoot {
		t.Errorf("unknown kind must fall back to the overview route, got %q", got)
	}
	cases := []struct {
		view     fleet.EntryPointView
		category string
		want     string
	}{
		{fleet.EntryPointAttention, "", "/fleet/attention"},
		{fleet.EntryPointAttention, "non-compliant", "/fleet/attention?category=non-compliant"},
		{fleet.EntryPointServices, "", "/fleet/services"},
		{fleet.EntryPointOverview, "", "/fleet"},
	}
	for _, c := range cases {
		if got := hrefForEntryPoint(c.view, c.category); got != c.want {
			t.Errorf("hrefForEntryPoint(%q,%q) = %q, want %q", c.view, c.category, got, c.want)
		}
	}
}

// TestStubProvidersForSchemaExport covers the no-op providers ExportOpenAPI wires
// for schema generation: they are registered but never invoked during export, so
// this exercises their (unreachable-in-export) error paths directly.
func TestStubProvidersForSchemaExport(t *testing.T) {
	s := &Server{}
	s.stubProvidersForSchemaExport()
	if _, err := s.fleetQuery(context.Background()); err == nil {
		t.Error("stub fleetQuery must error")
	}
	if _, err := s.impactProvider(context.Background(), "old", "new", false); err == nil {
		t.Error("stub impactProvider must error")
	}
}

// TestProductImpact_RejectsNegativePaging covers the paging-validation branch of
// the POST-impact handler.
func TestProductImpact_RejectsNegativePaging(t *testing.T) {
	q := richFleetQuery(t)
	from := appRevKey(t, q)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil },
		staticImpact(&impact.Result{SnapshotID: q.SnapshotID()}))
	defer cancel()
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: q.SnapshotID(), FromRevisionKey: from, ToRevisionKey: from, Limit: -1}, http.StatusUnprocessableEntity, nil)
}
