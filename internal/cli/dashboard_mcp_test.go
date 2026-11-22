package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

const (
	dashIssuer = "https://idp.example"
	agentSA    = "system:serviceaccount:agents:bot"
)

// issuerJWT is an ID token shape naming dashIssuer, unsigned: the fake
// verifiers never check a signature.
var issuerJWT = "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"`+dashIssuer+`","sub":"carol"}`)) + ".s"

// fakeIssuerAuth is the issuer path's verifier: it accepts any token and
// exchanges it for "issuer-exchanged".
type fakeIssuerAuth struct {
	mu       sync.Mutex
	verified []string
}

func (f *fakeIssuerAuth) Verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verified = append(f.verified, token)
	return &auth.TokenInfo{UserID: dashIssuer + "#carol", Expiration: time.Now().Add(time.Hour)}, nil
}

func (f *fakeIssuerAuth) CanExchange() bool { return true }

func (f *fakeIssuerAuth) Exchange(context.Context, *auth.TokenInfo) (string, error) {
	return "issuer-exchanged", nil
}

// dashMCPWorld is a dashboard with token sign-in over a fake cluster: the
// bearer "viewer" is an audience-only ServiceAccount token, "agent" a
// dual-audience one, "person" a person's token, and "issuer-exchanged" or
// issuerJWT name carol. "dashboard" is the dashboard's own credential.
type dashMCPWorld struct {
	v        *viper.Viper
	d        deps
	id       *dashboardIdentity
	issuer   *fakeIssuerAuth
	mcpCfg   authn.MCPConfig
	readers  map[string]bool
	mu       sync.Mutex
	tokens   []string // the bearer of every client built
	reviewed []string // every token a TokenReview body carried
	created  []string // the bearer of every client that created an Operation
	outside  map[string]http.Handler
	fleet    *fleet.Query // nil: viewFleet
}

func (w *dashMCPWorld) userOf(token string) string {
	switch token {
	case "agent":
		return agentSA
	case "person":
		return "alice"
	case "issuer-exchanged", issuerJWT:
		return dashIssuer + "#carol"
	}
	return ""
}

func (w *dashMCPWorld) dynamicFor(c *rest.Config) (dynamic.Interface, error) {
	w.mu.Lock()
	w.tokens = append(w.tokens, c.BearerToken)
	w.mu.Unlock()
	user := w.userOf(c.BearerToken)
	dyn, _ := deliveryDeps(user, nil).newDynamic()
	f := dyn.(*dynamicfake.FakeDynamicClient)
	if user == "" {
		f.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewUnauthorized("Unauthorized")
		})
	}
	f.PrependReactor("create", "tokenreviews", w.tokenReview)
	f.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		who, _, _ := unstructured.NestedString(a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).Object, "spec", "user")
		w.mu.Lock()
		defer w.mu.Unlock()
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": w.readers[who]}}}, nil
	})
	f.PrependReactor("create", "operations", func(k8stesting.Action) (bool, runtime.Object, error) {
		w.mu.Lock()
		w.created = append(w.created, c.BearerToken)
		w.mu.Unlock()
		return false, nil, nil
	})
	return f, nil
}

// tokenReview accepts the two ServiceAccount tokens for pacto-dashboard.
func (w *dashMCPWorld) tokenReview(a k8stesting.Action) (bool, runtime.Object, error) {
	obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
	token, _, _ := unstructured.NestedString(obj.Object, "spec", "token")
	w.mu.Lock()
	w.reviewed = append(w.reviewed, token)
	w.mu.Unlock()
	users := map[string]string{"viewer": viewerSA, "agent": agentSA}
	if users[token] == "" {
		obj.Object["status"] = map[string]any{"authenticated": false}
		return true, obj, nil
	}
	obj.Object["status"] = map[string]any{"authenticated": true, "audiences": []any{authn.DashboardAudience},
		"user": map[string]any{"username": users[token]}}
	return true, obj, nil
}

