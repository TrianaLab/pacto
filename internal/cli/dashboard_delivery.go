package cli

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
)

// deliveryGroupVersion is the API the chart's delivery section installs.
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
	auth    *authn.Dashboard
	cluster *rest.Config // nil: no cluster, so no delivery
	// policyNS is where the promoter's identity policy is published.
	policyNS  string
	registry  func(...string) app.DeliveryRegistry
	records   app.RecordRepo
	trust     *delivery.TrustConfig
	newClient func(*rest.Config) (dynamic.Interface, error)
	served    func(*rest.Config, string) (bool, error)
	now       func() time.Time
	state     func(context.Context) authn.State

	// probe guards the discovery answer; mu guards revisions. A slow cluster
	// never holds up describe.
	probe     sync.Mutex
	checked   time.Time
	installed bool
	probeErr  error

	mu        sync.Mutex
	revisions map[string]delivery.Revision
}

// Discovery: an answer is kept a minute, a failure a few seconds, and no call
// waits more than discoveryTimeout on a cluster that does not answer.
const (
	installedTTL     = time.Minute
	failureTTL       = 10 * time.Second
	discoveryTimeout = 5 * time.Second
)

// newDashboardIdentity picks the mode: shared with OIDC when an issuer is
// configured, local on a loopback bind without one, and open (no delivery)
// anywhere else.
func newDashboardIdentity(v *viper.Viper, host string, cluster *rest.Config, d deps) (*dashboardIdentity, error) {
	trust, err := deliveryTrust(v.GetString("dashboard.delivery-trust"))
	if err != nil {
		return nil, err
	}
	id := &dashboardIdentity{cluster: cluster, registry: d.newDeliveryRegistry, now: time.Now, served: d.served, state: authn.StateFrom,
		newClient: d.dynamicFor, records: d.newRecordRepo, trust: trust,
		revisions: map[string]delivery.Revision{}, policyNS: v.GetString("dashboard.delivery-namespace")}
	switch issuer := v.GetString("dashboard.oidc-issuer-url"); {
	case issuer != "":
		o, err := authn.NewOIDC(authn.Config{IssuerURL: issuer, ClientID: v.GetString("dashboard.oidc-client-id"),
			ClientSecret: v.GetString("dashboard.oidc-client-secret"), RedirectURL: v.GetString("dashboard.oidc-redirect-url"),
			ExtraScopes: splitList(v.GetStringSlice("dashboard.oidc-extra-scopes"))})
		if err != nil {
			return nil, err
		}
		id.auth = authn.NewShared(o)
		if v.GetBool("dashboard.auth-required") {
			id.auth.RequireLogin()
		}
	case v.GetBool("dashboard.auth-required"):
		return nil, errors.New("--auth-required needs sign-in: set --oidc-issuer-url")
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
			return dashboard.AuthState{SignedIn: st.SignedIn, Fresh: st.Fresh, CSRF: st.CSRF, Name: tokenName(st.Token)}
		}}
	if id.cluster != nil {
		o.Delivery = id.delivery
		o.DeliveryInstalled = id.deliveryInstalled
	}
}

// tokenName is the person's name in an ID token the identity layer already
// verified: name, else email; empty when the token says neither.
func tokenName(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	_ = json.Unmarshal(payload, &c) // a token without these claims has no name
	return cmp.Or(c.Name, c.Email)
}

// deliveryInstalled asks discovery, with the dashboard's own credentials,
// whether the delivery API group is served; the answer is kept a minute, so
// installing the chart later needs no restart. A failed call is returned and
// kept only briefly, so a cluster that is down is not asked on every request.
func (id *dashboardIdentity) deliveryInstalled(context.Context) (bool, error) {
	id.probe.Lock()
	defer id.probe.Unlock()
	now := id.now()
	ttl := installedTTL
	if id.probeErr != nil {
		ttl = failureTTL
	}
	if now.Sub(id.checked) > ttl {
		cfg := rest.CopyConfig(id.cluster)
		cfg.Timeout = discoveryTimeout
		id.installed, id.probeErr = id.served(cfg, deliveryGroupVersion)
		id.checked = now
	}
	return id.installed && id.probeErr == nil, id.probeErr
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
	return dashDelivery{d: &app.Delivery{Dynamic: dyn, Registry: id.registry, Records: id.records, Trust: id.trust,
		Policy: delivery.DefaultClassPolicy(), PolicyNamespace: id.policyNS, Now: id.now}, id: id}, nil
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
// their status and message, a stale plan carries the fresh one, and every
// other error keeps its reason as a refusal.
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
	var netErr net.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded):
		return dashboard.Refuse(http.StatusServiceUnavailable, "ClusterUnreachable", err.Error())
	}
	// Everything else is delivery saying no to the request itself.
	return dashboard.Refuse(http.StatusUnprocessableEntity, "Refused", err.Error())
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

