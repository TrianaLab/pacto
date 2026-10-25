package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
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
	v.Set("dashboard.auth-required", true)
	required := identityFor(t, v, "0.0.0.0", nil)
	rec := httptest.NewRecorder()
	required.auth.Middleware(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://dash.example/api/fleet/status", nil))
	expect(t, rec.Code == http.StatusUnauthorized, "--auth-required gates the fleet API: %d", rec.Code)
	v.Set("dashboard.oidc-issuer-url", "")
	if _, err := newDashboardIdentity(v, "0.0.0.0", nil, defaultDeps()); err == nil || !strings.Contains(err.Error(), "auth-required") {
		t.Fatalf("--auth-required without sign-in fails at start: %v", err)
	}
	v.Set("dashboard.auth-required", false)
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "")
	if _, err := newDashboardIdentity(v, "0.0.0.0", nil, defaultDeps()); err == nil {
		t.Fatal("an incomplete OIDC configuration fails at start")
	}
	if got := splitList([]string{"a, b", "c"}); len(got) != 3 || got[1] != "b" {
		t.Fatalf("splitList %v", got)
	}
}

// expect fails the test unless ok.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func TestDashboardIdentityOptions(t *testing.T) {
	v := viper.New()
	id := identityFor(t, v, "127.0.0.1", &rest.Config{Host: "https://k"})
	var o dashboard.Options
	id.options(&o)
	expect(t, o.Delivery != nil && o.DeliveryInstalled != nil, "local options %+v", o)
	expect(t, o.Auth.Local && !o.Auth.Login, "local options %+v", o.Auth)
	expect(t, !o.Auth.State(httptest.NewRequest(http.MethodGet, "/", nil)).SignedIn, "no middleware ran, nobody is signed in")
	api, err := o.Delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err == nil, "local delivery: %v", err)
	c, err := api.WhoAmI(context.Background())
	expect(t, err == nil && c.User == "alice", "local mode acts as the kubeconfig user: %+v %v", c, err)
	// Installed is asked once a minute.
	calls := 0
	var servedErr error
	id.served = func(cfg *rest.Config, _ string) (bool, error) {
		calls++
		expect(t, cfg.Timeout == discoveryTimeout && id.cluster.Timeout == 0, "discovery gives up after %v, on a copy", cfg.Timeout)
		return servedErr == nil, servedErr
	}
	base := time.Now()
	id.now = func() time.Time { return base }
	_, _ = id.deliveryInstalled(context.Background())
	_, _ = id.deliveryInstalled(context.Background())
	id.now = func() time.Time { return base.Add(2 * time.Minute) }
	ok, err := id.deliveryInstalled(context.Background())
	expect(t, ok && err == nil && calls == 2, "discovery calls %d", calls)
	// A failed call is returned and kept only a few seconds: a cluster that is
	// down is not asked on every request, and is asked again soon.
	servedErr = errors.New("connection refused")
	id.now = func() time.Time { return base.Add(4 * time.Minute) }
	_, err = id.deliveryInstalled(context.Background())
	expect(t, err != nil, "a discovery failure is an error")
	servedErr = nil
	ok, err = id.deliveryInstalled(context.Background())
	expect(t, !ok && err != nil && calls == 3, "a failure is kept briefly: %d calls", calls)
	id.now = func() time.Time { return base.Add(4*time.Minute + failureTTL + time.Second) }
	ok, err = id.deliveryInstalled(context.Background())
	expect(t, ok && err == nil && calls == 4, "then asked again: %d calls", calls)
	// No cluster: no delivery routes.
	var none dashboard.Options
	identityFor(t, v, "127.0.0.1", nil).options(&none)
	expect(t, none.Delivery == nil, "no cluster, no delivery")
}

func TestDashboardIdentityFactory(t *testing.T) {
	v := viper.New()
	cluster := &rest.Config{Host: "https://k"}
	// Open mode refuses; shared mode needs a session and uses its token.
	_, err := identityFor(t, v, "0.0.0.0", cluster).delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, isRefusal(err, http.StatusForbidden), "open: %v", err)
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "c")
	v.Set("dashboard.oidc-redirect-url", "https://d/cb")
	shared := identityFor(t, v, "0.0.0.0", cluster)
	_, err = shared.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, isRefusal(err, http.StatusUnauthorized), "signed out: %v", err)
	var got *rest.Config
	shared.newClient = func(c *rest.Config) (dynamic.Interface, error) { got = c; return nil, errors.New("bad config") }
	shared.state = func(context.Context) authn.State { return authn.State{SignedIn: true, Token: "id-token"} }
	_, err = shared.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err != nil, "a client error is passed on")
	expect(t, got.BearerToken == "id-token" && got.Host == "https://k", "shared mode builds the client from the person's token: %+v", got)
}