// newDashMCPWorld is the world's identity, its viewer narrowed by readers;
// tune sets more dashboard flags first.
func newDashMCPWorld(t *testing.T, tune func(v *viper.Viper)) *dashMCPWorld {
	t.Helper()
	w := &dashMCPWorld{v: viper.New(), issuer: &fakeIssuerAuth{},
		readers: map[string]bool{viewerSA: true, agentSA: true, "alice": true, dashIssuer + "#carol": true}}
	w.v.Set("dashboard.token-login", true)
	w.v.Set("dashboard.mcp", true)
	if tune != nil {
		tune(w.v)
	}
	w.d = deliveryDeps("", nil)
	w.d.dynamicFor = w.dynamicFor
	w.d.served = func(*rest.Config, string) (bool, error) { return true, nil }
	w.d.newMCPAuth = func(c authn.MCPConfig) (mcpAuth, error) { w.mcpCfg = c; return w.issuer, nil }
	cluster := &rest.Config{Host: "https://k", BearerToken: "dashboard"}
	id, err := newDashboardIdentity(w.v, "0.0.0.0", cluster, homeEntries(cluster), w.d)
	expect(t, err == nil, "identity: %v", err)
	id.viewer = authn.NewViewer(oneReview(func(_ context.Context, p authn.Person, _ string, _ authn.ResourceAttributes) (bool, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.readers[p.User], nil
	}))
	w.id = id
	return w
}

func (w *dashMCPWorld) build(t *testing.T) (map[string]http.Handler, error) {
	t.Helper()
	q := w.fleet
	if q == nil {
		q = viewFleet(t)
	}
	return newDashboardMCP(w.v, app.NewService(nil, nil), "v", nil, w.id, func(context.Context) (*fleet.Query, error) { return q, nil }, nil, w.d)
}

// mcp is the world's /mcp handler.
func (w *dashMCPWorld) mcp(t *testing.T) http.Handler {
	t.Helper()
	outside, err := w.build(t)
	expect(t, err == nil, "dashboard mcp: %v", err)
	w.outside = outside
	return outside["/mcp"]
}

func (w *dashMCPWorld) clientTokens() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.tokens)
}

func newDashMCP(t *testing.T) (*dashMCPWorld, http.Handler) {
	t.Helper()
	w := newDashMCPWorld(t, nil)
	return w, w.mcp(t)
}

// TestDashboardMCPViewerTokenIsFleetOnly: the NOTES pacto-viewer token reads
// the fleet narrowed to it, with no client, no delivery tool and no write.
func TestDashboardMCPViewerTokenIsFleetOnly(t *testing.T) {
	w, h := newDashMCP(t)
	names := remoteToolNames(t, h, "viewer")
	expect(t, slices.Contains(names, "pacto_fleet_status") && !hasPrefixed(names, "pacto_delivery_") && !slices.Contains(names, "pacto_declare"),
		"viewer tools: %v", names)
	caller := remoteCapabilitiesCaller(t, h, "viewer")
	expect(t, caller["fleetOnly"] == true && caller["writes"] == false && caller["user"] == viewerSA, "viewer caller: %v", caller)
	text, isErr := remoteCall(t, h, "viewer", "pacto_fleet_entities", "{}")
	expect(t, !isErr && strings.Contains(text, "web"), "the viewer reads the fleet: %s", text)
	// One client, for the token review's bearer check; its verdict is reused
	// and no request builds the viewer a client of its own.
	var built int
	for _, tok := range w.clientTokens() {
		if tok == "viewer" {
			built++
		}
	}
	expect(t, built == 1, "clients built from the viewer's token: %d", built)
}

// TestDashboardMCPToolsPerCaller: a dual-audience ServiceAccount proposes and
// cancels, a person reads, and nobody gets an approve tool.
func TestDashboardMCPToolsPerCaller(t *testing.T) {
	_, h := newDashMCP(t)
	agent := remoteToolNames(t, h, "agent")
	expect(t, slices.Contains(agent, "pacto_delivery_operation_propose") && slices.Contains(agent, "pacto_delivery_cancel"), "agent tools: %v", agent)
	person := remoteToolNames(t, h, "person")
	expect(t, !slices.Contains(person, "pacto_delivery_operation_propose") && slices.Contains(person, "pacto_delivery_get"), "person tools: %v", person)
	for _, names := range [][]string{agent, person, remoteToolNames(t, h, "viewer")} {
		expect(t, !slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, "approve") }), "an approve tool: %v", names)
	}
	rec := post(t, h, "", listTools)
	expect(t, rec.Code == http.StatusUnauthorized, "no bearer: %d", rec.Code)
	rec = post(t, h, "stranger", listTools)
	expect(t, rec.Code == http.StatusUnauthorized, "a token neither check accepts: %d", rec.Code)
}

