package fleet

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const infRepo = "ghcr.io/acme/pacto-inferred/stripe"

// operatorSig is the provenance of a revision only this installation's
// operator signed.
func operatorSig() *BundleProvenance {
	return &BundleProvenance{Signature: BundleSignature{Status: SignatureUnverified, Source: SignatureSourceOperator, Bundles: 1}, Source: "oci"}
}

// infSurface is a stripe surface seen by the k8s source.
func infSurface() *contract.InferredSurface {
	return &contract.InferredSurface{Identity: "stripe", Class: "vendor-api", AsOf: "2026-07-29T12:00:00Z",
		Completeness: &contract.SurfaceCompleteness{State: "complete"},
		Hosts:        []contract.SurfaceValue{{Value: "api.stripe.com:443", Sources: []string{"k8s"}}}}
}

// infRev is a revision of name read at ref: a 2.5 contract naming external
// (none when empty), with surface in its record when set.
func infRev(name, version, ref, external string, prov *BundleProvenance, surface *contract.InferredSurface) RawRevision {
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name, Version: version, External: external}}
	fsys := fstest.MapFS{}
	if surface != nil {
		rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"},
			Fields: []contract.Inferred{}, Surface: surface}
		fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	}
	return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}, ResolvedRef: ref + ":" + version, Digest: "sha256:" + name + "-" + version, Provenance: prov}
}

// infSvc is a consumer revision running in ns.
func infSvc(name, ns string, deps ...contract.Dependency) (RawRevision, RawTarget) {
	digest := "sha256:" + name
	return RawRevision{Bundle: &contract.Bundle{Contract: extContract(name, "1.0.0", deps...), FS: fstest.MapFS{}}, Digest: digest},
		RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: name, Service: name, Digest: digest, Compliance: StatusCompliant}
}

// pinStripe is a dependency that pins stripe's inferred contract.
func pinStripe(rng string) contract.Dependency {
	return contract.Dependency{Name: "stripe-pinned", Ref: "oci://" + infRepo, Compatibility: rng}
}

