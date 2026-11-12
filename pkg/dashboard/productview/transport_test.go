package productview

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
		{"owner", "team:team-a"},
		{"source", "local"},
	}
	for _, c := range cases {
		d, err := q.EntityDetail(fleet.EntityKind(c.kind), c.key)
		if err != nil {
			t.Fatalf("%s: %v", c.kind, err)
		}
		pd := ToEntityDetail(d)
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
	ov := ToOverview(q.Overview())
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
	pnb := ToNeighborhood(nb)
	if len(pnb.Edges) == 0 || pnb.Edges[0].Href == "" {
		t.Errorf("neighborhood edge must carry an href: %+v", pnb.Edges)
	}
	if pnb.UnresolvedDependencies.Count == 0 || pnb.UnresolvedDependencies.Items[0].From.Href == "" {
		t.Errorf("unresolved dependency must carry a navigable source: %+v", pnb.UnresolvedDependencies)
	}
	assertNoEmptyHrefs(t, "neighborhood", pnb)
}

// TestProductNeighborhood_ProjectionFocusHref proves the Part-4 wire contract: a target
// focus under the revision perspective keeps RequestedFocus the target the request asked
// for and carries an explicit, navigable ProjectionFocus (the resolved revision) with an
// href, so the client can canonicalize the URL to the revision identity. A projection
// focused on exactly the requested entity carries no ProjectionFocus.
func TestProductNeighborhood_ProjectionFocusHref(t *testing.T) {
	// The query-path semantics (RequestedFocus stays the target, ProjectionFocus is the
	// resolved revision) are proven in pkg/fleet; this proves the TRANSPORT wraps a
	// present ProjectionFocus into a navigable ProductRef with an href and leaves an
	// absent one absent.
	rev := fleet.EntityRef{Kind: fleet.KindRevision, Key: "domain-a/app@sha256:app", Label: "app"}
	tgt := fleet.EntityRef{Kind: fleet.KindTarget, Key: "prod/k8s/app-1", Label: "app-1"}
	withProj := ToNeighborhood(&fleet.Neighborhood{RequestedFocus: tgt, ProjectionFocus: &rev})
	if withProj.RequestedFocus.Kind != fleet.KindTarget {
		t.Errorf("requestedFocus must remain the target, got %q", withProj.RequestedFocus.Kind)
	}
	if withProj.ProjectionFocus == nil || withProj.ProjectionFocus.Kind != fleet.KindRevision || withProj.ProjectionFocus.Href == "" {
		t.Errorf("projectionFocus must be a navigable revision ref, got %+v", withProj.ProjectionFocus)
	}
	if noProj := ToNeighborhood(&fleet.Neighborhood{RequestedFocus: tgt}); noProj.ProjectionFocus != nil {
		t.Errorf("absent projection focus must stay absent, got %+v", noProj.ProjectionFocus)
	}
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
		r := Ref(fleet.EntityRef{Kind: k.kind, Key: k.key, Label: "ignore-me"})
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

// TestRouteBuilder covers the entry-point and unknown-kind branches of the
// canonical route builder.
func TestRouteBuilder(t *testing.T) {
	if got := Entity("bogus", "x"); got != Root {
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
		if got := EntryPoint(c.view, c.category); got != c.want {
			t.Errorf("EntryPoint(%q,%q) = %q, want %q", c.view, c.category, got, c.want)
		}
	}
}

// TestRouteBuilder_EntityHrefShapes pins the exact per-kind detail href shapes the
// transport emits. It is the backend half of the backend-href/frontend-router
// contract (the frontend half is router.test.ts): if either the plural segment or
// the key escaping changes here, the paired frontend contract test must change too,
// so a canonical href can never drift out from under the router that resolves it.
func TestRouteBuilder_EntityHrefShapes(t *testing.T) {
	entities := []struct {
		kind fleet.EntityKind
		key  string
		want string
	}{
		{fleet.KindService, "payments-domain/payments", "/fleet/services/payments-domain%2Fpayments"},
		{fleet.KindRevision, "payments@sha256:abc", "/fleet/revisions/payments@sha256:abc"},
		{fleet.KindTarget, "prod/k8s/payments", "/fleet/targets/prod%2Fk8s%2Fpayments"},
		// A canonical owner key is namespaced; the colon is legal unescaped in a path
		// segment, exactly as a revision digest's already is.
		{fleet.KindOwner, "team:team-a", "/fleet/owners/team:team-a"},
		{fleet.KindSource, "kubernetes", "/fleet/sources/kubernetes"},
		{fleet.KindDomain, "payments", "/fleet/domains/payments"},
	}
	for _, c := range entities {
		if got := Entity(c.kind, c.key); got != c.want {
			t.Errorf("Entity(%q,%q) = %q, want %q", c.kind, c.key, got, c.want)
		}
	}
	if got := Graph(fleet.KindTarget, "prod/k8s/payments"); got != "/fleet/graph/target/prod%2Fk8s%2Fpayments" {
		t.Errorf("Graph = %q, want /fleet/graph/target/prod%%2Fk8s%%2Fpayments", got)
	}
}

func TestProductOwnership_AbsentStaysAbsent(t *testing.T) {
	if got := productOwnership(nil); got != nil {
		t.Errorf("productOwnership(nil) = %+v, want nil", got)
	}
}

// TestProductServiceDetail_NewRefsHref: the owner revision, every signature row,
// every source, the origin of every unresolved dependency and every placement
// revision carry a canonical href.
func TestProductServiceDetail_NewRefsHref(t *testing.T) {
	rev := fleet.EntityRef{Kind: fleet.KindRevision, Key: "svc@1"}
	d := toProductServiceDetail(&fleet.ServiceDetailData{
		OwnerRevision: &rev,
		Signatures:    fleet.RevisionSignaturesPreview{Total: 1, Count: 1, Items: []fleet.RevisionSignature{{Revision: rev}}},
		Sources:       fleet.RefPreview{Total: 1, Count: 1, Items: []fleet.EntityRef{{Kind: fleet.KindSource, Key: "oci"}}},
		UnresolvedDependencies: fleet.UnresolvedDependenciesPreview{Total: 1, Count: 1, Items: []fleet.UnresolvedDependency{
			{From: fleet.EntityRef{Kind: fleet.KindService, Key: "svc"}, Ref: "oci://x/db", External: true},
		}},
		Placement: fleet.PlacementPreview{Total: 2, Count: 2, CurrentRevision: &rev, Items: []fleet.EnvironmentPlacement{
			{Environment: "prod", Targets: 1, Revisions: []fleet.EntityRef{rev}, RevisionsTotal: 1,
				Owner: fleet.EnvironmentOwner{Owner: "team-a", Ref: &fleet.EntityRef{Kind: fleet.KindOwner, Key: "team:team-a"}, Provenance: "declared"}},
			{Environment: "preview", Owner: fleet.EnvironmentOwner{Provenance: "unknown", Reason: "r"}},
		}},
	})
	checkProductPlacement(t, d.Placement)
	if d.OwnerRevision == nil || d.OwnerRevision.Href == "" {
		t.Errorf("ownerRevision = %+v", d.OwnerRevision)
	}
	if s := d.Signatures.Items; len(s) != 1 || s[0].Revision.Href == "" {
		t.Errorf("signatures = %+v", s)
	}
	for _, s := range d.Sources.Items {
		if s.Href == "" {
			t.Errorf("source %s has no href", s.Key)
		}
	}
	if u := d.UnresolvedDependencies.Items; len(u) != 1 || u[0].From.Href == "" || !u[0].External {
		t.Errorf("unresolvedDependencies = %+v", u)
	}
}

// checkProductPlacement asserts the hrefs of the placement built in
// TestProductServiceDetail_NewRefsHref.
func checkProductPlacement(t *testing.T, pl ProductPlacementPreview) {
	t.Helper()
	if p := pl.Items; len(p) != 2 || len(p[0].Revisions) != 1 || p[0].Revisions[0].Href == "" || p[0].Environment != "prod" || pl.Total != 2 {
		t.Errorf("placement = %+v", pl)
	}
	if o := pl.Items[0].Owner; o.Ref == nil || o.Ref.Href == "" || o.Provenance != "declared" || pl.Items[1].Owner.Ref != nil {
		t.Errorf("placement owners = %+v / %+v", o, pl.Items[1].Owner)
	}
	if c := pl.CurrentRevision; c == nil || c.Href == "" {
		t.Errorf("placement currentRevision = %+v", c)
	}
}

// TestProductServiceDetail_CurrentRevisionAndPolicies: the current revision and
// a referenced policy's destination carry a canonical href.
func TestProductServiceDetail_CurrentRevisionAndPolicies(t *testing.T) {
	rev := fleet.EntityRef{Kind: fleet.KindRevision, Key: "svc@1"}
	res := &fleet.RefResolution{Resolved: true, Service: &fleet.EntityRef{Kind: fleet.KindService, Key: "shared"}}
	d := toProductServiceDetail(&fleet.ServiceDetailData{
		CurrentRevision: &rev,
		Policies:        fleet.PoliciesPreview{Total: 1, Count: 1, Items: []fleet.PolicySummary{{Name: "p", Ref: "oci://x/shared", Resolution: res}}},
		Configurations:  fleet.ConfigurationsPreview{Total: 1, Count: 1, Items: []fleet.ConfigurationSummary{{Name: "c", Ref: "oci://x/shared", Resolution: res}}},
	})
	if d.CurrentRevision == nil || d.CurrentRevision.Href == "" {
		t.Errorf("currentRevision = %+v", d.CurrentRevision)
	}
	if p := d.Policies.Items; len(p) != 1 || p[0].Resolution.Service.Href == "" {
		t.Errorf("policies = %+v", p)
	}
	if c := d.Configurations.Items; len(c) != 1 || c[0].Resolution.Service.Href == "" {
		t.Errorf("configurations = %+v", c)
	}
}

// A reference with no resolution at all must transport as no resolution, not as
// an empty object that reads like a failed lookup.
func TestProductRefResolution_NilStaysNil(t *testing.T) {
	if got := productRefResolution(nil); got != nil {
		t.Errorf("want nil, got %+v", got)
	}
}

// TestProductLists_DomainOfferingAndDocumentHrefs covers the list, domain,
// offering and document converters: every reference they carry gets an href.
func TestProductLists_DomainOfferingAndDocumentHrefs(t *testing.T) {
	svc := fleet.EntityRef{Kind: fleet.KindService, Key: "pay"}
	off := fleet.EntityRef{Kind: fleet.KindOffering, Key: "postgres"}
	one := fleet.RefPreview{Total: 1, Count: 1, Items: []fleet.EntityRef{svc}}
	assertNoEmptyHrefs(t, "entities", ToEntityList(&fleet.EntityList{Entities: []fleet.EntityRef{svc, off}}))
	assertNoEmptyHrefs(t, "attention", ToAttentionList(&fleet.AttentionList{Items: []fleet.AttentionItem{{Entity: svc}}}))
	vrev := fleet.EntityRef{Kind: fleet.KindRevision, Key: "pay@sha256:b"}
	assertNoEmptyHrefs(t, "versions", ToOfferingVersions(&fleet.OfferingVersionsResult{Offering: off, Providers: []fleet.OfferingProviderVersions{{
		Provider: svc, Versions: fleet.ProviderVersionsPreview{Items: []fleet.ProviderVersion{{Revision: vrev}}, LatestCompatible: &vrev},
	}}}))
	assertNoEmptyHrefs(t, "document", ToRevisionDocument(&fleet.RevisionDocument{Revision: fleet.EntityRef{Kind: fleet.KindRevision, Key: "pay@sha256:a"}}))
	dom := ToEntityDetail(&fleet.EntityDetail{Entity: fleet.EntityRef{Kind: fleet.KindDomain, Key: "payments"}, Domain: &fleet.DomainDetailData{Services: one}})
	if dom.Domain == nil || dom.Entity.Href != "/fleet/domains/payments" || dom.Domain.Services.Items[0].Href == "" {
		t.Errorf("domain detail = %+v", dom)
	}
	rev := fleet.EntityRef{Kind: fleet.KindRevision, Key: "pay@sha256:a"}
	use := fleet.OfferingUse{Consumer: svc, Provider: svc, Provisioning: []fleet.ProvisioningStatus{{Phase: fleet.StatusUnknown}, {Phase: "Ready", Revision: &rev}}}
	od := ToEntityDetail(&fleet.EntityDetail{Entity: off, Offering: &fleet.OfferingDetailData{
		Providers: fleet.OfferingProvidersPreview{Total: 1, Count: 1, Items: []fleet.OfferingProvider{{Service: svc, Versions: fleet.ProviderVersionsPreview{
			Total: 1, Count: 1, Items: []fleet.ProviderVersion{{Revision: rev}}, LatestCompatible: &rev,
		}}}},
		Consumers: fleet.OfferingUsesPreview{Total: 1, Count: 1, Items: []fleet.OfferingUse{use}},
	}})
	if od.Offering == nil || od.Entity.Href != "/fleet/offerings/postgres" {
		t.Fatalf("offering detail = %+v", od)
	}
	assertNoEmptyHrefs(t, "offering", od)
	if v := od.Offering.Providers.Items[0].Versions; v.Items[0].Revision.Href == "" || v.LatestCompatible == nil || v.LatestCompatible.Href == "" {
		t.Errorf("provider versions = %+v", v)
	}
	if p := od.Offering.Consumers.Items[0].Provisioning; len(p) != 2 || p[0].Revision != nil || p[1].Revision.Href == "" {
		t.Errorf("provisioning = %+v", p)
	}
	inf := productInferredOfferings([]fleet.InferredOffering{{Offering: off, Provider: &svc}})
	if len(inf) != 1 || inf[0].Offering.Href == "" || inf[0].Provider.Href == "" {
		t.Errorf("inferred offerings = %+v", inf)
	}
}

// TestToEntityDetail_GoldenPathHrefs: a golden path's requirements, unresolved
// requirements and created services, and a declared origin's golden path and
// revision, carry a canonical href; absent sections stay absent.
func TestToEntityDetail_GoldenPathHrefs(t *testing.T) {
	path := fleet.EntityRef{Kind: fleet.KindService, Key: "acme/http-service"}
	rev := fleet.EntityRef{Kind: fleet.KindRevision, Key: "acme/http-service@1"}
	pg := fleet.EntityRef{Kind: fleet.KindService, Key: "acme/postgresql"}
	d := ToEntityDetail(&fleet.EntityDetail{Entity: path, Service: &fleet.ServiceDetailData{
		Template: &fleet.TemplateDetail{
			Requirements:           fleet.OfferingUsesPreview{Total: 1, Count: 1, Items: []fleet.OfferingUse{{Consumer: path, Provider: pg}}},
			UnresolvedRequirements: fleet.UnresolvedDependenciesPreview{Total: 1, Count: 1, Items: []fleet.UnresolvedDependency{{From: path, Ref: "cache"}}},
			CreatedServices:        fleet.RefPreview{Total: 1, Count: 1, Items: []fleet.EntityRef{{Kind: fleet.KindService, Key: "acme/gift-cards"}}},
		},
		Origin: &fleet.OriginDetail{Ref: "oci://acme/http-service:1.0.0", GoldenPath: &path, Revision: &rev},
	}})
	tpl, o := d.Service.Template, d.Service.Origin
	if r := tpl.Requirements.Items; len(r) != 1 || r[0].Provider.Href == "" || r[0].Consumer.Href == "" {
		t.Errorf("requirements = %+v", r)
	}
	if u := tpl.UnresolvedRequirements.Items; len(u) != 1 || u[0].From.Href == "" {
		t.Errorf("unresolved requirements = %+v", u)
	}
	if c := tpl.CreatedServices.Items; len(c) != 1 || c[0].Href == "" {
		t.Errorf("created services = %+v", c)
	}
	if o.GoldenPath == nil || o.GoldenPath.Href == "" || o.Revision == nil || o.Revision.Href == "" || o.Ref == "" {
		t.Errorf("origin = %+v", o)
	}
	plain := toProductServiceDetail(&fleet.ServiceDetailData{})
	if plain.Template != nil || plain.Origin != nil {
		t.Errorf("an ordinary service carries %+v, %+v", plain.Template, plain.Origin)
	}
}
