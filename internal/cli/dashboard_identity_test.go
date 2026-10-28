package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func identityFor(t *testing.T, v *viper.Viper, host string, cluster *rest.Config) *dashboardIdentity {
	t.Helper()
	d := deliveryDeps("alice", nil)
	d.served = func(*rest.Config, string) (bool, error) { return true, nil }
	id, err := newDashboardIdentity(v, host, cluster, d)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := id.newClient(&rest.Config{Host: "https://k"}); err != nil || c == nil {
		t.Fatalf("the default client builder: %v", err)
	}
	id.newClient = func(*rest.Config) (dynamic.Interface, error) { return d.newDynamic() }
	return id
}

func TestDashboardIdentityModes(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.delivery-namespace", "delivery")
	local := identityFor(t, v, "127.0.0.1", &rest.Config{Host: "https://k"})
	if !local.auth.Local() || local.policyNS != "delivery" {
		t.Fatalf("loopback without OIDC is local, reading the policy published in delivery: %+v", local)
	}
	if !identityFor(t, v, "localhost", nil).auth.Local() {
		t.Fatal("localhost is loopback")
	}
	if open := identityFor(t, v, "0.0.0.0", nil); open.auth.Local() || open.auth.Login() {
		t.Fatal("a shared bind without OIDC identifies nobody")
	}
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "pacto-dashboard")
	v.Set("dashboard.oidc-redirect-url", "https://dash.example/auth/callback")
	if shared := identityFor(t, v, "127.0.0.1", nil); !shared.auth.Login() {
		t.Fatal("an issuer makes it shared, even on loopback")
	}
	v.Set("dashboard.oidc-display-name", "Keycloak")
	gated := identityFor(t, v, "0.0.0.0", nil)
	var gatedOpts dashboard.Options
	gated.options(&gatedOpts)
	expect(t, gatedOpts.Auth.Login && gatedOpts.Auth.LoginName == "Keycloak", "the sign-in button names the provider: %+v", gatedOpts.Auth)
	rec := httptest.NewRecorder()
	gated.auth.Middleware(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://dash.example/api/fleet/status", nil))
	expect(t, rec.Code == http.StatusUnauthorized, "sign-in gates the fleet API: %d", rec.Code)
	v.Set("dashboard.oidc-internal-issuer-url", "ftp://idp")
	if _, err := newDashboardIdentity(v, "0.0.0.0", nil, defaultDeps()); err == nil || !strings.Contains(err.Error(), "internal issuer") {
		t.Fatalf("a bad internal issuer URL fails at start: %v", err)
	}
	v.Set("dashboard.oidc-internal-issuer-url", "http://idp.internal")
	if !identityFor(t, v, "0.0.0.0", nil).auth.Login() {
		t.Fatal("an internal issuer URL keeps sign-in")
	}
	v.Set("dashboard.oidc-internal-issuer-url", "")
	v.Set("dashboard.oidc-client-id", "")
	if _, err := newDashboardIdentity(v, "0.0.0.0", nil, defaultDeps()); err == nil {
		t.Fatal("an incomplete OIDC configuration fails at start")
	}
	if got := splitList([]string{"a, b", "c"}); len(got) != 3 || got[1] != "b" {
		t.Fatalf("splitList %v", got)
	}
}

func TestDashboardTokenLoginIdentity(t *testing.T) {
	v := viper.New()
	// Token sign-in alone makes it shared too, and needs a cluster to check tokens against.
	v.Set("dashboard.token-login", true)
	if _, err := newDashboardIdentity(v, "0.0.0.0", nil, defaultDeps()); err == nil || !strings.Contains(err.Error(), "token-login") {
		t.Fatalf("token sign-in without a cluster fails at start: %v", err)
	}
	tokens := identityFor(t, v, "127.0.0.1", &rest.Config{Host: "https://k"})
	expect(t, tokens.auth.SignsIn() && tokens.auth.TokenLogin() && !tokens.auth.Login() && tokens.viewer != nil, "token sign-in: %+v", tokens.auth)
	var to dashboard.Options
	tokens.options(&to)
	expect(t, to.Auth.TokenLogin && !to.Auth.Login && !to.Auth.Local && to.View != nil, "token sign-in options %+v", to.Auth)
	tokens.state = func(context.Context) authn.State {
		return authn.State{SignedIn: true, Token: "pasted", Method: "token"}
	}
	_, err := tokens.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err == nil, "a token session reaches delivery as itself: %v", err)
}

func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func TestTokenName(t *testing.T) {
	tok := func(claims string) string {
		return "h." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
	}
	for _, c := range []struct{ token, want string }{
		{tok(`{"name":"Carol Diaz","email":"carol@example.com"}`), "Carol Diaz"},
		{tok(`{"email":"carol@example.com"}`), "carol@example.com"},
		{tok(`{"sub":"u-carol"}`), ""},
		{tok(`not json`), ""},
		{"h.!!!.s", ""},
		{"", ""},
	} {
		expect(t, tokenName(c.token) == c.want, "tokenName(%q) = %q, want %q", c.token, tokenName(c.token), c.want)
	}
	v := viper.New()
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "pacto-dashboard")
	v.Set("dashboard.oidc-redirect-url", "https://dash.example/auth/callback")
	id := identityFor(t, v, "0.0.0.0", nil)
	id.state = func(context.Context) authn.State {
		return authn.State{SignedIn: true, Token: tok(`{"name":"Carol"}`), Method: "oidc", AuthTime: "1800000000",
			Person: authn.Person{User: "https://idp#carol", Groups: []string{"orders"}}}
	}
	o := &dashboard.Options{}
	id.options(o)
	st := o.Auth.State(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, st.Name == "Carol", "the session carries the token's name")
	expect(t, st.User == "https://idp#carol" && len(st.Groups) == 1 && st.Groups[0] == "orders" && st.Method == "oidc" && st.AuthTime == "1800000000",
		"the session names the person as the cluster does: %+v", st)
}