// infFleet builds a Kubernetes fleet of revs and the consumers svcs.
func infFleet(t *testing.T, revs []RawRevision, svcs ...func() (RawRevision, RawTarget)) *Query {
	t.Helper()
	col := &Collection{Revisions: slices.Clone(revs)}
	for _, s := range svcs {
		r, tg := s()
		col.Revisions, col.Targets = append(col.Revisions, r), append(col.Targets, tg)
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func svc(name, ns string, deps ...contract.Dependency) func() (RawRevision, RawTarget) {
	return func() (RawRevision, RawTarget) { return infSvc(name, ns, deps...) }
}

// stripeProvider is the operator-signed stripe inferred contract at 2023.10.16.
func stripeProvider() RawRevision {
	return infRev("stripe", "2023.10.16", infRepo, "stripe", operatorSig(), infSurface())
}

// depEdge is the dependency edge named to from svc.
func depEdge(t *testing.T, q *Query, from, to string) Relationship {
	t.Helper()
	for _, rel := range q.snap.Relationships {
		if rel.FromService == NewServiceKey(from) && rel.To == to && rel.Type == RelationshipDependency {
			return rel
		}
	}
	t.Fatalf("no edge %s -> %s", from, to)
	return Relationship{}
}

func TestInferredProviderNotAService(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	if q.snap.Services[NewServiceKey("stripe")] != nil {
		t.Error("an inferred contract must never be a service")
	}
	for _, r := range q.snap.Revisions {
		if r.Service == "stripe" {
			t.Errorf("an inferred contract must never be a fleet revision: %s", r.Key)
		}
	}
	rec := q.snap.Externals["stripe"]
	if rec == nil || rec.Provider == nil {
		t.Fatalf("stripe = %+v, want its provider", rec)
	}
	assertStripeProvider(t, rec.Provider)
	if st := q.snap.Sources[0]; st.Status != SourceAvailable {
		t.Errorf("a source that read an inferred contract is available: %+v", st)
	}
}

// assertStripeProvider checks p is stripeProvider's block.
func assertStripeProvider(t *testing.T, p *ExternalProvider) {
	t.Helper()
	if p.Repository != infRepo || p.RangeStatus != RangeUnknown || p.RangeReason != RangeReasonInferredProvider || p.RevisionsTotal != 1 ||
		len(p.Revisions) != 1 || p.Revisions[0].Version != "2023.10.16" || p.Revisions[0].Surface == nil ||
		p.Revisions[0].Signature == nil || p.Revisions[0].Signature.Source != SignatureSourceOperator {
		t.Errorf("provider = %+v", p)
	}
}

func TestInferredProviderCreatesExternalWhenNoConsumer(t *testing.T) {
	q := infFleet(t, []RawRevision{infRev("legacy", "0.0.1", "ghcr.io/acme/pacto-inferred/host/corp-legacy.example", "host/corp-legacy.example", operatorSig(),
		&contract.InferredSurface{Identity: "host/corp-legacy.example", Class: "legacy", AsOf: "2026-07-29T12:00:00Z",
			Completeness: &contract.SurfaceCompleteness{State: "complete"}})})
	rec := q.snap.Externals["host/corp-legacy.example"]
	if rec == nil || rec.Provider == nil || rec.Class != "legacy" || rec.Label != "host/corp-legacy.example" || rec.ConsumersTotal != 0 {
		t.Fatalf("record = %+v", rec)
	}
	// A provider with no surface keeps the catalog's (or no) class.
	q = infFleet(t, []RawRevision{infRev("x", "0.0.1", "ghcr.io/acme/pacto-inferred/host/x.example", "host/x.example", nil, nil)})
	if rec := q.snap.Externals["host/x.example"]; rec.Class != "unclassified" || rec.Provider.Revisions[0].Surface != nil || rec.Provider.Revisions[0].Signature != nil {
		t.Errorf("record = %+v", rec)
	}
}

func TestClaimedExternalStaysService(t *testing.T) {
	q := infFleet(t, []RawRevision{infRev("stripe", "1.0.0", "ghcr.io/acme/stripe", "stripe", nil, nil)})
	if q.snap.Services[NewServiceKey("stripe")] == nil {
		t.Error("a contract that names its external outside pacto-inferred is a service")
	}
	if rec := q.snap.Externals["stripe"]; rec != nil {
		t.Errorf("no provider: %+v", rec)
	}
}

func TestClaimedExternalWithSurfaceStaysService(t *testing.T) {
	q := infFleet(t, []RawRevision{infRev("stripe", "1.0.0", "ghcr.io/acme/stripe", "stripe", nil, infSurface())})
	if q.snap.Services[NewServiceKey("stripe")] == nil {
		t.Error("only the pacto-inferred location marks a contract inferred, never a surface")
	}
}

func TestInferredProviderIdentityFromPath(t *testing.T) {
	for _, external := range []string{"aws/rds/123456789012/eu-west-1/orders-db", ""} {
		q := infFleet(t, []RawRevision{infRev("stripe", "0.0.1", infRepo, external, nil, nil)})
		if len(q.snap.Externals) != 0 || q.snap.Services[NewServiceKey("stripe")] != nil {
			t.Errorf("%q: attached or made a service: %+v", external, q.snap.Externals)
		}
		if !hasLimitation(q.snap.Limitations, LimitationInferredIdentityMismatch) {
			t.Errorf("%q: limitations = %+v", external, q.snap.Limitations)
		}
	}
}

func TestInferredProviderShowsOperatorSignedFirst(t *testing.T) {
	q := infFleet(t, []RawRevision{
		infRev("stripe", "0.0.9999", infRepo, "stripe", nil, nil),
		infRev("stripe", "0.0.1", infRepo, "stripe", operatorSig(), nil),
		infRev("stripe", "0.0.2", infRepo, "stripe", operatorSig(), nil),
		infRev("stripe", "0.0.3", infRepo, "stripe", nil, nil),
	})
	var got []string
	for _, r := range q.snap.Externals["stripe"].Provider.Revisions {
		got = append(got, r.Version)
	}
	if want := []string{"0.0.2", "0.0.1", "0.0.9999", "0.0.3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestInferredProviderBounded(t *testing.T) {
	var revs []RawRevision
	for i := range MaxExternalSources + 3 {
		revs = append(revs, infRev("stripe", "0.0."+strconv.Itoa(i+1), infRepo, "stripe", nil, nil))
	}
	p := infFleet(t, revs).snap.Externals["stripe"].Provider
	if len(p.Revisions) != MaxExternalSources || p.RevisionsTotal != MaxExternalSources+3 {
		t.Errorf("provider = %d of %d", len(p.Revisions), p.RevisionsTotal)
	}
}

func TestInferredRefJoinsExternal(t *testing.T) {
	ext := pinStripe("")
	ext.External = true
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")), svc("pay-agent", "pay", ext))
	rec := q.snap.Externals["stripe"]
	if !reflect.DeepEqual(rec.Consumers, []ServiceKey{"checkout", "pay-agent"}) || rec.ConsumersTotal != 2 {
		t.Errorf("consumers = %v (%d)", rec.Consumers, rec.ConsumersTotal)
	}
	for _, s := range rec.Sources {
		if s.Kind != ExternalSourceContract || s.Match != matchInferredContract {
			t.Errorf("source = %+v", s)
		}
	}
	for _, from := range []string{"checkout", "pay-agent"} {
		rel := depEdge(t, q, from, "stripe-pinned")
		if rel.ToExternal != "stripe" || rel.Resolved || rel.missing() || !strings.Contains(rel.Reason, "through its inferred contract") {
			t.Errorf("%s edge = %+v", from, rel)
		}
	}
}

func TestInferredRefRangeUnknownEvenWhenSatisfied(t *testing.T) {
	// The provider revision is 2023.10.16, inside ^2023.10: still unknown.
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	rel := depEdge(t, q, "checkout", "stripe-pinned")
	if rel.RangeStatus != RangeUnknown || rel.RangeReason != RangeReasonInferredProvider {
		t.Errorf("edge = %+v", rel)
	}
	// A declared service named like the dependency never takes the ref.
	q = infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", contract.Dependency{Name: "pay", Ref: "oci://" + infRepo, Compatibility: "^1.0.0"}), svc("pay", "shop"))
	if rel := depEdge(t, q, "checkout", "pay"); rel.Resolved || rel.ToExternal != "stripe" || rel.RangeStatus != RangeUnknown {
		t.Errorf("edge = %+v", rel)
	}
}

func TestInferredRefWithoutRangeHasNoReason(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("")))
	if rel := depEdge(t, q, "checkout", "stripe-pinned"); rel.RangeStatus != "" || rel.RangeReason != "" {
		t.Errorf("edge = %+v", rel)
	}
	// A ref with no identity below pacto-inferred is no inferred ref at all.
	q = infFleet(t, nil, svc("checkout", "shop", contract.Dependency{Name: "x", Ref: "oci://ghcr.io/acme/pacto-inferred/Not_An_ID", Compatibility: "^1"}))
	if rel := depEdge(t, q, "checkout", "x"); rel.ToExternal != "" || rel.RangeReason != "" || !rel.missing() {
		t.Errorf("Not_An_ID: edge = %+v", rel)
	}
	// The reserved other-egress joins nothing, and its declared range is unknown.
	q = infFleet(t, nil, svc("checkout", "shop", contract.Dependency{Name: "x", Ref: "oci://ghcr.io/acme/pacto-inferred/other-egress", Compatibility: "^1"}))
	if rel := depEdge(t, q, "checkout", "x"); rel.ToExternal != "" || rel.RangeStatus != RangeUnknown || rel.RangeReason != RangeReasonInferredProvider || !rel.missing() {
		t.Errorf("other-egress: edge = %+v", rel)
	}
}

func TestInferredLocationContributionJoinsNoExternal(t *testing.T) {
	key, id, inferred := inferredLocation("oci://ghcr.io/acme/pacto-inferred/my-idp/services:checkout_1.0.0")
	if key != "" || id != "my-idp/services" || !inferred {
		t.Fatalf("inferredLocation = %q %q %v", key, id, inferred)
	}
}

// TestDependencyOnContributionRefIsNeverAServiceOrExternal: a dependency
// pinned at a producer's contribution repository joins no service and no
// external system, and a declared range on it is unknown, never checked.
func TestDependencyOnContributionRefIsNeverAServiceOrExternal(t *testing.T) {
	ref := "oci://ghcr.io/acme/pacto-inferred/my-idp/services:checkout_1.0.0"
	q := infFleet(t, nil, svc("orders", "shop", contract.Dependency{Name: "checkout", Ref: ref, Compatibility: "^1"}), svc("checkout", "shop"))
	rel := depEdge(t, q, "orders", "checkout")
	if rel.ToService != "" || rel.ToExternal != "" || rel.Resolved || rel.RangeStatus != RangeUnknown || rel.RangeReason != RangeReasonInferredProvider ||
		!strings.Contains(rel.Reason, "a producer's contributed record, never a service") || !strings.Contains(rel.Reason, "my-idp") {
		t.Errorf("edge = %+v", rel)
	}
	if items := q.inferredProviderAttention(); len(items) != 0 {
		t.Errorf("attention = %+v", items)
	}
	q = infFleet(t, nil, svc("orders", "shop", contract.Dependency{Name: "checkout", Ref: ref}))
	if rel := depEdge(t, q, "orders", "checkout"); rel.RangeStatus != "" || rel.RangeReason != "" {
		t.Errorf("no range: edge = %+v", rel)
	}
}

func TestInferredRefNotUnresolvedAttention(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
		if it.Code == "UNRESOLVED_DEPENDENCY" || it.Code == LimitationRangeUnsatisfied {
			t.Errorf("item = %+v", it)
		}
	}
}

