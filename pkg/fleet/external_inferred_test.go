package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// inferredSvc is one Kubernetes service of an inferred-use fleet: its name,
// the namespaces it runs in and its declared external dependencies.
type inferredSvc struct {
	name string
	ns   []string
	deps []string
}

// inferredFleet builds a Kubernetes fleet of svcs that reports uses and
// observed, with the target limitations lims on every target of the
// services they name.
func inferredFleet(t *testing.T, svcs []inferredSvc, uses []RawExternalUse, observed []ObservedEdge, lims map[string][]Limitation) *FleetSnapshot {
	t.Helper()
	col := &Collection{ExternalUses: uses, Observed: observed}
	for _, s := range svcs {
		var deps []contract.Dependency
		for _, d := range s.deps {
			deps = append(deps, extDep(d))
		}
		digest := "sha256:" + s.name
		col.Revisions = append(col.Revisions, RawRevision{Bundle: bundleOf(extContract(s.name, "1.0.0", deps...)), Digest: digest})
		for _, ns := range s.ns {
			col.Targets = append(col.Targets, RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: s.name, Service: s.name, Digest: digest,
				Compliance: StatusCompliant, Limitations: lims[s.name]})
		}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func bundleOf(c *contract.Contract) *contract.Bundle { return &contract.Bundle{Contract: c} }

// stripeUses are checkout's two stripe uses in shop: a host and a key name.
func stripeUses() []RawExternalUse {
	at := fixedNow().Add(-time.Minute)
	return []RawExternalUse{
		{Service: "checkout", Namespace: "shop", Identity: "stripe", Class: "vendor-api", Confidence: "possible", Kind: "secretKeyRef",
			Where: "deployment/checkout secretKeyRef stripe-credentials/STRIPE_SECRET_KEY", Entry: "stripe", Catalog: externalcatalog.DefaultDigest(), ObservedAt: &at},
		{Service: "checkout", Namespace: "shop", Identity: "stripe", Class: "vendor-api", Confidence: "likely", Kind: "env",
			Where: "deployment/checkout env STRIPE_API_BASE", Host: "api.stripe.com", Entry: "stripe", Catalog: externalcatalog.DefaultDigest(), ObservedAt: &at},
	}
}

func relsTo(snap *FleetSnapshot, key ExternalKey) []Relationship {
	var out []Relationship
	for _, r := range snap.Relationships {
		if r.ToExternal == key {
			out = append(out, r)
		}
	}
	return out
}

func TestInferredUseBecomesInferredEdge(t *testing.T) {
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, stripeUses(), nil, nil)
	rels := relsTo(snap, "stripe")
	if len(rels) != 1 {
		t.Fatalf("edges = %+v", rels)
	}
	r := rels[0]
	if r.Provenance != ProvenanceInferred || r.Resolved || r.ToService != "" || r.Confidence != "likely" || r.FromService != "checkout" {
		t.Fatalf("edge = %+v", r)
	}
	tk := snap.Services["checkout"].Targets[0]
	checkStripeSources(t, r.InferredSources, tk)
	rec := snap.Externals["stripe"]
	want := []ExternalSource{
		{Kind: ExternalSourcePodSpec, Service: "checkout", Target: tk, Provenance: ProvenanceInferred, Match: externalcatalog.KindHost},
		{Kind: ExternalSourcePodSpec, Service: "checkout", Target: tk, Provenance: ProvenanceInferred, Match: externalcatalog.KindKeyName},
	}
	if rec == nil || rec.Class != externalcatalog.ClassVendorAPI || rec.ConsumersTotal != 1 || !reflect.DeepEqual(rec.Sources, want) {
		t.Fatalf("record = %+v", rec)
	}
	res, err := NewQuery(snap).Explain("external:stripe")
	if err != nil || !slices.ContainsFunc(res.Reasons, func(r Reason) bool {
		return r.Code == ReasonExternalInferred && strings.Contains(r.Message, "target "+string(tk))
	}) {
		t.Errorf("explain = %+v, %v", res, err)
	}
}

// checkStripeSources: stripeUses' evidence, sorted, on target tk.
func checkStripeSources(t *testing.T, srcs []InferredSource, tk TargetKey) {
	t.Helper()
	if len(srcs) != 2 || srcs[0].Kind != "env" || srcs[1].Kind != "secretKeyRef" || srcs[0].Host != "api.stripe.com" ||
		srcs[0].Target != tk || srcs[0].Source != "k8s" || srcs[0].ObservedAt == nil || srcs[1].Confidence != "possible" {
		t.Fatalf("sources = %+v", srcs)
	}
}

