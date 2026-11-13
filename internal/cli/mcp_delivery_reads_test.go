package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var readRoutes = []string{"caller", "inbox", "environments", "autonomy", "published", "refusals"}

// readDeps is deliveryDeps whose cluster serves delivery, keeps no records
// and holds a second Stage, in staging. tune adjusts each caller client.
func readDeps(tune func(*dynamicfake.FakeDynamicClient)) deps {
	d := deliveryDeps("alice", nil)
	d.newRecordRepo = nil
	d.clusterConfig = func() (*rest.Config, error) {
		return &rest.Config{Host: "https://k", BearerToken: "process-token"}, nil
	}
	d.served = func(*rest.Config, string) (bool, error) { return true, nil }
	plain := d.newDynamic
	d.newDynamic = func() (dynamic.Interface, error) {
		c, err := plain()
		f := c.(*dynamicfake.FakeDynamicClient)
		st := u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "orders-staging", map[string]any{
			"spec": map[string]any{"service": map[string]any{"name": "orders"}, "applyRef": map[string]any{"name": "orders"}, "environment": "staging"}})
		st.SetNamespace("staging")
		_, _ = f.Resource(schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}).
			Namespace("staging").Create(context.Background(), st, metav1.CreateOptions{})
		if tune != nil {
			tune(f)
		}
		return f, err
	}
	return d
}

// actFor is a caller's delivery client over d's cluster.
func actFor(t *testing.T, d deps) *app.Delivery {
	t.Helper()
	dyn, err := d.newDynamic()
	expect(t, err == nil, "client: %v", err)
	return &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry, Policy: delivery.DefaultClassPolicy(), PolicyNamespace: delivery.PolicyNamespace}
}

// mcpReadServer is `pacto mcp --delivery` over stdio with flags, built as
// RunE builds it.
func mcpReadServer(t *testing.T, d deps, flags map[string]string) *mcpsdk.ClientSession {
	t.Helper()
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(context.Background())
	cmd.SetErr(io.Discard)
	expect(t, cmd.Flags().Set("delivery", "true") == nil, "delivery flag")
	for k, v := range flags {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	server, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil, d)
	expect(t, err == nil, "server: %v", err)
	return sessionOn(t, server)
}

func sessionOn(t *testing.T, server *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	ct, st := mcpsdk.NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	expect(t, err == nil, "server connect: %v", err)
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	expect(t, err == nil, "client connect: %v", err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callText calls tool and returns its text and whether it is an error.
func callText(t *testing.T, s *mcpsdk.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	expect(t, err == nil, "%s: %v", tool, err)
	return res.Content[0].(*mcpsdk.TextContent).Text, res.IsError
}

// routeBody is GET path on h.
func routeBody(t *testing.T, h http.Handler, ctx context.Context, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx))
	return rec.Body.String()
}

// sameAnswer compares two JSON answers without the keys drop names (times
// each read takes and dashboard links).
func sameAnswer(t *testing.T, what, a, b string, drop ...string) {
	t.Helper()
	var x, y any
	expect(t, json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil, "%s: not JSON:\n%s\n%s", what, a, b)
	strip(x, drop)
	strip(y, drop)
	expect(t, reflect.DeepEqual(x, y), "%s differs:\nmcp:   %s\nroute: %s", what, a, b)
}

func strip(v any, drop []string) {
	switch v := v.(type) {
	case map[string]any:
		for _, k := range drop {
			delete(v, k)
		}
		for _, e := range v {
			strip(e, drop)
		}
	case []any:
		for _, e := range v {
			strip(e, drop)
		}
	}
}

func TestDeliveryReadToolsMatchRoutes(t *testing.T) {
	d := readDeps(nil)
	session := mcpReadServer(t, d, nil)
	id := baseDashboardIdentity(&rest.Config{Host: "https://k"}, d, nil, delivery.PolicyNamespace)
	api := newDashDelivery(id, actFor(t, d), "")
	h := dashboard.NewServer(dashboard.Options{Delivery: func(*http.Request) (dashboard.DeliveryAPI, error) { return api, nil }}).APIHandler()
	for _, route := range readRoutes {
		text, isErr := callText(t, session, "pacto_delivery_"+route, map[string]any{})
		expect(t, !isErr, "%s: %s", route, text)
		sameAnswer(t, route, text, routeBody(t, h, context.Background(), "/api/delivery/"+route), "asOf", "authTime")
	}
	// Arguments reach the route as its query.
	text, _ := callText(t, session, "pacto_delivery_environments", map[string]any{"environment": "staging"})
	sameAnswer(t, "environments?environment=staging", text, routeBody(t, h, context.Background(), "/api/delivery/environments?environment=staging"), "asOf")
	expect(t, strings.Contains(text, "orders-staging") && !strings.Contains(text, "payments-prod"), "the filter applies: %s", text)
}