// ledgerFleet: billing depends on ledger ^rng; ledger has the given revisions,
// those in signed only this installation's operator signed.
func ledgerFleet(t *testing.T, rng string, versions []string, signed map[string]bool) *Query {
	t.Helper()
	var revs []RawRevision
	for _, v := range versions {
		c := extContract("ledger", v)
		c.Service.Offering = "ledger-api"
		r := RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, ResolvedRef: "ghcr.io/acme/ledger:" + v, Digest: "sha256:ledger-" + v}
		if signed[v] {
			r.Provenance = operatorSig()
		}
		revs = append(revs, r)
	}
	return infFleet(t, revs, svc("billing", "shop", contract.Dependency{Name: "ledger", Ref: "oci://ghcr.io/acme/ledger", Compatibility: rng}))
}

func TestSignerRuleOperatorOnlyUnknown(t *testing.T) {
	q := ledgerFleet(t, "^9.0.0", []string{"9.0.0"}, map[string]bool{"9.0.0": true})
	rel := depEdge(t, q, "billing", "ledger")
	if !rel.Resolved || rel.RangeStatus != RangeUnknown || rel.RangeReason != RangeReasonInferredProvider {
		t.Errorf("edge = %+v", rel)
	}
	if q.snap.Services["ledger"] == nil {
		t.Error("an operator signature outside pacto-inferred keeps the service")
	}
}

