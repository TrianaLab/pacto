package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// deliveryGroupVersion is the API the delivery chart installs.
const deliveryGroupVersion = "delivery.pacto.trianalab.io/v1alpha1"

// splitList reads a list flag that may also come from an environment
// variable as one comma- or space-separated string.
func splitList(in []string) []string {
	var out []string
	for _, s := range in {
		out = append(out, strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })...)
	}
	return out
}

// isLoopback reports whether the dashboard binds only this machine.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// dashboardIdentity is how the dashboard identifies callers and acts for them.
type dashboardIdentity struct {
	auth      *authn.Dashboard
	cluster   *rest.Config // nil: no cluster, so no delivery
	policy    delivery.ClassPolicy
	registry  func(...string) app.DeliveryRegistry
	newClient func(*rest.Config) (dynamic.Interface, error)
	served    func(*rest.Config, string) (bool, error)
	now       func() time.Time
	state     func(context.Context) authn.State

	mu        sync.Mutex
	checked   time.Time
	installed bool
	revisions map[string]delivery.Revision
}

// newDashboardIdentity picks the mode: shared with OIDC when an issuer is
// configured, local on a loopback bind without one, and open (no delivery)
// anywhere else.
func newDashboardIdentity(v *viper.Viper, host string, cluster *rest.Config, d deps) (*dashboardIdentity, error) {
	id := &dashboardIdentity{cluster: cluster, registry: d.newDeliveryRegistry, now: time.Now, served: d.served, state: authn.StateFrom,
		newClient: d.dynamicFor,
		revisions: map[string]delivery.Revision{},
		policy: delivery.ClassPolicy{AgentGroups: splitList(v.GetStringSlice("dashboard.agent-groups")),
			BreakGlassGroups: splitList(v.GetStringSlice("dashboard.break-glass-groups")),
			TeamGroupPrefix:  v.GetString("dashboard.team-group-prefix")}}
	switch issuer := v.GetString("dashboard.oidc-issuer-url"); {
	case issuer != "":
		o, err := authn.NewOIDC(authn.Config{IssuerURL: issuer, ClientID: v.GetString("dashboard.oidc-client-id"),
			ClientSecret: v.GetString("dashboard.oidc-client-secret"), RedirectURL: v.GetString("dashboard.oidc-redirect-url"),
			ExtraScopes: splitList(v.GetStringSlice("dashboard.oidc-extra-scopes"))})
		if err != nil {
			return nil, err
		}
		id.auth = authn.NewShared(o)
	case isLoopback(host):
		id.auth = authn.NewLocal()
	default:
		id.auth = authn.NewOpen()
	}
	return id, nil
}

// options are the dashboard options this identity fills in.
func (id *dashboardIdentity) options(o *dashboard.Options) {
	o.Auth = &dashboard.Auth{Local: id.auth.Local(), Login: id.auth.Login(), Routes: id.auth.Routes(), Middleware: id.auth.Middleware,
		State: func(r *http.Request) dashboard.AuthState {
			st := id.state(r.Context())
			return dashboard.AuthState{SignedIn: st.SignedIn, Fresh: st.Fresh, CSRF: st.CSRF}
		}}
	o.DeliveryPolicy = id.policy
	if id.cluster != nil {
		o.Delivery = id.delivery
		o.DeliveryInstalled = id.deliveryInstalled
	}
}

// deliveryInstalled asks discovery, with the dashboard's own credentials,
// whether the delivery API group is served; the answer is kept a minute, so
// installing the chart later needs no restart. A failed call is not an
// answer: it is returned and asked again next time.
func (id *dashboardIdentity) deliveryInstalled(context.Context) (bool, error) {
	id.mu.Lock()
	defer id.mu.Unlock()
	if now := id.now(); now.Sub(id.checked) > time.Minute {
		installed, err := id.served(id.cluster, deliveryGroupVersion)
		if err != nil {
			return false, err
		}
		id.installed, id.checked = installed, now
	}
	return id.installed, nil
}

