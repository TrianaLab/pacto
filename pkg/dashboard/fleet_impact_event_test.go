package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// eventFleetQuery: app depends on lib as required and web as optional; each
// runs on a fresh Kubernetes target.
func eventFleetQuery(t *testing.T) *fleet.Query {
	t.Helper()
	now := time.Now().UTC()
	ev := now.Add(-time.Hour)
	hex := map[string]string{"lib": "a", "app": "b", "web": "c"}
	bundle := func(name, team string, deps ...contract.Dependency) fleet.RawRevision {
		b := &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}},
			Dependencies: deps,
		}, RawYAML: []byte(name), FS: fstest.MapFS{}}
		return fleet.RawRevision{Bundle: b, ResolvedRef: "oci://x/" + name + "@" + validDigest(hex[name]), Digest: validDigest(hex[name])}
	}
	target := func(name string) fleet.RawTarget {
		return fleet.RawTarget{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: name, Service: name,
			ResolvedRef: "oci://x/" + name + "@" + validDigest(hex[name]), Digest: validDigest(hex[name]), EvidenceAt: &ev}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{
				bundle("lib", "team-l"),
				bundle("app", "team-a", contract.Dependency{Name: "lib", Ref: "oci://x/lib", Required: true}),
				bundle("web", "team-w", contract.Dependency{Name: "lib", Ref: "oci://x/lib"}),
			},
			Targets: []fleet.RawTarget{target("lib"), target("app"), target("web")},
		}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// noImpact registers POST /api/fleet/impact; an event never calls it.
func noImpact(t *testing.T) impactProviderFunc {
	return func(context.Context, *fleet.FleetSnapshot, string, string, bool) (*impact.Result, error) {
		t.Error("an event request must not call the contract impact provider")
		return nil, errors.New("unexpected")
	}
}

func eventServer(t *testing.T, o Options) string {
	t.Helper()
	q := eventFleetQuery(t)
	o.Fleet = func(context.Context) (*fleet.Query, error) { return q, nil }
	if o.Impact == nil {
		o.Impact = noImpact(t)
	}
	return serveOptions(t, o)
}

// libKey is lib's service key; base is unused, so every call reads alike.
func libKey(t *testing.T, _ string) string {
	t.Helper()
	v, err := eventFleetQuery(t).GetService("lib")
	if err != nil {
		t.Fatal(err)
	}
	return string(v.Service.Key)
}

func tally(buckets []impact.TallyBucket, key string) int {
	for _, b := range buckets {
		if b.Key == key {
			return b.Count
		}
	}
	return 0
}

func TestImpactEventUnavailable(t *testing.T) {
	base := eventServer(t, Options{})
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: libKey(t, base)}, http.StatusOK, &out)
	if out.Event == nil || out.Event.Kind != impact.EventUnavailable || out.Gate != impact.GateUnverified || out.SnapshotID == "" {
		t.Fatalf("answer = %+v", out.Product)
	}
	if tally(out.Consumers.ByOutcome, impact.EffectBreaks) != 1 || tally(out.Consumers.ByOutcome, impact.EffectMayDegrade) != 1 {
		t.Errorf("byOutcome = %+v, want one breaks and one may-degrade", out.Consumers.ByOutcome)
	}
	for _, c := range out.Consumers.Items {
		if c.CompatibilityVerdict == impact.CompatibilityCompatible {
			t.Errorf("%s is compatible in an event answer", c.Service.Key)
		}
	}
}

func TestImpactEventNeedsServiceKey(t *testing.T) {
	base := eventServer(t, Options{})
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable}, http.StatusUnprocessableEntity, nil)
	// An event is asked of one service, never between two revisions.
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: libKey(t, base), FromRevisionKey: "a", ToRevisionKey: "b"}, http.StatusUnprocessableEntity, nil)
	// A contract request without both keys is refused before anything is read.
	postJSON(t, base+"/api/fleet/impact", impactRequest{ToRevisionKey: "b"}, http.StatusUnprocessableEntity, nil)
}

