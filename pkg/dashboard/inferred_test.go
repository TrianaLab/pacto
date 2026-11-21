package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// inferredSnap holds orders, declared by a Pacto target, and cart, a workload
// no contract describes.
func inferredSnap(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "shop", Kind: "kubernetes", Name: "orders", Service: "orders"}}}),
		fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{{Namespace: "shop", Kind: "Deployment", Name: "cart"}}}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestContractShareExcludesInferred(t *testing.T) {
	snap := inferredSnap(t)
	if len(snap.Services) != 2 || declaredServices(snap) != 1 {
		t.Errorf("services %d, with a contract %d", len(snap.Services), declaredServices(snap))
	}
}

func TestStatsLinksAndOfferingsExcludeInferred(t *testing.T) {
	snap := inferredSnap(t)
	l := newStatsLinks(fleet.NewQuery(snap))
	if _, ok := l.keys["k8s:shop/Deployment/cart"]; ok || len(l.keys["orders"]) != 1 {
		t.Errorf("links %v", l.keys)
	}
	delete(snap.Services, fleet.NewServiceKey("orders"))
	if providesOffering(snap, "") {
		t.Error("an inferred service provides an offering")
	}
}

const cartName = "k8s:shop/Deployment/cart"

// inferredServer serves inferredSnap plus a kernel observer that saw cart call
// orders, so cart is an uncontracted caller with an entity.
func inferredServer(t *testing.T) string {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "shop", Kind: "kubernetes", Name: "orders", Service: "orders"}}}),
		fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{{
			Namespace: "shop", Kind: "Deployment", Name: "cart", Image: &fleet.InferredValue{Value: "registry.example/cart:1", From: "spec.template.spec.containers[name=cart].image"}}}}),
		fleet.NewMemorySource("kernel", "observation", &fleet.Collection{Observed: []fleet.ObservedEdge{{From: cartName, To: "orders", Count: 3}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	t.Cleanup(cancel)
	return base
}

// entityKeys are the kind/key of every listed entity.
func entityKeys(t *testing.T, url string) []string {
	t.Helper()
	var l struct {
		Entities []struct{ Kind, Key, Declaration string }
	}
	getJSON(t, url, http.StatusOK, &l)
	var out []string
	for _, it := range l.Entities {
		out = append(out, it.Kind+"/"+it.Key+"/"+it.Declaration)
	}
	return out
}

func TestEntitiesDeclarationQuery(t *testing.T) {
	base := inferredServer(t)
	cart := "service/" + string(fleet.NewServiceKey(cartName)) + "/inferred"
	if got := entityKeys(t, base+"/api/fleet/entities?declaration=inferred&kinds=service"); !slices.Equal(got, []string{cart}) {
		t.Errorf("inferred services %v", got)
	}
	if got := entityKeys(t, base+"/api/fleet/entities?declaration=declared&kinds=service"); slices.Contains(got, cart) || len(got) != 1 {
		t.Errorf("declared services %v", got)
	}
	expectStatus(t, base+"/api/fleet/entities?declaration=bogus", http.StatusUnprocessableEntity)
}

func TestEntitiesDeclarationWithoutKindsListsServices(t *testing.T) {
	base := inferredServer(t)
	got := entityKeys(t, base+"/api/fleet/entities?declaration=inferred")
	if len(got) != 1 || !strings.HasPrefix(got[0], "service/") {
		t.Errorf("declaration=inferred with no kinds %v", got)
	}
}

func TestServicesSearchDeclarationQuery(t *testing.T) {
	base := inferredServer(t)
	for decl, want := range map[string]string{"inferred": cartName, "declared": "orders"} {
		var res fleet.SearchResult
		getJSON(t, base+"/api/fleet/services?declaration="+decl, http.StatusOK, &res)
		if len(res.Services) != 1 || res.Services[0].Name != want || res.Services[0].Declaration != decl {
			t.Errorf("declaration=%s: %+v", decl, res.Services)
		}
	}
	expectStatus(t, base+"/api/fleet/services?declaration=bogus", http.StatusUnprocessableEntity)
}

func TestEntityDetailInferredServiceHasWorkloadAndCalls(t *testing.T) {
	base := inferredServer(t)
	var d struct {
		Service struct {
			Workload *fleet.InferredWorkload
			Calls    struct {
				Total int
				Items []struct{ Key, Href string }
			}
			CallsObserved *bool
		}
	}
	getJSON(t, base+"/api/fleet/entities/service?key="+url.QueryEscape(string(fleet.NewServiceKey(cartName))), http.StatusOK, &d)
	s := d.Service
	if s.Workload == nil || s.Workload.Image == nil || s.Workload.Image.Value != "registry.example/cart:1" {
		t.Errorf("workload %+v", s.Workload)
	}
	if s.CallsObserved == nil || !*s.CallsObserved || len(s.Calls.Items) != 1 || s.Calls.Items[0].Href == "" {
		t.Errorf("calls %+v observed %v", s.Calls, s.CallsObserved)
	}
	var v struct {
		Service struct {
			Declaration string
			Workload    *fleet.InferredWorkload
		}
	}
	getJSON(t, base+"/api/fleet/service?key="+url.QueryEscape(cartName), http.StatusOK, &v)
	if v.Service.Declaration != fleet.DeclarationInferred || v.Service.Workload == nil {
		t.Errorf("service view %+v", v.Service)
	}
}

func TestAttentionCallerHasHref(t *testing.T) {
	base := inferredServer(t)
	var l struct {
		Items []struct {
			Code   string
			Caller *struct{ Key, Href, Declaration string }
		}
	}
	getJSON(t, base+"/api/fleet/attention", http.StatusOK, &l)
	for _, it := range l.Items {
		if it.Caller != nil {
			if it.Caller.Key != string(fleet.NewServiceKey(cartName)) || it.Caller.Href == "" || it.Caller.Declaration != fleet.DeclarationInferred {
				t.Errorf("caller %+v", it.Caller)
			}
			return
		}
	}
	t.Errorf("no attention item names its caller: %+v", l.Items)
}

func TestMetricsCountInferredServices(t *testing.T) {
	var metrics struct {
		ServiceCount int `json:"serviceCount"`
	}
	getJSON(t, inferredServer(t)+"/metrics", http.StatusOK, &metrics)
	if metrics.ServiceCount != 2 {
		t.Errorf("metrics count %d services, want orders and the inferred cart", metrics.ServiceCount)
	}
}

// TestDashboardNextActionHasNoTool: the dashboard serves no MCP tool, so its
// attention, overview and entity answers carry each nextAction's verb and CLI
// command and never a tool.
func TestDashboardNextActionHasNoTool(t *testing.T) {
	base := inferredServer(t)
	for _, path := range []string{"/api/fleet/attention", "/api/fleet/overview", "/api/fleet/entities/service?key=" + url.QueryEscape(string(fleet.NewServiceKey(cartName)))} {
		var body any
		getJSON(t, base+path, http.StatusOK, &body)
		raw, _ := json.Marshal(body)
		if s := string(raw); !strings.Contains(s, `"nextAction":{"cli":"pacto declare cart --namespace shop","verb":"declare"}`) || strings.Contains(s, `"tool"`) {
			t.Errorf("%s: %s", path, s)
		}
	}
}
