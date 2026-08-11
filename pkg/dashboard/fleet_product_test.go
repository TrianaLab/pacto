package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// validDigest returns a syntactically valid lower-case sha256 content digest for
// tests, built programmatically so no 64-char body literal drifts. Distinct fill
// characters yield distinct digests (and thus distinct revision keys).
func validDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64/len(fill)+1)[:64]
}

func firstRevKey(t *testing.T, q *fleet.Query) string {
	t.Helper()
	v, err := q.GetService("payment-service")
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	return string(v.Revisions[0].Key)
}

// twoDomainDashboardQuery builds a fleet with payment-service in two domains, so
// a bare-name lookup is ambiguous at the product API boundary.
func twoDomainDashboardQuery(t *testing.T) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("a", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: newPaymentBundle(), Domain: "domain-a", ResolvedRef: "oci://a/payment-service@" + validDigest("a"), Digest: validDigest("a"),
		}}}),
		fleet.NewMemorySource("b", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: newPaymentBundle(), Domain: "domain-b", ResolvedRef: "oci://b/payment-service@" + validDigest("b"), Digest: validDigest("b"),
		}}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestProductEndpoints_Serve(t *testing.T) {
	q := demoFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	// Overview.
	var ov fleet.Overview
	getJSON(t, base+"/api/fleet/overview", http.StatusOK, &ov)
	if ov.Meta.SchemaVersion != fleet.ProductSchemaVersion {
		t.Errorf("overview schema = %q", ov.Meta.SchemaVersion)
	}

	// Entities search + comma-separated kinds + bad filter.
	var ents fleet.EntityList
	getJSON(t, base+"/api/fleet/entities?text=payment&kinds=service,", http.StatusOK, &ents)
	if ents.Count == 0 {
		t.Error("expected a service entity hit")
	}
	expectStatus(t, base+"/api/fleet/entities?status=Bogus", http.StatusUnprocessableEntity)

	// Entity detail: found, missing (404), unknown kind (422).
	var det fleet.EntityDetail
	getJSON(t, base+"/api/fleet/entities/service?key=payment-service", http.StatusOK, &det)
	if det.Entity.Kind != fleet.KindService {
		t.Errorf("detail kind = %q", det.Entity.Kind)
	}
	expectStatus(t, base+"/api/fleet/entities/service?key=nope", http.StatusNotFound)
	expectStatus(t, base+"/api/fleet/entities/weird?key=x", http.StatusUnprocessableEntity)

	// Neighborhood: found, bad direction (422), missing focus (404), views param.
	var nb fleet.Neighborhood
	getJSON(t, base+"/api/fleet/neighborhood?kind=service&key=payment-service&views=expected,observed", http.StatusOK, &nb)
	if nb.FocusService.Key != "payment-service" || nb.RequestedFocus.Key != "payment-service" {
		t.Errorf("neighborhood focus = %q / %q", nb.RequestedFocus.Key, nb.FocusService.Key)
	}
	expectStatus(t, base+"/api/fleet/neighborhood?kind=service&key=payment-service&direction=sideways", http.StatusUnprocessableEntity)
	expectStatus(t, base+"/api/fleet/neighborhood?kind=service&key=ghost", http.StatusNotFound)

	// Attention with a filter.
	var att fleet.AttentionList
	getJSON(t, base+"/api/fleet/attention?category=non-compliant", http.StatusOK, &att)
	if att.Total == 0 {
		t.Error("expected the non-compliant target in attention")
	}
	// An invalid attention filter is a typed 422, never a silent empty result.
	expectStatus(t, base+"/api/fleet/attention?kind=bogus", http.StatusUnprocessableEntity)
}

