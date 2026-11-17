package fleet

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

func listExternals(t *testing.T, q *Query, f EntityFilter) *EntityList {
	t.Helper()
	f.Kinds, f.Limit = []EntityKind{KindExternal}, MaxEntityLimit
	l, err := q.Entities(f)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestExternalCapFoldsLeastConsumed(t *testing.T) {
	q := foldFleet(t, 205, 0)
	l := listExternals(t, q, EntityFilter{})
	if l.Total != MaxExternalNodes || l.Meta.Completeness != CompletenessPartial || !hasLimitation(l.Meta.Limitations, LimitationExternalCap) {
		t.Fatalf("list total %d completeness %s limitations %+v", l.Total, l.Meta.Completeness, l.Meta.Limitations)
	}
	other, err := q.GetExternal(string(OtherEgress))
	if err != nil {
		t.Fatal(err)
	}
	if o := other.External; o.Folded != 6 || o.Class != ClassOtherEgress || o.ConsumersTotal != 6 || o.SourcesTotal != 21 {
		t.Fatalf("other egress = %+v", o)
	}
	for i := 0; i < 6; i++ {
		v, err := q.GetExternal(fmt.Sprintf("ext-%03d", i))
		if err != nil || v.FoldedInto != OtherEgress {
			t.Fatalf("ext-%03d = %+v, %v", i, v, err)
		}
	}
	if v, _ := q.GetExternal("ext-006"); v.FoldedInto != "" {
		t.Error("ext-006 is kept")
	}
	checkFoldProjection(t, q)
	checkFoldExplained(t, q)
}

// checkFoldProjection: answers carry FoldedInto, the snapshot never does.
func checkFoldProjection(t *testing.T, q *Query) {
	t.Helper()
	g, err := q.Graph(GraphQuery{Service: "external:other-egress"})
	if err != nil || len(g.Nodes) != 6 || len(g.Edges) != 21 {
		t.Fatalf("other egress graph = %+v, %v", g, err)
	}
	for _, e := range g.Edges {
		if e.FoldedInto != OtherEgress || e.ToExternal == OtherEgress {
			t.Errorf("projected edge = %+v", e)
		}
	}
	if !hasLimitation(q.Overview().Meta.Limitations, LimitationExternalCap) || q.Overview().Summary.Externals != 205 {
		t.Error("the overview counts every identity and says it folds")
	}
	snap := q.Snapshot()
	if len(snap.Externals) != 205 || hasLimitation(snap.Limitations, LimitationExternalCap) {
		t.Errorf("the snapshot keeps every record and no cap: %d", len(snap.Externals))
	}
	for _, r := range q.SnapshotForSerialization().Relationships {
		if r.FoldedInto != "" {
			t.Fatalf("the snapshot's edges are never projected: %+v", r)
		}
	}
}

// checkFoldExplained: explain and the neighborhood name the fold.
func checkFoldExplained(t *testing.T, q *Query) {
	t.Helper()
	ex, err := q.Explain("external:ext-000")
	if err != nil || !slices.ContainsFunc(ex.Reasons, func(r Reason) bool { return r.Code == LimitationExternalCap }) {
		t.Errorf("explain folded = %+v, %v", ex, err)
	}
	ex, _ = q.Explain("external:other-egress")
	if !slices.ContainsFunc(ex.Reasons, func(r Reason) bool { return r.Code == LimitationExternalCap }) ||
		slices.ContainsFunc(ex.Reasons, func(r Reason) bool { return r.Code == ReasonExternalNameOnly }) {
		t.Errorf("explain other egress = %+v", ex.Reasons)
	}
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "v000", MaxNodes: 500, MaxEdges: 500})
	if err != nil || !slices.ContainsFunc(nb.Nodes, func(n NeighborhoodNode) bool { return n.Ref.Key == string(OtherEgress) }) {
		t.Errorf("neighborhood draws other egress: %v", err)
	}
	nb, err = q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "ext-000"})
	if err != nil || len(nb.Edges) != 1 || nb.Edges[0].To.Key != "ext-000" {
		t.Errorf("a folded identity asked for by name keeps its edges: %+v, %v", nb, err)
	}
}

func TestExternalCapNotReachedIsComplete(t *testing.T) {
	q := foldFleet(t, 10, 0)
	l := listExternals(t, q, EntityFilter{})
	if l.Total != 10 || hasLimitation(l.Meta.Limitations, LimitationExternalCap) || l.Meta.Completeness != q.SnapshotForSerialization().Completeness {
		t.Fatalf("list = %+v", l.Meta)
	}
	if _, err := q.GetExternal(string(OtherEgress)); err == nil {
		t.Error("no other egress below the cap")
	}
}