// listing is the part of a list answer that says how complete it is.
type listing struct {
	AsOf         string   `json:"asOf"`
	Completeness string   `json:"completeness"`
	Unreadable   []string `json:"unreadable"`
}

func listingOf(t *testing.T, text string) listing {
	t.Helper()
	var l listing
	expect(t, json.Unmarshal([]byte(text), &l) == nil, "not a listing: %s", text)
	return l
}

func TestDeliveryReadsCarryListing(t *testing.T) {
	session := mcpReadServer(t, readDeps(nil), nil)
	for _, route := range []string{"inbox", "environments", "published"} {
		text, _ := callText(t, session, "pacto_delivery_"+route, map[string]any{})
		l := listingOf(t, text)
		expect(t, l.AsOf != "" && l.Completeness == "complete", "%s: %+v", route, l)
	}
	forbidProd := func(f *dynamicfake.FakeDynamicClient) {
		f.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetNamespace() == "prod" {
				return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "stages"}, "", errors.New("no"))
			}
			return false, nil, nil
		})
	}
	session = mcpReadServer(t, readDeps(forbidProd), map[string]string{"namespace": "prod"})
	for _, route := range []string{"inbox", "environments", "published"} {
		text, _ := callText(t, session, "pacto_delivery_"+route, map[string]any{})
		l := listingOf(t, text)
		expect(t, l.Completeness == "partial" && slices.Equal(l.Unreadable, []string{"prod"}), "%s: %+v", route, l)
	}
}

func TestDeliveryReadErrorIsToolError(t *testing.T) {
	d := readDeps(nil)
	d.served = func(*rest.Config, string) (bool, error) { return false, nil }
	text, isErr := callText(t, mcpReadServer(t, d, nil), "pacto_delivery_inbox", map[string]any{})
	expect(t, isErr && strings.Contains(text, "DeliveryNotInstalled") && strings.Contains(text, "delivery.enabled=true"), "not installed: %s", text)
	// A refusal huma raises itself (a bad query) keeps its title.
	err := routeError(&bodyRecorder{code: http.StatusUnprocessableEntity})
	expect(t, err.Error() == "Unprocessable Entity: ", "status text: %v", err)
}

// stageLists records with which bearer token every Stage list was asked.
type stageLists struct {
	mu     sync.Mutex
	tokens []string
}

func (s *stageLists) record(token string) func(*dynamicfake.FakeDynamicClient) {
	return func(f *dynamicfake.FakeDynamicClient) {
		f.PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
			s.mu.Lock()
			s.tokens = append(s.tokens, token)
			s.mu.Unlock()
			return false, nil, nil
		})
	}
}

func TestRemoteDeliveryReadsUseCallerToken(t *testing.T) {
	lists := &stageLists{}
	d := readDeps(nil)
	d.dynamicFor = func(c *rest.Config) (dynamic.Interface, error) {
		return readDeps(lists.record(c.BearerToken)).newDynamic()
	}
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(context.Background())
	expect(t, cmd.Flags().Set("delivery", "true") == nil, "flag")
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	expect(t, remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s) == nil, "remote delivery")
	rec := post(t, mcpHandler(s), "good", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_delivery_environments","arguments":{}}}`)
	expect(t, rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "payments-prod"), "environments: %d %s", rec.Code, rec.Body)
	expect(t, len(lists.tokens) > 0, "no Stage was listed")
	for _, tok := range lists.tokens {
		expect(t, tok == "for-the-cluster", "a Stage list went with %q, not the caller's exchanged token", tok)
	}
}