func isRefusal(err error, status int) bool {
	var de *dashboard.DeliveryError
	return errors.As(err, &de) && de.Status == status
}

func localAPI(t *testing.T) (*dashboardIdentity, dashboard.DeliveryAPI) {
	t.Helper()
	id := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://k"})
	api, err := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err == nil, "local delivery: %v", err)
	return id, api
}

// An accept-unknown from the dashboard is an Approval that says accept-unknown,
// bound to the held operation and signed by the caller: what the promoter
// matches against an Unknown hold.
func TestDashDeliveryAcceptUnknownApproval(t *testing.T) {
	id, _ := localAPI(t)
	var spec map[string]any
	next := id.newClient
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		c, err := next(cfg)
		c.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "approvals", func(a k8stesting.Action) (bool, runtime.Object, error) {
			spec = a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).Object["spec"].(map[string]any)
			return false, nil, nil
		})
		return c, err
	}
	api, err := id.delivery(httptest.NewRequest(http.MethodPost, "/", nil))
	expect(t, err == nil, "delivery: %v", err)
	n, err := api.Approve(context.Background(), "prod", "held", delivery.AcceptUnknown, "gates stay Unknown, ship it")
	expect(t, err == nil && n == "held-1", "accept-unknown %q %v", n, err)
	expect(t, spec["decision"] == "accept-unknown" && spec["operation"] == "held" && spec["intentDigest"] == "sha256:i" &&
		spec["by"] == "alice" && spec["authTime"] != "" && spec["note"] == "gates stay Unknown, ship it",
		"the Approval the promoter reads: %v", spec)
}

func TestDashDeliveryAdapterReads(t *testing.T) {
	_, api := localAPI(t)
	ctx := context.Background()
	stages, err := api.Stages(ctx, "prod")
	expect(t, err == nil && len(stages) == 1, "stages %v %v", stages, err)
	ops, err := api.Operations(ctx, "prod")
	expect(t, err == nil && len(ops) == 1, "ops %v %v", ops, err)
	op, err := api.Get(ctx, "prod", "held")
	expect(t, err == nil && op.Status.Phase == delivery.PhaseHeld, "get %v %v", op, err)
	e, err := api.Explain(ctx, "prod", "held")
	expect(t, err == nil && e.Operation == "prod/held", "explain %v %v", e, err)
	_, err = api.History(ctx, "prod", "payments-prod")
	expect(t, err == nil, "history %v", err)
	st, err := api.Stage(ctx, "prod", "payments-prod")
	expect(t, err == nil && st.Metadata.Name == "payments-prod", "stage %v %v", st, err)
	b, err := api.Blame(ctx, "prod", "payments-prod", "")
	expect(t, err == nil && b.Stage.Name == "payments-prod", "blame %v %v", b, err)
	_, err = api.Undo(ctx, "prod", "missing", "")
	expect(t, isRefusal(err, http.StatusNotFound), "undo of an operation that is not there keeps the 404: %v", err)
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
	id, _ := localAPI(t)
	id.state = func(context.Context) authn.State { return authn.State{SignedIn: true, Token: tok(`{"name":"Carol"}`)} }
	o := &dashboard.Options{}
	id.options(o)
	expect(t, o.Auth.State(httptest.NewRequest(http.MethodGet, "/", nil)).Name == "Carol", "the session carries the token's name")
}

