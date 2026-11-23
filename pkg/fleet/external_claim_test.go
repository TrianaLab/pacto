package fleet

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// claimOf shapes claimRev.
type claimOf struct {
	service, version, external, from, owner string
	prov                                    *BundleProvenance
	unreadable                              bool
}

// claimRev is a registry revision of c.service (stripe when "") at c.version
// (0.1.0 when "") under ghcr.io/acme, naming c.external in service.external,
// whose record carries ana's declaration of inferredFrom c.from (none when
// ""), signed as c.prov says.
func claimRev(c claimOf) RawRevision {
	name, version := cmpOr(c.service, "stripe"), cmpOr(c.version, "0.1.0")
	ct := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name, Version: version, External: c.external, Owner: contract.Owner{Team: c.owner}}}
	fsys := fstest.MapFS{}
	if c.from != "" {
		rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"}, Fields: []contract.Inferred{},
			Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana, InferredFrom: c.from}}
		fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	}
	if c.unreadable {
		fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: []byte("format: [")}
	}
	return RawRevision{Bundle: &contract.Bundle{Contract: ct, FS: fsys}, ResolvedRef: "ghcr.io/acme/" + name + ":" + version, Digest: "sha256:" + name + "-" + version, Provenance: c.prov}
}

// stripeClaim is ana's claim of stripe for team-platform.
func stripeClaim() RawRevision {
	return claimRev(claimOf{external: "stripe", from: "external:stripe", owner: "team-platform", prov: anaSigned()})
}