// The inventory axes are only usable if they survive the wire: a query param the
// transport accepts but never forwards returns the UNFILTERED list, which reads as
// "every service is in this state". payment-service declares no owner and no
// readiness, so each axis must include it under one value and exclude it under
// another — a forwarding bug fails at least one half.
func TestProductEntities_OwnershipAndReadinessFiltersReachTheQuery(t *testing.T) {
	q := demoFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	for _, tc := range []struct {
		query string
		want  int
	}{
		{"kinds=service&ownership=unowned", 1},
		{"kinds=service&ownership=consistent", 0},
		{"kinds=revision&readiness=not-declared", 1},
		{"kinds=revision&readiness=passing", 0},
	} {
		var list fleet.EntityList
		getJSON(t, base+"/api/fleet/entities?"+tc.query, http.StatusOK, &list)
		if list.Total != tc.want {
			t.Errorf("%s matched %d, want %d", tc.query, list.Total, tc.want)
		}
	}

	// The aggregate travels with the list, over the whole match rather than the page.
	var all fleet.EntityList
	getJSON(t, base+"/api/fleet/entities?kinds=service", http.StatusOK, &all)
	if all.Aggregate.Matched != all.Total || all.Aggregate.Ownership.Unowned != 1 {
		t.Errorf("aggregate = %+v, want the whole match with one unowned service", all.Aggregate)
	}
	// A value outside the enum is a typed 422, never a silently unfiltered page.
	expectStatus(t, base+"/api/fleet/entities?ownership=orphaned", http.StatusUnprocessableEntity)
	expectStatus(t, base+"/api/fleet/entities?readiness=green", http.StatusUnprocessableEntity)
}

// TestProductNeighborhood_CombinedProvenanceOverWire proves the combined
// "declared+observed" provenance survives the actual Product Neighborhood HTTP
// transport (not just the in-process projection): the richFleet app->lib edge is both
// declared and observed, so under an expected+observed view the wire response must carry
// provenance "declared+observed" -- the value the OpenAPI enum now declares
// (requirement, reopen section 5).
func TestProductNeighborhood_CombinedProvenanceOverWire(t *testing.T) {
	q := richFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()
	var nb fleet.Neighborhood
	getJSON(t, base+"/api/fleet/neighborhood?kind=service&key=app&direction=dependencies&views=expected,observed", http.StatusOK, &nb)
	var edge *fleet.NeighborhoodEdge
	for i := range nb.Edges {
		if nb.Edges[i].From.Key == "app" && nb.Edges[i].To.Key == "lib" {
			edge = &nb.Edges[i]
		}
	}
	if edge == nil {
		t.Fatalf("app->lib edge missing from wire response (edges %+v)", nb.Edges)
	}
	want := fleet.ProvenanceDeclared + "+" + fleet.ProvenanceObserved
	if edge.Provenance != want {
		t.Errorf("wire app->lib provenance = %q, want %q", edge.Provenance, want)
	}
}

func TestProductEntityDetail_Ambiguous(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()
	// A bare ambiguous name is a 422 (qualify it); the domain-qualified key resolves.
	expectStatus(t, base+"/api/fleet/entities/service?key=payment-service", http.StatusUnprocessableEntity)
	var det fleet.EntityDetail
	getJSON(t, base+"/api/fleet/entities/service?key=domain-a%2Fpayment-service", http.StatusOK, &det)
	if det.Entity.Domain != "domain-a" {
		t.Errorf("qualified key domain = %q", det.Entity.Domain)
	}
}

func TestProductEndpoints_ProviderError(t *testing.T) {
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) {
		return nil, fmt.Errorf("source down")
	}, func(context.Context, string, string, bool) (*impact.Result, error) { return &impact.Result{}, nil })
	defer cancel()
	for _, path := range []string{
		"/api/fleet/overview",
		"/api/fleet/entities",
		"/api/fleet/entities/service?key=x",
		"/api/fleet/neighborhood?kind=service&key=x",
		"/api/fleet/attention",
		"/api/fleet/revisions/document?key=x&path=docs/overview.md",
	} {
		expectStatus(t, base+path, http.StatusServiceUnavailable)
	}
	// POST impact also needs the fleet snapshot for parity + ref resolution.
	postJSON(t, base+"/api/fleet/impact", impactRequest{FromRevisionKey: "a", ToRevisionKey: "b"}, http.StatusServiceUnavailable, nil)
}

