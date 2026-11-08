package dashboard

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// offeringDashboardQuery is a fleet with two shared-config providers and a
// redis provider. payments-service consumes platform-worker-config through a
// dependency and declares an external redis, which is inferred.
func offeringDashboardQuery(t *testing.T) *fleet.Query {
	t.Helper()
	rev := func(c *contract.Contract, fill string) fleet.RawRevision {
		c.PactoVersion = "2.4"
		ref := "oci://ghcr.io/acme/" + c.Service.Name + ":" + c.Service.Version
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme", RequestedRef: ref, Digest: validDigest(fill)}
	}
	app := &contract.Contract{Service: contract.Service{Name: "platform-app-config", Version: "1.0.0", Offering: "shared-config"},
		Configurations: []contract.Configuration{{Name: "app", Schema: "s.json", Required: true}}}
	worker := &contract.Contract{Service: contract.Service{Name: "platform-worker-config", Version: "1.2.0", Offering: "shared-config"}, Workload: contract.WorkloadService}
	redis := &contract.Contract{Service: contract.Service{Name: "redis", Version: "7.0.0", Offering: "redis"}, Workload: contract.WorkloadService}
	pay := &contract.Contract{Service: contract.Service{Name: "payments-service", Version: "1.0.0"}, Dependencies: []contract.Dependency{
		{Name: "platform-worker-config", Ref: "oci://ghcr.io/acme/platform-worker-config", Required: true, Compatibility: "^1.0.0"},
		{Name: "redis", External: true, Version: "7"},
	}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "oci", &fleet.Collection{
		Revisions: []fleet.RawRevision{rev(app, "a"), rev(worker, "b"), rev(redis, "c"), rev(pay, "d")},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestProductOfferings_FiltersReachTheQuery(t *testing.T) {
	q := offeringDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	var list ProductEntityList
	getJSON(t, base+"/api/fleet/entities?kinds=offering&sort=name", http.StatusOK, &list)
	if list.Total != 2 || list.Entities[0].Href != "/fleet/offerings/redis" && list.Entities[1].Href != "/fleet/offerings/redis" {
		t.Errorf("offerings = %+v", list.Entities)
	}
	getJSON(t, base+"/api/fleet/entities?kinds=service&offering=shared-config", http.StatusOK, &list)
	if list.Total != 2 {
		t.Errorf("shared-config providers = %+v", list.Entities)
	}
	resp, err := http.Get(base + "/api/fleet/entities?kinds=revision&offering=redis")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "offering") {
		t.Errorf("offering on revisions = %d %s", resp.StatusCode, body)
	}
}

func TestProductOfferingDetail_OverTheWire(t *testing.T) {
	q := offeringDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	var det ProductEntityDetail
	getJSON(t, base+"/api/fleet/entities/offering?key=shared-config", http.StatusOK, &det)
	o := det.Offering
	if o == nil || det.Entity.Href != "/fleet/offerings/shared-config" || len(o.Providers.Items) != 2 {
		t.Fatalf("offering detail = %+v", det)
	}
	for _, p := range o.Providers.Items {
		if p.Service.Href == "" || p.CurrentRevision == nil || p.CurrentRevision.Href == "" || p.Versions.Items[0].Revision.Href == "" || p.Consume == nil || p.Consume.YAML == "" {
			t.Errorf("provider = %+v", p)
		}
	}
	if c := o.Consumers.Items; len(c) != 1 || !strings.HasSuffix(c[0].Consumer.Href, "payments-service") || c[0].Provider.Href == "" {
		t.Errorf("consumers = %+v", c)
	}
	expectStatus(t, base+"/api/fleet/entities/offering?key=nope", http.StatusNotFound)
}

func TestProductServiceDetail_ConsumedAndInferredOfferings(t *testing.T) {
	q := offeringDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	var svc ProductEntityDetail
	getJSON(t, base+"/api/fleet/entities/service?key="+url.QueryEscape(string(fleet.NewServiceKeyDomain("ghcr.io/acme", "payments-service"))), http.StatusOK, &svc)
	s := svc.Service
	if c := s.ConsumedOfferings.Items; len(c) != 1 || c[0].Offering != "shared-config" || c[0].Provider.Href == "" || c[0].Consumer.Href == "" {
		t.Errorf("consumed offerings = %+v", s.ConsumedOfferings)
	}
	if i := s.InferredOfferings; len(i) != 1 || i[0].Offering.Href != "/fleet/offerings/redis" || i[0].Provider != nil {
		t.Errorf("inferred offerings = %+v", i)
	}
}

func TestFleetVersionsRoute(t *testing.T) {
	q := offeringDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	var res ProductOfferingVersions
	getJSON(t, base+"/api/fleet/versions?offering=shared-config&range=%5E1.1.0", http.StatusOK, &res)
	if res.Offering.Href != "/fleet/offerings/shared-config" || res.Total != 2 || len(res.Providers) != 2 {
		t.Fatalf("versions = %+v", res)
	}
	app, worker := res.Providers[0], res.Providers[1]
	if app.Provider.Href == "" || app.Versions.Items[0].RangeStatus != fleet.RangeUnsatisfied || app.Versions.LatestCompatible != nil {
		t.Errorf("platform-app-config = %+v", app)
	}
	if v := worker.Versions; v.Items[0].Revision.Href == "" || v.Items[0].RangeStatus != fleet.RangeSatisfied || v.LatestCompatible == nil || v.LatestCompatible.Href == "" || v.Range != "^1.1.0" {
		t.Errorf("platform-worker-config = %+v", worker)
	}
	var one ProductOfferingVersions
	getJSON(t, base+"/api/fleet/versions?offering=shared-config&provider=platform-worker-config", http.StatusOK, &one)
	if len(one.Providers) != 1 || one.Providers[0].Versions.Items[0].RangeStatus != "" {
		t.Errorf("one provider, no range = %+v", one.Providers)
	}
	expectStatus(t, base+"/api/fleet/versions?offering=nope", http.StatusNotFound)
	expectStatus(t, base+"/api/fleet/versions?offering=redis&provider=platform-worker-config", http.StatusNotFound)
}

func TestFleetVersionsRoute_BadRange(t *testing.T) {
	q := offeringDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	resp, err := http.Get(base + "/api/fleet/versions?offering=shared-config&range=not-a-range")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "not a semver range") {
		t.Errorf("bad range = %d %s", resp.StatusCode, body)
	}
}

// TestFleetVersionsRoute_RestrictedView: the route answers from the viewer's
// view, so a provider whose targets the viewer cannot see omits InUse.
func TestFleetVersionsRoute_RestrictedView(t *testing.T) {
	q := offeringDashboardQuery(t)
	base := serveOptions(t, Options{
		Fleet: func(context.Context) (*fleet.Query, error) { return q, nil },
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(string) bool { return false }), nil
		},
		Auth: &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }},
	})
	var res ProductOfferingVersions
	getJSON(t, base+"/api/fleet/versions?offering=redis", http.StatusOK, &res)
	if len(res.Providers) != 1 || res.Providers[0].Versions.Items[0].InUse != nil {
		t.Errorf("restricted versions = %+v", res.Providers)
	}
}

func TestFleetEntitiesConsumesParam(t *testing.T) {
	q := offeringDashboardQuery(t)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	var list ProductEntityList
	getJSON(t, base+"/api/fleet/entities?kinds=service&consumes=shared-config", http.StatusOK, &list)
	if list.Total != 1 || !strings.HasSuffix(list.Entities[0].Href, "payments-service") {
		t.Errorf("consumers of shared-config = %+v", list.Entities)
	}
	getJSON(t, base+"/api/fleet/entities?kinds=service&consumes=redis", http.StatusOK, &list)
	if list.Total != 0 {
		t.Errorf("consumers of redis = %+v", list.Entities)
	}
	expectStatus(t, base+"/api/fleet/entities?kinds=revision&consumes=redis", http.StatusUnprocessableEntity)
}