// TestDashboardMCPProposeUsesCallerToken: the Operation is created by the
// client built from the agent's own token, never the dashboard's.
func TestDashboardMCPProposeUsesCallerToken(t *testing.T) {
	w, h := newDashMCP(t)
	body := post(t, h, "agent", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pacto_delivery_operation_propose","arguments":{`+
		`"namespace":"prod","stage":"payments-prod","to":"`+cliB+`","on_behalf_of":"erin"}}}`).Body.String()
	expect(t, strings.Contains(body, `\"user\": \"`+agentSA+`\"`), "propose: %s", body)
	w.mu.Lock()
	defer w.mu.Unlock()
	expect(t, len(w.created) > 0 && !slices.ContainsFunc(w.created, func(tok string) bool { return tok != "agent" }), "creators: %v", w.created)
}

// TestDashboardMCPFleetAnswerCarriesProvenance: fleet answers over /mcp read
// the dashboard's fleet narrowed by its viewer and keep asOf and completeness.
func TestDashboardMCPFleetAnswerCarriesProvenance(t *testing.T) {
	w := newDashMCPWorld(t, nil)
	w.readers["alice"] = false
	h := w.mcp(t)
	text, isErr := remoteCall(t, h, "person", "pacto_fleet_status", "{}")
	expect(t, !isErr && strings.Contains(text, `"asOf"`) && strings.Contains(text, `"completeness"`), "status: %s", text)
	text, _ = remoteCall(t, h, "person", "pacto_fleet_entities", "{}")
	expect(t, !strings.Contains(text, `"web"`), "the view narrows to alice: %s", text)
	text, _ = remoteCall(t, h, "agent", "pacto_fleet_entities", "{}")
	expect(t, strings.Contains(text, `"web"`), "the agent reads web: %s", text)
}

// TestDashboardMCPInferredRowsKeepProvenance: an inferred service reaches an
// agent over the dashboard's /mcp marked inferred, with a partial answer, and
// a caller the view hides never reads it.
func TestDashboardMCPInferredRowsKeepProvenance(t *testing.T) {
	w := newDashMCPWorld(t, nil)
	w.fleet = inferredQuery(t, true)
	w.readers["alice"] = false
	h := w.mcp(t)
	text, isErr := remoteCall(t, h, "viewer", "pacto_fleet_entities", `{"declaration":"inferred"}`)
	expect(t, !isErr && strings.Contains(text, billingName) && strings.Contains(text, `"declaration": "inferred"`), "viewer reads the inferred row: %s", text)
	text, _ = remoteCall(t, h, "viewer", "pacto_fleet_graph", `{"service":"`+billingName+`"}`)
	expect(t, strings.Contains(text, `"completeness": "partial"`) && strings.Contains(text, string(fleet.LimitationServiceInferred)), "an inferred graph is partial: %s", text)
	text, _ = remoteCall(t, h, "person", "pacto_fleet_entities", `{"declaration":"inferred"}`)
	expect(t, !strings.Contains(text, "billing"), "the view hides it from alice: %s", text)
}

func issuerWorld(t *testing.T, exchange bool) (*dashMCPWorld, http.Handler) {
	t.Helper()
	w := newDashMCPWorld(t, func(v *viper.Viper) {
		v.Set("dashboard.oidc-issuer-url", dashIssuer)
		v.Set("dashboard.oidc-client-id", "pacto-dashboard")
		v.Set("dashboard.oidc-client-secret", "s3cret")
		v.Set("dashboard.oidc-redirect-url", "https://pacto.example/auth/callback")
		v.Set("dashboard.mcp-url", "https://pacto.example/mcp")
		if exchange {
			v.Set("dashboard.mcp-exchange-audience", "https://kubernetes.default.svc")
		}
	})
	return w, w.mcp(t)
}