func TestProductEndpoints_NotRegisteredWithoutProvider(t *testing.T) {
	base, cancel := startFleetTestServer(t, nil, nil)
	defer cancel()
	for _, path := range []string{"/api/fleet/overview", "/api/fleet/entities", "/api/fleet/attention"} {
		expectStatus(t, base+path, http.StatusNotFound)
	}
}

func TestProductImpactPost(t *testing.T) {
	q := demoFleetQuery(t)
	revKey := firstRevKey(t, q)
	snapID := q.SnapshotID()

	want := &impact.Result{
		SchemaVersion: impact.SchemaVersion, SnapshotID: snapID, Service: "payment-service", Classification: "BREAKING",
		Consumers: []impact.AffectedConsumer{{
			Service: "order-service", Depth: 1, Direct: true, Path: []string{"order-service", "payment-service"},
			Owner: "team-o", Confidence: impact.ConfidenceContractual, CompatibilityVerdict: "incompatible",
		}},
		Owners: []string{"team-o"}, ActiveTargets: []string{"prod/k8s/pay"},
	}
	var gotOld, gotNew string
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil },
		func(_ context.Context, oldRef, newRef string, _ bool) (*impact.Result, error) {
			gotOld, gotNew = oldRef, newRef
			return want, nil
		})
	defer cancel()

	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: revKey, ToRevisionKey: revKey, IncludeObserved: true}, http.StatusOK, &out)
	if gotOld == "" || gotNew == "" {
		t.Error("expected the revision keys to resolve to refs")
	}
	if !out.SnapshotMatch || out.SnapshotID != snapID {
		t.Errorf("snapshot parity wrong: match=%v id=%q", out.SnapshotMatch, out.SnapshotID)
	}
	if out.OldRevision == nil || out.NewRevision == nil {
		t.Fatalf("revisions must be present: %+v", out)
	}
	// gotOld/gotNew are the digest-pinned refs the provider received.
	if !fleet.IsDigestPinnedRef(gotOld) || !fleet.IsDigestPinnedRef(gotNew) {
		t.Errorf("provider must receive digest-pinned refs, got %q / %q", gotOld, gotNew)
	}
	assertImpactShapeAndNavigable(t, out)
}

// assertImpactShapeAndNavigable checks the single-consumer shape and that every
// reference the impact answer returns carries a canonical href.
func assertImpactShapeAndNavigable(t *testing.T, out ProductImpact) {
	t.Helper()
	if out.Consumers.Count != 1 || out.Consumers.Total != 1 || out.Owners.Count != 1 || out.ActiveTargets.Count != 1 {
		t.Fatalf("unexpected shape: %+v", out)
	}
	c := out.Consumers.Items[0]
	if len(c.Path) != 2 || c.PathTotal != 2 || c.PathTruncated {
		t.Errorf("consumer path = %+v, want 2 (untruncated)", c.Path)
	}
	refs := []ProductRef{out.Service, *out.OldRevision, *out.NewRevision, c.Service}
	refs = append(refs, c.Path...)
	refs = append(refs, out.Owners.Items...)
	refs = append(refs, out.ActiveTargets.Items...)
	if !hrefsNonEmpty(refs...) {
		t.Errorf("every reference must be navigable: %+v", refs)
	}
}

func hrefsNonEmpty(refs ...ProductRef) bool {
	for _, r := range refs {
		if r.Href == "" {
			return false
		}
	}
	return true
}

func TestProductImpactPost_Errors(t *testing.T) {
	q := demoFleetQuery(t)
	revKey := firstRevKey(t, q)

	// Snapshot mismatch is rejected rather than silently analyzing another state.
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil },
		func(context.Context, string, string, bool) (*impact.Result, error) { return &impact.Result{}, nil })
	defer cancel()
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: "stale-id", FromRevisionKey: revKey, ToRevisionKey: revKey}, http.StatusConflict, nil)

	// An unknown revision key is a 422.
	postJSON(t, base+"/api/fleet/impact", impactRequest{FromRevisionKey: "nope@x", ToRevisionKey: revKey}, http.StatusUnprocessableEntity, nil)
	postJSON(t, base+"/api/fleet/impact", impactRequest{FromRevisionKey: revKey, ToRevisionKey: "nope@x"}, http.StatusUnprocessableEntity, nil)

	// An impact-provider error is mapped like the raw-ref endpoint.
	baseErr, cancelErr := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil },
		func(context.Context, string, string, bool) (*impact.Result, error) {
			return nil, &oci.InvalidRefError{Ref: "bad", Err: fmt.Errorf("parse")}
		})
	defer cancelErr()
	postJSON(t, baseErr+"/api/fleet/impact", impactRequest{FromRevisionKey: revKey, ToRevisionKey: revKey}, http.StatusUnprocessableEntity, nil)
}

