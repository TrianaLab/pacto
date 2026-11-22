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
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

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

// errOpenNeedsAllowAnonymous stops a dashboard that would show the whole fleet
// to everyone who reaches it, unless someone chose that.
var errOpenNeedsAllowAnonymous = errors.New("serving beyond loopback without sign-in exposes the whole fleet; " +
	"configure --oidc-issuer-url or --token-login, or pass --allow-anonymous")

// newDashboardIdentity picks the mode: shared when an issuer or token sign-in
// is configured (people sign in before the dashboard shows anything), local on
// a loopback bind without either, and open (no delivery) anywhere else, only
// with --allow-anonymous.
//
// cluster is the sign-in cluster; entries are the resolved contexts the fleet
// reads, whose reviewers narrow each signed-in person's view.
func newDashboardIdentity(v *viper.Viper, host string, cluster *rest.Config, entries func(context.Context) []app.K8sEntry, d deps) (*dashboardIdentity, error) {
	trust, err := app.DeliveryTrust(v.GetString("dashboard.delivery-trust"))
	if err != nil {
		return nil, err
	}
	mode, namespaces := v.GetString("dashboard.review"), splitNamespaces(v.GetString("dashboard.namespace"))
	if err := checkReview(mode, namespaces); err != nil {
		return nil, err
	}
	id := baseDashboardIdentity(cluster, d, trust, v.GetString("dashboard.delivery-namespace"))
	id.loginName = v.GetString("dashboard.oidc-display-name")
	life := authn.Lifetime{Idle: v.GetDuration("dashboard.session-idle-timeout"), Max: v.GetDuration("dashboard.session-max-age")}
	// nil: no cluster to ask who a token is. bearer names an ID token and
	// builds its client per call, so a dashboard without token sign-in builds
	// none at startup.
	var bearer authn.TokenReview
	if cluster != nil {
		bearer = authn.SelfSubjectReview(cluster, d.dynamicFor)
	}
	var o *authn.OIDC
	if issuer := v.GetString("dashboard.oidc-issuer-url"); issuer != "" {
		client, cerr := authn.IssuerClient(issuer, v.GetString("dashboard.oidc-internal-issuer-url"), v.GetString("dashboard.oidc-ca-file"))
		if cerr != nil {
			return nil, cerr
		}
		if o, err = authn.NewOIDC(authn.Config{IssuerURL: issuer, HTTPClient: client, ClientID: v.GetString("dashboard.oidc-client-id"),
			ClientSecret: v.GetString("dashboard.oidc-client-secret"), RedirectURL: v.GetString("dashboard.oidc-redirect-url"),
			ExtraScopes: splitList(v.GetStringSlice("dashboard.oidc-extra-scopes")), Lifetime: life, Review: bearer,
			Claims: claimsFlags(func(name string) string { return v.GetString("dashboard." + name) })}); err != nil {
			return nil, err
		}
	}
	if id.signing, err = newDashboardSigning(v, o); err != nil {
		return nil, err
	}
	var t *authn.TokenLogin
	var review authn.TokenReview // nil: bearer readers come with token sign-in
	if v.GetBool("dashboard.token-login") {
		if cluster == nil {
			return nil, errors.New("--token-login needs a cluster to check tokens against")
		}
		// review tells a sign-in link token from a person's pasted token.
		own, oerr := sarClient(cluster, d.dynamicFor)
		if oerr != nil {
			return nil, oerr
		}
		id.audience, id.bearer = authn.AudienceReview(own, authn.DashboardAudience), bearer
		review = authn.DashboardTokenReview(id.audience, bearer)
		t = authn.NewTokenLogin(review, life)
	}
	switch {
	case o != nil || t != nil:
		id.auth = authn.NewShared(o, t).AcceptBearer(review).WithSigner(id.signing.login())
		home := newHomeCluster(cluster, d)
		id.viewer, id.homeUID = clusterViewer(entries, home, d, mode, namespaces), home.UID
	case isLoopback(host):
		id.auth = authn.NewLocal()
	case v.GetBool("dashboard.allow-anonymous"):
		id.auth = authn.NewOpen()
	default:
		return nil, errOpenNeedsAllowAnonymous
	}
	return id, nil
}

// baseDashboardIdentity is an identity with everything delivery reads need
// set; the dashboard adds its sign-in on top, the MCP delivery reads use it
// as it is.
func baseDashboardIdentity(cluster *rest.Config, d deps, trust *delivery.TrustConfig, policyNS string) *dashboardIdentity {
	return &dashboardIdentity{cluster: cluster, registry: d.newDeliveryRegistry, now: time.Now, served: d.served, state: authn.StateFrom,
		newClient: d.dynamicFor, records: d.newRecordRepo, trust: trust,
		revisions: map[string]delivery.Revision{}, stageNS: map[string]stageNamespaces{}, policyNS: policyNS, reads: newReadCache()}
}