func TestDeliveryReadToolsMirrorRouteParams(t *testing.T) {
	raw, err := dashboard.ExportOpenAPI()
	expect(t, err == nil, "openapi: %v", err)
	var doc struct {
		Paths map[string]map[string]struct {
			Description string `json:"description"`
			Parameters  []struct {
				Name        string         `json:"name"`
				In          string         `json:"in"`
				Description string         `json:"description"`
				Schema      map[string]any `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	err = json.Unmarshal(raw, &doc)
	expect(t, err == nil, "decode openapi: %v", err)
	reads := func(context.Context, string, url.Values) (json.RawMessage, error) { return nil, nil }
	server, err := pactomcp.New("v", pactomcp.Authoring{}, pactomcp.Modes{Delivery: &pactomcp.DeliveryMode{Reads: reads}})
	expect(t, err == nil, "server: %v", err)
	res, err := sessionOn(t, server).ListTools(context.Background(), nil)
	expect(t, err == nil, "list: %v", err)
	tools := map[string]*mcpsdk.Tool{}
	for _, tl := range res.Tools {
		tools[tl.Name] = tl
	}
	for _, route := range readRoutes {
		op := doc.Paths["/api/delivery/"+route]["get"]
		tool := tools["pacto_delivery_"+route]
		expect(t, tool != nil && strings.Contains(tool.Description, op.Description), "%s: description %q lacks %q", route, tool.Description, op.Description)
		props, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		expect(t, len(props) == len(op.Parameters), "%s: %d arguments, the route takes %d", route, len(props), len(op.Parameters))
		for _, p := range op.Parameters {
			got, _ := props[p.Name].(map[string]any)
			expect(t, p.In == "query" && got != nil && got["description"] == p.Description, "%s %s: %v, want %q", route, p.Name, got, p.Description)
			typ := slices.DeleteFunc(jsonStrings(p.Schema["type"]), func(s string) bool { return s == "null" })[0]
			expect(t, slices.Contains(jsonStrings(got["type"]), typ), "%s %s: type %v, want %s", route, p.Name, got["type"], typ)
			expect(t, slices.Equal(slices.DeleteFunc(jsonStrings(got["enum"]), func(s string) bool { return s == "" }), jsonStrings(p.Schema["enum"])),
				"%s %s: enum %v, want %v", route, p.Name, got["enum"], p.Schema["enum"])
			if typ == "array" {
				expect(t, reflect.DeepEqual(got["items"], p.Schema["items"]), "%s %s: items %v, want %v", route, p.Name, got["items"], p.Schema["items"])
			}
		}
	}
}

// jsonStrings reads a JSON string or list of strings, null as "".
func jsonStrings(v any) []string {
	var out []string
	switch v := v.(type) {
	case string:
		out = append(out, v)
	case []any:
		for _, e := range v {
			s, _ := e.(string)
			out = append(out, s)
		}
	}
	return out
}

func TestMCPDeliveryReadsScopeFollowsNamespaceFlag(t *testing.T) {
	namespaces := func(flags map[string]string) []string {
		text, _ := callText(t, mcpReadServer(t, readDeps(nil), flags), "pacto_delivery_environments", map[string]any{})
		var b struct {
			Environments []struct{ Namespace string } `json:"environments"`
		}
		expect(t, json.Unmarshal([]byte(text), &b) == nil, "environments: %s", text)
		var out []string
		for _, e := range b.Environments {
			out = append(out, e.Namespace)
		}
		slices.Sort(out)
		return out
	}
	expect(t, slices.Equal(namespaces(nil), []string{"prod", "staging"}), "default flags: %v", namespaces(nil))
	expect(t, slices.Equal(namespaces(map[string]string{"namespace": "prod"}), []string{"prod"}), "--namespace prod")
	expect(t, slices.Equal(namespaces(map[string]string{"delivery-namespace": "other"}), []string{"prod", "staging"}), "--delivery-namespace alone narrows nothing")
}

// secretFleet: payments (team-payments) runs in prod, vault (team-secret) in
// secret.
func secretFleet(t *testing.T) *fleet.Query {
	t.Helper()
	rev := func(name, team string) fleet.RawRevision {
		return fleet.RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService,
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}}}, FS: fstest.MapFS{}},
			Digest: "sha256:" + strings.Repeat(name[:1], 64)}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Revisions: []fleet.RawRevision{rev("payments", "team-payments"), rev("vault", "team-secret")},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: fleet.SourceKindKubernetes, Name: "payments", Service: "payments"},
			{Scope: "secret", Kind: fleet.SourceKindKubernetes, Name: "vault", Service: "vault"}},
	}))
	expect(t, err == nil, "fleet: %v", err)
	return fleet.NewQuery(snap)
}

// blindDeps: the caller may not list Stages cluster-wide, nor in secret,
// and the server may not list namespaces, so the listing falls back to the
// namespaces of the caller's fleet view.
func blindDeps() deps {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "stages"}, "", errors.New("no"))
	d := readDeps(func(f *dynamicfake.FakeDynamicClient) {
		f.PrependReactor("list", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if ns := a.GetNamespace(); ns == "" || ns == "secret" {
				return true, nil, forbidden
			}
			return false, nil, nil
		})
	})
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) {
		c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvrNamespaces: "NamespaceList"})
		c.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbidden })
		return c, nil
	}
	return d
}

func TestRemoteDeliveryReadsNarrowedByFleetView(t *testing.T) {
	d := blindDeps()
	q := secretFleet(t)
	view := func(ctx context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		if _, ok := personFrom(ctx); !ok {
			return nil, errNobodyReviewed
		}
		return q.Restrict(func(ns string) bool { return ns == "prod" }), nil
	}
	fm := &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return q, nil }, Opts: pactomcp.FleetOptions{View: view}}
	id := baseDashboardIdentity(&rest.Config{Host: "https://k"}, d, nil, delivery.PolicyNamespace)
	act, dave := actFor(t, d), authn.Person{User: "dave", Groups: []string{"team-payments"}}
	reads := mcpDeliveryReads(id, fm, "")(act, remoteCallerKey(dave))
	ctx := context.WithValue(context.Background(), reviewedKey{}, reviewed{person: dave})
	h := dashboard.NewServer(dashboard.Options{Fleet: fm.Src,
		View: func(ctx context.Context, q *fleet.Query) (*fleet.Query, error) { return view(ctx, nil, q) },
		Delivery: func(*http.Request) (dashboard.DeliveryAPI, error) {
			return newDashDelivery(id, act, remoteCallerKey(dave)), nil
		}}).APIHandler()
	for _, route := range []string{"inbox", "environments"} {
		body, err := reads(ctx, route, nil)
		expect(t, err == nil, "%s: %v", route, err)
		l := listingOf(t, string(body))
		expect(t, l.Completeness == "partial" && !slices.Contains(l.Unreadable, "secret") && !strings.Contains(string(body), "team-secret"),
			"%s names what dave cannot read: %s", route, body)
		sameAnswer(t, route, string(body), routeBody(t, h, ctx, "/api/delivery/"+route), "asOf")
	}
	_, err := reads(context.Background(), "inbox", nil)
	expect(t, errors.Is(err, errNobodyReviewed), "a view with nobody to narrow for fails the call: %v", err)

	// Without the view the hidden namespace is asked about and named.
	body, err := mcpDeliveryReads(id, &pactomcp.FleetMode{Src: fm.Src}, "")(act, "")(context.Background(), "inbox", nil)
	expect(t, err == nil && slices.Contains(listingOf(t, string(body)).Unreadable, "secret"), "the control case: %v %s", err, body)
}

func TestRemoteDeliveryReadsDoNotShareNamespaceCache(t *testing.T) {
	allowed := map[string]string{"alice": "prod", "bob": "staging"}
	d := readDeps(nil)
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) {
		c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvrNamespaces: "NamespaceList"})
		c.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
			l := &unstructured.UnstructuredList{}
			for _, n := range []string{"prod", "staging"} {
				l.Items = append(l.Items, unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": n}}})
			}
			return true, l, nil
		})
		return c, nil
	}
	id := baseDashboardIdentity(&rest.Config{Host: "https://k"}, d, nil, delivery.PolicyNamespace)
	reads := mcpDeliveryReads(id, nil, "")
	listed := func(user string) []string {
		caller := readDeps(func(f *dynamicfake.FakeDynamicClient) {
			f.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
				if a.GetNamespace() == "" {
					return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "stages"}, "", errors.New("no"))
				}
				return false, nil, nil
			})
			f.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
				ns, _, _ := unstructured.NestedString(a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).Object, "spec", "resourceAttributes", "namespace")
				return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": ns == allowed[user]}}}, nil
			})
		})
		p := authn.Person{User: user, Groups: []string{"team-" + user}}
		body, err := reads(actFor(t, caller), remoteCallerKey(p))(context.Background(), "environments", nil)
		expect(t, err == nil, "%s: %v", user, err)
		var b struct {
			Environments []struct{ Namespace string } `json:"environments"`
		}
		_ = json.Unmarshal(body, &b)
		var out []string
		for _, e := range b.Environments {
			out = append(out, e.Namespace)
		}
		return out
	}
	expect(t, slices.Equal(listed("alice"), []string{"prod"}), "alice reads prod only")
	expect(t, slices.Equal(listed("bob"), []string{"staging"}), "bob reads staging only, never alice's cached answer")
	expect(t, len(id.stageNS) == 2, "one cache entry per caller: %d", len(id.stageNS))
	expect(t, remoteCallerKey(authn.Person{User: "a", Groups: []string{"x", "y"}}) == remoteCallerKey(authn.Person{User: "a", Groups: []string{"y", "x"}}) &&
		remoteCallerKey(authn.Person{User: "a"}) != remoteCallerKey(authn.Person{User: "a", Groups: []string{"x"}}) && remoteCallerKey(authn.Person{}) != "",
		"the key is the identity, stable and never empty")
}

func TestMCPDashboardIdentityIsComplete(t *testing.T) {
	d := readDeps(nil)
	id := baseDashboardIdentity(&rest.Config{Host: "https://k"}, d, nil, "pacto-delivery")
	expect(t, id.newClient != nil && id.now != nil && id.registry != nil && id.served != nil && id.state != nil && id.reads != nil &&
		id.revisions != nil && id.stageNS != nil && id.policyNS == "pacto-delivery", "incomplete identity: %+v", id)
	_, err := newDashDelivery(id, actFor(t, d), "").StageNamespaces(context.Background())
	expect(t, err != nil, "the fake cannot list namespaces, and says so without panicking")
}

// docFleet is payments with docs/overview.md.
func docFleet(t *testing.T) (*fleet.Query, string) {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "local", &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService,
			Service: contract.Service{Name: "payments", Version: "1.0.0"}}, FS: fstest.MapFS{"docs/overview.md": {Data: []byte("# Payments\n")}}},
			Digest: "sha256:" + strings.Repeat("a", 64)}}}))
	expect(t, err == nil, "fleet: %v", err)
	q := fleet.NewQuery(snap)
	v, err := q.GetService("payments")
	expect(t, err == nil, "service: %v", err)
	return q, string(v.Revisions[0].Key)
}

func TestFleetRevisionDocumentMatchesRoute(t *testing.T) {
	q, key := docFleet(t)
	src := func(context.Context) (*fleet.Query, error) { return q, nil }
	server, err := pactomcp.New("v", pactomcp.Authoring{}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{Src: src}})
	expect(t, err == nil, "server: %v", err)
	session := sessionOn(t, server)
	h := dashboard.NewServer(dashboard.Options{Fleet: src}).APIHandler()
	for _, path := range []string{"docs/overview.md", "docs/absent.md"} {
		text, isErr := callText(t, session, "pacto_fleet_revision_document", map[string]any{"key": key, "path": path})
		route := routeBody(t, h, context.Background(), "/api/fleet/revisions/document?key="+url.QueryEscape(key)+"&path="+url.QueryEscape(path))
		if isErr {
			expect(t, path == "docs/absent.md" && strings.Contains(route, "not found") && strings.Contains(text, "not found"), "%s: %s / %s", path, text, route)
			continue
		}
		sameAnswer(t, path, text, route, "href")
	}
}

// TestRemoteBundleThatCannotRegisterFailsAtStartup: a remote server checks
// its bundle once, before any request.
func TestRemoteBundleThatCannotRegisterFailsAtStartup(t *testing.T) {
	d := readDeps(nil)
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(context.Background())
	expect(t, cmd.Flags().Set("delivery", "true") == nil, "flag")
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	err := remoteCallerFor(cmd, app.NewService(nil, nil), "v", []string{writeCapabilityBundle(t)}, d, &s)
	wantErr(t, err, "no base URL")
}