func TestMixedRevisionsIgnoreOperatorSigned(t *testing.T) {
	q := ledgerFleet(t, "^2.0.0", []string{"2.1.0", "9.0.0"}, map[string]bool{"9.0.0": true})
	if rel := depEdge(t, q, "billing", "ledger"); rel.RangeStatus != RangeSatisfied || rel.RangeReason != "" {
		t.Errorf("edge = %+v", rel)
	}
	// A provider with no revision at all is unknown with no reason.
	q.snap.Services["nothing"] = &ServiceRecord{Key: "nothing"}
	if st, reason := dependencyRangeStatus(q.snap, Relationship{Compatibility: "^1", ToService: "nothing"}); st != RangeUnknown || reason != "" {
		t.Errorf("no revisions = %s %s", st, reason)
	}
}

func TestRangeReasonOnOfferingUseClaimVersions(t *testing.T) {
	q := ledgerFleet(t, "^9.0.0", []string{"9.0.0"}, map[string]bool{"9.0.0": true})
	uses, _ := q.offeringUsesIn(q.snap.Relationships, func(p *ServiceRecord) bool { return p.Name == "ledger" }, "", false)
	if len(uses) != 1 || uses[0].RangeStatus != RangeUnknown || uses[0].RangeReason != RangeReasonInferredProvider {
		t.Errorf("uses = %+v", uses)
	}
	if c := declaredClaim(depEdge(t, q, "billing", "ledger")); c.RangeReason != RangeReasonInferredProvider {
		t.Errorf("claim = %+v", c)
	}
	v := q.offeringProviderVersions(q.snap.Services["ledger"], "^9.0.0")
	if v.Items[0].RangeReason != RangeReasonInferredProvider {
		t.Errorf("versions = %+v", v.Items)
	}
	// mergeUse keeps a common reason and drops one the revisions disagree on.
	u := OfferingUse{}
	mergeUse(&u, &Relationship{RangeStatus: RangeUnknown, RangeReason: RangeReasonInferredProvider})
	mergeUse(&u, &Relationship{RangeStatus: RangeUnknown, RangeReason: RangeReasonInferredProvider})
	mergeUse(&u, &Relationship{})
	if u.RangeReason != RangeReasonInferredProvider {
		t.Errorf("common reason = %+v", u)
	}
	mergeUse(&u, &Relationship{RangeStatus: RangeSatisfied})
	if u.RangeStatus != RangeUnknown || u.RangeReason != "" {
		t.Errorf("disagreeing = %+v", u)
	}
}

func TestVersionsOperatorSignedNeverLatestCompatible(t *testing.T) {
	q := ledgerFleet(t, "^2.0.0", []string{"2.1.0", "9.0.0"}, map[string]bool{"9.0.0": true})
	v := q.offeringProviderVersions(q.snap.Services["ledger"], ">=2.0.0")
	if v.Items[0].Version != "9.0.0" || v.Items[0].RangeStatus != RangeUnknown || v.Items[0].RangeReason != RangeReasonInferredProvider {
		t.Errorf("9.0.0 = %+v", v.Items[0])
	}
	if v.LatestCompatible == nil || v.LatestCompatible.Label != v.Items[1].Revision.Label || v.Items[1].RangeReason != "" {
		t.Errorf("latest compatible = %+v, items %+v", v.LatestCompatible, v.Items)
	}
}