// TestDashboardMCPIssuerTokenExchanged: with an exchange audience an issuer
// token is verified and exchanged by the dashboard's OIDC client, and only
// the exchanged token reaches the cluster.
func TestDashboardMCPIssuerTokenExchanged(t *testing.T) {
	w, h := issuerWorld(t, true)
	want := authn.MCPConfig{IssuerURL: dashIssuer, Audience: "https://pacto.example/mcp", ClientID: "pacto-dashboard", ClientSecret: "s3cret",
		ExchangeAudience: "https://kubernetes.default.svc"}
	got := w.mcpCfg
	got.HTTPClient = nil
	expect(t, got == want && w.mcpCfg.HTTPClient != nil, "the issuer path's configuration: %+v", w.mcpCfg)
	names := remoteToolNames(t, h, issuerJWT)
	expect(t, slices.Contains(names, "pacto_delivery_get") && !slices.Contains(names, "pacto_delivery_operation_propose"), "carol's tools: %v", names)
	tokens := w.clientTokens()
	expect(t, slices.Contains(tokens, "issuer-exchanged") && !slices.Contains(tokens, issuerJWT), "the cluster saw: %v", tokens)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://pacto.example/.well-known/oauth-protected-resource/mcp", nil))
	expect(t, strings.Contains(rec.Body.String(), `"authorization_servers":["`+dashIssuer+`"]`) &&
		strings.Contains(rec.Body.String(), `"resource":"https://pacto.example/mcp"`), "metadata: %s", rec.Body)
}

// TestDualAuthRoutesByIssuer: with an exchange audience an issuer token never
// reaches a TokenReview and a Kubernetes token never reaches the issuer
// verifier; without one, an issuer token takes the token path as a person.
func TestDualAuthRoutesByIssuer(t *testing.T) {
	w, h := issuerWorld(t, true)
	_ = remoteToolNames(t, h, issuerJWT)
	_ = remoteToolNames(t, h, "agent")
	w.mu.Lock()
	expect(t, !slices.Contains(w.reviewed, issuerJWT), "an issuer token reached a TokenReview: %v", w.reviewed)
	w.mu.Unlock()
	expect(t, !slices.Contains(w.issuer.verified, "agent"), "a Kubernetes token reached the issuer verifier: %v", w.issuer.verified)

	w, h = issuerWorld(t, false)
	caller := remoteCapabilitiesCaller(t, h, issuerJWT)
	expect(t, caller["user"] == dashIssuer+"#carol" && caller["fleetOnly"] == nil, "an issuer token on the token path: %v", caller)
	expect(t, len(w.issuer.verified) == 0 && slices.Contains(w.clientTokens(), issuerJWT), "without an exchange audience: %v %v", w.issuer.verified, w.clientTokens())
	w.mu.Lock()
	expect(t, !slices.Contains(w.reviewed, issuerJWT), "a person's token entered a TokenReview: %v", w.reviewed)
	w.mu.Unlock()

	only := dualAuth{oidc: &fakeIssuerAuth{}, issuer: dashIssuer}
	_, err := only.Verify(context.Background(), "agent", nil)
	expect(t, errors.Is(err, auth.ErrInvalidToken), "no token path: %v", err)
	info, err := only.Verify(context.Background(), issuerJWT, nil)
	expect(t, err == nil && info.Extra[viaIssuer] == true, "issuer path: %v %+v", err, info)
	failing := dualAuth{oidc: &failingAuth{}, issuer: dashIssuer}
	_, err = failing.Verify(context.Background(), issuerJWT, nil)
	expect(t, errors.Is(err, auth.ErrInvalidToken), "a refused issuer token: %v", err)
}

type failingAuth struct{ fakeMCPAuth }

func (*failingAuth) Verify(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
	return nil, auth.ErrInvalidToken
}