func TestExternalAnswerBoundsConsumers(t *testing.T) {
	var svcs []extSvc
	for i := 0; i < 60; i++ {
		svcs = append(svcs, extSvc{fmt.Sprintf("s%02d", i), []string{"shop"}, []string{"stripe"}})
	}
	v, err := extK8sFleet(t, svcs...).GetExternal("stripe")
	if err != nil {
		t.Fatal(err)
	}
	e := v.External
	if len(e.Consumers) != MaxExternalSources || e.ConsumersTotal != 60 || len(e.Sources) != MaxExternalSources || e.SourcesTotal != 60 {
		t.Fatalf("bounded = %d/%d consumers, %d/%d sources", len(e.Consumers), e.ConsumersTotal, len(e.Sources), e.SourcesTotal)
	}
	for _, src := range e.Sources {
		if !slices.Contains(e.Consumers, src.Service) {
			t.Errorf("a source of an unlisted consumer: %+v", src)
		}
	}
}

// externalsFleet: checkout declares Stripe (required) and legacy-mq, billing
// declares stripe-api, and front depends on checkout through its ref.
func externalsFleet(t *testing.T) *Query {
	t.Helper()
	stripe := extDep("Stripe")
	stripe.Required = true
	front := extContract("front", "1.0.0", contract.Dependency{Name: "checkout", Ref: "oci://x/checkout", Required: true})
	return NewQuery(externalSnap(t, BuildOptions{},
		extContract("checkout", "1.0.0", stripe, extDep("legacy-mq")),
		extContract("billing", "1.0.0", extDep("stripe-api")),
		front))
}

func TestEntitiesListExternalOnlyWhenAsked(t *testing.T) {
	q := externalsFleet(t)
	all, err := q.Entities(EntityFilter{Limit: MaxEntityLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all.Entities {
		if e.Kind == KindExternal {
			t.Fatalf("a kind-less listing names an external: %+v", e)
		}
	}
	l := listExternals(t, q, EntityFilter{})
	want := []EntityRef{
		{Kind: KindExternal, Key: "legacy-mq", Label: "legacy-mq", Secondary: externalcatalog.ClassUnclassified, Status: StatusUnknown, Consumers: 1, JoinedBy: externalcatalog.MatchNormalizedName},
		{Kind: KindExternal, Key: "stripe", Label: "Stripe", Secondary: externalcatalog.ClassVendorAPI, Status: StatusUnknown, Consumers: 2, CatalogEntry: "stripe", JoinedBy: externalcatalog.MatchCatalogAlias},
	}
	if !reflect.DeepEqual(l.Entities, want) || l.Hidden != 0 {
		t.Fatalf("externals = %+v", l.Entities)
	}
	if l = listExternals(t, q, EntityFilter{Sort: SortConsumers}); l.Entities[0].Key != "stripe" {
		t.Fatalf("most consumed first = %+v", l.Entities)
	}
}

func TestProductKindsUnchanged(t *testing.T) {
	if got := ProductKinds(); !reflect.DeepEqual(got, []string{"service", "owner", "domain", "offering"}) {
		t.Fatalf("product kinds = %v", got)
	}
	if !slices.Contains(EntityKinds(), string(KindExternal)) || slices.Contains(ListKinds("", EntityFilter{}), KindExternal) {
		t.Error("external is a kind, listed only when asked for")
	}
}

func TestEntitiesExternalClassFilter(t *testing.T) {
	q := externalsFleet(t)
	if l := listExternals(t, q, EntityFilter{Class: externalcatalog.ClassVendorAPI}); l.Total != 1 || l.Entities[0].Key != "stripe" {
		t.Fatalf("vendor-api = %+v", l.Entities)
	}
	if l := listExternals(t, q, EntityFilter{Text: "STRIPE-API"}); l.Total != 1 || l.Entities[0].Key != "stripe" {
		t.Fatalf("by alias = %+v", l.Entities)
	}
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService, KindExternal}, Class: externalcatalog.ClassUnclassified, Limit: MaxEntityLimit})
	if err != nil || l.Total != 1 || l.Entities[0].Key != "legacy-mq" {
		t.Fatalf("a class filter keeps externals only: %+v, %v", l, err)
	}
}

