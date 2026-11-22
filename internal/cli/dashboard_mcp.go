package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// dashboardMCPSource is installation.mcp's source on a dashboard that serves
// /mcp.
const dashboardMCPSource = "this dashboard, at /mcp"

var (
	errMCPNeedsSignIn = errors.New("--mcp needs sign-in: --token-login or --oidc-issuer-url with --mcp-exchange-audience")
	errMCPNoExchange  = errors.New("--mcp with --oidc-issuer-url alone has no way to name callers to the cluster: " +
		"set --mcp-exchange-audience to exchange issuer tokens, or --token-login to accept Kubernetes tokens")
	errMCPExchangeNeedsURL    = errors.New("--mcp-exchange-audience needs --mcp-url, the /mcp URL agents reach: issuer tokens must name it as their audience")
	errMCPExchangeNeedsIssuer = errors.New("--mcp-exchange-audience needs --oidc-issuer-url, the issuer whose tokens are exchanged")
	errMCPNeedsCluster        = errors.New("--mcp needs a cluster to name each caller: run the dashboard where a kubeconfig or in-cluster configuration resolves")
	errMCPURL                 = errors.New("--mcp-url must be an absolute URL whose path is /mcp, as agents reach it (for example https://pacto.example/mcp)")
)

// mountDashboardMCP mounts /mcp on opts with --mcp and returns the banner
// line naming it; without --mcp it changes nothing.
func mountDashboardMCP(v *viper.Viper, svc *app.Service, version string, repos []string, id *dashboardIdentity, src pactomcp.FleetQuerySource,
	prov *app.ProvenanceChecker, d deps, opts *dashboard.Options) (string, error) {
	if !v.GetBool("dashboard.mcp") {
		return "", nil
	}
	outside, err := newDashboardMCP(v, svc, version, repos, id, src, prov, d)
	if err != nil {
		return "", err
	}
	opts.Outside, opts.MCPServed, opts.MCPSource = outside, true, dashboardMCPSourceFor(v, id)
	return "MCP: " + cmp.Or(v.GetString("dashboard.mcp-url"), "/mcp") + "\n", nil
}

// dashboardMCPSourceFor is installation.mcp's source: without token sign-in
// /mcp takes only issuer tokens, at the fixed URL they name as audience, so
// the source says so and the Data sources line shows no Kubernetes-token
// recipe.
func dashboardMCPSourceFor(v *viper.Viper, id *dashboardIdentity) string {
	if id.audience != nil {
		return dashboardMCPSource
	}
	return "this dashboard, at " + v.GetString("dashboard.mcp-url") + "; your MCP client signs in with " + v.GetString("dashboard.oidc-issuer-url")
}

// newDashboardMCP builds the dashboard's /mcp endpoint and its RFC 9728
// metadata from what the dashboard already holds: its identity (sign-in,
// viewer, trust and policy namespace), its fleet and its delivery probe. Every
// request carries a bearer the handlers verify themselves, so they mount
// outside the session guard (dashboard.Options.Outside).
func newDashboardMCP(v *viper.Viper, svc *app.Service, version string, repos []string, id *dashboardIdentity, src pactomcp.FleetQuerySource,
	prov *app.ProvenanceChecker, d deps) (map[string]http.Handler, error) {
	issuer, resource, exchange := v.GetString("dashboard.oidc-issuer-url"), v.GetString("dashboard.mcp-url"), v.GetString("dashboard.mcp-exchange-audience")
	if err := checkDashboardMCP(id, issuer, resource, exchange); err != nil {
		return nil, err
	}
	a, err := dashboardMCPAuth(v, id, issuer, resource, exchange, d)
	if err != nil {
		return nil, err
	}
	rm := remoteMCP{version: version, auth: a, cluster: id.cluster, newClient: d.dynamicFor,
		claims: claimsFlags(func(name string) string { return v.GetString("dashboard." + name) })}
	rm.modes = pactomcp.Modes{Transport: "http", Fleet: &pactomcp.FleetMode{Src: src, Opts: pactomcp.FleetOptions{
		View: viewerFleetView(id.viewer), Impact: dashboardMCPImpact(svc, prov), DashboardURL: strings.TrimSuffix(resource, "/mcp")}}}
	rm.authoring = remoteAuthoring(svc, repos, rm.modes.Fleet)
	// Delivery follows the dashboard: served with a cluster, with its trust
	// and policy namespace.
	rm.delivery = deliveryClientsFor(id.trust, id.policyNS, d)
	rm.reads = mcpDeliveryReads(id, rm.modes.Fleet, deliveryNarrowing(splitNamespaces(v.GetString("dashboard.namespace"))))
	rm.installed, rm.installedAt = id.installedProbe(), id.installedAt()
	// ponytail: only a bundle can fail to register, and the dashboard serves
	// none; check the error here once a mode that can fail is added.
	mw, _ := rm.middleware()
	s := mcpServe{auth: a, resource: resource, perRequest: mw}
	if a.oidc != nil {
		s.issuer = issuer
	}
	h := mcpHandler(s)
	return map[string]http.Handler{"/mcp": h, "/.well-known/oauth-protected-resource": h, "/.well-known/oauth-protected-resource/mcp": h}, nil
}