func TestInferredConfidenceNeverStronger(t *testing.T) {
	uses := stripeUses()[:1]
	uses[0].Confidence = "certain"
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, uses, nil, nil)
	if r := relsTo(snap, "stripe"); r[0].Confidence != "possible" || r[0].InferredSources[0].Confidence != "possible" {
		t.Errorf("edge = %+v", r)
	}
}

func TestInferredAndObservedStaySeparate(t *testing.T) {
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, stripeUses(),
		[]ObservedEdge{{From: "checkout", To: "api.stripe.com", Count: 4}}, nil)
	rels := relsTo(snap, "stripe")
	if len(rels) != 2 || rels[0].Provenance != ProvenanceInferred || rels[1].Provenance != ProvenanceObserved ||
		len(rels[1].InferredSources) != 0 || rels[0].ObservedCount != 0 {
		t.Fatalf("edges = %+v", rels)
	}
	kinds := map[string]int{}
	for _, s := range snap.Externals["stripe"].Sources {
		kinds[s.Kind]++
	}
	if kinds[ExternalSourcePodSpec] != 2 || kinds[ExternalSourceObserved] != 1 {
		t.Errorf("record sources = %v", kinds)
	}
	res, _ := NewQuery(snap).Explain("external:stripe")
	if !slices.ContainsFunc(res.Reasons, func(r Reason) bool { return r.Code == ReasonExternalObserved }) {
		t.Errorf("explain = %+v", res.Reasons)
	}
}

func TestExternalEdgesStayOutOfServiceIndexes(t *testing.T) {
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}, {name: "stripe", ns: []string{"shop"}}}, stripeUses(),
		[]ObservedEdge{{From: "checkout", To: "api.stripe.com", Count: 4}}, nil)
	if len(relsTo(snap, "stripe")) != 2 {
		t.Fatalf("edges = %+v", snap.Relationships)
	}
	checkIndexesEmpty(t, snap)
	for _, r := range snap.Relationships {
		if ConsumesEdge(r) {
			t.Errorf("consumes edge %+v", r)
		}
	}
	q := NewQuery(snap)
	for _, dir := range []Direction{DirectionDependents, DirectionDependencies} {
		g, err := q.Graph(GraphQuery{Service: "stripe", Direction: dir, IncludeObserved: true})
		if err != nil || len(g.Edges) != 0 {
			t.Errorf("%s of the stripe service = %+v, %v", dir, g, err)
		}
	}
	// The external's graph draws the inferred consumer, labelled inferred, and
	// the observed edge only when asked; the consumer's own graph draws the
	// same edges by the same rule.
	provs := func(g *GraphResult) []string {
		var out []string
		for _, e := range g.Edges {
			if e.ToExternal == "stripe" {
				out = append(out, e.Provenance)
			}
		}
		slices.Sort(out)
		return out
	}
	for _, obs := range []bool{false, true} {
		want := []string{ProvenanceInferred}
		if obs {
			want = []string{ProvenanceInferred, ProvenanceObserved}
		}
		g, _ := q.Graph(GraphQuery{Service: "external:stripe", IncludeObserved: obs})
		wantProv := map[bool]string{false: ProvenanceInferred, true: ProvenanceObserved}[obs]
		if len(g.Nodes) != 1 || g.Nodes[0].Provenance != wantProv || !slices.Equal(provs(g), want) {
			t.Errorf("graph of external:stripe (observed %v) = %+v", obs, g)
		}
		g, _ = q.Graph(GraphQuery{Service: "checkout", IncludeObserved: obs})
		if !slices.Equal(provs(g), want) {
			t.Errorf("graph of checkout (observed %v): stripe edges %v, want %v", obs, provs(g), want)
		}
	}
}

// TestExternalGraphInferredOnlyConsumer: an external known only by inference
// draws its consumer, and its edges are a list, never null.
func TestExternalGraphInferredOnlyConsumer(t *testing.T) {
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, stripeUses(), nil, nil)
	g, err := NewQuery(snap).Graph(GraphQuery{Service: "external:stripe", Direction: DirectionDependents, IncludeObserved: true})
	if err != nil || len(g.Nodes) != 1 || g.Nodes[0].Key != "checkout" || g.Nodes[0].Provenance != ProvenanceInferred || len(g.Edges) != 1 {
		t.Fatalf("graph = %+v, %v", g, err)
	}
	b, _ := json.Marshal(g)
	if strings.Contains(string(b), `"edges":null`) {
		t.Errorf("edges null: %s", b)
	}
}

