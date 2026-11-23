package fleet

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func intp(n int) *int { return &n }

// clientSnap is a snapshot whose revision r's inferred record names fields.
func clientSnap(fields ...contract.Inferred) *FleetSnapshot {
	return &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"r":    {Record: &contract.InferredRecord{Fields: fields}},
		"none": {},
	}}
}

func clientField(id string, c *contract.InferredClient) contract.Inferred {
	return contract.Inferred{External: &contract.InferredExternal{Identity: id, Client: c}}
}

func TestExternalClientOf(t *testing.T) {
	snap := clientSnap(contract.Inferred{}, clientField("twilio", &contract.InferredClient{SDK: "twilio-go", Major: intp(1)}),
		clientField("stripe", &contract.InferredClient{SDK: "stripe-go", Major: intp(76)}))
	got := ExternalClientOf(snap, "r", "stripe")
	if got == nil || *got != (ExternalClient{SDK: "stripe-go", Major: 76, Confidence: "possible", Revision: "r"}) {
		t.Errorf("client = %+v", got)
	}
	for _, c := range []struct {
		rk  RevisionKey
		key ExternalKey
	}{{"", "stripe"}, {"missing", "stripe"}, {"none", "stripe"}, {"r", "adyen"}} {
		if got := ExternalClientOf(snap, c.rk, c.key); got != nil {
			t.Errorf("%s %s = %+v, want nil", c.rk, c.key, got)
		}
	}
	if got := ExternalClientOf(clientSnap(clientField("stripe", nil)), "r", "stripe"); got != nil {
		t.Errorf("no client = %+v", got)
	}
}

func TestExternalClientOfNoMajorIsNil(t *testing.T) {
	if got := ExternalClientOf(clientSnap(clientField("stripe", &contract.InferredClient{SDK: "stripe-go"})), "r", "stripe"); got != nil {
		t.Errorf("a missing major is absent, never zero: %+v", got)
	}
}

func link(fill string) string {
	return "https://docs.stripe.com/openapi.json@sha256:" + strings.Repeat(fill, 64)
}

// changeRev is a stripe inferred revision at version with surface values
// clients and hosts, linking openapi (none when empty), signed when signed.
func changeRev(version, openapi string, signed bool, asOf time.Time, clients, hosts []string) *ContractRevision {
	s := &contract.InferredSurface{Identity: "stripe", AsOf: asOf.UTC().Format(time.RFC3339)}
	for _, c := range clients {
		s.Clients = append(s.Clients, contract.SurfaceValue{Value: c})
	}
	for _, h := range hosts {
		s.Hosts = append(s.Hosts, contract.SurfaceValue{Value: h})
	}
	rev := &ContractRevision{Version: version, Contract: &contract.Contract{}, Record: &contract.InferredRecord{Surface: s}}
	if openapi != "" {
		rev.Contract.Metadata = map[string]any{ExternalOpenAPIMetadataKey: openapi}
	}
	if signed {
		rev.Provenance = operatorSig()
	}
	return rev
}

func TestProviderChangeClientsAndOpenAPI(t *testing.T) {
	at := fixedNow()
	older := changeRev("0.0.1", link("a"), true, at, []string{"stripe-go/76"}, []string{"api.stripe.com:443"})
	newer := changeRev("0.0.2", link("b"), true, at, []string{"stripe-go/77"}, []string{"api.stripe.com:443", "files.stripe.com:443"})
	c := providerChange(newer, older)
	want := ExternalProviderChange{From: "0.0.1", To: "0.0.2", At: "2026-07-29T12:00:00Z", Checked: true,
		Added: []string{"hosts files.stripe.com:443", "clients stripe-go/77"}, Removed: []string{"clients stripe-go/76"},
		OpenAPIFrom: link("a"), OpenAPITo: link("b")}
	if b, w := mustJSON(t, c), mustJSON(t, want); b != w {
		t.Errorf("change = %s, want %s", b, w)
	}
	if providerChange(nil, older) != nil || providerChange(newer, nil) != nil {
		t.Error("a missing revision has no change")
	}
	// A revision without a record changes by what the other holds.
	if c := providerChange(newer, &ContractRevision{Version: "0.0.1", Contract: &contract.Contract{}}); c == nil || len(c.Added) != 3 || c.OpenAPIFrom != "" || c.OpenAPITo != link("b") {
		t.Errorf("from nothing = %+v", c)
	}
}

