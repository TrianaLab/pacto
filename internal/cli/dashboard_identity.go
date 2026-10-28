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
	"time"

	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

// newDashboardIdentity picks the mode: shared when an issuer or token sign-in
// is configured (people sign in before the dashboard shows anything), local on
// a loopback bind without either, and open (no delivery) anywhere else.
func newDashboardIdentity(v *viper.Viper, host string, cluster *rest.Config, d deps) (*dashboardIdentity, error) {
	trust, err := deliveryTrust(v.GetString("dashboard.delivery-trust"))
	if err != nil {
		return nil, err
	}
	id := &dashboardIdentity{cluster: cluster, registry: d.newDeliveryRegistry, now: time.Now, served: d.served, state: authn.StateFrom,
		newClient: d.dynamicFor, records: d.newRecordRepo, trust: trust,
		revisions: map[string]delivery.Revision{}, stageNS: map[string]stageNamespaces{}, policyNS: v.GetString("dashboard.delivery-namespace"), reads: newReadCache(),
		loginName: v.GetString("dashboard.oidc-display-name")}
	life := authn.Lifetime{Idle: v.GetDuration("dashboard.session-idle-timeout"), Max: v.GetDuration("dashboard.session-max-age")}
	var review authn.TokenReview // nil: no cluster to ask who a token is
	if cluster != nil {
		review = authn.SelfSubjectReview(cluster, d.dynamicFor)
	}
	var o *authn.OIDC
	if issuer := v.GetString("dashboard.oidc-issuer-url"); issuer != "" {
		client, cerr := authn.IssuerClient(issuer, v.GetString("dashboard.oidc-internal-issuer-url"), v.GetString("dashboard.oidc-ca-file"))
		if cerr != nil {
			return nil, cerr
		}
		if o, err = authn.NewOIDC(authn.Config{IssuerURL: issuer, HTTPClient: client, ClientID: v.GetString("dashboard.oidc-client-id"),
			ClientSecret: v.GetString("dashboard.oidc-client-secret"), RedirectURL: v.GetString("dashboard.oidc-redirect-url"),
			ExtraScopes: splitList(v.GetStringSlice("dashboard.oidc-extra-scopes")), Lifetime: life, Review: review,
			Claims: authn.Claims{UsernameClaim: v.GetString("dashboard.oidc-username-claim"), UsernamePrefix: v.GetString("dashboard.oidc-username-prefix"),
				GroupsClaim: v.GetString("dashboard.oidc-groups-claim"), GroupsPrefix: v.GetString("dashboard.oidc-groups-prefix")}}); err != nil {
			return nil, err
		}
	}
	var t *authn.TokenLogin
	if v.GetBool("dashboard.token-login") {
		if cluster == nil {
			return nil, errors.New("--token-login needs a cluster to check tokens against")
		}
		t = authn.NewTokenLogin(review, life)
	}
	switch {
	case o != nil || t != nil:
		id.auth = authn.NewShared(o, t)
		id.viewer = authn.NewViewer(clusterReview(cluster, d.dynamicFor))
	case isLoopback(host):
		id.auth = authn.NewLocal()
	default:
		id.auth = authn.NewOpen()
	}
	return id, nil
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
				User: st.Person.User, Groups: st.Person.Groups, Method: st.Method, AuthTime: st.AuthTime}
		}}
	if id.viewer != nil {
		o.View = id.view
	}
	if id.cluster != nil {
		o.Delivery = id.delivery
		o.DeliveryInstalled = id.deliveryInstalled
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
	r, err := id.viewer.Query(ctx, p, q)
	if err != nil {
		return nil, fmt.Errorf("checking your Kubernetes permissions failed: %w", err)
	}
	return r, nil
}

// errNoClusterToAsk: the fleet holds Kubernetes namespaces but the dashboard
// has no cluster to ask who may read them.
var errNoClusterToAsk = errors.New("the dashboard has no cluster to ask who may read which namespace")

// clusterReview asks the cluster, with the dashboard's own credentials, what a
// person may read.
func clusterReview(cluster *rest.Config, newClient func(*rest.Config) (dynamic.Interface, error)) authn.Review {
	var dyn dynamic.Interface
	err := errNoClusterToAsk
	if cluster != nil {
		// client-go's default 5 a second made fifty people signing in at once wait ten seconds (see sarQPS).
		cfg := rest.CopyConfig(cluster)
		cfg.QPS, cfg.Burst = sarQPS, sarBurst
		dyn, err = newClient(cfg)
	}
	if err != nil {
		return func(context.Context, authn.Person, string) (bool, error) { return false, err }
	}
	return authn.SubjectAccessReview(dyn)
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