func TestImpactEventHiddenServiceNotFound(t *testing.T) {
	q := viewQuery(t)
	base := serveOptions(t, Options{
		Fleet: func(context.Context) (*fleet.Query, error) { return q, nil },
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(ns string) bool { return ns == "shop" }), nil
		},
		Impact: noImpact(t),
		Auth:   &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }},
	})
	_, hidden := call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: "secret-svc"})
	code, missing := call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: "no-such-svc"})
	if code != http.StatusNotFound || hidden["detail"] != missing["detail"] || hidden["status"] != missing["status"] {
		t.Errorf("hidden %v and missing %v must answer the same 404", hidden, missing)
	}
	code, _ = call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: "shop-svc"})
	if code != http.StatusOK {
		t.Errorf("a visible service answered %d", code)
	}
}

func TestImpactEventRevisionOnlyRegressed(t *testing.T) {
	base := eventServer(t, Options{})
	key := libKey(t, base)
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: key, Revision: dRun}, http.StatusUnprocessableEntity, nil)
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventRegressed, ServiceKey: key}, http.StatusUnprocessableEntity, nil)
}

// libStage is a Stage of lib that lists digest as failed.
func libStage(digest string) delivery.StageObject {
	st := delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: "shop", Name: "lib-prod"}}
	st.Spec.Service = delivery.ServiceRef{Name: "lib"}
	st.Status.FailedDigests = []string{digest}
	return st
}

func regressed(t *testing.T, base, revision string) ProductImpact {
	t.Helper()
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventRegressed, ServiceKey: libKey(t, base), Revision: revision}, http.StatusOK, &out)
	return out
}

func limitation(p ProductImpact, code string) string {
	for _, l := range p.Limitations.Items {
		if l.Code == code {
			return l.Message
		}
	}
	return ""
}

func TestImpactEventRegressionReadsCallerDelivery(t *testing.T) {
	other := libStage(dNext)
	other.Spec.Service.Name = "app"
	f := &fakeDelivery{stages: []delivery.StageObject{libStage(dRun), other},
		history: &delivery.History{Stage: delivery.StageNow{Name: "lib-prod"}, Entries: []delivery.HistoryEntry{{Type: delivery.TypeRollback, Reason: "error rate", Record: "rec-1",
			Pointer: &delivery.PointerChange{Before: dRun, After: dNext}}}}}
	var asked bool
	base := eventServer(t, Options{Delivery: func(r *http.Request) (DeliveryAPI, error) {
		asked = r != nil
		return f, nil
	}})
	out := regressed(t, base, dRun)
	if !asked {
		t.Error("delivery must be read with the request's own identity")
	}
	kinds := map[string]bool{}
	for _, e := range out.Regression {
		kinds[e.Kind] = true
		if e.Stage != "lib-prod" {
			t.Errorf("evidence from another service's Stage: %+v", e)
		}
	}
	if !kinds[impact.RegressionFailed] || !kinds[impact.RegressionRollback] {
		t.Errorf("regression = %+v, want the failed digest and the rollback", out.Regression)
	}
	if m := limitation(out, impact.LimitationDeliveryEvidenceUnavailable); m != "" {
		t.Errorf("evidence read in full yet limited: %s", m)
	}
	// A version names no single artifact.
	if m := limitation(regressed(t, base, "1.0.0"), impact.LimitationDeliveryEvidenceUnavailable); !strings.Contains(m, "artifact digest") {
		t.Errorf("version limitation = %q", m)
	}
	// A history that cannot be read is named, never read as no rollback.
	f.historyErr = errors.New("records unreachable")
	if m := limitation(regressed(t, base, dRun), impact.LimitationDeliveryEvidenceUnavailable); !strings.Contains(m, "shop/lib-prod") {
		t.Errorf("unreadable history limitation = %q", m)
	}
	// A namespace the caller may not list is named.
	f.historyErr, f.forbidAll, f.stageNS = nil, true, []string{"shop", "vault"}
	f.forbid = map[string]bool{"vault": true}
	if m := limitation(regressed(t, base, dRun), impact.LimitationDeliveryEvidenceUnavailable); !strings.Contains(m, "vault") {
		t.Errorf("forbidden namespace limitation = %q", m)
	}
	// A listing that fails outright is the reason, never "no failure".
	f.forbidAll, f.listErr = false, Refuse(http.StatusBadGateway, "Upstream", "apiserver timed out")
	if m := limitation(regressed(t, base, dRun), impact.LimitationDeliveryEvidenceUnavailable); !strings.Contains(m, "apiserver timed out") {
		t.Errorf("failed listing limitation = %q", m)
	}
	f.listErr = errors.New("connection reset")
	if m := limitation(regressed(t, base, dRun), impact.LimitationDeliveryEvidenceUnavailable); !strings.Contains(m, "connection reset") {
		t.Errorf("failed listing limitation = %q", m)
	}
}