func TestRestrictInferredProviderFollowsExternal(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	if r := q.Restrict(anyCluster(shopView), anyClusterKind(nil)); r.snap.Externals["stripe"] == nil || r.snap.Externals["stripe"].Provider.Revisions[0].Surface == nil {
		t.Errorf("visible consumer: %+v", r.snap.Externals["stripe"])
	}
	if r := q.Restrict(anyCluster(func(string) bool { return false }), anyClusterKind(nil)); r.snap.Externals["stripe"] != nil {
		t.Error("the provider follows its external's visibility")
	}
}

func TestRestrictInferredProviderHidesSurfaceFromHiddenSources(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")), svc("vault", "secret", pinStripe("")))
	v, err := q.Restrict(anyCluster(shopView), anyClusterKind(nil)).GetExternal("external:stripe")
	if err != nil {
		t.Fatal(err)
	}
	p := v.External.Provider
	if p == nil || p.Revisions[0].Surface != nil || p.Revisions[0].Version != "2023.10.16" || p.Revisions[0].Signature == nil || p.RangeReason != RangeReasonInferredProvider {
		t.Errorf("provider = %+v", p)
	}
	if q.snap.Externals["stripe"].Provider.Revisions[0].Surface == nil {
		t.Error("restriction must never mutate the base snapshot")
	}
	if b, _ := json.Marshal(p); strings.Contains(string(b), "vault") {
		t.Errorf("provider names a hidden consumer: %s", b)
	}
}

func TestRestrictClearsRangeReason(t *testing.T) {
	rel := Relationship{ToService: "ledger", RangeStatus: RangeUnknown, RangeReason: RangeReasonInferredProvider}
	hideProvider(&rel)
	if rel.RangeStatus != "" || rel.RangeReason != "" {
		t.Errorf("hidden edge = %+v", rel)
	}
}

// inferredItems is q's INFERRED_PROVIDER attention.
func inferredItems(t *testing.T, q *Query) []AttentionItem {
	t.Helper()
	var out []AttentionItem
	for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
		if it.Code == RangeReasonInferredProvider {
			out = append(out, it)
		}
	}
	return out
}

func TestInferredProviderAttentionOnePerProvider(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()},
		svc("checkout", "shop", pinStripe("^2023.10")), svc("pay-agent", "pay", pinStripe("^2023")),
		svc("a", "shop", pinStripe("^1")), svc("b", "shop", pinStripe("^1")), svc("c", "shop", pinStripe("^1")))
	items := inferredItems(t, q)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Entity.Kind != KindExternal || it.Entity.Key != "stripe" || it.Category != categoryKnowledge || it.Severity != severityInfo ||
		it.Label != "5 services check a range against stripe's inferred contract" || !strings.HasPrefix(it.Reason, "a, b, c and 2 more pin") {
		t.Errorf("item = %+v", it)
	}
	// The signer rule groups by service.
	q = ledgerFleet(t, "^9.0.0", []string{"9.0.0"}, map[string]bool{"9.0.0": true})
	items = inferredItems(t, q)
	if len(items) != 1 || items[0].Service != "ledger" || items[0].Label != "1 service checks a range against ledger's inferred contract" || !strings.HasPrefix(items[0].Reason, "billing pins") {
		t.Errorf("items = %+v", items)
	}
}

func TestInferredProviderAttentionNextActionDeclare(t *testing.T) {
	items := inferredItems(t, infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10"))))
	// pacto declare reads only workloads, so an external's step is the one
	// that exists today: a hand-written contract naming it.
	if len(items) != 1 || items[0].NextAction == nil || *items[0].NextAction != *editContract() ||
		items[0].NextStep != "Write a contract with service.external: stripe, publish it outside pacto-inferred, then point the ranges at it" ||
		strings.Contains(items[0].Reason, "published") {
		t.Errorf("items = %+v", items)
	}
	// A service under the signer rule is declared with pacto declare.
	items = inferredItems(t, ledgerFleet(t, "^9.0.0", []string{"9.0.0"}, map[string]bool{"9.0.0": true}))
	if len(items) != 1 || items[0].NextStep != "Declare a contract for ledger with pacto declare, then point the ranges at it" ||
		items[0].NextAction == nil || *items[0].NextAction != (NextAction{Verb: "declare", Tool: "pacto_declare", CLI: "pacto declare ledger"}) {
		t.Errorf("service items = %+v", items)
	}
}

func TestInferredProviderAttentionCountsVisibleOnly(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")), svc("vault", "secret", pinStripe("^1")))
	items := inferredItems(t, q.Restrict(anyCluster(shopView), anyClusterKind(nil)))
	if len(items) != 1 || items[0].Label != "1 service checks a range against stripe's inferred contract" || strings.Contains(items[0].Reason, "vault") {
		t.Errorf("items = %+v", items)
	}
	if items := inferredItems(t, q.Restrict(anyCluster(func(ns string) bool { return ns == "" }), anyClusterKind(nil))); len(items) != 0 {
		t.Errorf("all consumers hidden: %+v", items)
	}
}

func TestKnowledgeTileCountsInferredProvider(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	n := 0
	for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
		if it.Category == categoryKnowledge {
			n++
		}
	}
	if got := q.Overview().Summary.KnowledgeItems; got != n || n == 0 {
		t.Errorf("tile = %d, list = %d", got, n)
	}
}