// checkIndexesEmpty: no service index holds an edge.
func checkIndexesEmpty(t *testing.T, snap *FleetSnapshot) {
	t.Helper()
	for name, idx := range map[string]map[ServiceKey][]ServiceKey{
		"reverseDeps": snap.reverseDeps, "forwardDeps": snap.forwardDeps, "observedForward": snap.observedForward, "observedReverse": snap.observedReverse,
	} {
		for k, v := range idx {
			if len(v) > 0 {
				t.Errorf("%s[%s] = %v", name, k, v)
			}
		}
	}
	for k, v := range snap.forwardDepsByRevision {
		if len(v) > 0 {
			t.Errorf("forwardDepsByRevision[%s] = %v", k, v)
		}
	}
}

func TestInferredUseUnattributed(t *testing.T) {
	uses := []RawExternalUse{
		{Service: "ghost", Namespace: "shop", Identity: "stripe", Kind: "env", Where: "deployment/ghost env LEAKY_WHERE", Host: "api.stripe.com"},
		{Service: "checkout", Namespace: "shop", Identity: "Not An Identity", Kind: "env", Where: "deployment/checkout env OTHER_WHERE"},
		{Service: "checkout", Namespace: "shop", Identity: string(OtherEgress), Kind: "env"},
	}
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, uses, nil, nil)
	if len(snap.Externals) != 0 || len(relsTo(snap, "stripe")) != 0 {
		t.Fatalf("externals = %+v", snap.Externals)
	}
	var got []Limitation
	for _, l := range snap.Limitations {
		if l.Code == LimitationExternalUseUnattributed {
			got = append(got, l)
		}
	}
	if len(got) != 1 || got[0].Source != "k8s" || !strings.HasPrefix(got[0].Message, "3 external uses") ||
		strings.Contains(got[0].Message, "WHERE") || strings.Contains(got[0].Message, "ghost") {
		t.Fatalf("limitations = %+v", got)
	}
}

func TestInferredUsesRespectCap(t *testing.T) {
	var uses []RawExternalUse
	for i := 0; i < MaxExternalNodes+5; i++ {
		uses = append(uses, RawExternalUse{Service: "checkout", Namespace: "shop", Identity: fmt.Sprintf("host/ext-%03d.com", i), Kind: "env", Confidence: "likely"})
	}
	q := NewQuery(inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, uses, nil, nil))
	l := listExternals(t, q, EntityFilter{})
	if l.Total != MaxExternalNodes || l.Meta.Completeness != CompletenessPartial || !hasLimitation(l.Meta.Limitations, LimitationExternalCap) {
		t.Fatalf("list total %d completeness %s", l.Total, l.Meta.Completeness)
	}
	other, err := q.GetExternal(string(OtherEgress))
	if err != nil || other.External.Folded != 6 {
		t.Fatalf("other egress = %+v, %v", other, err)
	}
}

func TestNeighborhoodInferredOnlyEdge(t *testing.T) {
	uses := append(stripeUses(), RawExternalUse{Service: "checkout", Namespace: "shop", Identity: "legacy-mq", Kind: "env", Confidence: "likely", Host: "mq.corp-legacy.co.uk"})
	q := NewQuery(inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}, deps: []string{"legacy-mq"}}}, uses, nil, nil))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "checkout", Views: allViews()})
	if err != nil {
		t.Fatal(err)
	}
	edges := map[string]NeighborhoodEdge{}
	for _, e := range nb.Edges {
		edges[e.To.Key] = e
	}
	stripe, mq := edges["stripe"], edges["legacy-mq"]
	if stripe.Provenance != ProvenanceInferred || !stripe.Inferred || stripe.Expected || stripe.Observed || stripe.Difference != "" {
		t.Fatalf("inferred-only edge = %+v", stripe)
	}
	if b, _ := json.Marshal(stripe); strings.Contains(string(b), `"difference"`) || !strings.Contains(string(b), `"inferred":true`) {
		t.Errorf("edge JSON = %s", b)
	}
	if mq.Provenance != ProvenanceDeclared || !mq.Inferred || !mq.Expected {
		t.Errorf("declared and inferred edge = %+v", mq)
	}
	if edgeDifference(stripe) != "" {
		t.Error("an inferred-only edge has no verdict")
	}
	checkInferredOutsideDifferences(t, q)
}