func TestEntitiesExternalClassFilterRejectsUnknown(t *testing.T) {
	q := externalsFleet(t)
	var iq *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}, Class: "bogus"}); !errors.As(err, &iq) || iq.Field != "class" {
		t.Fatalf("bogus class = %v", err)
	}
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Class: externalcatalog.ClassLegacy}); !errors.As(err, &iq) {
		t.Fatalf("class on services = %v", err)
	}
}

func TestExternalDetail(t *testing.T) {
	q := externalsFleet(t)
	d, err := q.EntityDetail(KindExternal, "stripe")
	if err != nil {
		t.Fatal(err)
	}
	x := d.External
	if d.Entity.Kind != KindExternal || d.Status != StatusUnknown || x == nil || x.Catalog == nil || x.Catalog.Origin != externalcatalog.OriginDefault ||
		x.Catalog.Digest != externalcatalog.DefaultDigest() || x.External.Catalog.Entry != "stripe" || x.FoldedInto != "" {
		t.Fatalf("detail = %+v", d)
	}
	want := []ExternalConsumer{
		{Service: q.serviceRef(NewServiceKey("billing")), Versions: []string{"v-stripe-api"}},
		{Service: q.serviceRef(NewServiceKey("checkout")), Required: true, Versions: []string{"v-Stripe"}},
	}
	if !reflect.DeepEqual(x.Consumers, want) {
		t.Errorf("consumers = %+v", x.Consumers)
	}
	if d, _ := q.EntityDetail(KindExternal, "legacy-mq"); d.External.Catalog != nil {
		t.Error("a name-only external has no catalog")
	}
	snap := *q.SnapshotForSerialization()
	snap.ExternalCatalogs = nil
	if d, _ := NewQuery(&snap).EntityDetail(KindExternal, "stripe"); d.External.Catalog != nil {
		t.Error("a catalog the snapshot does not record is not invented")
	}
}

func TestGetExternalNotFound(t *testing.T) {
	var nf *NotFoundError
	if _, err := externalsFleet(t).GetExternal("external:paypal"); !errors.As(err, &nf) || nf.Kind != "external" {
		t.Fatalf("err = %v", err)
	}
	if _, err := externalsFleet(t).EntityDetail(KindExternal, "paypal"); !errors.As(err, &nf) {
		t.Fatalf("detail err = %v", err)
	}
	if k, ok := ParseExternalSubject("external:"); ok || k != "" {
		t.Error("an empty identity is no subject")
	}
}

func TestExplainExternalReasons(t *testing.T) {
	q := externalsFleet(t)
	res, err := q.Explain("external:stripe")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "external" || res.Status != StatusUnknown || res.Subject != "external:stripe" {
		t.Fatalf("explain = %+v", res)
	}
	codes := map[string]int{}
	for _, r := range res.Reasons {
		codes[r.Code]++
	}
	if codes[ReasonExternalDeclared] != 2 || codes[ReasonExternalCatalogMatch] != 1 || codes[ReasonExternalNameOnly] != 0 {
		t.Errorf("reasons = %+v", res.Reasons)
	}
	if !slices.ContainsFunc(res.Reasons, func(r Reason) bool {
		return r.Message == "declared by checkout dependencies[0] (name: Stripe, version: v-Stripe)"
	}) {
		t.Errorf("declared message = %+v", res.Reasons)
	}
	res, _ = q.Explain("external:legacy-mq")
	if !slices.ContainsFunc(res.Reasons, func(r Reason) bool { return r.Code == ReasonExternalNameOnly }) {
		t.Errorf("name only = %+v", res.Reasons)
	}
	if _, err := q.Explain("external:nobody"); err == nil {
		t.Error("an unknown external is not found")
	}
}