// checkDashboardMCP refuses, at startup, a dashboard that would serve /mcp
// with no way to name its callers: anonymous and local dashboards never serve
// it, and the issuer path needs an exchange audience and a fixed URL.
func checkDashboardMCP(id *dashboardIdentity, issuer, resource, exchange string) error {
	switch {
	case id.auth.Local() || !id.auth.SignsIn():
		return errMCPNeedsSignIn
	case id.cluster == nil:
		return errMCPNeedsCluster
	case resource != "" && !mcpURLOK(resource):
		return errMCPURL
	case exchange != "" && issuer == "":
		return errMCPExchangeNeedsIssuer
	case exchange != "" && resource == "":
		return errMCPExchangeNeedsURL
	case exchange == "" && id.audience == nil:
		return errMCPNoExchange
	}
	return nil
}

func mcpURLOK(resource string) bool {
	u, err := url.Parse(resource)
	return err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && u.Path == "/mcp"
}

// dashboardMCPAuth is the token path over the token sign-in's own checks
// (with token sign-in) and the issuer path through the dashboard's OIDC client
// (with an exchange audience).
func dashboardMCPAuth(v *viper.Viper, id *dashboardIdentity, issuer, resource, exchange string, d deps) (dualAuth, error) {
	a := dualAuth{issuer: issuer}
	if id.audience != nil {
		a.token = authn.NewTokenMCP(authn.MCPTokenReview(id.audience, id.bearer))
	}
	if exchange == "" {
		return a, nil
	}
	client, err := authn.IssuerClient(issuer, v.GetString("dashboard.oidc-internal-issuer-url"), v.GetString("dashboard.oidc-ca-file"))
	if err != nil {
		return a, err
	}
	a.oidc, err = d.newMCPAuth(authn.MCPConfig{IssuerURL: issuer, Audience: resource, ClientID: v.GetString("dashboard.oidc-client-id"),
		ClientSecret: v.GetString("dashboard.oidc-client-secret"), ExchangeAudience: exchange, HTTPClient: client})
	return a, err
}

// viaIssuer marks a TokenInfo the issuer path verified, so Exchange asks the
// same verifier.
const viaIssuer = "pacto.dashboard/issuer"

// dualAuth routes each bearer to one verifier: a token naming the issuer, when
// an exchange audience is set, to the issuer path; every other token to the
// token path. The unverified iss only routes: both verifiers still verify.
type dualAuth struct {
	oidc   mcpAuth         // nil without an exchange audience
	token  *authn.TokenMCP // nil without token sign-in
	issuer string
}

var errNoVerifier = fmt.Errorf("%w: this dashboard accepts no token of this kind on /mcp", auth.ErrInvalidToken)

func (a dualAuth) Verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	if a.routeByIssuer(token) {
		info, err := a.oidc.Verify(ctx, token, r)
		if err != nil {
			return nil, err
		}
		if info.Extra == nil {
			info.Extra = map[string]any{}
		}
		info.Extra[viaIssuer] = true
		return info, nil
	}
	if a.token == nil {
		return nil, errNoVerifier
	}
	return a.token.Verify(ctx, token, r)
}

// routeByIssuer reports whether token goes to the issuer path.
func (a dualAuth) routeByIssuer(token string) bool {
	return a.oidc != nil && authn.UnverifiedIssuer(token) == a.issuer
}

// CanExchange is always true: each verifier decides per caller.
func (a dualAuth) CanExchange() bool { return true }

func (a dualAuth) Exchange(ctx context.Context, info *auth.TokenInfo) (string, error) {
	if via, _ := info.Extra[viaIssuer].(bool); via {
		return a.oidc.Exchange(ctx, info)
	}
	return a.token.Exchange(ctx, info)
}

// PersonOf names a caller the token path found no credential for.
func (a dualAuth) PersonOf(info *auth.TokenInfo) authn.Person { return a.token.PersonOf(info) }

// dashboardMCPImpact answers pacto_impact over the dashboard's /mcp as the
// dashboard's own impact route does, over refs pinned in the caller's view:
// no file path, no local ref, no running question (the view may hide a
// target) and no delivery read for a regression.
func dashboardMCPImpact(svc *app.Service, prov *app.ProvenanceChecker) func(context.Context, *fleet.FleetSnapshot, pactomcp.ImpactRequest) (*impact.Result, error) {
	return func(ctx context.Context, snap *fleet.FleetSnapshot, req pactomcp.ImpactRequest) (*impact.Result, error) {
		if req.Running {
			return nil, errRunningRestricted
		}
		req, err := refuseUntrustedImpact(req, false, snap)
		if err != nil {
			return nil, err
		}
		if req.Event != nil {
			return svc.ImpactEventOnSnapshot(ctx, app.ImpactEventOptions{Event: *req.Event,
				ReadRegression: func(context.Context, impact.Event) *impact.Regression {
					return &impact.Regression{Unavailable: mcpNoDelivery}
				}}, snap)
		}
		return svc.ImpactWithSnapshot(ctx, app.ImpactOptions{OldPath: req.OldRef, NewPath: req.NewRef, IncludeObserved: req.IncludeObserved,
			Domain: req.Domain, Policy: req.Policy, Fleet: app.FleetOptions{Provenance: prov}}, snap)
	}
}
