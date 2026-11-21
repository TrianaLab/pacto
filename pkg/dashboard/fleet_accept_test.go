package dashboard

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// viewerAuth is an identity layer whose every caller is st, in local mode
// when local is set.
func viewerAuth(local bool, st AuthState) *Auth {
	return &Auth{Local: local, Login: !local, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h },
		State: func(*http.Request) AuthState { return st }}
}

// acceptServer serves eventFleetQuery (narrowed by view when set) with plan
// as the accept planner.
func acceptServer(t *testing.T, auth *Auth, view func(context.Context, *fleet.Query) (*fleet.Query, error), plan func(context.Context, fleet.ServiceKey, *fleet.ContractRevision) (AcceptPlanAnswer, error)) string {
	t.Helper()
	q := eventFleetQuery(t)
	return serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, View: view, Auth: auth, AcceptPlan: plan})
}

func TestAcceptPlanForVisibleService(t *testing.T) {
	var gotKey fleet.ServiceKey
	var gotRev *fleet.ContractRevision
	plan := func(_ context.Context, k fleet.ServiceKey, rev *fleet.ContractRevision) (AcceptPlanAnswer, error) {
		gotKey, gotRev = k, rev
		return AcceptPlanAnswer{Plan: &AcceptPlanView{Service: string(k), Ranges: []delivery.AcceptedRange{{Field: "dependencies[lib].compatibility", Value: "^1.0.0"}},
			IntentDigest: "sha256:i"}, Command: "pacto accept oci://x/app --proposed --intent sha256:i"}, nil
	}
	for _, auth := range []*Auth{viewerAuth(true, AuthState{}), viewerAuth(false, AuthState{SignedIn: true})} {
		base := acceptServer(t, auth, nil, plan)
		key := libKey(t, base)
		var out AcceptPlanAnswer
		postJSON(t, base+"/api/fleet/accept", map[string]string{"serviceKey": key}, http.StatusOK, &out)
		if string(gotKey) != key || gotRev == nil || gotRev.Contract.Service.Name != "lib" {
			t.Errorf("planned %s %+v", gotKey, gotRev)
		}
		if out.Plan == nil || out.Command == "" || out.Plan.IntentDigest != "sha256:i" {
			t.Errorf("answer = %+v", out)
		}
	}
}

func TestAcceptHiddenServiceNotFound(t *testing.T) {
	plan := func(context.Context, fleet.ServiceKey, *fleet.ContractRevision) (AcceptPlanAnswer, error) {
		t.Error("a hidden service must never be planned")
		return AcceptPlanAnswer{}, nil
	}
	hideAll := func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, _ string) bool { return false }, nil), nil
	}
	base := acceptServer(t, viewerAuth(false, AuthState{SignedIn: true}), hideAll, plan)
	code, hidden := call(t, http.MethodPost, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base)})
	_, missing := call(t, http.MethodPost, base+"/api/fleet/accept", map[string]string{"serviceKey": "no-such-svc"})
	if code != http.StatusNotFound || hidden["detail"] != missing["detail"] {
		t.Errorf("hidden %d %v and missing %v must answer the same 404", code, hidden, missing)
	}
}

func TestAcceptPlanOfAnotherServiceNotFound(t *testing.T) {
	// A visible contract whose publish.bundle names a hidden service's
	// repository plans that service: it answers as a hidden one.
	plan := func(context.Context, fleet.ServiceKey, *fleet.ContractRevision) (AcceptPlanAnswer, error) {
		return AcceptPlanAnswer{Plan: &AcceptPlanView{Service: "ghcr.io/acme/ledger", WhoMayAccept: "ana@acme.com"}, Command: "pacto accept x"}, nil
	}
	base := acceptServer(t, viewerAuth(true, AuthState{}), nil, plan)
	code, out := call(t, http.MethodPost, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base)})
	_, missing := call(t, http.MethodPost, base+"/api/fleet/accept", map[string]string{"serviceKey": "no-such-svc"})
	if code != http.StatusNotFound || out["detail"] != missing["detail"] {
		t.Errorf("a plan of another service answered %d %v", code, out)
	}
}