func TestDashboardMCPExchangeNeedsURL(t *testing.T) {
	w := newDashMCPWorld(t, func(v *viper.Viper) {
		v.Set("dashboard.oidc-issuer-url", dashIssuer)
		v.Set("dashboard.oidc-client-id", "pacto-dashboard")
		v.Set("dashboard.oidc-redirect-url", "https://pacto.example/auth/callback")
		v.Set("dashboard.mcp-exchange-audience", "kube")
	})
	_, err := w.build(t)
	expect(t, errors.Is(err, errMCPExchangeNeedsURL), "exchange without a URL: %v", err)
	for _, bad := range []string{"pacto.example/mcp", "https://pacto.example/other", "ftp://pacto.example/mcp"} {
		w.v.Set("dashboard.mcp-url", bad)
		_, err = w.build(t)
		expect(t, errors.Is(err, errMCPURL), "%s: %v", bad, err)
	}
	w.v.Set("dashboard.mcp-url", "https://pacto.example/mcp")
	w.v.Set("dashboard.oidc-ca-file", "/missing/ca.pem")
	_, err = w.build(t)
	expect(t, err != nil && strings.Contains(err.Error(), "ca.pem"), "a bad CA file: %v", err)
	w.v.Set("dashboard.oidc-ca-file", "")
	w.v.Set("dashboard.oidc-issuer-url", "")
	_, err = w.build(t)
	expect(t, errors.Is(err, errMCPExchangeNeedsIssuer), "exchange without an issuer: %v", err)
}

// TestDashboardMCPNoUsablePath: an issuer without token sign-in and without
// an exchange audience has no way to name callers to the cluster.
func TestDashboardMCPNoUsablePath(t *testing.T) {
	w := newDashMCPWorld(t, func(v *viper.Viper) {
		v.Set("dashboard.token-login", false)
		v.Set("dashboard.oidc-issuer-url", dashIssuer)
		v.Set("dashboard.oidc-client-id", "pacto-dashboard")
		v.Set("dashboard.oidc-redirect-url", "https://pacto.example/auth/callback")
	})
	_, err := w.build(t)
	expect(t, errors.Is(err, errMCPNoExchange), "no usable path: %v", err)
	w.d.newMCPAuth = func(authn.MCPConfig) (mcpAuth, error) { return nil, errors.New("issuer down") }
	w.v.Set("dashboard.mcp-exchange-audience", "kube")
	w.v.Set("dashboard.mcp-url", "https://pacto.example/mcp")
	_, err = w.build(t)
	expect(t, err != nil && strings.Contains(err.Error(), "issuer down"), "the issuer verifier fails: %v", err)
}

func TestMCPOffInLocalAndOpenMode(t *testing.T) {
	d := deliveryDeps("alice", nil)
	cluster := &rest.Config{Host: "https://k"}
	local, err := newDashboardIdentity(viper.New(), "127.0.0.1", cluster, homeEntries(cluster), d)
	expect(t, err == nil, "local: %v", err)
	open := viper.New()
	open.Set("dashboard.allow-anonymous", true)
	anon, err := newDashboardIdentity(open, "0.0.0.0", cluster, homeEntries(cluster), d)
	expect(t, err == nil, "open: %v", err)
	for name, id := range map[string]*dashboardIdentity{"local": local, "open": anon} {
		_, err := newDashboardMCP(viper.New(), app.NewService(nil, nil), "v", nil, id, nil, nil, d)
		expect(t, errors.Is(err, errMCPNeedsSignIn), "%s: %v", name, err)
	}
	signin := viper.New()
	signin.Set("dashboard.oidc-issuer-url", dashIssuer)
	signin.Set("dashboard.oidc-client-id", "pacto-dashboard")
	signin.Set("dashboard.oidc-redirect-url", "https://pacto.example/auth/callback")
	nocluster, err := newDashboardIdentity(signin, "0.0.0.0", nil, homeEntries(nil), d)
	expect(t, err == nil, "no cluster: %v", err)
	_, err = newDashboardMCP(signin, app.NewService(nil, nil), "v", nil, nocluster, nil, nil, d)
	expect(t, errors.Is(err, errMCPNeedsCluster), "no cluster: %v", err)
}

// serveDashboard serves opts as `pacto dashboard` does and returns its URL.
func serveDashboard(t *testing.T, opts dashboard.Options) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	expect(t, err == nil, "listen: %v", err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = dashboard.NewServer(opts).ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String()
}