func TestExplainExternalNoVersion(t *testing.T) {
	q := NewQuery(externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", contract.Dependency{Name: "legacy-mq", External: true})))
	res, _ := q.Explain("external:legacy-mq")
	if !slices.ContainsFunc(res.Reasons, func(r Reason) bool { return r.Message == "declared by a dependencies[0] (name: legacy-mq)" }) {
		t.Errorf("reasons = %+v", res.Reasons)
	}
}

func TestGraphExternalDependents(t *testing.T) {
	q := externalsFleet(t)
	g, err := q.Graph(GraphQuery{Service: "external:stripe", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	var keys []ServiceKey
	for _, n := range g.Nodes {
		keys = append(keys, n.Key)
	}
	want := []ServiceKey{NewServiceKey("billing"), NewServiceKey("checkout"), NewServiceKey("front")}
	if g.Root != "" || g.External != "stripe" || g.Direction != DirectionDependents || !reflect.DeepEqual(keys, want) || g.Nodes[2].Depth != 2 {
		t.Fatalf("graph = %+v", g)
	}
	toStripe := 0
	for _, e := range g.Edges {
		if e.ToExternal == "stripe" {
			toStripe++
		}
	}
	if toStripe != 2 || len(g.Unresolved) != 0 {
		t.Errorf("edges = %+v unresolved %+v", g.Edges, g.Unresolved)
	}
	if _, err := q.Graph(GraphQuery{Service: "external:nobody"}); err == nil {
		t.Error("an unknown external is not found")
	}
}

func TestGraphExternalCycles(t *testing.T) {
	a := extContract("a", "1.0.0", extDep("stripe"), contract.Dependency{Name: "b", Ref: "oci://x/b"})
	b := extContract("b", "1.0.0", contract.Dependency{Name: "a", Ref: "oci://x/a"})
	g, err := NewQuery(externalSnap(t, BuildOptions{}, a, b)).Graph(GraphQuery{Service: "external:stripe", Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range g.Cycles {
		if c[0] == "external:stripe" {
			t.Errorf("a cycle starts at a consumer: %v", c)
		}
	}
	if len(g.Cycles) == 0 {
		t.Error("a and b form a cycle")
	}
}

func TestGraphExternalDependenciesRejected(t *testing.T) {
	var iq *InvalidQueryError
	if _, err := externalsFleet(t).Graph(GraphQuery{Service: "external:stripe", Direction: DirectionDependencies}); !errors.As(err, &iq) || iq.Field != "direction" {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchFindsExternalsByAlias(t *testing.T) {
	res, err := externalsFleet(t).Search(SearchFilter{Text: "stripe-api"})
	if err != nil || res.ExternalsTotal != 1 || res.Externals[0].Key != "stripe" {
		t.Fatalf("search = %+v, %v", res, err)
	}
	if res, _ := externalsFleet(t).Search(SearchFilter{}); res.Externals != nil {
		t.Error("only a text search lists externals")
	}
	var deps []contract.Dependency
	for i := 0; i < 25; i++ {
		deps = append(deps, extDep(fmt.Sprintf("legacy-%02d", i)))
	}
	res, _ = NewQuery(externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", deps...))).Search(SearchFilter{Text: "legacy"})
	if len(res.Externals) != MaxSearchExternals || res.ExternalsTotal != 25 {
		t.Fatalf("bounded search = %d of %d", len(res.Externals), res.ExternalsTotal)
	}
}

func TestNeighborhoodExternalLeaves(t *testing.T) {
	q := externalsFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "checkout", Direction: DirectionBoth, Views: allViews()})
	if err != nil {
		t.Fatal(err)
	}
	var ext []string
	for _, n := range nb.Nodes {
		if n.Ref.Kind == KindExternal {
			ext = append(ext, n.Ref.Key)
			if n.Depth != 1 || n.Status != StatusUnknown {
				t.Errorf("leaf = %+v", n)
			}
		}
	}
	if !reflect.DeepEqual(ext, []string{"legacy-mq", "stripe"}) || nb.TotalNodes != len(nb.Nodes) {
		t.Fatalf("leaves = %v total %d nodes %d", ext, nb.TotalNodes, len(nb.Nodes))
	}
	toExt := 0
	for _, e := range nb.Edges {
		if e.To.Kind == KindExternal {
			toExt++
			if !e.Expected || e.Difference != "" || e.Provenance != ProvenanceDeclared || e.DeclaredClaims.Total != 1 {
				t.Errorf("edge = %+v", e)
			}
		}
	}
	if toExt != 2 {
		t.Errorf("edges = %+v", nb.Edges)
	}
}

// TestNeighborhoodExternalLeavesBounded: leaves count against the caps, and
// a dependents or observed-only view draws none.
func TestNeighborhoodExternalLeavesBounded(t *testing.T) {
	q := externalsFleet(t)
	nb, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "checkout", Direction: DirectionDependencies, MaxNodes: 2, Views: allViews()})
	if len(nb.Nodes) != 2 || !nb.Truncated || nb.TotalNodes != 3 || len(nb.Edges) != 1 {
		t.Errorf("capped nodes = %+v edges %d", nb.Nodes, len(nb.Edges))
	}
	nb, _ = q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "checkout", Direction: DirectionDependencies, MaxEdges: 1, Views: allViews()})
	if len(nb.Edges) != 1 || !nb.TruncatedEdges {
		t.Errorf("capped edges = %+v", nb.Edges)
	}
	// Dependents and observed-only views draw none.
	for _, nq := range []NeighborhoodQuery{
		{Kind: KindService, Key: "checkout", Direction: DirectionDependents},
		{Kind: KindService, Key: "checkout", Views: []KnowledgeView{ViewObserved}},
	} {
		nb, _ = q.Neighborhood(nq)
		if slices.ContainsFunc(nb.Nodes, func(n NeighborhoodNode) bool { return n.Ref.Kind == KindExternal }) {
			t.Errorf("%+v draws an external", nq)
		}
	}
}

// TestNeighborhoodFocusExternalCapped: a node cap draws edges only between
// drawn nodes; an edge cap says it cut edges.
func TestNeighborhoodFocusExternalCapped(t *testing.T) {
	q := externalsFleet(t)
	nb, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "stripe", MaxNodes: 2, MaxEdges: 5})
	if len(nb.Nodes) != 2 || len(nb.Edges) != 1 || !nb.Truncated || nb.TruncatedEdges {
		t.Errorf("node-capped focus = %+v", nb)
	}
	drawn := map[string]bool{}
	for _, n := range nb.Nodes {
		drawn[n.Ref.Key] = true
	}
	for _, e := range nb.Edges {
		if !drawn[e.From.Key] || !drawn[e.To.Key] {
			t.Errorf("edge %s names a node the answer leaves out", e.ID)
		}
	}
	nb, _ = q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "stripe", MaxEdges: 1})
	if len(nb.Edges) != 1 || !nb.Truncated || !nb.TruncatedEdges {
		t.Errorf("edge-capped focus = %+v", nb)
	}
}

