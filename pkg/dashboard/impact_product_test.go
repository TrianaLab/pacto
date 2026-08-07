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
	c := out.Consumers.Items[0]
	if c.Service.Key != "domain-b/payment-service" {
		t.Errorf("consumer key = %q, want domain-b/payment-service", c.Service.Key)
	}
	// The path keys are already canonical and must NOT be re-encoded.
	if len(c.Path) != 2 || c.Path[0].Key != "domain-a/payment-service" || c.Path[1].Key != "domain-b/payment-service" {
		t.Errorf("path double-encoded or wrong: %+v", c.Path)
	}
	for _, ref := range append([]ProductRef{c.Path[0], c.Path[1]}, out.Service) {
		if strings.Contains(ref.Key, "%2F") {
			t.Errorf("reference key is double-encoded: %q", ref.Key)
		}
		// The transport adds a canonical href for each reference.
		if ref.Href == "" {
			t.Errorf("reference must carry an href: %+v", ref)
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

// An exact OCI digest ref analyzes successfully with SnapshotMatch=true and no
// mutable-content limitation.
func TestProductImpact_ExactDigestSucceeds(t *testing.T) {
	q := twoDomainDashboardQuery(t) // revisions carry digest-pinned ResolvedRefs
	snapID := q.SnapshotID()
	from := revKeyForDomain(t, q, "domain-a")
	res := &impact.Result{SnapshotID: snapID, Service: "payment-service"}
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, staticImpact(res))
	defer cancel()
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: from, ToRevisionKey: from}, http.StatusOK, &out)
	if !out.SnapshotMatch {
		t.Error("an exact digest revision must report SnapshotMatch=true")
	}
	for _, l := range out.Limitations.Items {
		if l.Code == fleet.LimitationRevisionContentMutable {
			t.Errorf("an exact OCI digest must not carry a mutable-content limitation: %+v", out.Limitations)
		}
	}
}

// countingImpact records whether the provider was invoked.
func countingImpact(called *bool, res *impact.Result) impactProviderFunc {
	return func(context.Context, string, string, bool) (*impact.Result, error) {
		*called = true
		return res, nil
	}
}

// A revision resolved only through a MUTABLE RequestedRef (a tag) must be rejected
// BEFORE the provider is invoked: the server must never fetch potentially-different
// content and then claim snapshot parity. There must be no successful ProductImpact
// with SnapshotMatch=true after falling back to a mutable reference.
func TestProductImpact_MutableRefRejectedBeforeProvider(t *testing.T) {
	msnap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("m", "local", &fleet.Collection{Revisions: []fleet.RawRevision{{
		Bundle: newPaymentBundle(), RequestedRef: "oci://x/payment-service:latest", Digest: "sha256:m",
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	mq := fleet.NewQuery(msnap)
	mfrom := revKeyForDomain(t, mq, "")
	called := false
	mbase, mcancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return mq, nil },
		countingImpact(&called, &impact.Result{SnapshotID: mq.SnapshotID(), Service: "payment-service"}))
	defer mcancel()
	postJSON(t, mbase+"/api/fleet/impact", impactRequest{SnapshotID: mq.SnapshotID(), FromRevisionKey: mfrom, ToRevisionKey: mfrom}, http.StatusUnprocessableEntity, nil)
	if called {
		t.Error("the impact provider must NOT be invoked for a mutable-only revision")
	}
}

// A tag-shaped ResolvedRef must not be accepted as exact merely by being non-empty,
// and a local-path revision (no digest-pinned ref) is likewise rejected.
func TestProductImpact_TagAndLocalRejected(t *testing.T) {
	for name, rev := range map[string]fleet.RawRevision{
		"tag":   {Bundle: newPaymentBundle(), ResolvedRef: "oci://x/payment-service:1.0", Digest: "sha256:t"},
		"local": {Bundle: newPaymentBundle(), RequestedRef: "file:///abs/payment-service"},
	} {
		t.Run(name, func(t *testing.T) {
			snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("s", "local", &fleet.Collection{Revisions: []fleet.RawRevision{rev}}))
			if err != nil {
				t.Fatal(err)
			}
			q := fleet.NewQuery(snap)
			key := revKeyForDomain(t, q, "")
			called := false
			base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil },
				countingImpact(&called, &impact.Result{SnapshotID: q.SnapshotID()}))
			defer cancel()
			postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: q.SnapshotID(), FromRevisionKey: key, ToRevisionKey: key}, http.StatusUnprocessableEntity, nil)
			if called {
				t.Errorf("%s: provider must not be invoked for a non-exact revision", name)
			}
		})
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
	if out.ActiveTargets.Count != 1 || out.ActiveTargets.Items[0].Label != "production/kubernetes-workload/pay/payment-service" {
		t.Errorf("active target label must be the record DisplayName: %+v", out.ActiveTargets)
	}
}

// A revision with an exact (digest-pinned) ref succeeds on both sides; a revision
// with no resolvable exact reference is a 422, on either side of the comparison.
func TestProductImpact_NoResolvableRef(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("s", "local", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: newPaymentBundle(), ResolvedRef: "oci://x/payment-service@sha256:d1", Digest: "sha256:d1"},
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
	// The exact revision analyzed against itself succeeds.
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: withRef, ToRevisionKey: withRef}, http.StatusOK, nil)
	// from has no ref.
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: noRef, ToRevisionKey: noRef}, http.StatusUnprocessableEntity, nil)
	// from resolves, to has no ref.
	postJSON(t, base+"/api/fleet/impact", impactRequest{SnapshotID: snapID, FromRevisionKey: withRef, ToRevisionKey: noRef}, http.StatusUnprocessableEntity, nil)
}