func statusOf(t *testing.T, method, url string) int {
	t.Helper()
	r, _ := http.NewRequest(method, url, strings.NewReader(listTools))
	resp, err := http.DefaultClient.Do(r)
	expect(t, err == nil, "%s %s: %v", method, url, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func installationMCP(t *testing.T, opts dashboard.Options) fleet.InstallationMCP {
	t.Helper()
	rec := httptest.NewRecorder()
	dashboard.NewServer(opts).APIHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	var caps struct {
		Installation fleet.Installation `json:"installation"`
	}
	expect(t, rec.Code == http.StatusOK && json.Unmarshal(rec.Body.Bytes(), &caps) == nil, "capabilities: %d %s", rec.Code, rec.Body)
	return caps.Installation.MCP
}

// TestDashboardWithoutMCPUnchanged: without --mcp the dashboard mounts nothing
// outside sign-in, /mcp answers as any other unknown path and
// installation.mcp says it serves none.
func TestDashboardWithoutMCPUnchanged(t *testing.T) {
	w := newDashMCPWorld(t, nil)
	var opts dashboard.Options
	w.id.options(&opts)
	expect(t, opts.Outside == nil && !opts.MCPServed, "options without --mcp: %+v", opts.Outside)
	opts.UI = dashboard.EmbeddedUI()
	base := serveDashboard(t, opts)
	expect(t, statusOf(t, http.MethodGet, base+"/mcp") == statusOf(t, http.MethodGet, base+"/no-such-page"), "/mcp is any unknown path")
	m := installationMCP(t, opts)
	expect(t, !m.Served && m.Source == "this dashboard serves no /mcp endpoint", "installation.mcp %+v", m)
}

// TestDashboardMCPOutsideRoutes: every path newDashboardMCP mounts outside
// sign-in serves public metadata to GET and refuses POST, or answers 401
// without a bearer.
func TestDashboardMCPOutsideRoutes(t *testing.T) {
	w, _ := newDashMCP(t)
	var opts dashboard.Options
	w.id.options(&opts)
	opts.UI, opts.Outside = dashboard.EmbeddedUI(), w.outside
	base := serveDashboard(t, opts)
	for path := range w.outside {
		if strings.HasPrefix(path, "/.well-known/") {
			resp, err := http.Get(base + path)
			expect(t, err == nil && resp.StatusCode == http.StatusOK, "GET %s: %v", path, err)
			body := new(strings.Builder)
			_, _ = io.Copy(body, resp.Body)
			_ = resp.Body.Close()
			expect(t, strings.Contains(body.String(), `"resource":"`+base+`/mcp"`) && !strings.Contains(body.String(), "web"), "%s: %s", path, body)
			expect(t, statusOf(t, http.MethodPost, base+path) == http.StatusMethodNotAllowed, "POST %s is refused", path)
			continue
		}
		expect(t, statusOf(t, http.MethodPost, base+path) == http.StatusUnauthorized, "POST %s without a bearer", path)
		expect(t, statusOf(t, http.MethodGet, base+path) == http.StatusUnauthorized, "GET %s without a bearer", path)
	}
	expect(t, len(w.outside) == 3, "outside routes: %v", w.outside)
	expect(t, statusOf(t, http.MethodGet, base+"/api/fleet/services") == http.StatusUnauthorized, "the fleet API keeps its session guard")
}

// TestDashboardMCPInstallationCapability: the dashboard reports the /mcp it
// serves, and its /mcp's pacto_capabilities keeps the MCP-side source.
func TestDashboardMCPInstallationCapability(t *testing.T) {
	_, h := newDashMCP(t)
	m := installationMCP(t, dashboard.Options{MCPServed: true, MCPSource: dashboardMCPSource})
	expect(t, m.Served && m.Source == "this dashboard, at /mcp", "installation.mcp %+v", m)
	text, _ := remoteCall(t, h, "person", "pacto_capabilities", "{}")
	expect(t, strings.Contains(text, `"served": true`) && strings.Contains(text, "this MCP server, over http"), "pacto_capabilities: %s", text)
}

// TestBothHTTPServersRegisterSameTools: pacto mcp -t http --fleet --delivery
// and the dashboard's /mcp list the same tools to the same agent.
func TestBothHTTPServersRegisterSameTools(t *testing.T) {
	_, h := newDashMCP(t)
	dash := remoteToolNames(t, h, "agent")
	d := deliveryDeps(agentSA, nil)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://k"}, nil }
	d.resolveK8s = ambientEntries(&d)
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return deliveryDeps(agentSA, nil).newDynamic() }
	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	cmd.SetContext(context.Background())
	for k, v := range map[string]string{"fleet": "true", "delivery": "true", "local": t.TempDir(), "transport": "http", "auth-issuer": "https://idp"} {
		expect(t, cmd.Flags().Set(k, v) == nil, "flag %s", k)
	}
	s := mcpServe{auth: &fakeMCPAuth{exchange: true}, issuer: "https://idp", resource: "https://mcp.example/mcp"}
	expect(t, remoteCallerFor(cmd, app.NewService(nil, nil), "v", nil, d, &s) == nil, "remote composed")
	remote := remoteToolNames(t, mcpHandler(s), "good")
	slices.Sort(dash)
	slices.Sort(remote)
	expect(t, slices.Equal(dash, remote), "dashboard %v\nremote    %v", dash, remote)
}

// TestDashboardMCPImpact: pacto_impact over /mcp refuses what the server
// would read with its own access, answers an event without delivery and
// resolves visible refs pinned to what the caller's view holds.
func TestDashboardMCPImpact(t *testing.T) {
	svc, pulled := pullRecorder()
	f := dashboardMCPImpact(svc, nil)
	for name, req := range map[string]pactomcp.ImpactRequest{
		"traces":  {OldRef: "oci://reg.example/orders:1", NewRef: "oci://reg.example/orders:2", TracesPath: "/etc/passwd"},
		"local":   {OldRef: "./a", NewRef: "./b"},
		"hidden":  {OldRef: "oci://reg.example/ledger:1", NewRef: "oci://reg.example/ledger:2"},
		"running": {NewRef: "oci://reg.example/orders:2", Running: true},
	} {
		_, err := f(context.Background(), viewSnapshot(), req)
		expect(t, err != nil, "%s is refused", name)
	}
	expect(t, len(*pulled) == 0, "a refused request pulled %v", *pulled)
	res, err := f(context.Background(), viewFleet(t).Snapshot(), pactomcp.ImpactRequest{Event: &impact.Event{Kind: impact.EventRegressed, Service: "web", Revision: "1.0.0"}})
	b, _ := json.Marshal(res)
	expect(t, err == nil && strings.Contains(string(b), "reads no delivery for its callers"), "event: %v %s", err, b)
	_, _ = f(context.Background(), viewSnapshot(), pactomcp.ImpactRequest{OldRef: "oci://reg.example/orders:1", NewRef: "oci://reg.example/orders:2"})
	expect(t, slices.ContainsFunc(*pulled, func(r string) bool { return strings.Contains(r, "@sha256:") }), "visible refs are pulled pinned: %v", *pulled)
}

// TestDashboardMCPImpactHonoursPolicy: the caller's policy reaches the
// analysis, as it does on `pacto mcp -t http`.
func TestDashboardMCPImpactHonoursPolicy(t *testing.T) {
	store := &testutil.MockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) { return testutil.TestBundle(), nil }}
	policy := impact.Policy{UnknownRange: impact.PolicyFail, Uncontracted: impact.PolicyWarn}
	res, err := dashboardMCPImpact(app.NewService(store, nil), nil)(context.Background(), viewSnapshot(),
		pactomcp.ImpactRequest{OldRef: "oci://reg.example/orders:1", NewRef: "oci://reg.example/orders:2", Policy: policy})
	expect(t, err == nil && res.Policy != nil && *res.Policy == policy, "policy: %v %+v", err, res)
}

// TestDashboardCommandServesMCP: --mcp names the endpoint on the banner, and a
// local dashboard refuses it at startup.
func TestDashboardCommandServesMCP(t *testing.T) {
	dir := isolateHost(t)
	writeKubeconfig(t, dir)
	svcDir := filepath.Join(dir, "svc")
	writeBundle(t, svcDir, "svc")
	stderr, err := runDashboard(t, app.NewService(nil, nil), viper.New(), svcDir, "--port", "0", "--host", "0.0.0.0", "--token-login", "--mcp")
	expect(t, err == nil && strings.Contains(stderr, "MCP: /mcp\n"), "banner: %v\n%s", err, stderr)
	_, err = runDashboard(t, app.NewService(nil, nil), viper.New(), svcDir, "--port", "0", "--mcp")
	expect(t, errors.Is(err, errMCPNeedsSignIn), "local: %v", err)
	expect(t, dualAuth{}.CanExchange(), "each verifier decides per caller")
}