func TestNextActionEnums(t *testing.T) {
	f, _ := reflect.TypeFor[NextAction]().FieldByName("Verb")
	g, _ := reflect.TypeFor[NextAction]().FieldByName("Tool")
	verbs, tools := strings.Split(f.Tag.Get("enum"), ","), strings.Split(g.Tag.Get("enum"), ",")
	if !slices.Equal(verbs, []string{"edit-contract", "declare", "accept", "claim", "approve-cluster"}) || !slices.Equal(tools, []string{"pacto_edit", "pacto_declare", "pacto_accept"}) {
		t.Errorf("enums %v %v", verbs, tools)
	}
	for _, a := range []*NextAction{editContract(), declareAction("x", "")} {
		if !slices.Contains(verbs, a.Verb) || !slices.Contains(tools, a.Tool) || a.CLI == "" {
			t.Errorf("constructor %+v outside the enums", a)
		}
	}
}

// wireKeys is the JSON object keys of v.
func wireKeys(t *testing.T, v any) map[string]bool {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func TestRelationshipJSONKeepsFields(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	keys := wireKeys(t, depEdge(t, q, "checkout", "stripe-pinned"))
	for _, k := range []string{"fromService", "fromRevision", "to", "type", "provenance", "compatibility", "resolved", "requestedRef", "reason", "rangeStatus", "rangeReason", "toExternal"} {
		if !keys[k] {
			t.Errorf("relationship lost %q: %v", k, keys)
		}
	}
}

func TestExternalRecordJSONKeepsFields(t *testing.T) {
	q := infFleet(t, []RawRevision{stripeProvider()}, svc("checkout", "shop", pinStripe("^2023.10")))
	keys := wireKeys(t, q.snap.Externals["stripe"])
	if q.snap.Externals["stripe"].JoinedBy != "identity" {
		t.Errorf("a ref to the inferred contract joins by identity: %+v", q.snap.Externals["stripe"])
	}
	for _, k := range []string{"key", "label", "class", "sources", "sourcesTotal", "consumers", "consumersTotal", "joinedBy", "provider"} {
		if !keys[k] {
			t.Errorf("external lost %q: %v", k, keys)
		}
	}
	pk := wireKeys(t, q.snap.Externals["stripe"].Provider)
	for _, k := range []string{"repository", "revisions", "revisionsTotal", "rangeStatus", "rangeReason"} {
		if !pk[k] {
			t.Errorf("provider lost %q", k)
		}
	}
}

// TestExternalProviderLines: the shown revision's own signature words the
// provenance, the range line always says unverified and a dropped surface
// says why it is missing.
func TestExternalProviderLines(t *testing.T) {
	sig := operatorSig().Signature
	p := &ExternalProvider{Repository: infRepo, RevisionsTotal: 2, RangeStatus: RangeUnknown, RangeReason: RangeReasonInferredProvider,
		Revisions: []ExternalProviderRevision{{Version: "0.0.2", Digest: "sha256:b", Surface: infSurface(), Signature: &sig}}}
	want := []string{infRepo + ":0.0.2 (sha256:b), 2 revisions read", "signed by this installation's operator from what Pacto sees",
		"range checks: unverified (INFERRED_PROVIDER)", "surface as of 2026-07-29T12:00:00Z: hosts 1, ports 0, clients 0"}
	if got := p.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("operator signed:\n%q\nwant\n%q", got, want)
	}
	p.RevisionsTotal, p.Revisions[0].Signature, p.Revisions[0].Surface = 1, &BundleSignature{Status: SignatureUnsigned}, nil
	got := p.Lines()
	if got[0] != infRepo+":0.0.2 (sha256:b)" || got[1] != "unsigned, publisher unknown" || got[3] != "surface: none recorded in this revision" {
		t.Errorf("unsigned: %q", got)
	}
	p.SurfaceHidden = true
	if got := p.Lines(); got[3] != "surface hidden: your view is restricted" {
		t.Errorf("restricted: %q", got)
	}
	// A partial run says so, with its codes, so carried-over values never
	// read as current.
	s := infSurface()
	s.Completeness = &contract.SurfaceCompleteness{State: contract.CoveragePartial, Limitations: []string{LimitationExternalsUnreported}}
	p.Revisions[0].Surface = s
	if got := p.Lines(); !strings.HasPrefix(got[3], "surface as of 2026-07-29T12:00:00Z, partial (EXTERNALS_UNREPORTED): some sources did not answer") {
		t.Errorf("partial: %q", got)
	}
	p.Revisions = nil
	if got := p.Lines(); !reflect.DeepEqual(got, []string{infRepo, "range checks: unverified (INFERRED_PROVIDER)"}) {
		t.Errorf("no revision: %q", got)
	}
}