// options are the dashboard options this identity fills in.
func (id *dashboardIdentity) options(o *dashboard.Options) {
	o.Auth = &dashboard.Auth{Local: id.auth.Local(), Login: id.auth.Login(), LoginName: id.loginName, TokenLogin: id.auth.TokenLogin(),
		Routes: id.auth.Routes(), Middleware: id.auth.Middleware,
		State: func(r *http.Request) dashboard.AuthState {
			st := id.state(r.Context())
			if st.SignedIn && id.auth.Local() {
				st.Person, st.AuthTime = id.localSelf(r.Context())
			}
			// User and Groups: who the fleet views are narrowed for.
			return dashboard.AuthState{SignedIn: st.SignedIn, CSRF: st.CSRF, Name: tokenName(st.Token),
				User: st.Person.User, Groups: st.Person.Groups, Method: st.Method, AuthTime: st.AuthTime, FleetOnly: linkSession(st)}
		}}
	if id.viewer != nil {
		o.View = id.view
		o.OperationAccess = id.operationAccess(o.Fleet)
	}
	if id.cluster != nil {
		o.Delivery = id.delivery
		o.DeliveryInstalled, o.DeliveryCheckedAt = id.deliveryInstalled, id.deliveryCheckedAt
	}
}

// localSelf is the kubeconfig user as the API server names them, with their
// login time when it is known. A local dashboard acts as that user and the
// kubeconfig never changes while it runs, so the answer is kept; a failure is
// asked again after failureTTL. Without a cluster nobody is named.
func (id *dashboardIdentity) localSelf(ctx context.Context) (authn.Person, string) {
	id.selfMu.Lock()
	defer id.selfMu.Unlock()
	if id.cluster == nil || id.self.User != "" || id.now().Sub(id.selfAt) < failureTTL {
		return id.self, id.selfAuthTime
	}
	id.selfAt = id.now()
	dyn, err := id.newClient(id.cluster)
	if err != nil {
		return id.self, ""
	}
	ctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	p, extra, err := authn.ReviewSelf(ctx, dyn)
	if err != nil {
		return id.self, ""
	}
	id.self = p
	if v := extra[authn.AuthTimeExtra]; len(v) > 0 {
		id.selfAuthTime = v[0]
	}
	return id.self, id.selfAuthTime
}

// view is the fleet as the caller's Kubernetes RBAC lets them read it: a
// signed-in person as the cluster names them, anyone else as the apiserver's
// anonymous user.
func (id *dashboardIdentity) view(ctx context.Context, q *fleet.Query) (*fleet.Query, error) {
	p := authn.Anonymous
	if st := id.state(ctx); st.SignedIn {
		p = st.Person
	}
	return id.viewer.Query(ctx, p, q)
}

// operationAccess says which Stage namespaces the caller may list operations
// in, for the run records of the Metrics page: each namespace of their fleet
// view in the sign-in cluster is reviewed for the person (as the API server
// names their token, with uid and extra; anyone else as its anonymous user)
// through the shared dashboard's cached SubjectAccessReview, the rule every
// delivery view on it follows. Namespaces outside the view are refused
// unreviewed, so registry data cannot choose what is asked, and records of
// any other cluster are hidden and counted (id.homeUID, read from its cache).
func (id *dashboardIdentity) operationAccess(full func(context.Context) (*fleet.Query, error)) func(context.Context, []string) (stats.OperationAccess, error) {
	return func(ctx context.Context, nss []string) (stats.OperationAccess, error) {
		var pairs []fleet.ClusterNamespace
		if full != nil {
			q, err := full(ctx)
			if err == nil {
				q, err = id.view(ctx, q)
			}
			if err != nil {
				return stats.OperationAccess{}, err
			}
			pairs = q.Namespaces()
		}
		known, clusters := homeNamespaces(ctx, id.homeUID, pairs)
		who := sync.OnceValues(func() (subject, error) { return id.subject(ctx) })
		visible, err := reviewOperations(ctx, known, nss, func(ctx context.Context, ns string) (bool, error) {
			p, err := who()
			if err != nil {
				return false, err
			}
			return id.shared.may(ctx, p, "list", sharedOps, ns, "")
		})
		return stats.OperationAccess{Visible: visible, Clusters: clusters}, err
	}
}

