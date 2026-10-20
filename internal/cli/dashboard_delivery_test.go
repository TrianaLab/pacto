package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spf13/viper"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func identityFor(t *testing.T, v *viper.Viper, host string, cluster *rest.Config) *dashboardIdentity {
	t.Helper()
	d := deliveryDeps("alice", nil)
	d.served = func(*rest.Config, string) bool { return true }
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
	v.Set("dashboard.agent-groups", []string{"pacto:agents,bots"})
	v.Set("dashboard.team-group-prefix", "acme:")
	local := identityFor(t, v, "127.0.0.1", &rest.Config{Host: "https://k"})
	if !local.auth.Local() || len(local.policy.AgentGroups) != 2 || local.policy.TeamGroupPrefix != "acme:" {
		t.Fatalf("loopback without OIDC is local: %+v", local.policy)
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
	v.Set("dashboard.oidc-client-id", "")
	if _, err := newDashboardIdentity(v, "0.0.0.0", nil, defaultDeps()); err == nil {
		t.Fatal("an incomplete OIDC configuration fails at start")
	}
	if got := splitList([]string{"a, b", "c"}); len(got) != 3 || got[1] != "b" {
		t.Fatalf("splitList %v", got)
	}
}

func TestDashboardIdentityOptionsAndFactory(t *testing.T) {
	v := viper.New()
	cluster := &rest.Config{Host: "https://k"}
	id := identityFor(t, v, "127.0.0.1", cluster)
	var o dashboard.Options
	id.options(&o)
	if o.Delivery == nil || o.DeliveryInstalled == nil || !o.Auth.Local || o.Auth.Login {
		t.Fatalf("local options %+v", o.Auth)
	}
	if st := o.Auth.State(httptest.NewRequest(http.MethodGet, "/", nil)); st.SignedIn {
		t.Fatal("no middleware ran, nobody is signed in")
	}
	api, err := o.Delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if c, err := api.WhoAmI(context.Background()); err != nil || c.User != "alice" {
		t.Fatalf("local mode acts as the kubeconfig user: %+v %v", c, err)
	}
	// Installed is asked once a minute.
	calls := 0
	id.served = func(*rest.Config, string) bool { calls++; return true }
	base := time.Now()
	id.now = func() time.Time { return base }
	id.deliveryInstalled(context.Background())
	id.deliveryInstalled(context.Background())
	id.now = func() time.Time { return base.Add(2 * time.Minute) }
	if !id.deliveryInstalled(context.Background()) || calls != 2 {
		t.Fatalf("discovery calls %d", calls)
	}
	// No cluster: no delivery routes.
	var none dashboard.Options
	identityFor(t, v, "127.0.0.1", nil).options(&none)
	if none.Delivery != nil {
		t.Fatal("no cluster, no delivery")
	}
	// Open mode refuses; shared mode needs a session and uses its token.
	open := identityFor(t, v, "0.0.0.0", cluster)
	if _, err := open.delivery(httptest.NewRequest(http.MethodGet, "/", nil)); !isRefusal(err, http.StatusForbidden) {
		t.Fatalf("open: %v", err)
	}
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "c")
	v.Set("dashboard.oidc-redirect-url", "https://d/cb")
	shared := identityFor(t, v, "0.0.0.0", cluster)
	if _, err := shared.delivery(httptest.NewRequest(http.MethodGet, "/", nil)); !isRefusal(err, http.StatusUnauthorized) {
		t.Fatalf("signed out: %v", err)
	}
	var got *rest.Config
	shared.newClient = func(c *rest.Config) (dynamic.Interface, error) { got = c; return nil, errors.New("bad config") }
	shared.state = func(context.Context) authn.State { return authn.State{SignedIn: true, Token: "id-token"} }
	if _, err := shared.delivery(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil || got.BearerToken != "id-token" || got.Host != "https://k" {
		t.Fatalf("shared mode builds the client from the person's token: %v %+v", err, got)
	}
}

func isRefusal(err error, status int) bool {
	var de *dashboard.DeliveryError
	return errors.As(err, &de) && de.Status == status
}

func TestDashDeliveryAdapter(t *testing.T) {
	id := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://k"})
	api, _ := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	ctx := context.Background()
	stages, err := api.Stages(ctx, "prod")
	if err != nil || len(stages) != 1 {
		t.Fatalf("stages %v %v", stages, err)
	}
	if ops, err := api.Operations(ctx, "prod"); err != nil || len(ops) != 1 {
		t.Fatalf("ops %v %v", ops, err)
	}
	if op, err := api.Get(ctx, "prod", "held"); err != nil || op.Status.Phase != delivery.PhaseHeld {
		t.Fatalf("get %v %v", op, err)
	}
	if e, err := api.Explain(ctx, "prod", "held"); err != nil || e.Operation != "prod/held" {
		t.Fatalf("explain %v %v", e, err)
	}
	if _, err := api.History(ctx, "prod", "payments-prod"); err != nil {
		t.Fatal(err)
	}
	if p, err := api.Plan(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: cliB}); err != nil || p.IntentDigest == "" {
		t.Fatalf("plan %v %v", p, err)
	}
	if _, _, err := api.Propose(ctx, delivery.Request{Namespace: "prod", Stage: "payments-prod", To: cliB, IntentDigest: "sha256:old"}); !isRefusal(err, http.StatusConflict) {
		t.Fatalf("a stale plan is a 409 with the fresh plan: %v", err)
	}
	if n, err := api.Approve(ctx, "prod", "held", delivery.Approve, ""); err != nil || n != "held-1" {
		t.Fatalf("approve %q %v", n, err)
	}
	if err := api.Cancel(ctx, "prod", "missing"); !isRefusal(err, http.StatusNotFound) {
		t.Fatalf("the apiserver's 404 keeps its status: %v", err)
	}
	// Describe is cached, and an unreadable artifact is not.
	r := api.Describe(ctx, &stages[0], cliB)
	if r.Bundle == nil || len(id.revisions) != 1 || api.Describe(ctx, &stages[0], cliB).Bundle != r.Bundle {
		t.Fatalf("describe %+v", r)
	}
	gone := stages[0]
	gone.Spec.ApplyRef.Name = "missing"
	if api.Describe(ctx, &gone, cliA); len(id.revisions) != 1 {
		t.Fatal("a failed read is not cached")
	}
	for i := 0; i < maxRevisions; i++ {
		id.revisions[string(rune(i))] = delivery.Revision{}
	}
	if api.Describe(ctx, &stages[0], cliA); len(id.revisions) != 1 {
		t.Fatalf("a full cache resets: %d", len(id.revisions))
	}
	if refusal(nil) != nil || refusal(errors.New("x")).Error() != "x" {
		t.Fatal("refusal passes anything else through")
	}
	if err := refusal(apierrors.NewForbidden(schema.GroupResource{Resource: "approvals"}, "", errors.New("spec.groups must be the groups of the user"))); !isRefusal(err, http.StatusForbidden) ||
		err.(*dashboard.DeliveryError).Code != "Forbidden" {
		t.Fatalf("a VAP refusal keeps its status and message: %v", err)
	}
	var _ app.DeliveryRegistry = cliRegistry{}
}