func TestProviderChangeUncheckedWhenOlderUnsigned(t *testing.T) {
	c := providerChange(changeRev("0.0.2", link("b"), true, fixedNow(), nil, nil), changeRev("0.0.1", link("a"), false, fixedNow(), nil, nil))
	if c == nil || c.Checked {
		t.Errorf("change = %+v, want unchecked", c)
	}
}

// A newer unsigned revision beside an older signed one changes from the
// older to the newer, never in trust order.
func TestNewestChangeChronologicalWhenMixed(t *testing.T) {
	older := changeRev("0.0.4", "", true, fixedNow(), []string{"stripe-go/76"}, nil)
	newer := changeRev("0.0.5", "", false, fixedNow(), []string{"stripe-go/77"}, nil)
	c := newestChange([]*ContractRevision{newer, older})
	if c == nil || c.From != "0.0.4" || c.To != "0.0.5" || c.Checked ||
		!slices.Equal(c.Added, []string{"clients stripe-go/77"}) || !slices.Equal(c.Removed, []string{"clients stripe-go/76"}) {
		t.Errorf("change = %+v, want unchecked 0.0.4 -> 0.0.5 adding stripe-go/77", c)
	}
	// Two signed revisions win over a newer unsigned one.
	oldest := changeRev("0.0.3", "", true, fixedNow(), []string{"stripe-go/75"}, nil)
	if c := newestChange([]*ContractRevision{newer, older, oldest}); c == nil || c.From != "0.0.3" || c.To != "0.0.4" || !c.Checked {
		t.Errorf("signed pair change = %+v, want checked 0.0.3 -> 0.0.4", c)
	}
	if newestChange([]*ContractRevision{newer}) != nil {
		t.Error("one revision has no change")
	}
}

func TestProviderChangeNilWhenEqual(t *testing.T) {
	older := changeRev("0.0.1", link("a"), true, fixedNow(), []string{"stripe-go/76"}, nil)
	newer := changeRev("0.0.2", link("a"), true, fixedNow().Add(time.Hour), []string{"stripe-go/76"}, nil)
	if c := providerChange(newer, older); c != nil {
		t.Errorf("change = %+v, want nil", c)
	}
}

func TestProviderChangeComparesDigestOnly(t *testing.T) {
	older := changeRev("0.0.1", "oci://ghcr.io/acme/specs/stripe@sha256:"+strings.Repeat("a", 64), true, fixedNow(), nil, nil)
	newer := changeRev("0.0.2", link("a"), true, fixedNow(), nil, nil)
	if c := providerChange(newer, older); c != nil {
		t.Errorf("a re-hosted document with the same digest is no change: %+v", c)
	}
}

// changeFleet builds a fleet of two stripe inferred revisions, 0.0.1 and
// 0.0.2, whose 0.0.2 surface is age old, and the consumers svcs.
func changeFleet(t *testing.T, signed bool, age time.Duration, links [2]string, clients, hosts [2][]string, svcs ...func() (RawRevision, RawTarget)) *Query {
	t.Helper()
	var revs []RawRevision
	for i, v := range []string{"0.0.1", "0.0.2"} {
		s := infSurface()
		s.AsOf = fixedNow().Add(-age).Format(time.RFC3339)
		s.Hosts = nil
		for _, c := range clients[i] {
			s.Clients = append(s.Clients, contract.SurfaceValue{Value: c, Sources: []string{"k8s"}})
		}
		for _, h := range hosts[i] {
			s.Hosts = append(s.Hosts, contract.SurfaceValue{Value: h, Sources: []string{"k8s"}})
		}
		var prov *BundleProvenance
		if signed {
			prov = operatorSig()
		}
		r := infRev("stripe", v, infRepo, "stripe", prov, s)
		if links[i] != "" {
			r.Bundle.Contract.Metadata = map[string]any{ExternalOpenAPIMetadataKey: links[i]}
		}
		revs = append(revs, r)
	}
	return infFleet(t, revs, svcs...)
}

// changedItems is q's EXTERNAL_CHANGED attention.
func changedItems(t *testing.T, q *Query) []AttentionItem {
	t.Helper()
	var out []AttentionItem
	for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
		if it.Code == AttentionExternalChanged {
			out = append(out, it)
		}
	}
	return out
}

var (
	digestMove  = [2]string{link("a"), link("b")}
	sameClients = [2][]string{{"stripe-go/76"}, {"stripe-go/76"}}
	movedClient = [2][]string{{"stripe-go/76"}, {"stripe-go/77"}}
)