// subject is the caller as the API server names them, for a review through
// the shared cache, which it starts.
func (id *dashboardIdentity) subject(ctx context.Context) (subject, error) {
	var s *sharedReads
	if id.cluster != nil {
		s = id.sharedReads() // nil when no client can be built
	}
	if s == nil {
		return subject{}, errNoClusterToAsk
	}
	st := id.state(ctx)
	if !st.SignedIn {
		return subject{User: authn.Anonymous.User, Groups: authn.Anonymous.Groups}, nil
	}
	if linkSession(st) {
		// The TokenReview named this identity at sign-in and on every recheck;
		// a SelfSubjectReview without a token would name system:anonymous.
		// Bearer sessions keep whoami, which carries uid and extra from their
		// own token.
		return subject{User: st.Person.User, UID: st.UID, Groups: st.Person.Groups, Extra: st.Extra}, nil
	}
	dyn, err := id.newClient(authn.RESTConfig(id.cluster, st.Token))
	if err != nil {
		return subject{}, err
	}
	return s.whoami(ctx, sharedClient{Interface: dyn, s: s, token: st.Token})
}

// linkSession reports a sign-in link session: a token session that holds no
// apiserver credential, so it reads the fleet index and nothing else.
func linkSession(st authn.State) bool { return st.Method == "token" && st.Token == "" }

// errNoClusterToAsk: the fleet holds Kubernetes namespaces but the dashboard
// has no cluster to ask who may read them.
var errNoClusterToAsk = errors.New("the dashboard has no cluster to ask who may read which namespace")

// The review modes of --review: cluster asks one cluster-scoped review about
// every namespace at once first (it needs a ClusterRole); local asks a
// LocalSubjectAccessReview in each watched namespace only (a namespaced Role
// grants it), and nobody then reads a service without a namespace.
const (
	reviewCluster = "cluster"
	reviewLocal   = "local"
)

// reviewUsage is the --review help of the dashboard and the MCP server.
const reviewUsage = "how people are reviewed: cluster asks once about every namespace and needs a ClusterRole; " +
	"local asks in each --namespace only, which a namespaced Role grants, and then services without a namespace show to nobody"

// errLocalReviewNeedsNamespace: a local review only asks in named namespaces.
var errLocalReviewNeedsNamespace = errors.New("--review=local needs --namespace")

// checkReview refuses a review mode it does not know, and a local review
// with no namespace to ask in. Empty is the cluster review.
func checkReview(mode string, namespaces []string) error {
	switch mode {
	case "", reviewCluster:
		return nil
	case reviewLocal:
		if len(namespaces) == 0 {
			return errLocalReviewNeedsNamespace
		}
		return nil
	}
	return fmt.Errorf("--review must be %s or %s, not %q", reviewCluster, reviewLocal, mode)
}

// clusterViewer narrows answers to what each person may read in each cluster
// of entries, reviewed there with that cluster's own credentials, in the
// namespaces list only when mode is local (checkReview passed). home is the
// sign-in cluster, the only one that reviews a cluster-local identity.
func clusterViewer(entries func(context.Context) []app.K8sEntry, home homeCluster, d deps, mode string, namespaces []string) *authn.Viewer {
	set := clusterReviews(entries, home, d, mode)
	if mode == reviewLocal {
		return authn.NewScopedViewer(set.Reviews, namespaces).SignInCluster(set.Home)
	}
	return authn.NewViewer(set.Reviews).SignInCluster(set.Home)
}

// clusterReview asks the cluster, with the dashboard's own credentials, what a
// person may read: per namespace when mode is local, otherwise cluster-wide.
func clusterReview(cluster *rest.Config, newClient func(*rest.Config) (dynamic.Interface, error), mode string) authn.Review {
	dyn, err := sarClient(cluster, newClient)
	if err != nil {
		return func(context.Context, authn.Person, string, authn.ResourceAttributes) (bool, error) { return false, err }
	}
	if mode == reviewLocal {
		return authn.LocalSubjectAccessReview(dyn)
	}
	return authn.SubjectAccessReview(dyn)
}

// sarClient is the dashboard's own client for the reviews it asks for people
// (SubjectAccessReview, TokenReview).
func sarClient(cluster *rest.Config, newClient func(*rest.Config) (dynamic.Interface, error)) (dynamic.Interface, error) {
	if cluster == nil {
		return nil, errNoClusterToAsk
	}
	// client-go's default 5 a second made fifty people signing in at once wait ten seconds (see sarQPS).
	cfg := rest.CopyConfig(cluster)
	cfg.QPS, cfg.Burst = sarQPS, sarBurst
	return newClient(cfg)
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