func TestImpactEventRegressionNoDeliveryHost(t *testing.T) {
	base := eventServer(t, Options{})
	if m := limitation(regressed(t, base, dRun), impact.LimitationDeliveryEvidenceUnavailable); !strings.Contains(m, "no cluster") {
		t.Errorf("limitation = %q", m)
	}
}

func TestImpactEventRegressionForbidden(t *testing.T) {
	base := eventServer(t, Options{Delivery: func(*http.Request) (DeliveryAPI, error) {
		return nil, Refuse(http.StatusForbidden, "Forbidden", "you may not read delivery in shop")
	}})
	out := regressed(t, base, dRun)
	if m := limitation(out, impact.LimitationDeliveryEvidenceUnavailable); !strings.Contains(m, "you may not read delivery in shop") {
		t.Errorf("limitation = %q", m)
	}
	if len(out.Regression) != 0 || strings.Contains(out.GateReason, "recorded no failure") {
		t.Errorf("a refused read must never read as no failure: %q", out.GateReason)
	}
}

func TestImpactEventOutcomeFilter(t *testing.T) {
	base := eventServer(t, Options{})
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: libKey(t, base), Outcome: impact.EffectMayDegrade}, http.StatusOK, &out)
	if out.Consumers.Count != 1 || out.Consumers.Items[0].Effect != impact.EffectMayDegrade || out.Consumers.Total != 2 {
		t.Errorf("filtered page = %+v", out.Consumers)
	}
}

// TestImpactRequestContractStillWorks: today's body (two revision keys, no
// event) answers through the contract provider as before.
func TestImpactRequestContractStillWorks(t *testing.T) {
	var called bool
	base := eventServer(t, Options{Impact: func(_ context.Context, snap *fleet.FleetSnapshot, _, _ string, _ bool) (*impact.Result, error) {
		called = true
		return &impact.Result{SnapshotID: snap.SnapshotID, Classification: "NON_BREAKING", Gate: impact.GateClear}, nil
	}})
	v, err := eventFleetQuery(t).GetService("lib")
	if err != nil {
		t.Fatal(err)
	}
	rev := string(v.Revisions[0].Key)
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{FromRevisionKey: rev, ToRevisionKey: rev}, http.StatusOK, &out)
	if !called || out.Event != nil || out.Classification != "NON_BREAKING" || out.OldRevision == nil {
		t.Errorf("contract answer = %+v", out.Product)
	}
}

func TestImpactOwnerRefsHaveHrefs(t *testing.T) {
	base := eventServer(t, Options{})
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ServiceKey: libKey(t, base)}, http.StatusOK, &out)
	if out.ChangedOwner == nil || out.ChangedOwner.Href == "" || out.ChangedOwner.Key != "team:team-l" {
		t.Errorf("changed owner = %+v", out.ChangedOwner)
	}
	if !hrefsNonEmpty(out.Owners.Items...) || out.Owners.Count != 2 {
		t.Errorf("owners = %+v", out.Owners)
	}
	for _, c := range out.Consumers.Items {
		if c.OwnerRef == nil || c.OwnerRef.Href == "" || len(c.Targets) == 0 || !hrefsNonEmpty(c.Targets...) {
			t.Errorf("%s owner %+v targets %+v", c.Service.Key, c.OwnerRef, c.Targets)
		}
	}
}

// TestImpactUncontractedCallerHasNoHref: a caller no contract describes has
// no page to link to.
func TestImpactUncontractedCallerHasNoHref(t *testing.T) {
	p := toProductImpact(&impact.Product{Consumers: impact.ConsumersPage{Items: []impact.ProductConsumer{
		{Service: fleet.EntityRef{Kind: fleet.KindService, Key: "ghost"}, Uncontracted: true},
	}}})
	if h := p.Consumers.Items[0].Service.Href; h != "" {
		t.Errorf("uncontracted caller links to %q", h)
	}
}