// TestSignatureWordsFollowTheCheck: only a check that ran says unsigned or
// names a signer; nil and unknown never read unsigned.
func TestSignatureWordsFollowTheCheck(t *testing.T) {
	for sig, want := range map[*BundleSignature]string{
		nil: "signature not checked, publisher unknown",
		{Status: SignatureUnverified, Source: SignatureSourceOperator}:             "signed by this installation's operator from what Pacto sees",
		{Status: SignatureUnsigned, Source: "none"}:                                "unsigned, publisher unknown",
		{Status: "unknown", Source: "none"}:                                        "signature could not be checked, publisher unknown",
		{Status: "verified", Source: "rule", Signer: "https://github.com/acme/ci"}: "signed by https://github.com/acme/ci, not this installation's operator (verified)",
		{Status: SignatureUnverified, Source: "inferred"}:                          "signed, not by this installation's operator (unverified)",
	} {
		if got := SignatureWords(sig); got != want {
			t.Errorf("%+v: %q, want %q", sig, got, want)
		}
	}
}

// TestRestrictedViewDropsStaleSurface: a published surface may name values
// from consumers no longer in the record (an older revision, or a value a
// partial run carried forward), so every restricted view drops it even when
// every current source is visible.
func TestRestrictedViewDropsStaleSurface(t *testing.T) {
	stale := infSurface()
	stale.Hosts = append(stale.Hosts, contract.SurfaceValue{Value: "payments-db.internal:5432", Sources: []string{"k8s-ns-a"}})
	q := infFleet(t, []RawRevision{infRev("stripe", "0.0.2", infRepo, "stripe", operatorSig(), stale)},
		svc("checkout", "shop", pinStripe("^0.0.1")), svc("vault", "secret"))
	r := q.Restrict(anyCluster(shopView), anyClusterKind(nil)).snap.Externals["stripe"]
	if r == nil || r.HiddenConsumers != 0 || !r.Provider.SurfaceHidden || r.Provider.Revisions[0].Surface != nil {
		t.Fatalf("restricted = %+v", r)
	}
	if p := q.snap.Externals["stripe"].Provider; p.SurfaceHidden || p.Revisions[0].Surface == nil {
		t.Errorf("unrestricted = %+v", p)
	}
}

// TestProviderSurfaceOnShownRevisionOnly: an older revision never ships its
// surface, so the payload stays one surface whatever the revision count.
func TestProviderSurfaceOnShownRevisionOnly(t *testing.T) {
	q := infFleet(t, []RawRevision{infRev("stripe", "0.0.1", infRepo, "stripe", operatorSig(), infSurface()), infRev("stripe", "0.0.2", infRepo, "stripe", operatorSig(), infSurface())})
	p := q.snap.Externals["stripe"].Provider
	if len(p.Revisions) != 2 || p.Revisions[0].Version != "0.0.2" || p.Revisions[0].Surface == nil || p.Revisions[1].Surface != nil {
		t.Errorf("provider = %+v", p)
	}
}

