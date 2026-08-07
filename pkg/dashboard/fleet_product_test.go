package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

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
			Bundle: newPaymentBundle(), Domain: "domain-a", ResolvedRef: "oci://a/payment-service:1.0.0", Digest: "sha256:a",
		}}}),
		fleet.NewMemorySource("b", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: newPaymentBundle(), Domain: "domain-b", ResolvedRef: "oci://b/payment-service:1.0.0", Digest: "sha256:b",
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
	if nb.Focus.Key != "payment-service" {
		t.Errorf("neighborhood focus = %q", nb.Focus.Key)
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
		SchemaVersion: impact.SchemaVersion, Service: "payment-service", Classification: "BREAKING",
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
	if len(out.Consumers) != 1 || len(out.Owners) != 1 || len(out.ActiveTargets) != 1 {
		t.Fatalf("unexpected shape: %+v", out)
	}
	c := out.Consumers[0]
	if len(c.Path) != 2 {
		t.Errorf("consumer path = %d, want 2", len(c.Path))
	}
	// Every reference the answer returns must be navigable.
	refs := []fleet.EntityRef{out.Service, *out.OldRevision, *out.NewRevision, c.Service}
	refs = append(refs, c.Path...)
	refs = append(refs, out.Owners...)
	refs = append(refs, out.ActiveTargets...)
	if !routesNonEmpty(refs...) {
		t.Errorf("every reference must be navigable: %+v", refs)
	}
}

func routesNonEmpty(refs ...fleet.EntityRef) bool {
	for _, r := range refs {
		if r.Route == "" {
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

func TestRevisionRefResolution(t *testing.T) {
	snap := &fleet.FleetSnapshot{Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{
		"r/resolved":  {ResolvedRef: "oci://x:1"},
		"r/requested": {RequestedRef: "oci://x:2"},
		"r/none":      {},
	}}
	if ref, err := revisionRef(snap, "r/resolved"); err != nil || ref != "oci://x:1" {
		t.Errorf("resolved: %q %v", ref, err)
	}
	if ref, err := revisionRef(snap, "r/requested"); err != nil || ref != "oci://x:2" {
		t.Errorf("requested fallback: %q %v", ref, err)
	}
	if _, err := revisionRef(snap, "r/none"); err == nil {
		t.Error("a revision with no ref must error")
	}
	if _, err := revisionRef(snap, "missing"); err == nil {
		t.Error("a missing revision must error")
	}

	if revisionRefLink("") != nil {
		t.Error("an empty revision key yields no link")
	}
	if l := revisionRefLink("svc@d"); l == nil || l.Route == "" {
		t.Errorf("non-empty key must produce a navigable link: %+v", l)
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
