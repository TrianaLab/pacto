package dashboard

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

// revKeyForDomain finds the payment-service revision key in the given domain.
func revKeyForDomain(t *testing.T, q *fleet.Query, domain string) string {
	t.Helper()
	for k, r := range q.Snapshot().Revisions {
		if r.Domain == domain {
			return string(k)
		}
	}
	t.Fatalf("no revision in domain %q", domain)
	return ""
}

// staticImpact returns a mock provider that yields res (with its SnapshotID set
// to the current snapshot so the parity check passes).
func staticImpact(res *impact.Result) impactProviderFunc {
	return func(context.Context, string, string, bool) (*impact.Result, error) { return res, nil }
}

// Cross-domain identity: the changed service and every consumer/path step keep
// their own domain-qualified key; a path key that is already canonical is never
// re-encoded (no domain-of-consumer stamped onto every step).
func TestProductImpact_CrossDomainCanonicalIdentity(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	// The raw result: consumer in domain-b, a transitive path that crosses domains.
	res := &impact.Result{
		SchemaVersion: impact.SchemaVersion, SnapshotID: snapID, Service: "payment-service", Classification: "BREAKING",
		Consumers: []impact.AffectedConsumer{{
			Service: "payment-service", Domain: "domain-b", Depth: 2, Path: []string{"domain-a/payment-service", "domain-b/payment-service"},
			Confidence: impact.ConfidenceContractual, CompatibilityVerdict: "incompatible",
		}},
	}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()

	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)

	if out.Service.Key != "domain-a/payment-service" {
		t.Errorf("changed service key = %q, want domain-a/payment-service", out.Service.Key)
	}
	c := out.Consumers[0]
	if c.Service.Key != "domain-b/payment-service" {
		t.Errorf("consumer key = %q, want domain-b/payment-service", c.Service.Key)
	}
	// The path keys are already canonical and must NOT be re-encoded.
	if len(c.Path) != 2 || c.Path[0].Key != "domain-a/payment-service" || c.Path[1].Key != "domain-b/payment-service" {
		t.Errorf("path double-encoded or wrong: %+v", c.Path)
	}
	for _, ref := range append([]fleet.EntityRef{c.Path[0], c.Path[1]}, out.Service) {
		if strings.Contains(ref.Key, "%2F") {
			t.Errorf("reference key is double-encoded: %q", ref.Key)
		}
	}
}

// Correct labels come from records, not raw keys.
func TestProductImpact_LabelsFromRecords(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	res := &impact.Result{SchemaVersion: impact.SchemaVersion, SnapshotID: snapID, Service: "payment-service", Classification: "NON_BREAKING"}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()

	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)
	if out.OldRevision == nil || out.OldRevision.Label == out.OldRevision.Key {
		t.Errorf("revision label must come from the record, not the raw key: %+v", out.OldRevision)
	}
	if !strings.Contains(out.OldRevision.Label, "payment-service") {
		t.Errorf("revision label = %q, want a human label", out.OldRevision.Label)
	}
}

// Two revisions of different logical services are rejected.
func TestProductImpact_MismatchedServicesRejected(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	to := revKeyForDomain(t, q, "domain-b")
	res := &impact.Result{SnapshotID: snapID}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: to}, http.StatusUnprocessableEntity, nil)
}

// An explicit ServiceKey that does not match the revisions' service is rejected.
func TestProductImpact_MismatchedServiceKeyRejected(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	res := &impact.Result{SnapshotID: snapID}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, ServiceKey: "domain-b/payment-service", FromRevisionKey: from, ToRevisionKey: from}, http.StatusUnprocessableEntity, nil)
}

// A snapshot published between validation and analysis (the analysis result's
// snapshot id differs from the validated snapshot) is a 409, not a silent answer.
func TestProductImpact_RefreshRaceRejected(t *testing.T) {
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	// The provider analyzed a DIFFERENT snapshot than the handler validated.
	res := &impact.Result{SnapshotID: "a-different-snapshot"}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusConflict, nil)
}

// An exact OCI digest ref carries no mutable-content limitation; a mutable-only
// ref does (parity is never silently claimed over potentially different content).
func TestProductImpact_MutableContentLimitation(t *testing.T) {
	// twoDomainDashboardQuery revisions carry ResolvedRef (immutable), so no
	// mutable-content limitation is added.
	q := twoDomainDashboardQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	res := &impact.Result{SnapshotID: snapID, Service: "payment-service"}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)
	for _, l := range out.Limitations {
		if l.Code == fleet.LimitationRevisionContentMutable {
			t.Errorf("an exact OCI digest must not carry a mutable-content limitation: %+v", out.Limitations)
		}
	}

	// A revision resolved only through a mutable RequestedRef DOES carry the
	// limitation: snapshot parity is never silently claimed over mutable content.
	msnap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("m", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
		Bundle: newPaymentBundle(), RequestedRef: "oci://x/payment-service:latest", Digest: "sha256:m",
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	mq := fleet.NewQuery(msnap)
	mfrom := revKeyForDomain(t, mq, "")
	mbase, mcancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return mq, nil },
		staticImpact(&impact.Result{SnapshotID: mq.SnapshotID(), Service: "payment-service"}))
	defer mcancel()
	var mout ProductImpact
	postJSON(t, mbase+"/api/fleet/impact", impactRequest{SnapshotID: mq.SnapshotID(), FromRevisionKey: mfrom, ToRevisionKey: mfrom}, http.StatusOK, &mout)
	hasMutable := false
	for _, l := range mout.Limitations {
		if l.Code == fleet.LimitationRevisionContentMutable {
			hasMutable = true
		}
	}
	if !hasMutable {
		t.Errorf("a mutable-only ref must carry a mutable-content limitation: %+v", mout.Limitations)
	}
}

// A real active target's label is its DisplayName from the record.
func TestProductImpact_TargetLabelFromRecord(t *testing.T) {
	q := demoFleetQuery(t)
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "")
	realKey := string(fleet.NewTargetKey("production", "kubernetes-workload", "pay/payment-service"))
	res := &impact.Result{SnapshotID: snapID, Service: "payment-service", ActiveTargets: []string{realKey}}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)
	if len(out.ActiveTargets) != 1 || out.ActiveTargets[0].Label != "production/kubernetes-workload/pay/payment-service" {
		t.Errorf("active target label must be the record DisplayName: %+v", out.ActiveTargets)
	}
}

// A revision with no resolvable reference (neither resolved nor requested) is a
// 422, on either side of the comparison.
func TestProductImpact_NoResolvableRef(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("s", "local", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: newPaymentBundle(), ResolvedRef: "oci://x:1", Digest: "sha256:d1"},
		{Bundle: newPaymentBundle(), Digest: "sha256:d2"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	snapID := q.SnapshotID()
	var withRef, noRef string
	for k, r := range q.Snapshot().Revisions {
		if r.ResolvedRef != "" {
			withRef = string(k)
		} else {
			noRef = string(k)
		}
	}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil },
		staticImpact(&impact.Result{SnapshotID: snapID}))
	defer cancel()
	// from has no ref.
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: noRef, ToRevisionKey: noRef}, http.StatusUnprocessableEntity, nil)
	// from resolves, to has no ref.
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: withRef, ToRevisionKey: noRef}, http.StatusUnprocessableEntity, nil)
}