func TestNeighborhoodFocusExternal(t *testing.T) {
	q := externalsFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "stripe"})
	if err != nil {
		t.Fatal(err)
	}
	if !nb.Nodes[0].Focus || nb.Nodes[0].Ref.Kind != KindExternal || len(nb.Nodes) != 3 || len(nb.Edges) != 2 || nb.TotalNodes != 3 || nb.FocusService.Key != "stripe" {
		t.Fatalf("focus = %+v", nb)
	}
	nb, _ = q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "stripe", Direction: DirectionDependencies})
	if len(nb.Nodes) != 1 {
		t.Errorf("an external has no dependencies: %+v", nb.Nodes)
	}
	if _, err := q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "nobody"}); err == nil {
		t.Error("an unknown external is not found")
	}
}

func TestOverviewCountsExternals(t *testing.T) {
	ov := externalsFleet(t).Overview().Summary
	if ov.Externals != 2 || ov.HiddenExternals != 0 {
		t.Fatalf("overview = %d/%d", ov.Externals, ov.HiddenExternals)
	}
}

func TestExplainExternalWordsTheJoin(t *testing.T) {
	db := contract.Dependency{Name: "orders", External: true, Identity: "corp/orders-db"}
	q := NewQuery(externalSnap(t, BuildOptions{}, extContract("a", "1.0.0", db, extDep("mq.corp-legacy.co.uk"))))
	for subject, want := range map[string]string{
		"external:corp/orders-db":         "no catalog entry classifies it; its consumers join through the identity they declare",
		"external:host/corp-legacy.co.uk": "no catalog entry classifies it; its consumers join through its registrable domain",
	} {
		res, err := q.Explain(subject)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(res.Reasons, func(r Reason) bool { return r.Code == ReasonExternalUnclassified && r.Message == want }) ||
			slices.ContainsFunc(res.Reasons, func(r Reason) bool { return r.Code == ReasonExternalNameOnly }) {
			t.Errorf("%s reasons = %+v", subject, res.Reasons)
		}
	}
	rec, _ := q.GetExternal("corp/orders-db")
	if host, _ := q.GetExternal("host/corp-legacy.co.uk"); rec.External.JoinWords() != "joined by its declared identity" || host.External.JoinWords() != "joined by its registrable domain" || (ExternalRecord{}).JoinWords() != "joined by name" {
		t.Errorf("join words = %q", rec.External.JoinWords())
	}
}