func TestExternalChangedAttentionWindow(t *testing.T) {
	for _, c := range []struct {
		age  time.Duration
		want int
	}{{6 * 24 * time.Hour, 1}, {8 * 24 * time.Hour, 0}} {
		q := changeFleet(t, true, c.age, digestMove, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe("")))
		if got := len(changedItems(t, q)); got != c.want {
			t.Errorf("age %s: items = %d, want %d", c.age, got, c.want)
		}
	}
	// An unparsable time raises nothing.
	q := changeFleet(t, true, time.Hour, digestMove, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe("")))
	q.snap.Externals["stripe"].Provider.Change.At = "yesterday"
	if items := changedItems(t, q); len(items) != 0 {
		t.Errorf("items = %+v", items)
	}
}

func TestExternalChangedSeverity(t *testing.T) {
	q := changeFleet(t, true, time.Hour, digestMove, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe("")))
	items := changedItems(t, q)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	it := items[0]
	if it.Severity != severityWarning || it.Category != categoryKnowledge || it.Entity.Kind != KindExternal || it.Entity.Key != "stripe" ||
		it.Service != "" || it.NextAction != nil || it.NextStep != "pacto impact --changed external:stripe" ||
		it.Label != "stripe's linked OpenAPI document moved: 1 consumer to review" ||
		it.Reason != "the organization catalog's linked OpenAPI digest for stripe moved from sha256:"+strings.Repeat("a", 64)+" to sha256:"+strings.Repeat("b", 64)+" between 0.0.1 and 0.0.2" {
		t.Errorf("digest item = %+v", it)
	}
}

func TestExternalChangedClientsSeverity(t *testing.T) {
	items := changedItems(t, changeFleet(t, true, time.Hour, [2]string{}, movedClient, [2][]string{}, svc("checkout", "shop", pinStripe(""))))
	if len(items) != 1 || items[0].Severity != severityInfo || items[0].Label != "a stripe client moved to stripe-go/77" || strings.Contains(items[0].Label, "review") {
		t.Errorf("clients items = %+v", items)
	}
	items = changedItems(t, changeFleet(t, true, time.Hour, [2]string{}, [2][]string{{"stripe-go/76"}, nil}, [2][]string{}, svc("checkout", "shop", pinStripe(""))))
	if len(items) != 1 || items[0].Severity != severityInfo || items[0].Label != "a stripe client is no longer used: stripe-go/76" {
		t.Errorf("client removed items = %+v", items)
	}
}

func TestExternalChangedLinkAddedIsInfo(t *testing.T) {
	added := changedItems(t, changeFleet(t, true, time.Hour, [2]string{"", link("b")}, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe(""))))
	removed := changedItems(t, changeFleet(t, true, time.Hour, [2]string{link("a"), ""}, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe(""))))
	if len(added) != 1 || added[0].Severity != severityInfo || added[0].Label != "the organization catalog now links stripe's OpenAPI document" {
		t.Errorf("added = %+v", added)
	}
	if len(removed) != 1 || removed[0].Severity != severityInfo || removed[0].Label != "the organization catalog no longer links stripe's OpenAPI document" {
		t.Errorf("removed = %+v", removed)
	}
}

func TestExternalChangedIgnoresHostsAndPorts(t *testing.T) {
	q := changeFleet(t, true, time.Hour, [2]string{}, sameClients, [2][]string{{"api.stripe.com:443"}, {"api.stripe.com:8443"}}, svc("checkout", "shop", pinStripe("")))
	if c := q.snap.Externals["stripe"].Provider.Change; c == nil || !c.Checked {
		t.Fatalf("change = %+v, want a checked host change", c)
	}
	if items := changedItems(t, q); len(items) != 0 {
		t.Errorf("a host move is the organization's own call: %+v", items)
	}
}

func TestExternalChangedNeedsCheckedChange(t *testing.T) {
	q := changeFleet(t, false, time.Hour, digestMove, movedClient, [2][]string{}, svc("checkout", "shop", pinStripe("")))
	c := q.snap.Externals["stripe"].Provider.Change
	if c == nil || c.Checked || c.From != "0.0.1" || c.To != "0.0.2" {
		t.Errorf("change = %+v, want unchecked 0.0.1 -> 0.0.2", c)
	}
	if items := changedItems(t, q); len(items) != 0 {
		t.Errorf("an unsigned pair raises nothing: %+v", items)
	}
}

func TestExternalChangedCountMatchesImpact(t *testing.T) {
	q := changeFleet(t, true, time.Hour, digestMove, sameClients, [2][]string{},
		svc("checkout", "shop", pinStripe("")), svc("billing", "shop", pinStripe("")), svc("vault", "secret", pinStripe("")))
	for _, v := range []*Query{q, q.Restrict(anyCluster(shopView), anyClusterKind(nil))} {
		rec := v.snap.Externals["stripe"]
		items := changedItems(t, v)
		if want := strconv.Itoa(rec.ConsumersTotal+rec.HiddenConsumers) + " consumers to review"; len(items) != 1 || !strings.HasSuffix(items[0].Label, want) {
			t.Errorf("items = %+v, want %q", items, want)
		}
	}
}