// checkInferredOutsideDifferences: an inferred-only edge shows only with
// the differences view, focused on its caller or on the external.
func checkInferredOutsideDifferences(t *testing.T, q *Query) {
	t.Helper()
	// Without the differences view the inferred-only edge and its leaf go.
	nb, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "checkout", Views: []KnowledgeView{ViewExpected, ViewObserved}})
	for _, e := range nb.Edges {
		if e.To.Key == "stripe" || e.Inferred {
			t.Errorf("edge outside differences = %+v", e)
		}
	}
	if slices.ContainsFunc(nb.Nodes, func(n NeighborhoodNode) bool { return n.Ref.Key == "stripe" }) {
		t.Error("the stripe leaf is drawn outside differences")
	}
	// Focused on the external, an expected view draws no consumer.
	nb, _ = q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "stripe", Views: []KnowledgeView{ViewExpected}})
	if len(nb.Nodes) != 1 || len(nb.Edges) != 0 || nb.TotalNodes != 1 {
		t.Errorf("expected view of stripe = %+v", nb)
	}
	nb, _ = q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "stripe", Views: allViews()})
	if len(nb.Nodes) != 2 || len(nb.Edges) != 1 || nb.Edges[0].Provenance != ProvenanceInferred {
		t.Errorf("differences view of stripe = %+v", nb)
	}
}

func TestInferredUseCatalogMismatch(t *testing.T) {
	uses := stripeUses()
	uses[0].Catalog = "sha256:other"
	another := uses[0]
	another.Catalog = "sha256:another"
	snap := inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, append(uses, uses[0], another), nil, nil)
	var got []Limitation
	for _, l := range snap.Limitations {
		if l.Code == LimitationExternalCatalogMismatch {
			got = append(got, l)
		}
	}
	if len(got) != 2 || !strings.Contains(got[0].Message, "sha256:another") || !strings.Contains(got[1].Message, "sha256:other") ||
		!strings.Contains(got[1].Message, externalcatalog.DefaultDigest()) {
		t.Fatalf("limitations = %+v", got)
	}
	snap = inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, stripeUses(), nil, nil)
	if hasLimitation(snap.Limitations, LimitationExternalCatalogMismatch) {
		t.Error("the snapshot's own catalog is no mismatch")
	}
}

func TestExternalAnswerPartialWhenTargetUnreported(t *testing.T) {
	svcs := []inferredSvc{{name: "checkout", ns: []string{"shop"}, deps: []string{"stripe"}}, {name: "billing", ns: []string{"shop"}}}
	q := NewQuery(inferredFleet(t, svcs, nil, nil, nil))
	if v, _ := q.GetExternal("stripe"); v.Meta.Completeness != CompletenessComplete || hasLimitation(v.Meta.Limitations, LimitationExternalsUnreported) {
		t.Fatalf("reported fleet = %+v", v.Meta)
	}
	lims := map[string][]Limitation{"billing": {{Code: LimitationExternalsUnreported, Message: "status-format"}}}
	q = NewQuery(inferredFleet(t, svcs, nil, nil, lims))
	v, err := q.GetExternal("external:stripe")
	if err != nil || v.Meta.Completeness != CompletenessPartial || !hasLimitation(v.Meta.Limitations, LimitationExternalsUnreported) {
		t.Fatalf("get = %+v, %v", v, err)
	}
	ex, _ := q.Explain("external:stripe")
	d, err := q.EntityDetail(KindExternal, "stripe")
	if ex.Meta.Completeness != CompletenessPartial || err != nil || d.Meta.Completeness != CompletenessPartial || !hasLimitation(d.Meta.Limitations, LimitationExternalsUnreported) {
		t.Errorf("explain %+v detail %+v, %v", ex.Meta, d, err)
	}
	// Every answer that lists the external or its consumers says so too.
	g, _ := q.Graph(GraphQuery{Service: "external:stripe", Direction: DirectionDependents, IncludeObserved: true})
	nb, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindExternal, Key: "stripe", Views: allViews()})
	el, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}})
	sr, _ := q.Search(SearchFilter{Text: "stripe"})
	for name, m := range map[string]Completeness{"graph": g.Meta.Completeness, "neighborhood": nb.Meta.Completeness, "entities": el.Meta.Completeness, "search": sr.Meta.Completeness} {
		if m != CompletenessPartial {
			t.Errorf("%s completeness = %s", name, m)
		}
	}
	if !hasLimitation(nb.Meta.Limitations, LimitationExternalsUnreported) || !hasLimitation(el.Meta.Limitations, LimitationExternalsUnreported) {
		t.Errorf("neighborhood %+v entities %+v", nb.Meta.Limitations, el.Meta.Limitations)
	}
	if partialOf(CompletenessEmpty) != CompletenessEmpty {
		t.Error("empty stays empty")
	}
}