// TestUnsignedSurfaceNeverClassifies: a hand-pushed revision's class is not
// the external's; an operator-signed one's is, and explain says where from.
func TestUnsignedSurfaceNeverClassifies(t *testing.T) {
	ref := "ghcr.io/acme/pacto-inferred/legacy-mq"
	s := &contract.InferredSurface{Identity: "legacy-mq", Class: "managed-cloud", AsOf: "2026-07-29T12:00:00Z", Completeness: &contract.SurfaceCompleteness{State: "complete"}}
	q := infFleet(t, []RawRevision{infRev("legacy-mq", "0.0.99", ref, "legacy-mq", nil, s)})
	if rec := q.snap.Externals["legacy-mq"]; rec.Class != "unclassified" || inferredClassRef(rec) != "" {
		t.Errorf("unsigned classified it: %+v", rec)
	}
	q = infFleet(t, []RawRevision{infRev("legacy-mq", "0.0.1", ref, "legacy-mq", operatorSig(), s)})
	ex, err := q.explainExternal("legacy-mq")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range ex.Reasons {
		found = found || r.Code == RangeReasonInferredProvider && strings.Contains(r.Message, "classified managed-cloud by its inferred contract "+ref+":0.0.1")
	}
	if q.snap.Externals["legacy-mq"].Class != "managed-cloud" || !found {
		t.Errorf("signed: %+v", ex.Reasons)
	}
	if inferredClassRef(&ExternalRecord{Catalog: &ExternalCatalogMatch{}}) != "" || inferredClassRef(&ExternalRecord{}) != "" {
		t.Error("a catalog class or no provider names an inferred contract")
	}
}

// TestUnparsableInferredRefNeverAService: a ref under pacto-inferred whose
// identity does not parse is diverted (never a service) and a dependency on
// it stays unresolved with its reason, never a checked range.
func TestUnparsableInferredRefNeverAService(t *testing.T) {
	for _, repo := range []string{"ghcr.io/acme/pacto-inferred/host/corp.example/api", "ghcr.io/acme/pacto-inferred/other-egress"} {
		q := infFleet(t, []RawRevision{infRev("api", "1.2.0", repo, "", nil, nil)},
			svc("checkout", "shop", contract.Dependency{Name: "api", Ref: "oci://" + repo, Compatibility: "^1"}))
		if q.snap.Services[NewServiceKey("api")] != nil {
			t.Errorf("%s: became a service", repo)
		}
		rel := depEdge(t, q, "checkout", "api")
		if rel.Resolved || rel.RangeStatus == RangeSatisfied || !strings.Contains(rel.Reason, "no external identity") {
			t.Errorf("%s: edge = %+v", repo, rel)
		}
		if !slices.ContainsFunc(q.snap.Limitations, func(l Limitation) bool { return l.Code == LimitationInferredIdentityMismatch }) {
			t.Errorf("%s: no mismatch limitation: %+v", repo, q.snap.Limitations)
		}
	}
}

// TestRestrictedViewCountsIdentityMismatches: a mismatch names an external
// and a ref, so a restricted view only counts them.
func TestRestrictedViewCountsIdentityMismatches(t *testing.T) {
	q := infFleet(t, []RawRevision{infRev("db", "0.0.3", "ghcr.io/acme/pacto-inferred/aws/rds/123/eu-west-1/payments-db", "", nil, nil)},
		svc("checkout", "shop"), svc("vault", "secret"))
	var got []Limitation
	for _, l := range q.Restrict(anyCluster(shopView), anyClusterKind(nil)).snap.Limitations {
		if l.Code == LimitationInferredIdentityMismatch {
			got = append(got, l)
		}
	}
	if len(got) != 1 || strings.Contains(got[0].Message, "payments-db") || !strings.HasPrefix(got[0].Message, "1 contracts") {
		t.Errorf("restricted mismatches = %+v", got)
	}
}

// TestDivertedRevisionKeepsLimitationsAndMergesCopies: what reading a
// diverted revision could not establish is said, and a second source's
// operator signature on the same digest is kept.
func TestDivertedRevisionKeepsLimitationsAndMergesCopies(t *testing.T) {
	bad := infRev("stripe", "0.0.1", infRepo, "stripe", nil, nil)
	bad.Bundle.FS = fstest.MapFS{contract.InferredRecordFile: &fstest.MapFile{Data: []byte("not: [a record")}}
	q := infFleet(t, []RawRevision{bad})
	if !slices.ContainsFunc(q.snap.Limitations, func(l Limitation) bool { return l.Code == LimitationInferredRecordUnreadable }) {
		t.Errorf("record limitation dropped: %+v", q.snap.Limitations)
	}
	first := infRev("stripe", "0.0.1", infRepo, "stripe", nil, nil)
	first.Digest = "sha256:stripe-0.0.1"
	second := infRev("stripe", "0.0.1", infRepo, "stripe", operatorSig(), infSurface())
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("cache", SourceKindKubernetes, &Collection{Revisions: []RawRevision{first}}),
		NewMemorySource("oci", SourceKindKubernetes, &Collection{Revisions: []RawRevision{second}}))
	if err != nil {
		t.Fatal(err)
	}
	if r := snap.Externals["stripe"].Provider.Revisions[0]; r.Signature == nil || r.Signature.Source != SignatureSourceOperator || r.Surface == nil {
		t.Errorf("merged revision = %+v", r)
	}
}
