package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const springKey = "template:default/springboot-service"

// templateServer serves a fleet whose catalog lists springboot-service
// (configured, so acceptable) and orders, a name a service already holds,
// with d as the declarer.
func templateServer(t *testing.T, auth *Auth, d Declarer) string {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("paths", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "shop", Kind: "kubernetes", Name: "orders", Service: "orders"}}}),
		fleet.NewMemorySource("catalog", "catalog", &fleet.Collection{ListsTemplates: true, InferredTemplates: []fleet.RawInferredTemplate{
			{Name: "springboot-service", Entity: springKey, From: "Catalog (catalog.acme.io)", Acceptable: true},
			{Name: "orders", Entity: "template:default/orders", From: "Catalog (catalog.acme.io)", Acceptable: true},
		}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	return serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, Auth: auth, Declare: d})
}

func TestDashboardDeclareTemplatePlan(t *testing.T) {
	d := &fakeDeclarer{write: true, accept: func(DeclareAcceptInput) (*DeclareAcceptAnswer, error) {
		return &DeclareAcceptAnswer{Tag: "draft-1"}, nil
	}}
	base := templateServer(t, viewerAuth(true, AuthState{}), d)
	code, out := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": springKey})
	if code != http.StatusOK || len(d.plans) != 1 {
		t.Fatalf("%d %v", code, out)
	}
	w := d.plans[0]
	if w.Key != springKey || w.Name != "springboot-service" || w.Template == nil || !w.Template.Acceptable || w.Namespace != "" || w.Kind != "" {
		t.Errorf("workload %+v", w)
	}
	code, _ = call(t, http.MethodPost, base+"/api/fleet/declare/accept", map[string]any{"serviceKey": springKey, "intentDigest": "sha256:i", "mode": "draft"})
	if len(d.plans) != 2 || d.plans[1].Template == nil || code != http.StatusOK {
		t.Errorf("accept %d %+v", code, d.plans)
	}
}

func TestDashboardDeclareTemplateUnknownIsNotFound(t *testing.T) {
	d := &fakeDeclarer{write: true}
	base := templateServer(t, viewerAuth(true, AuthState{}), d)
	_, missing := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "no-such-svc"})
	code, out := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "template:default/nope"})
	if code != http.StatusNotFound || out["detail"] != missing["detail"] {
		t.Errorf("%d %v, want the missing-service 404 %v", code, out, missing)
	}
	// A name a service holds is listed: the host refuses it, never the route.
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "template:default/orders"}); code != http.StatusOK || !d.plans[0].Template.NameTaken {
		t.Errorf("name taken: %d %+v", code, d.plans)
	}
	// A fleet with no catalog cannot tell: it says why, never "not found".
	d = &fakeDeclarer{write: true}
	base = declareServer(t, viewerAuth(true, AuthState{}), nil, d)
	if code, out := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": springKey}); code != http.StatusConflict ||
		!strings.Contains(fmt.Sprint(out), "TemplateCatalogUnset") || len(d.plans) != 0 {
		t.Errorf("no catalog: %d %v %v", code, out, d.plans)
	}
}

// TestDashboardDeclareTemplateCatalogUnavailable: a catalog that could not be
// read whole leaves a missing template unknown; one past the list cap is
// still found.
func TestDashboardDeclareTemplateCatalogUnavailable(t *testing.T) {
	down := fleet.NewMemorySource("catalog", "catalog", &fleet.Collection{ListsTemplates: true, State: &fleet.SourceState{Status: fleet.SourceUnavailable}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, down)
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	d := &fakeDeclarer{write: true}
	base := serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, Auth: viewerAuth(true, AuthState{}), Declare: d})
	if code, out := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": springKey}); code != http.StatusServiceUnavailable ||
		!strings.Contains(fmt.Sprint(out), "TemplateCatalogUnavailable") || len(d.plans) != 0 {
		t.Errorf("unavailable: %d %v", code, out)
	}
	var raw []fleet.RawInferredTemplate
	for i := range fleet.MaxInferredTemplates + 2 {
		raw = append(raw, fleet.RawInferredTemplate{Name: fmt.Sprintf("t%03d", i), Entity: fmt.Sprintf("template:default/t%03d", i), Acceptable: true})
	}
	snap, err = fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("catalog", "catalog", &fleet.Collection{ListsTemplates: true, InferredTemplates: raw}))
	if err != nil {
		t.Fatal(err)
	}
	q = fleet.NewQuery(snap)
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/declare", map[string]string{"serviceKey": "template:default/t201"}); code != http.StatusOK || len(d.plans) != 1 {
		t.Errorf("past the cap: %d %v", code, d.plans)
	}
}

func TestCapabilitiesDeclareTemplate(t *testing.T) {
	for _, c := range []struct {
		name string
		auth *Auth
		may  bool
		want bool
	}{
		{"allowed", viewerAuth(false, AuthState{SignedIn: true}), true, true},
		{"not allowed", viewerAuth(false, AuthState{SignedIn: true}), false, false},
		{"no declare", openAuth(), true, false},
	} {
		base := declareServer(t, c.auth, nil, &fakeDeclarer{mayTemplate: c.may})
		var caps struct{ DeclareTemplate bool }
		getJSON(t, base+"/api/capabilities", http.StatusOK, &caps)
		if caps.DeclareTemplate != c.want {
			t.Errorf("%s: declareTemplate = %v", c.name, caps.DeclareTemplate)
		}
	}
}