func TestProductImpactPost_NotRegisteredWithoutImpactProvider(t *testing.T) {
	q := demoFleetQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()
	postJSON(t, base+"/api/fleet/impact", impactRequest{FromRevisionKey: "a", ToRevisionKey: "b"}, http.StatusNotFound, nil)
}

func TestImmutableRef(t *testing.T) {
	d1 := validDigest("a")
	d2 := validDigest("b")
	// A canonical digest-pinned ResolvedRef consistent with the content digest is exact.
	if ref, err := immutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@" + d1, Digest: d1}); err != nil || ref != "oci://x/a@"+d1 {
		t.Errorf("digest-pinned ref must be exact: %q %v", ref, err)
	}
	// A canonical digest-pinned ref with no recorded digest is still exact (nothing to contradict).
	if _, err := immutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@" + d1}); err != nil {
		t.Errorf("digest-pinned ref without a recorded digest must be exact: %v", err)
	}
	// The dashboard bug: a digest-pinned ref with the oci:// scheme STRIPPED is a
	// local path to the resolver and must be rejected, not silently accepted.
	if _, err := immutableRef(&fleet.ContractRevision{ResolvedRef: "x/a@" + d1, Digest: d1}); err == nil {
		t.Error("a scheme-less digest ref must be rejected (it resolves as a local path)")
	}
	// A mutable tag ResolvedRef must NOT be accepted as exact merely for being non-empty.
	if _, err := immutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a:1.0", Digest: d1}); err == nil {
		t.Error("a tag ResolvedRef must be rejected as non-exact")
	}
	// A short/invalid digest body must be rejected even with the oci:// scheme.
	if _, err := immutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@sha256:abc", Digest: "sha256:abc"}); err == nil {
		t.Error("a malformed digest body must be rejected")
	}
	// A local path is mutable.
	if _, err := immutableRef(&fleet.ContractRevision{RequestedRef: "file:///abs"}); err == nil {
		t.Error("a local-path revision must be rejected")
	}
	// A revision with no ref at all is rejected.
	if _, err := immutableRef(&fleet.ContractRevision{}); err == nil {
		t.Error("a revision with no ref must error")
	}
	// An inconsistent digest-pinned ref (valid ref digest != valid content digest) is rejected.
	if _, err := immutableRef(&fleet.ContractRevision{ResolvedRef: "oci://x/a@" + d1, Digest: d2}); err == nil {
		t.Error("an inconsistent digest/ref must be rejected")
	}

	// The revision label comes from the record (service + version), not the key.
	l := revisionRefFromRecord(&fleet.ContractRevision{Key: "svc@d", Service: "svc", Version: "1.2.3", Digest: "d"})
	if l == nil || l.Label != "svc 1.2.3" {
		t.Errorf("revision link must be a record label: %+v", l)
	}
}

