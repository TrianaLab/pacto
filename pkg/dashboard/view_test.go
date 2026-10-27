package dashboard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// viewQuery: shop-svc runs in shop, secret-svc in secret.
func viewQuery(t *testing.T) *fleet.Query {
	t.Helper()
	k := fleet.SourceKindKubernetes
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: k, Name: "shop-svc", Service: "shop-svc"},
			{Scope: "secret", Kind: k, Name: "secret-svc", Service: "secret-svc"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func serveOptions(t *testing.T, o Options) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o.UI = testUI()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = NewServer(o).ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String()
}

// TestViewNarrowsEveryFleetAnswer: the View option is applied to every fleet
// read, the published one and impact included.
func TestViewNarrowsEveryFleetAnswer(t *testing.T) {
	q := viewQuery(t)
	var gotSnap *fleet.FleetSnapshot
	base := serveOptions(t, Options{
		Fleet:     func(context.Context) (*fleet.Query, error) { return q, nil },
		Published: func() *fleet.Query { return q },
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(ns string) bool { return ns == "shop" }), nil
		},
		Impact: func(_ context.Context, snap *fleet.FleetSnapshot, _, _ string, _ bool) (*impact.Result, error) {
			gotSnap = snap
			return &impact.Result{}, nil
		},
		Auth: &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }},
	})

	var search fleet.SearchResult
	getJSON(t, base+"/api/fleet/services", http.StatusOK, &search)
	if search.Total != 1 || search.Services[0].Name != "shop-svc" {
		t.Errorf("search = %+v", search.Services)
	}
	var overview ProductOverview
	getJSON(t, base+"/api/fleet/overview", http.StatusOK, &overview)
	if overview.Summary.Services != 1 {
		t.Errorf("overview counts %d services", overview.Summary.Services)
	}
	var metrics struct {
		ServiceCount int `json:"serviceCount"`
	}
	getJSON(t, base+"/metrics", http.StatusOK, &metrics)
	if metrics.ServiceCount != 1 {
		t.Errorf("metrics count %d services", metrics.ServiceCount)
	}
	var caps capabilitiesOutput
	getJSON(t, base+"/api/capabilities", http.StatusOK, &caps.Body)
	if !caps.Body.Auth.Hidden || caps.Body.Auth.NothingVisible {
		t.Error("capabilities must say some services are hidden, not all")
	}
	expectStatus(t, base+"/api/fleet/impact?old=a&new=b", http.StatusOK)
	if gotSnap == nil || len(gotSnap.Services) != 1 {
		t.Errorf("impact ran over %+v, want the narrowed snapshot", gotSnap)
	}
}

// TestViewOfNothing: a person whose RBAC reads no namespace sees an empty
// fleet, and capabilities say so.
func TestViewOfNothing(t *testing.T) {
	q := viewQuery(t)
	base := serveOptions(t, Options{
		Fleet:     func(context.Context) (*fleet.Query, error) { return q, nil },
		Published: func() *fleet.Query { return q },
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(string) bool { return false }), nil
		},
		Auth: &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }},
	})
	var caps capabilitiesOutput
	getJSON(t, base+"/api/capabilities", http.StatusOK, &caps.Body)
	if !caps.Body.Auth.Hidden || !caps.Body.Auth.NothingVisible {
		t.Errorf("capabilities must say every service is hidden: %+v", caps.Body.Auth)
	}
}

// TestViewFailureHidesEverything: a caller whose permissions cannot be checked
// gets an error from the fleet views and no published snapshot at all.
func TestViewFailureHidesEverything(t *testing.T) {
	q := viewQuery(t)
	base := serveOptions(t, Options{
		Fleet: func(context.Context) (*fleet.Query, error) { return q, nil },
		View:  func(context.Context, *fleet.Query) (*fleet.Query, error) { return nil, errors.New("apiserver down") },
		Impact: func(context.Context, *fleet.FleetSnapshot, string, string, bool) (*impact.Result, error) {
			return &impact.Result{}, nil
		},
		Auth: &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{} }},
	})
	expectStatus(t, base+"/api/fleet/services", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/overview", http.StatusServiceUnavailable)
	expectStatus(t, base+"/api/fleet/impact?old=a&new=b", http.StatusServiceUnavailable)
	var metrics struct {
		ServiceCount int `json:"serviceCount"`
	}
	getJSON(t, base+"/metrics", http.StatusOK, &metrics)
	var caps capabilitiesOutput
	getJSON(t, base+"/api/capabilities", http.StatusOK, &caps.Body)
	if metrics.ServiceCount != 0 || caps.Body.Auth.Hidden {
		t.Errorf("a failed check leaked %d services (hidden=%v)", metrics.ServiceCount, caps.Body.Auth.Hidden)
	}
}

// TestImpactWithoutFleet: an impact-only host hands the provider no snapshot.
func TestImpactWithoutFleet(t *testing.T) {
	called := false
	base := serveOptions(t, Options{
		Impact: func(_ context.Context, snap *fleet.FleetSnapshot, _, _ string, _ bool) (*impact.Result, error) {
			called = snap == nil
			return &impact.Result{}, nil
		},
	})
	expectStatus(t, base+"/api/fleet/impact?old=a&new=b", http.StatusOK)
	if !called {
		t.Error("the provider was not called with a nil snapshot")
	}
}