func TestExternalChangedNamesNoHiddenConsumer(t *testing.T) {
	q := changeFleet(t, true, time.Hour, digestMove, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe("")), svc("vault", "secret", pinStripe("")))
	items := changedItems(t, q.Restrict(anyCluster(shopView), anyClusterKind(nil)))
	if len(items) != 1 || strings.Contains(mustJSON(t, items), "vault") {
		t.Errorf("items = %+v", items)
	}
}

func TestRestrictedClientOnlyChangeRaisesNothing(t *testing.T) {
	q := changeFleet(t, true, time.Hour, [2]string{}, movedClient, [2][]string{}, svc("checkout", "shop", pinStripe("")), svc("vault", "secret", pinStripe("")))
	if len(changedItems(t, q)) != 1 {
		t.Fatal("the full view raises the clients item")
	}
	if items := changedItems(t, q.Restrict(anyCluster(shopView), anyClusterKind(nil))); len(items) != 0 {
		t.Errorf("a clients move may be a hidden consumer's: %+v", items)
	}
}

func TestRestrictDropsProviderChangeValues(t *testing.T) {
	q := changeFleet(t, true, time.Hour, digestMove, movedClient, [2][]string{}, svc("checkout", "shop", pinStripe("")), svc("vault", "secret", pinStripe("")))
	v, err := q.Restrict(anyCluster(shopView), anyClusterKind(nil)).GetExternal("external:stripe")
	if err != nil {
		t.Fatal(err)
	}
	p := v.External.Provider
	if c := p.Change; c == nil || len(c.Added)+len(c.Removed) != 0 || !c.Checked || c.From != "0.0.1" || c.To != "0.0.2" || c.At == "" ||
		c.OpenAPIFrom != link("a") || c.OpenAPITo != link("b") || !p.SurfaceHidden {
		t.Errorf("restricted change = %+v (surfaceHidden %v)", c, p.SurfaceHidden)
	}
	if c := q.snap.Externals["stripe"].Provider.Change; len(c.Added) == 0 {
		t.Error("restriction must never mutate the base snapshot")
	}
	// A change of the link alone keeps nothing to drop.
	q = changeFleet(t, true, time.Hour, digestMove, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe("")))
	if p := withoutSurface(q.snap.Externals["stripe"].Provider); p.Change != q.snap.Externals["stripe"].Provider.Change {
		t.Errorf("link-only change = %+v", p.Change)
	}
}

func TestScopedAttentionSkipsExternalChanged(t *testing.T) {
	q := changeFleet(t, true, time.Hour, digestMove, sameClients, [2][]string{}, svc("checkout", "shop", pinStripe("")))
	has := func(items []AttentionItem) bool {
		for _, it := range items {
			if it.Code == AttentionExternalChanged {
				return true
			}
		}
		return false
	}
	if has(q.attentionFor(map[ServiceKey]bool{NewServiceKey("checkout"): true})) {
		t.Error("a scoped page carries no external item")
	}
	if !has(q.collectAttention()) {
		t.Error("the full list carries it")
	}
}

func TestKnowledgeTileCountsExternalChanged(t *testing.T) {
	q := changeFleet(t, true, time.Hour, digestMove, sameClients, [2][]string{})
	n := 0
	for _, it := range attentionOf(t, q, AttentionFilter{}).Items {
		if it.Category == categoryKnowledge {
			n++
		}
	}
	if got := q.Overview().Summary.KnowledgeItems; got != n || n != 1 {
		t.Errorf("tile = %d, list = %d", got, n)
	}
}

func TestExternalRef(t *testing.T) {
	snap := &FleetSnapshot{Externals: map[ExternalKey]*ExternalRecord{"stripe": {Key: "stripe", Label: "Stripe", Class: "vendor-api"}}}
	if got := ExternalRef(snap, "stripe"); got != (EntityRef{Kind: KindExternal, Key: "stripe", Label: "Stripe", Secondary: "vendor-api", Status: StatusUnknown}) {
		t.Errorf("record ref %+v", got)
	}
	if got := ExternalRef(snap, "adyen"); got != (EntityRef{Kind: KindExternal, Key: "adyen", Label: "adyen", Status: StatusUnknown}) {
		t.Errorf("bare ref %+v", got)
	}
}