// delivery is the caller's delivery API: the kubeconfig user in local mode,
// the signed-in person's own token in shared mode, nothing in open mode. The
// dashboard's own credentials are never used for delivery.
func (id *dashboardIdentity) delivery(r *http.Request) (dashboard.DeliveryAPI, error) {
	cfg := id.cluster
	switch {
	case id.auth.Local():
	case !id.auth.Login():
		return nil, dashboard.Refuse(http.StatusForbidden, "LoginNotConfigured",
			"this dashboard is shared without sign-in, so it serves no delivery data and no actions")
	default:
		st := id.state(r.Context())
		if !st.SignedIn {
			return nil, dashboard.Refuse(http.StatusUnauthorized, "LoginRequired", "sign in to see and act on delivery")
		}
		cfg = authn.RESTConfig(id.cluster, st.Token)
	}
	dyn, err := id.newClient(cfg)
	if err != nil {
		return nil, err
	}
	return dashDelivery{d: &app.Delivery{Dynamic: dyn, Registry: id.registry, Policy: id.policy, Now: id.now}, id: id}, nil
}

// describe caches what a digest carries: a digest never changes.
// ponytail: a bounded map reset when full; an LRU if dashboards ever cycle
// through more than maxRevisions digests.
const maxRevisions = 1024

func (id *dashboardIdentity) describe(ctx context.Context, d *app.Delivery, stage *delivery.StageObject, digest string) delivery.Revision {
	key := stage.Spec.ApplyRef.Name + "/" + stage.Metadata.Namespace + "@" + digest
	id.mu.Lock()
	r, ok := id.revisions[key]
	id.mu.Unlock()
	if ok {
		return r
	}
	r = d.Describe(ctx, stage, digest)
	if r.Bundle != nil || r.NoContract {
		id.mu.Lock()
		if len(id.revisions) >= maxRevisions {
			id.revisions = map[string]delivery.Revision{}
		}
		id.revisions[key] = r
		id.mu.Unlock()
	}
	return r
}

// dashDelivery adapts app.Delivery to the dashboard: apiserver refusals keep
// their status and message, and a stale plan carries the fresh one.
type dashDelivery struct {
	d  *app.Delivery
	id *dashboardIdentity
}

func refusal(err error) error {
	var stale *app.PlanStaleError
	if errors.As(err, &stale) {
		return &dashboard.DeliveryError{Status: http.StatusConflict, Code: "PlanStale", Detail: err.Error(), Plan: &stale.Plan}
	}
	var api apierrors.APIStatus
	if errors.As(err, &api) {
		st := api.Status()
		return dashboard.Refuse(int(st.Code), string(st.Reason), st.Message)
	}
	return err
}

func (a dashDelivery) WhoAmI(ctx context.Context) (delivery.Caller, error) {
	c, err := a.d.WhoAmI(ctx)
	return c, refusal(err)
}

func (a dashDelivery) Stages(ctx context.Context, ns string) ([]delivery.StageObject, error) {
	s, err := a.d.Stages(ctx, ns)
	return s, refusal(err)
}

func (a dashDelivery) Operations(ctx context.Context, ns string) ([]delivery.OperationObject, error) {
	o, err := a.d.Operations(ctx, ns)
	return o, refusal(err)
}

func (a dashDelivery) Get(ctx context.Context, ns, name string) (*delivery.OperationObject, error) {
	o, err := a.d.Get(ctx, ns, name)
	return o, refusal(err)
}

func (a dashDelivery) Explain(ctx context.Context, ns, name string) (*delivery.Explanation, error) {
	e, err := a.d.Explain(ctx, ns, name)
	return e, refusal(err)
}

func (a dashDelivery) History(ctx context.Context, ns, stage string) (*delivery.History, error) {
	h, err := a.d.History(ctx, ns, stage)
	return h, refusal(err)
}

func (a dashDelivery) Plan(ctx context.Context, req delivery.Request) (delivery.Plan, error) {
	p, _, err := a.d.Plan(ctx, req)
	return p, refusal(err)
}

func (a dashDelivery) Propose(ctx context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error) {
	o, p, err := a.d.Propose(ctx, req)
	return o, p, refusal(err)
}

func (a dashDelivery) Approve(ctx context.Context, ns, name string, decision delivery.Decision, note string) (string, error) {
	n, err := a.d.Approve(ctx, ns, name, decision, note)
	return n, refusal(err)
}

func (a dashDelivery) Cancel(ctx context.Context, ns, name string) error {
	return refusal(a.d.Cancel(ctx, ns, name))
}

func (a dashDelivery) Describe(ctx context.Context, stage *delivery.StageObject, digest string) delivery.Revision {
	return a.id.describe(ctx, a.d, stage, digest)
}