func TestTargetsByScope(t *testing.T) {
	snap := &FleetSnapshot{Targets: map[TargetKey]*TargetRecord{
		"shop/kubernetes/b": {Kind: SourceKindKubernetes, Scope: "shop", ServiceKey: "web", Source: "east"},
		"shop/kubernetes/a": {Kind: SourceKindKubernetes, Scope: "shop", ServiceKey: "web", Source: "west"},
		"shop/kubernetes/c": {Kind: SourceKindKubernetes, Scope: "shop", ServiceKey: "web", Source: "west", Sources: []string{"west", "east"}},
		"vm/web":            {Kind: "vm", Scope: "dc1", ServiceKey: "web", Source: "east"},
	}}
	got := targetsByScope(snap)
	for _, c := range []struct {
		source string
		want   TargetKey
	}{{"west", "shop/kubernetes/a"}, {"east", "shop/kubernetes/b"}, {"other", "shop/kubernetes/a"}} {
		if k := targetOfUse(got, c.source, "web", "shop"); k != c.want {
			t.Errorf("source %s: target = %q, want %q", c.source, k, c.want)
		}
	}
	if k := targetOfUse(got, "east", "web", "dc1"); k != "" {
		t.Errorf("non-Kubernetes target = %q", k)
	}
}

// TestInferredUseIsNoDeclaredDependency: a service that declares nothing and
// has inferred external uses keeps a declared dependency count of zero.
func TestInferredUseIsNoDeclaredDependency(t *testing.T) {
	q := NewQuery(inferredFleet(t, []inferredSvc{{name: "checkout", ns: []string{"shop"}}}, stripeUses(), nil, nil))
	var sum ServiceSummary
	q.tallyServiceEdges(&sum, "checkout")
	if sum.DeclaredDependencies != 0 {
		t.Errorf("declared dependencies = %d", sum.DeclaredDependencies)
	}
}

// TestExternalConsumerProvenance: a consumer known only from traces or only
// from configuration says so, and carries no declaration's required flag.
func TestExternalConsumerProvenance(t *testing.T) {
	svcs := []inferredSvc{{name: "checkout", ns: []string{"shop"}}, {name: "mailer", ns: []string{"shop"}}, {name: "billing", ns: []string{"shop"}, deps: []string{"stripe"}}}
	snap := inferredFleet(t, svcs, stripeUses(), []ObservedEdge{{From: "mailer", To: "api.stripe.com", Count: 1}}, nil)
	d, err := NewQuery(snap).EntityDetail(KindExternal, "stripe")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range d.External.Consumers {
		got[c.Service.Key] = c.Provenance
	}
	want := map[string]string{"billing": ProvenanceDeclared, "checkout": ProvenanceInferred, "mailer": ProvenanceObserved}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("consumer provenance = %v", got)
	}
}

// TestNamespaceShortFormIsNoExternal: <pod>.<service>.<namespace> of a
// namespace a target runs in names no external, whether a pod spec or a trace
// names it, though the namespace (dev) is also a top-level domain.
func TestNamespaceShortFormIsNoExternal(t *testing.T) {
	use := RawExternalUse{Service: "checkout", Namespace: "shop", Identity: "host/kafka-headless.dev", Class: "unclassified", Confidence: "likely",
		Kind: "env", Where: "deployment/checkout env KAFKA_BROKERS", Host: "kafka-0.kafka-headless.dev:9092"}
	svcs := []inferredSvc{{name: "checkout", ns: []string{"shop"}}, {name: "kafka", ns: []string{"dev"}}}
	snap := inferredFleet(t, svcs, []RawExternalUse{use}, []ObservedEdge{{From: "checkout", To: "kafka-0.kafka-headless.dev:9092", Count: 1}}, nil)
	if len(snap.Externals) != 0 {
		t.Errorf("externals = %v", slices.Collect(maps.Keys(snap.Externals)))
	}
	// With no target in dev, the name still reads as a domain.
	snap = inferredFleet(t, svcs[:1], []RawExternalUse{use}, nil, nil)
	if snap.Externals["host/kafka-headless.dev"] == nil {
		t.Error("an unknown namespace was dropped")
	}
}