func TestDashboardIdentityRefusesABadTrustConfig(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.delivery-trust", filepath.Join(t.TempDir(), "absent"))
	if _, err := newDashboardIdentity(v, "127.0.0.1", nil, defaultDeps()); err == nil || !strings.Contains(err.Error(), "trust config") {
		t.Fatalf("a missing trust config must fail at start: %v", err)
	}
}

func viewFleet(t *testing.T) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Targets: []fleet.RawTarget{{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "web", Service: "web"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestDashboardIdentityView(t *testing.T) {
	v := viper.New()
	var o dashboard.Options
	identityFor(t, v, "127.0.0.1", nil).options(&o)
	expect(t, o.View == nil, "a local dashboard narrows nothing")

	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "pacto-dashboard")
	v.Set("dashboard.oidc-redirect-url", "https://dash.example/auth/callback")
	id := identityFor(t, v, "0.0.0.0", nil)
	var asked []authn.Person
	id.viewer = authn.NewViewer(func(_ context.Context, p authn.Person, ns string) (bool, error) {
		asked = append(asked, p)
		return false, nil
	})
	o = dashboard.Options{}
	id.options(&o)
	q := viewFleet(t)

	id.state = func(context.Context) authn.State { return authn.State{} }
	r, err := o.View(context.Background(), q)
	expect(t, err == nil && len(r.SnapshotForSerialization().Services) == 0 && asked[0].User == authn.Anonymous.User,
		"a visitor who has not signed in is the anonymous user: %v %+v", err, asked)

	id.state = func(context.Context) authn.State {
		return authn.State{SignedIn: true, Person: authn.Person{User: "https://idp.example#u1", Groups: []string{"g"}}}
	}
	asked = nil
	_, _ = o.View(context.Background(), q)
	expect(t, len(asked) > 0 && asked[0].User == "https://idp.example#u1", "a signed-in person is asked about as the cluster names them: %+v", asked)

	id.viewer = authn.NewViewer(func(context.Context, authn.Person, string) (bool, error) { return false, errors.New("apiserver down") })
	id.state = func(context.Context) authn.State {
		return authn.State{SignedIn: true, Person: authn.Person{User: "x#y"}}
	}
	_, err = o.View(context.Background(), q)
	expect(t, err != nil && strings.Contains(err.Error(), "checking your Kubernetes permissions failed"), "a failed check: %v", err)
}

func TestClusterReview(t *testing.T) {
	if _, err := clusterReview(nil, nil)(context.Background(), authn.Anonymous, "a"); !errors.Is(err, errNoClusterToAsk) {
		t.Errorf("no cluster: %v", err)
	}
	broken := errors.New("bad config")
	if _, err := clusterReview(&rest.Config{}, func(*rest.Config) (dynamic.Interface, error) { return nil, broken })(context.Background(), authn.Anonymous, "a"); !errors.Is(err, broken) {
		t.Errorf("no client: %v", err)
	}
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		_ = unstructured.SetNestedField(obj.Object, true, "status", "allowed")
		return true, obj, nil
	})
	ok, err := clusterReview(&rest.Config{}, func(*rest.Config) (dynamic.Interface, error) { return c, nil })(context.Background(), authn.Anonymous, "a")
	if err != nil || !ok {
		t.Errorf("review through the cluster: %v %v", ok, err)
	}
}

// A local dashboard names the kubeconfig user as the API server does, asked
// once; a failure is asked again after failureTTL.
func TestLocalDashboardNamesTheKubeconfigUser(t *testing.T) {
	id := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://k"})
	id.state = func(context.Context) authn.State { return authn.State{SignedIn: true, CSRF: "c"} }
	base := time.Now()
	id.now = func() time.Time { return base }
	next := id.newClient
	id.newClient = func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("bad config") }
	var o dashboard.Options
	id.options(&o)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	expect(t, o.Auth.State(r).User == "", "a client that cannot be built names nobody")
	id.newClient = func(*rest.Config) (dynamic.Interface, error) {
		c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
		c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("connection refused")
		})
		return c, nil
	}
	id.now = func() time.Time { return base.Add(failureTTL + time.Second) }
	expect(t, o.Auth.State(r).User == "", "a review that fails names nobody")
	id.newClient = next
	expect(t, o.Auth.State(r).User == "", "a failure is not asked again at once")
	id.now = func() time.Time { return base.Add(2 * (failureTTL + time.Second)) }
	st := o.Auth.State(r)
	expect(t, st.User == "alice" && st.Groups[0] == "team-payments" && st.AuthTime != "", "then the kubeconfig user: %+v", st)
	id.newClient = func(*rest.Config) (dynamic.Interface, error) { t.Fatal("asked again"); return nil, nil }
	expect(t, o.Auth.State(r).User == "alice", "the answer is kept")
	none := identityFor(t, viper.New(), "127.0.0.1", nil)
	none.state = id.state
	o = dashboard.Options{}
	none.options(&o)
	expect(t, o.Auth.State(r).User == "", "no cluster names nobody")
}