// TestImmutableRef_ExactMatchButNonRetrievable is requirement item 9: Product Impact
// by canonical identity rejects non-retrievable content EVEN WHEN a target's revision
// match is exact. The two dimensions are independent -- a runtime target carrying a
// trusted content digest with no canonical ref is an EXACT revision match (see
// fleet.TestMatchRevision_RecordedDigestNoRef_Exact and
// fleet.TestTargetIdentity_ExactMatch_NonRetrievable), yet its content cannot be
// retrieved through a canonical ref, so canonical Product Impact must refuse it.
func TestImmutableRef_ExactMatchButNonRetrievable(t *testing.T) {
	dA := validDigest("a")
	// A revision known only by its recorded content digest (no canonical ResolvedRef):
	// exactly the shape a k8s-observed target matches EXACTLY.
	rev := &fleet.ContractRevision{Key: "svc@a", Service: "svc", ServiceKey: fleet.NewServiceKey("svc"), Digest: dA}
	if _, err := immutableRef(rev); err == nil {
		t.Error("Product Impact must reject content that is not resolver-retrievable, even when a target matches this revision exactly")
	}
	// The predicate immutableRef enforces (content retrievability) is false, which is
	// independent of the exact revision-match certainty a linked target would report.
	if fleet.ClassifyContentIdentity(rev.ResolvedRef, rev.Digest).Retrievable() {
		t.Error("a recorded digest with no canonical ref must not be retrievable")
	}
}

func postJSON(t *testing.T, url string, body any, wantStatus int, into any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s: expected %d, got %d", url, wantStatus, resp.StatusCode)
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
}

// ownerCollisionQuery builds a two-service fleet whose owner keys collide as
// substrings: team-a owns app, team-a-platform owns lib. Both services have a
// non-compliant target, so each also owns a share of the attention backlog.
func ownerCollisionQuery(t *testing.T) *fleet.Query {
	t.Helper()
	bundle := func(name, team string) *contract.Bundle {
		return &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}},
		}, RawYAML: []byte(name), FS: fstest.MapFS{}}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{
				{Bundle: bundle("app", "team-a"), ResolvedRef: "oci://x/app@" + validDigest("a"), Digest: validDigest("a")},
				{Bundle: bundle("lib", "team-a-platform"), ResolvedRef: "oci://x/lib@" + validDigest("b"), Digest: validDigest("b")},
			},
			Targets: []fleet.RawTarget{
				{Scope: "prod", Kind: "k8s", Name: "app-1", Service: "app", Compliance: fleet.StatusNonCompliant},
				{Scope: "prod", Kind: "k8s", Name: "lib-1", Service: "lib", Compliance: fleet.StatusNonCompliant},
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// Owner SEARCH and owner IDENTITY are two different questions, and the transport has
// to carry both or the distinction dies at the wire: a canonical owner link sends
// ownerKey, and if the server dropped it the reply would be the UNFILTERED list --
// the failure mode that reads as "team-a owns all of this".
func TestProductOwnerKey_IsAnExactFilterOverTheWire(t *testing.T) {
	q := ownerCollisionQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	for _, tc := range []struct{ query, want string }{
		// The search is generous by design: team-a is a substring of team-a-platform.
		{"kinds=service&owner=team-a", "[app lib]"},
		// The identity is exact, and it is what an owner page's links carry.
		{"kinds=service&ownerKey=team-a", "[app]"},
		{"kinds=service&ownerKey=team-a-platform", "[lib]"},
		// They compose rather than override: an exact owner narrowed by a search.
		{"kinds=service&ownerKey=team-a&owner=platform", "[]"},
	} {
		var list fleet.EntityList
		getJSON(t, base+"/api/fleet/entities?"+tc.query, http.StatusOK, &list)
		keys := []string{}
		for _, e := range list.Entities {
			keys = append(keys, e.Key)
		}
		if got := fmt.Sprint(keys); got != tc.want {
			t.Errorf("%s listed %s, want %s", tc.query, got, tc.want)
		}
	}

	// The same two questions on the attention backlog, where an owner page's
	// "View all for this owner" and every posture bar land.
	var fuzzy, exact fleet.AttentionList
	getJSON(t, base+"/api/fleet/attention?owner=team-a", http.StatusOK, &fuzzy)
	getJSON(t, base+"/api/fleet/attention?ownerKey=team-a", http.StatusOK, &exact)
	if fuzzy.Total == 0 || exact.Total == 0 || exact.Total >= fuzzy.Total {
		t.Errorf("attention: ownerKey matched %d of the %d the search matched, want a strict subset",
			exact.Total, fuzzy.Total)
	}
	for _, it := range exact.Items {
		if it.Service != "app" {
			t.Errorf("ownerKey=team-a returned an item for %s, which team-a does not own", it.Service)
		}
	}
}
