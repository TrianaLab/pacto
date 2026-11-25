package dashboard

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// fakeCreator records the golden paths the routes hand it.
type fakeCreator struct {
	write  bool
	paths  []CreateServicePath
	inputs []CreateServiceInput
	err    error
}

func (f *fakeCreator) Plan(_ *http.Request, p CreateServicePath, in CreateServiceInput) (*CreateServiceAnswer, error) {
	f.paths, f.inputs = append(f.paths, p), append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	return &CreateServiceAnswer{Plan: &CreateServicePlan{Service: in.Name, Path: p.Ref, IntentDigest: "sha256:i"}, Offerings: []CreateServiceOfferingView{}}, nil
}

func (f *fakeCreator) Accept(_ *http.Request, p CreateServicePath, in CreateServiceAcceptInput) (*CreateServiceAcceptAnswer, error) {
	f.paths, f.inputs = append(f.paths, p), append(f.inputs, in.CreateServiceInput)
	if f.err != nil {
		return nil, f.err
	}
	return &CreateServiceAcceptAnswer{Tag: "0.1.0", Signed: in.Mode == "sign"}, nil
}

func (f *fakeCreator) CanWrite() bool { return f.write }

var createPin = "oci://ghcr.io/acme/http-service@" + validDigest("a")

// createQuery is the http-service golden path pinned by digest, the
// unpinned worker golden path and an ordinary service.
func createQuery(t *testing.T) *fleet.Query {
	t.Helper()
	path := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "http-service", Version: "1.0.0", Template: &contract.Template{}}}
	worker := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "worker", Version: "1.0.0", Template: &contract.Template{}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: path}, Digest: validDigest("a"), ResolvedRef: createPin},
			{Bundle: &contract.Bundle{Contract: worker}, Digest: validDigest("b"), ResolvedRef: "oci://ghcr.io/acme/worker:1.0.0"},
			{Bundle: newPaymentBundle(), Digest: validDigest("c")},
		}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func createServer(t *testing.T, auth *Auth, view func(context.Context, *fleet.Query) (*fleet.Query, error), c ServiceCreator, portal string) string {
	t.Helper()
	q := createQuery(t)
	return serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, View: view, Auth: auth, CreateService: c, PortalCreateURL: portal})
}

func createBody(path string) map[string]any {
	return map[string]any{"path": path, "name": "invoices", "owner": "billing", "namespace": "team-billing"}
}

func acceptBody(path, mode string) map[string]any {
	b := createBody(path)
	b["mode"], b["intentDigest"] = mode, "sha256:i"
	return b
}

func TestCreateServiceRoutesNeedIdentity(t *testing.T) {
	for name, c := range map[string]struct {
		auth *Auth
		code string
	}{
		"open host":     {openAuth(), "IdentityRequired"},
		"signed out":    {viewerAuth(false, AuthState{}), "IdentityRequired"},
		"sign-in link":  {viewerAuth(false, AuthState{SignedIn: true, FleetOnly: true}), "SignInLinkReadsFleet"},
		"no auth layer": {nil, "IdentityRequired"},
	} {
		f := &fakeCreator{write: true}
		base := createServer(t, c.auth, nil, f, "")
		code, out := call(t, http.MethodPost, base+"/api/fleet/services", createBody("http-service"))
		if code != http.StatusForbidden || out["code"] != c.code || len(f.paths) != 0 {
			t.Errorf("%s plan: %d %v", name, code, out)
		}
		code, out = call(t, http.MethodPost, base+"/api/fleet/services/accept", acceptBody("http-service", "sign"))
		if code != http.StatusForbidden || out["code"] != c.code || len(f.paths) != 0 {
			t.Errorf("%s accept: %d %v", name, code, out)
		}
	}
}

func TestCreateServicePathFromView(t *testing.T) {
	f := &fakeCreator{write: true}
	base := createServer(t, viewerAuth(true, AuthState{}), nil, f, "")
	code, out := call(t, http.MethodPost, base+"/api/fleet/services", createBody("http-service"))
	plan, _ := out["plan"].(map[string]any)
	if code != http.StatusOK || plan["path"] != createPin || len(f.paths) != 1 || f.paths[0].Ref != createPin || f.paths[0].View == nil {
		t.Fatalf("%d %v %+v", code, out, f.paths)
	}
	draft := acceptBody("http-service", "sign")
	draft["fromDraft"] = map[string]string{"tag": "draft-1", "digest": validDigest("d")}
	if code, out := call(t, http.MethodPost, base+"/api/fleet/services/accept", draft); code != http.StatusOK || out["tag"] != "0.1.0" ||
		f.paths[1].Ref != createPin || f.inputs[1].FromDraft == nil {
		t.Fatalf("accept from a draft: %d %v", code, out)
	}
}