func (a dashDelivery) Stage(ctx context.Context, ns, name string) (*delivery.StageObject, error) {
	s, err := a.d.Stage(ctx, ns, name)
	return s, refusal(err)
}

func (a dashDelivery) Blame(ctx context.Context, ns, stage, digest string) (*delivery.Blame, error) {
	b, err := a.d.Blame(ctx, ns, stage, digest)
	return b, refusal(err)
}

func (a dashDelivery) Undo(ctx context.Context, ns, name, stage string) (*delivery.Undo, error) {
	u, err := a.d.Undo(ctx, ns, name, stage)
	return u, refusal(err)
}

// PublishRefusals reads the version-rule refusals pacto publish kept in each
// service's contract bundle repository: the one the versions its Stages know
// name. A service whose repository cannot be found or read makes the answer
// partial, never "none".
// ponytail: signatures are not checked (the dashboard holds no publisher
// key), so the note says a refusal is what the registry holds; verify against
// the key the Stage's OCIRepository verifies with if forged refusals matter.
func (a dashDelivery) PublishRefusals(ctx context.Context, ns string) (*dashboard.PublishRefusals, error) {
	if a.d.Records == nil {
		return nil, nil
	}
	stages, err := a.d.Stages(ctx, ns)
	if err != nil {
		return nil, refusal(err)
	}
	repos, services := map[string]string{}, []string{}
	for i := range stages {
		st := &stages[i]
		svc := st.Spec.Service.Name
		if _, ok := repos[svc]; !ok {
			services = append(services, svc)
		}
		if repos[svc] == "" {
			repos[svc] = a.contractRepo(ctx, st)
		}
	}
	out := &dashboard.PublishRefusals{Completeness: "complete", Refusals: []dashboard.PublishRefusal{},
		Note: "read from each service's contract bundle repository; their signatures are not checked here"}
	for _, svc := range services {
		if err := a.refusalsOf(ctx, svc, repos[svc], out); err != nil {
			out.Completeness, out.Unreadable = "partial", append(out.Unreadable, svc+": "+err.Error())
		}
	}
	return out, nil
}

// contractRepo is the contract bundle repository the first version st knows
// names, or "" when none does.
func (a dashDelivery) contractRepo(ctx context.Context, st *delivery.StageObject) string {
	ss := st.Status
	for _, d := range slices.Concat([]string{ss.Pointer}, ss.Published, ss.Offered, ss.Verified, ss.FailedDigests) {
		if d == "" {
			continue
		}
		if repo, _, ok := strings.Cut(contract.TrimOCIScheme(a.Describe(ctx, st, d).ContractRef), "@"); ok {
			return repo
		}
	}
	return ""
}

func (a dashDelivery) refusalsOf(ctx context.Context, svc, repo string, out *dashboard.PublishRefusals) error {
	if repo == "" {
		return errors.New("no version its Stages know names a contract, so where its refusals are kept is unknown")
	}
	rr, err := a.d.Records(repo)
	if err != nil {
		return err
	}
	bounded, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	l, err := deliveryrecord.ListRefusals(bounded, rr, svc, func([]byte) error { return nil })
	if err != nil {
		return fmt.Errorf("reading %s: %w", repo, err)
	}
	for _, u := range l.Unreadable {
		out.Completeness, out.Unreadable = "partial", append(out.Unreadable, svc+": "+u)
	}
	for _, r := range l.Refusals {
		p := r.Statement.Predicate
		out.Refusals = append(out.Refusals, dashboard.PublishRefusal{Service: p.Service, Version: p.Version, Check: "version",
			Reason: p.Message, Baseline: p.Baseline, RequiredBump: p.RequiredBump, At: p.At})
	}
	return nil
}

func (a dashDelivery) Describe(ctx context.Context, stage *delivery.StageObject, digest string) delivery.Revision {
	return a.id.describe(ctx, a.d, stage, digest)
}