// claimFleet is the registry revisions revs, read with signature checks
// under ana's signer policy, and the Kubernetes consumers svcs.
func claimFleet(t *testing.T, revs []RawRevision, svcs ...func() (RawRevision, RawTarget)) *Query {
	t.Helper()
	col := &Collection{}
	for _, s := range svcs {
		r, tg := s()
		col.Revisions, col.Targets = append(col.Revisions, r), append(col.Targets, tg)
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: anaPolicy()},
		NewMemorySource("oci", "oci", &Collection{Revisions: revs}), NewMemorySource("k8s", SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func checkoutStripe() func() (RawRevision, RawTarget) {
	return svc("checkout", "shop", extDep("stripe"))
}

// limitationWith is the limitations of q whose message contains s.
func limitationWith(q *Query, s string) []Limitation {
	var out []Limitation
	for _, l := range q.snap.Limitations {
		if strings.Contains(l.Message, s) {
			out = append(out, l)
		}
	}
	return out
}

// unownedExternals is q's UNOWNED_EXTERNAL attention, in list order.
func unownedExternals(t *testing.T, q *Query) []AttentionItem {
	t.Helper()
	var out []AttentionItem
	for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
		if it.Code == "UNOWNED_EXTERNAL" {
			out = append(out, it)
		}
	}
	return out
}

// inShop hides every namespace but shop and every service with no
// Kubernetes target.
func inShop(q *Query) *Query {
	return q.Restrict(anyCluster(func(ns string) bool { return ns == "shop" }), anyClusterKind(nil))
}

func TestClaimLinksDeclaredPerson(t *testing.T) {
	q := claimFleet(t, []RawRevision{stripeClaim()}, checkoutStripe())
	want := &ExternalClaim{Service: NewServiceKey("stripe"), Ref: "ghcr.io/acme/stripe@sha256:stripe-0.1.0", Version: "0.1.0", By: anaSubject,
		DeclaredVersion: "0.1.0", Owner: "team-platform", OwnerKey: "team:team-platform"}
	if got := q.snap.Externals["stripe"].Claim; !reflect.DeepEqual(got, want) {
		t.Errorf("claim = %+v, want %+v", got, want)
	}
	if q.snap.Externals["stripe"].ClaimedRepository() != "ghcr.io/acme/stripe" {
		t.Errorf("repository = %q", q.snap.Externals["stripe"].ClaimedRepository())
	}
	// A revision no registry ref names keeps its claim and names no ref.
	local := stripeClaim()
	local.ResolvedRef = ""
	q = claimFleet(t, []RawRevision{local}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c == nil || c.Ref != "" || q.snap.Externals["stripe"].ClaimedRepository() != "" {
		t.Errorf("local claim = %+v", c)
	}
}

func TestClaimCarriedByLaterCIPublish(t *testing.T) {
	ci := claimRev(claimOf{version: "0.2.0", external: "stripe", owner: "team-platform", prov: ciSigned()})
	q := claimFleet(t, []RawRevision{stripeClaim(), ci}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c == nil || c.Version != "0.2.0" || c.By != anaSubject || c.Ref != "ghcr.io/acme/stripe@sha256:stripe-0.2.0" {
		t.Errorf("claim = %+v, want carried to 0.2.0", c)
	}
	if ls := limitationWith(q, "without a claim"); len(ls) != 0 {
		t.Errorf("a carried claim raises nothing: %+v", ls)
	}
}

func TestClaimRetractedByNewerRevisionWithoutExternal(t *testing.T) {
	dropped := claimRev(claimOf{version: "0.2.0", owner: "team-platform", prov: ciSigned()})
	q := claimFleet(t, []RawRevision{stripeClaim(), dropped}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c != nil {
		t.Errorf("claim = %+v, want retracted", c)
	}
}

func TestClaimByCIOnlyServiceLinksNothing(t *testing.T) {
	ci := claimRev(claimOf{service: "billing", external: "stripe", owner: "billing", prov: ciSigned()})
	q := claimFleet(t, []RawRevision{ci}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c != nil {
		t.Errorf("claim = %+v", c)
	}
	ls := limitationWith(q, "billing declares external stripe without a claim: run pacto claim stripe --owner <team>")
	if len(ls) != 1 || ls[0].Code != LimitationInferredLinkIgnored {
		t.Errorf("limitations = %+v", q.snap.Limitations)
	}
}

func TestCIServiceExternalCannotCancelDeclaredClaim(t *testing.T) {
	ci := claimRev(claimOf{service: "billing", external: "stripe", owner: "billing", prov: ciSigned()})
	q := claimFleet(t, []RawRevision{stripeClaim(), ci}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c == nil || c.Service != NewServiceKey("stripe") {
		t.Errorf("claim = %+v, want stripe's", c)
	}
	if ls := limitationWith(q, "is claimed by"); len(ls) != 0 {
		t.Errorf("no conflict: %+v", ls)
	}
}

func TestInferredFromClaimsSkipsExternal(t *testing.T) {
	q := claimFleet(t, []RawRevision{stripeClaim()}, checkoutStripe())
	for _, l := range q.snap.Limitations {
		if l.Code == LimitationInferredLinkIgnored || l.Code == LimitationInferredReplacementAmbiguous {
			t.Errorf("an external claim is no workload claim: %+v", l)
		}
	}
}

func TestClaimOperatorSignedNeverClaims(t *testing.T) {
	op := claimRev(claimOf{external: "stripe", from: "external:stripe", owner: "team-platform", prov: operatorSig()})
	q := claimFleet(t, []RawRevision{op}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c != nil || len(limitationWith(q, "only this installation's operator signed it")) != 1 {
		t.Errorf("claim = %+v, limitations %+v", c, q.snap.Limitations)
	}
	// An inferred contract under pacto-inferred never claims, even declared.
	inf := claimRev(claimOf{external: "stripe", from: "external:stripe", prov: anaSigned()})
	inf.ResolvedRef = infRepo + ":0.1.0"
	q = claimFleet(t, []RawRevision{inf}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c != nil {
		t.Errorf("inferred claim = %+v", c)
	}
}

func TestClaimUnverifiedLinksNothing(t *testing.T) {
	unsigned := claimRev(claimOf{external: "stripe", from: "external:stripe", owner: "team-platform"})
	q := claimFleet(t, []RawRevision{unsigned}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c != nil || len(limitationWith(q, "stripe claims external stripe, but this source checks no signatures")) != 1 {
		t.Errorf("claim = %+v, limitations %+v", c, q.snap.Limitations)
	}
}

func TestClaimByTwoServicesLinksNothing(t *testing.T) {
	other := claimRev(claimOf{service: "stripe-two", external: "stripe", from: "external:stripe", owner: "payments", prov: anaSigned()})
	q := claimFleet(t, []RawRevision{stripeClaim(), other}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c != nil {
		t.Errorf("claim = %+v", c)
	}
	if ls := limitationWith(q, "external stripe is claimed by stripe, stripe-two"); len(ls) != 1 || ls[0].Code != LimitationInferredLinkIgnored {
		t.Errorf("limitations = %+v", q.snap.Limitations)
	}
}

func TestClaimRetractedByUnreadableRevision(t *testing.T) {
	bad := claimRev(claimOf{version: "0.2.0", external: "stripe", owner: "team-platform", prov: ciSigned(), unreadable: true})
	q := claimFleet(t, []RawRevision{stripeClaim(), bad}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c != nil || len(limitationWith(q, "the claim of stripe on external stripe is not trusted")) != 1 {
		t.Errorf("claim = %+v, limitations %+v", c, q.snap.Limitations)
	}
}

func TestClaimWithoutRecordLinksNothing(t *testing.T) {
	// A claimed external no consumer and no inferred contract names has no
	// record to link; a declaration naming another system links nothing.
	q := claimFleet(t, []RawRevision{stripeClaim()})
	if q.snap.Externals["stripe"] != nil {
		t.Errorf("record = %+v", q.snap.Externals["stripe"])
	}
	mismatch := claimRev(claimOf{external: "twilio", from: "external:stripe", owner: "team-platform", prov: anaSigned()})
	q = claimFleet(t, []RawRevision{mismatch}, checkoutStripe(), svc("sms", "shop", extDep("twilio")))
	if q.snap.Externals["stripe"].Claim != nil || q.snap.Externals["twilio"].Claim != nil {
		t.Error("a declaration naming another system claims nothing")
	}
}

func TestClaimKeepsObservedAndInferredSources(t *testing.T) {
	consumers := []func() (RawRevision, RawTarget){checkoutStripe(), svc("pay", "shop", pinStripe("^2023"))}
	before := claimFleet(t, []RawRevision{stripeProvider()}, consumers...).snap.Externals["stripe"]
	after := claimFleet(t, []RawRevision{stripeProvider(), stripeClaim()}, consumers...).snap.Externals["stripe"]
	if after.Claim == nil || !reflect.DeepEqual(before.Sources, after.Sources) || before.ConsumersTotal != after.ConsumersTotal {
		t.Errorf("sources before %+v, after %+v", before.Sources, after.Sources)
	}
}

func TestClaimKeepsInferredProviderBlock(t *testing.T) {
	rec := claimFleet(t, []RawRevision{stripeProvider(), stripeClaim()}, checkoutStripe()).snap.Externals["stripe"]
	if rec.Claim == nil || rec.Provider == nil {
		t.Fatalf("record = %+v", rec)
	}
	assertStripeProvider(t, rec.Provider)
}

func TestUnownedExternalItem(t *testing.T) {
	items := unownedExternals(t, claimFleet(t, nil, checkoutStripe()))
	want := AttentionItem{
		Entity: EntityRef{Kind: KindExternal, Key: "stripe", Label: "Stripe", Secondary: "vendor-api", Status: StatusUnknown},
		Label:  "Stripe: 1 service uses it", Severity: severityInfo, Code: "UNOWNED_EXTERNAL", Category: categoryUnowned,
		Summary:    "External system has no owner",
		Reason:     "checkout uses Stripe, and no declared contract claims it",
		NextStep:   "Claim stripe for a team with pacto claim stripe --owner <team>; until then it is routed to the installation's default approvers (routing only; it grants no right)",
		NextAction: &NextAction{Verb: "claim", Tool: "pacto_declare", CLI: "pacto claim stripe --owner <team>"},
		rank:       1,
	}
	if len(items) != 1 || !reflect.DeepEqual(items[0], want) {
		t.Errorf("items = %+v", items)
	}
	items = unownedExternals(t, claimFleet(t, nil, checkoutStripe(), svc("a", "shop", extDep("stripe")), svc("b", "shop", extDep("stripe")),
		svc("c", "shop", extDep("stripe")), svc("d", "shop", extDep("stripe"))))
	if len(items) != 1 || items[0].Label != "Stripe: 5 services use it" || !strings.HasPrefix(items[0].Reason, "a, b, c and 2 more use Stripe") {
		t.Errorf("items = %+v", items)
	}
}

func TestClaimedExternalRaisesNothing(t *testing.T) {
	if items := unownedExternals(t, claimFleet(t, []RawRevision{stripeClaim()}, checkoutStripe())); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestClaimedUnverifiedOwnerStaysUnowned(t *testing.T) {
	ownerless := claimRev(claimOf{external: "stripe", from: "external:stripe", prov: anaSigned()})
	items := unownedExternals(t, claimFleet(t, []RawRevision{ownerless}, checkoutStripe()))
	if len(items) != 1 || items[0].Reason != "checkout uses Stripe, and the service that claims it has no verified owner" {
		t.Errorf("items = %+v", items)
	}
}

func TestHiddenClaimRaisesNoUnownedExternal(t *testing.T) {
	// The claimer has no Kubernetes target, so a shop viewer cannot read it.
	q := inShop(claimFleet(t, []RawRevision{stripeClaim()}, checkoutStripe()))
	if c := q.snap.Externals["stripe"].Claim; c == nil || !c.Hidden {
		t.Fatalf("claim = %+v", c)
	}
	if items := unownedExternals(t, q); len(items) != 0 {
		t.Errorf("a hidden claim is no unclaimed external: %+v", items)
	}
	// A hidden claimer with no owner still has none.
	ownerless := claimRev(claimOf{external: "stripe", from: "external:stripe", prov: anaSigned()})
	if items := unownedExternals(t, inShop(claimFleet(t, []RawRevision{ownerless}, checkoutStripe()))); len(items) != 1 {
		t.Errorf("items = %+v", items)
	}
}

func TestRestrictHidesClaimer(t *testing.T) {
	q := inShop(claimFleet(t, []RawRevision{stripeClaim()}, checkoutStripe()))
	if c := q.snap.Externals["stripe"].Claim; !reflect.DeepEqual(c, &ExternalClaim{Hidden: true}) {
		t.Errorf("claim = %+v, want hidden with no service, ref, version or person", c)
	}
	if q.snap.Externals["stripe"].ClaimedRepository() != "" {
		t.Error("a hidden claim names no repository")
	}
	// A visible claimer stays.
	visible := func() (RawRevision, RawTarget) {
		r, tg := infSvc("stripe", "shop")
		return stripeClaim(), RawTarget{Scope: tg.Scope, Kind: tg.Kind, Name: "stripe", Service: "stripe", Digest: r.Digest, Compliance: StatusCompliant}
	}
	q = inShop(claimFleet(t, nil, checkoutStripe(), visible))
	if c := q.snap.Externals["stripe"].Claim; c == nil || c.Hidden || c.Service != NewServiceKey("stripe") {
		t.Errorf("visible claim = %+v", c)
	}
}

func TestRestrictDropsClaimConflictNamingHiddenService(t *testing.T) {
	other := claimRev(claimOf{service: "stripe-two", external: "stripe", from: "external:stripe", owner: "payments", prov: anaSigned()})
	full := claimFleet(t, []RawRevision{stripeClaim(), other}, checkoutStripe())
	if len(limitationWith(full, "stripe-two")) == 0 {
		t.Fatal("the full view names the conflict")
	}
	q := inShop(full)
	for _, l := range q.snap.Limitations {
		if strings.Contains(l.Message, "stripe-two") {
			t.Errorf("a restricted view names a hidden claimer: %+v", l)
		}
	}
	if q.snap.Externals["stripe"].Claim != nil {
		t.Error("a conflict links nothing in any view")
	}
}

func TestUnownedExternalCountsHiddenConsumers(t *testing.T) {
	q := inShop(claimFleet(t, nil, checkoutStripe(), svc("vault", "secret", extDep("stripe"))))
	items := unownedExternals(t, q)
	if len(items) != 1 || items[0].Label != "Stripe: 2 services use it" || items[0].Reason != "checkout and 1 you cannot see use Stripe, and no declared contract claims it" ||
		strings.Contains(items[0].Reason, "vault") {
		t.Errorf("items = %+v", items)
	}
}

func TestUnownedExternalAllConsumersHiddenRaisesNothing(t *testing.T) {
	q := claimFleet(t, nil, svc("vault", "secret", extDep("stripe")))
	if items := unownedExternals(t, inShop(q)); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestUnownedExternalRankedByConsumers(t *testing.T) {
	var svcs []func() (RawRevision, RawTarget)
	for _, s := range []struct {
		name string
		deps []string
	}{{"a", []string{"twilio", "sendgrid", "stripe"}}, {"b", []string{"twilio", "sendgrid"}}, {"c", []string{"twilio"}}, {"d", []string{"twilio"}}} {
		var deps []contract.Dependency
		for _, d := range s.deps {
			deps = append(deps, extDep(d))
		}
		svcs = append(svcs, svc(s.name, "shop", deps...))
	}
	var keys []string
	for _, it := range unownedExternals(t, claimFleet(t, nil, svcs...)) {
		keys = append(keys, it.Entity.Key)
	}
	if !slices.Equal(keys, []string{"twilio", "sendgrid", "stripe"}) {
		t.Errorf("order = %v", keys)
	}
}

func TestSortAttentionRankOnlyOrdersWithinCategory(t *testing.T) {
	items := []AttentionItem{
		{Severity: severityInfo, Category: categoryUnowned, Label: "a"},
		{Severity: severityInfo, Category: categoryUnowned, Label: "z", rank: 3},
		{Severity: severityInfo, Category: categoryKnowledge, Label: "k"},
		{Severity: severityWarning, Category: categoryUnowned, Label: "w"},
		{Severity: severityInfo, Category: categoryUnowned, Label: "y", rank: 1},
	}
	sortAttention(items)
	var got []string
	for _, it := range items {
		got = append(got, it.Label)
	}
	if !slices.Equal(got, []string{"w", "k", "z", "y", "a"}) {
		t.Errorf("order = %v", got)
	}
}

func TestUnownedExternalSkipsOtherEgressAndNoConsumers(t *testing.T) {
	q := &Query{snap: &FleetSnapshot{Externals: map[ExternalKey]*ExternalRecord{
		OtherEgress: {Key: OtherEgress, Class: ClassOtherEgress, ConsumersTotal: 3},
		"legacy":    {Key: "legacy", Class: "legacy"},
	}}}
	if items := q.unownedExternalAttention(); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
	// A per-service answer holds no external.
	q.snap.Externals["stripe"] = &ExternalRecord{Key: "stripe", ConsumersTotal: 1}
	if len(q.unownedExternalAttention()) != 1 || len(q.attentionFor(map[ServiceKey]bool{"checkout": true})) != 0 {
		t.Error("scope")
	}
}

func TestUnclaimedExternalRoutingIsNoOwner(t *testing.T) {
	q := claimFleet(t, nil, checkoutStripe())
	items := unownedExternals(t, q)
	if len(items) != 1 || items[0].Entity.Owner != "" || items[0].Service != "" {
		t.Fatalf("items = %+v", items)
	}
	// The routing phrase names no owner, membership or approver: the
	// ownership tally counts services only, and no owner key comes from it.
	ov := q.Overview()
	if o := ov.Summary.Ownership; o.Unowned != 0 || o.Consistent != 1 {
		t.Errorf("ownership = %+v", o)
	}
	for _, s := range q.snap.Services {
		if b, key := q.ownershipBucket(s); strings.Contains(key, "approver") || b == "" {
			t.Errorf("%s owner %s %s", s.Key, b, key)
		}
	}
}

func TestOverviewTileCountsUnownedExternals(t *testing.T) {
	q := claimFleet(t, []RawRevision{claimRev(claimOf{service: "orphan"})}, checkoutStripe(), svc("sms", "shop", extDep("twilio")))
	ov := q.Overview()
	n := len(attentionOf(t, q, AttentionFilter{Category: categoryUnowned}).Items)
	i := slices.IndexFunc(ov.EntryPoints, func(e EntryPoint) bool { return e.Category == categoryUnowned })
	if ov.Summary.UnownedExternals != 2 || i < 0 || ov.EntryPoints[i].Count != n || ov.EntryPoints[i].Label != "Services and external systems with no owner" {
		t.Errorf("summary %d, tile %+v, list %d", ov.Summary.UnownedExternals, ov.EntryPoints, n)
	}
}

func TestClaimActionRefusesNonIdentity(t *testing.T) {
	for _, id := range []string{"Stripe", "stripe x", "", string(OtherEgress), "a;rm -rf"} {
		if a := claimAction(id); a != nil {
			t.Errorf("claimAction(%q) = %+v", id, a)
		}
	}
	if a := claimAction("aws/rds/123456789012/eu-west-1/orders-db"); a == nil || a.CLI != "pacto claim aws/rds/123456789012/eu-west-1/orders-db --owner <team>" {
		t.Errorf("action = %+v", a)
	}
}

func TestInferredProviderItemUnclaimedClaims(t *testing.T) {
	items := inferredItems(t, claimFleet(t, []RawRevision{stripeProvider()}, svc("pay", "shop", pinStripe("^2023"))))
	if len(items) != 1 || !reflect.DeepEqual(items[0].NextAction, claimAction("stripe")) ||
		items[0].NextStep != "Claim stripe with pacto claim stripe --owner <team>, then point the ranges at the claimed contract" {
		t.Errorf("items = %+v", items)
	}
}

func TestInferredProviderItemClaimedPointsAtRef(t *testing.T) {
	items := inferredItems(t, claimFleet(t, []RawRevision{stripeProvider(), stripeClaim()}, svc("pay", "shop", pinStripe("^2023"))))
	if len(items) != 1 || !reflect.DeepEqual(items[0].NextAction, editContract()) ||
		items[0].NextStep != "Point the ranges at ghcr.io/acme/stripe, the claimed contract of stripe" {
		t.Errorf("items = %+v", items)
	}
}

func TestInferredProviderItemHiddenClaimNoClaimAction(t *testing.T) {
	q := inShop(claimFleet(t, []RawRevision{stripeProvider(), stripeClaim()}, svc("pay", "shop", pinStripe("^2023"))))
	items := inferredItems(t, q)
	if len(items) != 1 || items[0].NextAction != nil || items[0].NextStep != "Claimed by a service you cannot see; ask the installation's default approvers" {
		t.Errorf("items = %+v", items)
	}
}

func TestClaimedExternalInferredPinStaysUnknown(t *testing.T) {
	q := claimFleet(t, []RawRevision{stripeProvider(), stripeClaim()}, svc("pay", "shop", pinStripe("^2023")))
	rel := depEdge(t, q, "pay", "stripe-pinned")
	if rel.RangeStatus != RangeUnknown || rel.RangeReason != RangeReasonInferredProvider || rel.ToService != "" || rel.ToExternal != "stripe" {
		t.Errorf("edge = %+v", rel)
	}
}

func TestClaimedExternalDeclaredPinJudged(t *testing.T) {
	pin := contract.Dependency{Name: "stripe", Ref: "oci://ghcr.io/acme/stripe", Compatibility: "^0.1.0"}
	q := claimFleet(t, []RawRevision{stripeProvider(), stripeClaim()}, svc("pay", "shop", pin))
	rel := depEdge(t, q, "pay", "stripe")
	if rel.ToService != NewServiceKey("stripe") || rel.RangeReason == RangeReasonInferredProvider || rel.RangeStatus == "" || rel.RangeStatus == RangeUnknown {
		t.Errorf("edge = %+v", rel)
	}
}

func TestExplainExternalClaimReasons(t *testing.T) {
	reason := func(q *Query, code string) string {
		t.Helper()
		res, err := q.Explain("external:stripe")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Reasons {
			if r.Code == code {
				return r.Message
			}
		}
		return ""
	}
	if got := reason(claimFleet(t, nil, checkoutStripe()), ReasonExternalUnclaimed); got != "Unclaimed: "+routedToApprovers {
		t.Errorf("unclaimed = %q", got)
	}
	q := claimFleet(t, []RawRevision{stripeClaim()}, checkoutStripe())
	if got := reason(q, ReasonExternalClaimed); got != "Claimed by stripe 0.1.0 (claim established by "+anaSubject+" at 0.1.0)" {
		t.Errorf("claimed = %q", got)
	}
	if got := reason(inShop(q), ReasonExternalClaimed); got != "Claimed by a service you may not read" {
		t.Errorf("hidden = %q", got)
	}
}

func TestClaimNamesTheRevisionThatEstablishedIt(t *testing.T) {
	ci := claimRev(claimOf{version: "0.3.0", external: "stripe", owner: "team-platform", prov: ciSigned()})
	q := claimFleet(t, []RawRevision{stripeClaim(), ci}, checkoutStripe())
	c := q.snap.Externals["stripe"].Claim
	if c == nil || c.Version != "0.3.0" || c.DeclaredVersion != "0.1.0" {
		t.Fatalf("claim = %+v", c)
	}
	if got := claimReason(q.snap.Externals["stripe"], false).Message; got != "Claimed by stripe 0.3.0 (claim established by "+anaSubject+" at 0.1.0)" {
		t.Errorf("reason = %q", got)
	}
}

func TestClaimOwnerKeyOfDRIOnlyClaimer(t *testing.T) {
	dri := claimRev(claimOf{version: "0.2.0", external: "stripe", prov: ciSigned()})
	dri.Bundle.Contract.Service.Owner = contract.Owner{DRI: "alice"}
	q := claimFleet(t, []RawRevision{stripeClaim(), dri}, checkoutStripe())
	if c := q.snap.Externals["stripe"].Claim; c == nil || c.Owner != "alice" || c.OwnerKey != "dri:alice" {
		t.Errorf("claim = %+v, want owner alice keyed dri:alice", c)
	}
}

func TestClaimOwnerRuleVerdictGatesTheClaim(t *testing.T) {
	for code, why := range map[string]string{"OWNER_SIGNER_NOT_ALLOWED": "the owner rule refused its owner", ownerPolicyUnverified: "the owner rule could not check its owner"} {
		prov := anaSigned()
		prov.OwnerRight = &OwnerRightCheck{Code: code}
		refused := claimRev(claimOf{service: "stripe-mine", external: "stripe", from: "external:stripe", owner: "team-m", prov: prov})
		q := claimFleet(t, []RawRevision{refused, stripeClaim()}, checkoutStripe())
		if c := q.snap.Externals["stripe"].Claim; c == nil || c.Service != NewServiceKey("stripe") || q.snap.Externals["stripe"].Claimants != 0 {
			t.Errorf("%s: claim = %+v, want the valid claim linked", code, c)
		}
		if len(limitationWith(q, "stripe-mine claims external stripe, but "+why)) != 1 || len(limitationWith(q, "is claimed by")) != 0 {
			t.Errorf("%s: limitations = %+v", code, q.snap.Limitations)
		}
	}
}

func TestClaimByTwoServicesSaysSo(t *testing.T) {
	other := claimRev(claimOf{service: "stripe-two", external: "stripe", from: "external:stripe", owner: "payments", prov: anaSigned()})
	q := claimFleet(t, []RawRevision{stripeProvider(), stripeClaim(), other}, checkoutStripe(), svc("pay", "shop", pinStripe("^2023")))
	for _, view := range []*Query{q, inShop(q)} {
		rec := view.snap.Externals["stripe"]
		if rec.Claimants != 2 {
			t.Fatalf("claimants = %d", rec.Claimants)
		}
		items := unownedExternals(t, view)
		if len(items) != 1 || !strings.HasSuffix(items[0].Reason, "and 2 services claim it, so no claim counts") || items[0].NextStep != claimantsStep || items[0].NextAction != nil {
			t.Errorf("items = %+v", items)
		}
		if got := claimReason(rec, false).Message; got != "Claimed by 2 services, so no claim counts until all but one are retracted" {
			t.Errorf("reason = %q", got)
		}
		if rec.InferredNextStep("stripe") != claimantsStep {
			t.Errorf("inferred step = %q", rec.InferredNextStep("stripe"))
		}
	}
	for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
		if it.Code == "INFERRED_PROVIDER" && (it.NextAction != nil || it.NextStep != claimantsStep) {
			t.Errorf("inferred provider item = %+v", it)
		}
	}
}

func TestOwnerlessVisibleClaimIsClaimedAgainUnderItsName(t *testing.T) {
	ownerless := claimRev(claimOf{service: "payments-stripe", external: "stripe", from: "external:stripe", prov: anaSigned()})
	q := claimFleet(t, []RawRevision{ownerless}, checkoutStripe())
	items := unownedExternals(t, q)
	want := "pacto claim stripe --owner <team> --name payments-stripe"
	if len(items) != 1 || items[0].NextAction == nil || items[0].NextAction.CLI != want || !strings.Contains(items[0].NextStep, want) {
		t.Errorf("items = %+v", items)
	}
	// A hidden ownerless claimer invites no second claim.
	items = unownedExternals(t, inShop(q))
	if len(items) != 1 || items[0].NextAction != nil || items[0].NextStep != hiddenClaimStep {
		t.Errorf("hidden items = %+v", items)
	}
}

func TestClaimOfConflictingOwnersShowsNoOwner(t *testing.T) {
	a := claimRev(claimOf{external: "stripe", from: "external:stripe", owner: "team-a", prov: anaSigned()})
	b := claimRev(claimOf{external: "stripe", from: "external:stripe", owner: "team-b", prov: anaSigned()})
	b.Digest, b.ResolvedRef = "sha256:stripe-0.1.0-b", "ghcr.io/acme/stripe@sha256:stripe-0.1.0-b"
	q := claimFleet(t, []RawRevision{a, b}, checkoutStripe())
	if bk, _ := q.ownershipBucket(q.snap.Services[NewServiceKey("stripe")]); bk != OwnershipConflicting {
		t.Fatalf("fixture is %s, not conflicting", bk)
	}
	if c := q.snap.Externals["stripe"].Claim; c == nil || c.Owner != "" || c.OwnerKey != "" {
		t.Errorf("claim = %+v, want no owner shown", c)
	}
	if items := unownedExternals(t, q); len(items) != 0 {
		t.Errorf("conflicting owners are OWNER_CONFLICT, not unowned: %+v", items)
	}
}

func TestUnclaimedWithIncompleteSourcesIsUnknown(t *testing.T) {
	q := claimFleet(t, nil, checkoutStripe())
	q.snap.Sources[0].Status = SourceUnavailable
	items := unownedExternals(t, q)
	if len(items) != 1 || !strings.HasSuffix(items[0].Reason, "no claim of it was read; some sources are incomplete, so it may be claimed") {
		t.Errorf("items = %+v", items)
	}
	if got := claimReason(q.snap.Externals["stripe"], true).Message; !strings.HasPrefix(got, "No claim was read, and some sources are incomplete") {
		t.Errorf("reason = %q", got)
	}
}

func TestRestrictKeepsClaimDeclarationSource(t *testing.T) {
	visible := func() (RawRevision, RawTarget) {
		r, tg := infSvc("stripe", "shop")
		return stripeClaim(), RawTarget{Scope: tg.Scope, Kind: tg.Kind, Name: "stripe", Service: "stripe", Digest: r.Digest, Compliance: StatusCompliant}
	}
	full := claimFleet(t, nil, checkoutStripe(), visible)
	for _, r := range full.snap.Revisions {
		if declarationSourceHidden(full.snap, r, func(string, string, string) bool { return true }) {
			t.Errorf("a claim reads no workload, so nothing hides its source: %s", r.Service)
		}
	}
	q := inShop(full)
	for _, r := range q.snap.Revisions {
		if r.Service == "stripe" && (r.declarationHidden || r.Record == nil || r.Record.Declaration.InferredFrom != "external:stripe") {
			t.Errorf("restricted claimed revision = %+v", r)
		}
	}
}