// TestCreateServicePathRefusedOutsideView: a golden path the caller's view
// does not hold, an unpinned one and a service that is no golden path are
// refused before the creator is asked, a hidden one as a missing one.
func TestCreateServicePathRefusedOutsideView(t *testing.T) {
	f := &fakeCreator{write: true}
	base := createServer(t, viewerAuth(true, AuthState{}), nil, f, "")
	// A view that does not hold the golden path: the route resolves it in
	// the caller's view alone.
	hideAll := func(context.Context, *fleet.Query) (*fleet.Query, error) {
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "local", &fleet.Collection{}))
		return fleet.NewQuery(snap), err
	}
	hidden := createServer(t, viewerAuth(false, AuthState{SignedIn: true}), hideAll, f, "")
	_, missing := call(t, http.MethodPost, base+"/api/fleet/services", createBody("no-such-path"))
	for name, c := range map[string]struct{ base, path string }{
		"hidden":       {hidden, "http-service"},
		"missing":      {base, "no-such-path"},
		"no template":  {base, "payment-service"},
		"unpinned":     {base, "worker"},
		"hidden draft": {hidden, "http-service"},
	} {
		body := createBody(c.path)
		if name == "hidden draft" {
			body["fromDraft"] = map[string]string{"tag": "draft-1", "digest": validDigest("d")}
		}
		code, out := call(t, http.MethodPost, c.base+"/api/fleet/services", body)
		if code != http.StatusNotFound || out["code"] != "GoldenPathNotDeclared" || len(f.paths) != 0 {
			t.Errorf("%s: %d %v", name, code, out)
		}
		if (name == "hidden" || name == "hidden draft") && out["detail"] != missing["detail"] {
			t.Errorf("%s: a hidden path answers %v, a missing one %v", name, out["detail"], missing["detail"])
		}
	}
	if code, out := call(t, http.MethodPost, base+"/api/fleet/services/accept", acceptBody("worker", "sign")); code != http.StatusNotFound || out["code"] != "GoldenPathNotDeclared" {
		t.Errorf("accept of an unpinned path: %d %v", code, out)
	}
}

func TestCreateServiceRouteErrors(t *testing.T) {
	f := &fakeCreator{write: true, err: Refuse(http.StatusForbidden, "NotAllowed", "ana may not create services in team-billing")}
	base := createServer(t, viewerAuth(true, AuthState{}), nil, f, "")
	for _, path := range []string{"/api/fleet/services", "/api/fleet/services/accept"} {
		body := acceptBody("http-service", "draft")
		if path == "/api/fleet/services" {
			body = createBody("http-service")
		}
		if code, out := call(t, http.MethodPost, base+path, body); code != http.StatusForbidden || out["code"] != "NotAllowed" {
			t.Errorf("%s: %d %v", path, code, out)
		}
	}
	broken := serveOptions(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return nil, context.Canceled }, Auth: viewerAuth(true, AuthState{}), CreateService: f})
	if code, _ := call(t, http.MethodPost, broken+"/api/fleet/services", createBody("http-service")); code != http.StatusServiceUnavailable {
		t.Errorf("no snapshot: %d", code)
	}
}

func TestCreateServiceTakesNoReference(t *testing.T) {
	base := createServer(t, viewerAuth(true, AuthState{}), nil, &fakeCreator{write: true}, "")
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	getJSON(t, base+"/openapi.json", http.StatusOK, &spec)
	for _, name := range []string{"CreateServiceInput", "CreateServiceAcceptInput", "CreateServiceOffering"} {
		props, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("no %s schema", name)
		}
		for p := range props.Properties {
			if strings.Contains(strings.ToLower(p), "ref") {
				t.Errorf("%s takes %s", name, p)
			}
		}
	}
}

func TestNoRegistryNoCreateService(t *testing.T) {
	base := createServer(t, viewerAuth(true, AuthState{}), nil, nil, "")
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/services", createBody("http-service")); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("plan route registered without a creator: %d", code)
	}
	var caps struct{ CreateService bool }
	getJSON(t, base+"/api/capabilities", http.StatusOK, &caps)
	if caps.CreateService {
		t.Error("createService without a creator")
	}
	// The search stays the search.
	var search fleet.SearchResult
	getJSON(t, base+"/api/fleet/services?template=only", http.StatusOK, &search)
	if len(search.Services) != 2 {
		t.Errorf("search = %+v", search.Services)
	}
}

func TestCreateServicePlanOnlyWithoutWrite(t *testing.T) {
	f := &fakeCreator{}
	base := createServer(t, viewerAuth(true, AuthState{}), nil, f, "")
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/services", createBody("http-service")); code != http.StatusOK {
		t.Errorf("plan: %d", code)
	}
	if code, _ := call(t, http.MethodPost, base+"/api/fleet/services/accept", acceptBody("http-service", "sign")); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("accept route registered without write: %d", code)
	}
}

func TestCapabilitiesPortalCreateURL(t *testing.T) {
	for _, c := range []struct {
		name   string
		auth   *Auth
		cr     ServiceCreator
		portal string
		want   bool
	}{
		{"local", viewerAuth(true, AuthState{}), &fakeCreator{}, "https://backstage.acme.io/create", true},
		{"signed in", viewerAuth(false, AuthState{SignedIn: true}), &fakeCreator{}, "", true},
		{"sign-in link", viewerAuth(false, AuthState{SignedIn: true, FleetOnly: true}), &fakeCreator{}, "", false},
		{"open", openAuth(), &fakeCreator{}, "https://backstage.acme.io/create", false},
	} {
		base := createServer(t, c.auth, nil, c.cr, c.portal)
		var caps struct {
			CreateService   bool
			PortalCreateURL string
		}
		getJSON(t, base+"/api/capabilities", http.StatusOK, &caps)
		if caps.CreateService != c.want || caps.PortalCreateURL != c.portal {
			t.Errorf("%s: %+v", c.name, caps)
		}
	}
}