func TestAcceptRouteAbsentWithoutProvider(t *testing.T) {
	base := acceptServer(t, viewerAuth(true, AuthState{}), nil, nil)
	postJSON(t, base+"/api/fleet/accept", map[string]string{"serviceKey": "x"}, http.StatusNotFound, nil)
}

func TestAcceptNotPlannedForAnonymousViewer(t *testing.T) {
	plan := func(context.Context, fleet.ServiceKey, *fleet.ContractRevision) (AcceptPlanAnswer, error) {
		t.Error("an anonymous viewer must never have acceptances read")
		return AcceptPlanAnswer{}, nil
	}
	open := &Auth{Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{} }}
	for _, auth := range []*Auth{nil, open, viewerAuth(false, AuthState{SignedIn: true, FleetOnly: true})} {
		base := acceptServer(t, auth, nil, plan)
		postJSON(t, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base)}, http.StatusForbidden, nil)
	}
}

func TestAcceptNothingProposedRefusal(t *testing.T) {
	plan := func(context.Context, fleet.ServiceKey, *fleet.ContractRevision) (AcceptPlanAnswer, error) {
		return AcceptPlanAnswer{Refusal: &AcceptRefusal{Code: "AcceptNothingProposed", Message: "nothing proposed", Command: "pacto ci --publish ."}}, nil
	}
	base := acceptServer(t, viewerAuth(true, AuthState{}), nil, plan)
	var out AcceptPlanAnswer
	postJSON(t, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base)}, http.StatusOK, &out)
	if out.Plan != nil || out.Refusal == nil || out.Refusal.Code != "AcceptNothingProposed" || out.Refusal.Command == "" {
		t.Errorf("answer = %+v", out)
	}
}

func TestAcceptPlanErrorIsMapped(t *testing.T) {
	plan := func(context.Context, fleet.ServiceKey, *fleet.ContractRevision) (AcceptPlanAnswer, error) {
		return AcceptPlanAnswer{}, &oci.ArtifactNotFoundError{Ref: "x"}
	}
	base := acceptServer(t, viewerAuth(true, AuthState{}), nil, plan)
	postJSON(t, base+"/api/fleet/accept", map[string]string{"serviceKey": libKey(t, base)}, http.StatusNotFound, nil)
	failing := func(context.Context) (*fleet.Query, error) { return nil, errors.New("no snapshot") }
	base = serveOptions(t, Options{Fleet: failing, Auth: viewerAuth(true, AuthState{}), AcceptPlan: plan})
	code, _ := call(t, http.MethodPost, base+"/api/fleet/accept", map[string]string{"serviceKey": "x"})
	if code < 500 {
		t.Errorf("a failed snapshot answered %d", code)
	}
}

// readsSeen serves impact with a provider that records whether it may read
// acceptances, behind auth.
func readsSeen(t *testing.T, auth *Auth) bool {
	t.Helper()
	var seen bool
	base := serveOptions(t, Options{Auth: auth, Impact: func(ctx context.Context, _ *fleet.FleetSnapshot, _, _ string, _ bool) (*impact.Result, error) {
		seen = ReadsAcceptances(ctx)
		return &impact.Result{}, nil
	}})
	if code, _ := call(t, http.MethodGet, base+"/api/fleet/impact?old=oci://x/a:1&new=oci://x/a:2", nil); code != http.StatusOK {
		t.Fatalf("impact answered %d", code)
	}
	return seen
}

func TestImpactBreaksReadForSignedInViewer(t *testing.T) {
	if !readsSeen(t, viewerAuth(false, AuthState{SignedIn: true})) || !readsSeen(t, viewerAuth(true, AuthState{})) {
		t.Error("a signed-in person and a local viewer must have acceptances read")
	}
}

func TestImpactBreaksNotReadForAnonymousViewer(t *testing.T) {
	open := &Auth{Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{} }}
	if readsSeen(t, nil) || readsSeen(t, open) || readsSeen(t, viewerAuth(false, AuthState{SignedIn: true, FleetOnly: true})) {
		t.Error("an anonymous viewer or a sign-in link session must never have acceptances read")
	}
	if ReadsAcceptances(context.Background()) {
		t.Error("a bare context reads no acceptances")
	}
}