func TestDashDeliveryAdapterActions(t *testing.T) {
	_, api := localAPI(t)
	ctx := context.Background()
	p, err := api.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: cliB})
	expect(t, err == nil && p.IntentDigest != "", "plan %v %v", p, err)
	_, _, err = api.Propose(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: cliB, IntentDigest: "sha256:old"})
	expect(t, isRefusal(err, http.StatusConflict), "a stale plan is a 409 with the fresh plan: %v", err)
	n, err := api.Approve(ctx, "prod", "held", delivery.Approve, "")
	expect(t, err == nil && n == "held-1", "approve %q %v", n, err)
	err = api.Cancel(ctx, "prod", "missing")
	expect(t, isRefusal(err, http.StatusNotFound), "the apiserver's 404 keeps its status: %v", err)
	expect(t, refusal(nil) == nil, "no error, no refusal")
	err = refusal(errors.New("--to is required"))
	expect(t, isRefusal(err, http.StatusUnprocessableEntity) && err.(*dashboard.DeliveryError).Detail == "--to is required",
		"a plain refusal keeps its reason: %v", err)
	err = refusal(fmt.Errorf("asking the apiserver who you are: %w", &net.OpError{Op: "dial", Err: errors.New("connection refused")}))
	expect(t, isRefusal(err, http.StatusServiceUnavailable) && strings.Contains(err.(*dashboard.DeliveryError).Detail, "connection refused"),
		"a transport failure says the cluster is unreachable, and why: %v", err)
	err = refusal(fmt.Errorf("listing: %w", context.DeadlineExceeded))
	expect(t, isRefusal(err, http.StatusServiceUnavailable), "a timeout says the cluster is unreachable: %v", err)
	err = refusal(apierrors.NewForbidden(schema.GroupResource{Resource: "approvals"}, "", errors.New("spec.groups must be the groups of the user")))
	expect(t, isRefusal(err, http.StatusForbidden) && err.(*dashboard.DeliveryError).Code == "Forbidden", "a VAP refusal keeps its status and message: %v", err)
	var _ app.DeliveryRegistry = cliRegistry{}
}

func TestDashDeliveryDescribeCache(t *testing.T) {
	id, api := localAPI(t)
	ctx := context.Background()
	stages, _ := api.Stages(ctx, "prod")
	r := api.Describe(ctx, &stages[0], cliB)
	expect(t, r.Bundle != nil && len(id.revisions) == 1, "describe %+v", r)
	expect(t, api.Describe(ctx, &stages[0], cliB).Bundle == r.Bundle, "a second describe is cached")
	gone := stages[0]
	gone.Spec.ApplyRef.Name = "missing"
	api.Describe(ctx, &gone, cliA)
	expect(t, len(id.revisions) == 1, "a failed read is not cached")
	for i := 0; i < maxRevisions; i++ {
		id.revisions[string(rune(i))] = delivery.Revision{}
	}
	api.Describe(ctx, &stages[0], cliA)
	expect(t, len(id.revisions) == 1, "a full cache resets: %d", len(id.revisions))
}

func TestDashboardIdentityRefusesABadTrustConfig(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.delivery-trust", filepath.Join(t.TempDir(), "absent"))
	if _, err := newDashboardIdentity(v, "127.0.0.1", nil, defaultDeps()); err == nil || !strings.Contains(err.Error(), "trust config") {
		t.Fatalf("a missing trust config must fail at start: %v", err)
	}
}

// The published config schema names only variables the dashboard reads: every
// PACTO_DASHBOARD_* property is a bound flag (or one of the env-only settings),
// and the delivery settings are in it.
func TestDashboardConfigSchemaMatchesTheFlags(t *testing.T) {
	cmd := newDashboardCommand(app.NewService(nil, nil), viper.New(), "test", defaultDeps())
	data, err := dashboard.ExportConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	envOnly := map[string]bool{"PACTO_DASHBOARD_REPO": true, "PACTO_DASHBOARD_OIDC_CLIENT_SECRET": true}
	for k := range s.Properties {
		rest, ok := strings.CutPrefix(k, "PACTO_DASHBOARD_")
		if !ok || envOnly[k] {
			continue
		}
		if cmd.Flags().Lookup(strings.ToLower(strings.ReplaceAll(rest, "_", "-"))) == nil {
			t.Errorf("schema lists %s but the dashboard reads no such setting", k)
		}
	}
	for _, k := range []string{"PACTO_DASHBOARD_DELIVERY_NAMESPACE", "PACTO_DASHBOARD_DELIVERY_TRUST"} {
		if s.Properties[k] == nil {
			t.Errorf("schema leaves out %s", k)
		}
	}
}
