package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// mcpAuth is the remote MCP server's identity layer: *authn.MCP.
type mcpAuth interface {
	Verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error)
	CanExchange() bool
	Exchange(ctx context.Context, info *auth.TokenInfo) (string, error)
}

func newMCPAuth(cfg authn.MCPConfig) (mcpAuth, error) { return authn.NewMCP(cfg) }

// newDynamicForConfig is a dynamic client for a caller's own config.
func newDynamicForConfig(c *rest.Config) (dynamic.Interface, error) { return dynamic.NewForConfig(c) }

// mcpServe is what `pacto mcp` serves and how.
type mcpServe struct {
	transport string
	host      string
	port      int
	// server answers every request; perRequest, when set, picks the server
	// for each request instead (remote fleet and delivery: the apiserver names
	// each request's caller).
	server     *mcpsdk.Server
	perRequest func(http.Handler) http.Handler
	auth       mcpAuth
	issuer     string
	resource   string
	// remoteAuthoring: the server answers over HTTP and offers only
	// pacto_schema from the contract tools (pactomcp.Authoring.Remote).
	remoteAuthoring bool
	// launch is the per-launch bearer of a loopback server without an issuer,
	// checked by launchVerify.
	launch       string
	launchVerify auth.TokenVerifier
}

// errServiceCredsBeyondLoopback refuses to lend this process's service
// credential, or its write tools, to remote callers: every caller would act
// as the server, and the service could not tell them apart.
var errServiceCredsBeyondLoopback = errors.New("--auth and --allow-writes serve loopback only: beyond it every caller would " +
	"call the service with this process's credential; run `pacto mcp` beside the agent, under the agent's own credentials")

var errAuthBeyondLoopback = errors.New("binding the MCP server beyond loopback needs --auth-issuer and --resource: " +
	"every request must carry a token the server verifies")

// errFleetNoExchange: a remote fleet server names each caller by the
// apiserver's review of their exchanged token.
var errFleetNoExchange = errors.New("--fleet over HTTP with --auth-issuer needs token exchange (RFC 8693) so the apiserver names each caller: " +
	"set --exchange-client-id and PACTO_MCP_EXCHANGE_CLIENT_SECRET, or run pacto mcp --fleet over stdio")

// errDeliveryNoExchange: remote delivery needs each caller's token exchanged
// for the apiserver, and the MCP token itself may never be passed through.
var errDeliveryNoExchange = errors.New("--delivery over HTTP needs token exchange (RFC 8693): set --exchange-client-id and " +
	"PACTO_MCP_EXCHANGE_CLIENT_SECRET for the server's own OAuth client, or run `pacto mcp --delivery` over stdio beside the agent")

type mcpServerKey struct{}

// mcpHandler serves /mcp, behind bearer auth and with protected-resource
// metadata (RFC 9728) when auth is configured.
func mcpHandler(s mcpServe) http.Handler {
	var h http.Handler = mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		if srv, ok := r.Context().Value(mcpServerKey{}).(*mcpsdk.Server); ok {
			return srv
		}
		return s.server
	}, &mcpsdk.StreamableHTTPOptions{Stateless: s.perRequest != nil})
	if s.perRequest != nil {
		h = s.perRequest(h)
	}
	mux := http.NewServeMux()
	if s.auth != nil {
		h = protectedResource(s, h, mux)
	}
	if s.launchVerify != nil {
		h = auth.RequireBearerToken(s.launchVerify, nil)(h)
	}
	mux.Handle("/mcp", h)
	return mux
}

// protectedResource puts h behind s.auth and serves its RFC 9728 metadata on
// mux. The resource is s.resource, or, when it is empty (the token path, whose
// audience is a constant), the request's own origin plus /mcp. The metadata
// names s.issuer as authorization server only when there is one.
func protectedResource(s mcpServe, h http.Handler, mux *http.ServeMux) http.Handler {
	var servers []string
	if s.issuer != "" {
		servers = []string{s.issuer}
	}
	const wellKnown = "/.well-known/oauth-protected-resource"
	// resource and metaURL name the endpoint as the request reached it when
	// no resource is configured.
	resource := func(r *http.Request) string { return requestOrigin(r) + "/mcp" }
	metaURL := func(r *http.Request) string { return requestOrigin(r) + wellKnown + "/mcp" }
	path := "/mcp"
	if s.resource != "" {
		u, _ := url.Parse(s.resource) // validated at start
		meta := *u
		meta.Path, path = wellKnown+u.Path, u.Path
		resource = func(*http.Request) string { return s.resource }
		metaURL = func(*http.Request) string { return meta.String() }
	}
	prm := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{Resource: resource(r),
			AuthorizationServers: servers, BearerMethodsSupported: []string{"header"}}).ServeHTTP(w, r)
	})
	mux.Handle(wellKnown, prm)
	mux.Handle(wellKnown+path, prm)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.RequireBearerToken(s.auth.Verify, &auth.RequireBearerTokenOptions{ResourceMetadataURL: metaURL(r)})(h).ServeHTTP(w, r)
	})
}

// requestOrigin is the origin a request reached: https only over TLS this
// process terminated, since X-Forwarded-* is not trusted. A forged Host only
// changes the metadata the forger reads.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// remoteCaller names the caller of every request the way the apiserver does:
// their verified token is exchanged for one the apiserver accepts and a
// SelfSubjectReview says who they are. The fleet view narrows to that person;
// with delivery only an agent gets propose and cancel. Nobody gets approve.
type remoteCaller struct {
	auth      mcpAuth
	cluster   *rest.Config
	newClient func(*rest.Config) (dynamic.Interface, error)
	// delivery, when set, builds the caller's delivery client: WhoAmI reviews
	// the caller and classifies them.
	delivery func(dyn dynamic.Interface) *app.Delivery
	// claims names a caller in a refusal logged before the cluster could
	// (see claimsFlags).
	claims authn.Claims
	// person, when set, names a caller whose token is no credential
	// (Exchange returns authn.ErrNoCredential): it reads the fleet only.
	person func(*auth.TokenInfo) authn.Person
	build  func(ctx context.Context) *mcpsdk.Server
}

// credentialless is an mcpAuth whose Exchange may answer
// authn.ErrNoCredential for a verified caller, and that names such a caller
// itself (*authn.TokenMCP).
type credentialless interface {
	PersonOf(*auth.TokenInfo) authn.Person
}

// reviewed is who the apiserver said a request's caller is, their own
// client (pacto_stats reviews operation access with it) and, with delivery,
// their delivery client and whether they get the write tools. own is nil for
// a fleet-only caller, whose server registers no tool that reads it; delivery
// only when the server serves delivery.
type reviewed struct {
	person    authn.Person
	own       *app.Delivery
	delivery  *app.Delivery
	caller    delivery.Caller
	writes    bool
	fleetOnly bool
}

type reviewedKey struct{}

// personFrom returns the caller the apiserver reviewed for this request.
func personFrom(ctx context.Context) (authn.Person, bool) {
	r, ok := ctx.Value(reviewedKey{}).(reviewed)
	return r.person, ok
}

// reviewedFrom returns the reviewed caller of this request.
func reviewedFrom(ctx context.Context) reviewed {
	r, _ := ctx.Value(reviewedKey{}).(reviewed)
	return r
}

var errNobodyReviewed = errors.New("the apiserver's SelfSubjectReview named no user")

// review asks the apiserver who dyn's credentials are; with delivery, WhoAmI
// asks it and classifies the caller as well.
func (rc remoteCaller) review(ctx context.Context, dyn dynamic.Interface) (reviewed, []any, error) {
	if rc.delivery == nil {
		p, _, err := authn.ReviewSelf(ctx, dyn)
		if err == nil && p.User == "" {
			err = errNobodyReviewed
		}
		return reviewed{person: p, own: &app.Delivery{Dynamic: dyn}}, []any{"user", p.User, "writeTools", false}, err
	}
	c := rc.delivery(dyn)
	caller, err := c.WhoAmI(ctx)
	if err == nil && caller.User == "" {
		err = errNobodyReviewed
	}
	writes := caller.Class == delivery.ClassAgent
	return reviewed{person: authn.Person{User: caller.User, Groups: caller.Groups}, own: c, delivery: c, caller: caller, writes: writes},
		[]any{"user", caller.User, "class", caller.Class, "writeTools", writes}, err
}

func (rc remoteCaller) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, attrs, ok := rc.caller(w, r)
		if !ok {
			return
		}
		logging.LoggerFromContext(r.Context()).Info("remote MCP request", attrs...)
		// The audit line names the reviewed user, the name admission records.
		ctx := pactomcp.WithCaller(context.WithValue(r.Context(), reviewedKey{}, who), who.person.User)
		ctx = context.WithValue(ctx, mcpServerKey{}, rc.build(ctx))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// caller names the request's caller, or refuses the request and reports
// false.
func (rc remoteCaller) caller(w http.ResponseWriter, r *http.Request) (reviewed, []any, bool) {
	info := auth.TokenInfoFromContext(r.Context())
	// Before the cluster names the caller, a refusal can only say who the
	// claim mapping says they are.
	claimed := rc.claims.PersonFromTokenInfo(info).User
	refuse := func(code int, what string, err error) (reviewed, []any, bool) {
		authn.Refused(r.Context(), r, what, "claimed", claimed, "reason", err.Error())
		http.Error(w, err.Error(), code)
		return reviewed{}, nil, false
	}
	tok, err := rc.auth.Exchange(r.Context(), info)
	if errors.Is(err, authn.ErrNoCredential) && rc.person != nil {
		who, attrs := fleetOnlyCaller(rc.person(info))
		if who.person.User == "" {
			return refuse(http.StatusForbidden, "remote MCP: the token review did not say who a caller is", errNobodyReviewed)
		}
		return who, attrs, true
	}
	if err != nil {
		return refuse(http.StatusForbidden, "remote MCP could not exchange a caller's token", err)
	}
	dyn, err := rc.newClient(authn.RESTConfig(rc.cluster, tok))
	if err != nil {
		return refuse(http.StatusInternalServerError, "remote MCP could not build a caller's client", err)
	}
	who, attrs, err := rc.review(r.Context(), dyn)
	if err != nil {
		return refuse(http.StatusForbidden, "remote MCP: the apiserver did not say who a caller is", err)
	}
	return who, attrs, true
}

// fleetOnlyCaller is a caller the token review named whose token is no
// credential for the cluster: no client, no delivery and no writes.
func fleetOnlyCaller(p authn.Person) (reviewed, []any) {
	return reviewed{person: p, fleetOnly: true}, []any{"user", p.User, "fleetOnly", true, "writeTools", false}
}

// mcpRemote reads the remote flags: the bind host and, with --auth-issuer,
// the verifier every request goes through.
func mcpRemote(cmd *cobra.Command, d deps, s *mcpServe) error {
	s.host, _ = cmd.Flags().GetString("host")
	s.issuer, _ = cmd.Flags().GetString("auth-issuer")
	s.resource, _ = cmd.Flags().GetString("resource")
	if s.transport != "http" {
		return nil
	}
	creds, _ := cmd.Flags().GetStringArray("auth")
	writes, _ := cmd.Flags().GetBool("allow-writes")
	if !isLoopback(s.host) && (len(creds) > 0 || writes) {
		return errServiceCredsBeyondLoopback
	}
	if s.issuer == "" {
		if !isLoopback(s.host) {
			return errAuthBeyondLoopback
		}
		// Loopback is reachable by every local process: only the one that
		// saw this launch's bearer may call.
		s.launch, s.launchVerify = authn.NewLaunchToken()
		return nil
	}
	if u, err := url.Parse(s.resource); s.resource == "" || err != nil || u.Host == "" {
		return fmt.Errorf("--auth-issuer needs --resource, the MCP endpoint's URL as clients reach it (for example https://mcp.example/mcp)")
	}
	audience, _ := cmd.Flags().GetString("audience")
	if audience == "" {
		audience = s.resource
	}
	clientID, _ := cmd.Flags().GetString("exchange-client-id")
	exchangeAud, _ := cmd.Flags().GetString("exchange-audience")
	a, err := d.newMCPAuth(authn.MCPConfig{IssuerURL: s.issuer, Audience: audience, ClientID: clientID,
		ClientSecret: os.Getenv("PACTO_MCP_EXCHANGE_CLIENT_SECRET"), ExchangeAudience: exchangeAud})
	if err != nil {
		return err
	}
	s.auth = a
	return nil
}

// remoteCallerFor wires a per-request server for an authenticated HTTP fleet
// or delivery server: each request is named by the apiserver, so without
// token exchange there is none. Without delivery one server answers every
// request; with it, delivery is built for each request's caller.
func remoteCallerFor(cmd *cobra.Command, svc *app.Service, version string, args []string, d deps, s *mcpServe) error {
	deliveryMode, _ := cmd.Flags().GetBool("delivery")
	switch {
	case deliveryMode && s.auth == nil:
		return errDeliveryOverHTTP
	case deliveryMode && !s.auth.CanExchange():
		return errDeliveryNoExchange
	case !s.auth.CanExchange():
		return errFleetNoExchange
	}
	cluster, err := d.clusterConfig()
	if err != nil {
		return fmt.Errorf("remote MCP needs the cluster's address: %w", err)
	}
	rm := remoteMCP{version: version, auth: s.auth, cluster: cluster, newClient: d.dynamicFor, claims: claimsFlags(flagString(cmd))}
	if deliveryMode {
		if rm.delivery, err = remoteDeliveryClients(cmd, d); err != nil {
			return err
		}
	}
	if rm.modes, err = mcpModes(cmd, svc, version, args, d); err != nil {
		return err
	}
	// This path always answers over HTTP.
	rm.modes.Transport = "http"
	ociRefs, _ := cmd.Flags().GetStringArray("oci")
	rm.authoring = remoteAuthoring(svc, ociRefs, rm.modes.Fleet)
	if rm.delivery != nil {
		policy := rm.delivery(nil) // only its trust and policy namespace are read
		id := baseDashboardIdentity(cluster, d, policy.Trust, policy.PolicyNamespace)
		rm.reads = mcpDeliveryReads(id, rm.modes.Fleet, deliveryNarrowing(flagNamespaces(cmd)))
		rm.installed, rm.installedAt = id.installedProbe(), id.installedAt()
	}
	if s.perRequest, err = rm.middleware(); err != nil {
		return err
	}
	s.remoteAuthoring = true
	return nil
}

// remoteMCP builds an HTTP MCP server whose callers the apiserver names per
// request: `pacto mcp --transport http` and the dashboard's /mcp both build
// theirs here, so both register the same tools.
type remoteMCP struct {
	version   string
	auth      mcpAuth
	cluster   *rest.Config
	newClient func(*rest.Config) (dynamic.Interface, error)
	claims    authn.Claims
	// delivery builds a caller's delivery client; nil without delivery.
	delivery    func(dynamic.Interface) *app.Delivery
	modes       pactomcp.Modes
	authoring   pactomcp.Authoring
	reads       func(*app.Delivery, string) pactomcp.DeliveryReads
	installed   func(context.Context) (bool, error)
	installedAt func() time.Time
}

// middleware names each request's caller and picks their server. The modes
// register the same way on every request, so a bundle that cannot register
// fails here, at startup, and reports what it skipped once. When auth can
// answer authn.ErrNoCredential, a second server, built once here, answers the
// callers whose token reads the fleet only.
func (rm remoteMCP) middleware() (func(http.Handler) http.Handler, error) {
	shared, err := pactomcp.New(rm.version, rm.authoring, rm.modes)
	if err != nil {
		return nil, err
	}
	rc := remoteCaller{auth: rm.auth, cluster: rm.cluster, newClient: rm.newClient, delivery: rm.delivery, claims: rm.claims}
	// Nothing else is per caller: the fleet view and the audit line read the
	// caller from the request's context.
	build := func(context.Context) *mcpsdk.Server { return shared }
	if rm.delivery != nil {
		build = perCallerBuild(rm.version, rm.authoring, rm.modes, rm.reads, rm.installed, rm.installedAt)
	}
	rc.build = build
	if cl, ok := rm.auth.(credentialless); ok {
		fleetOnly, _ := pactomcp.New(rm.version, fleetOnlyAuthoring(rm.authoring), fleetOnlyModes(rm.modes, authn.APIAudience(rm.cluster))) // registered once above
		rc.person = cl.PersonOf
		rc.build = func(ctx context.Context) *mcpsdk.Server {
			if reviewedFrom(ctx).fleetOnly {
				return fleetOnly
			}
			return build(ctx)
		}
	}
	return rc.middleware, nil
}

// fleetOnlyAuthoring is the authoring a for a caller with no client: pacto_declare and
// pacto_accept read the caller's own client, so neither is registered.
func fleetOnlyAuthoring(a pactomcp.Authoring) pactomcp.Authoring {
	a.Declare, a.Accept = nil, nil
	return a
}

// fleetOnlyModes is m for a caller whose token reads the fleet only: no
// delivery, and pacto_capabilities says how to get more. aud is the API
// server's audience, when known.
func fleetOnlyModes(m pactomcp.Modes, aud string) pactomcp.Modes {
	if aud == "" {
		aud = "<your cluster's API audience>"
	}
	m.Delivery, m.Bundle = nil, quietBundle(m.Bundle)
	m.FleetOnlyHint = "this token reads the fleet only; to propose changes, mint your ServiceAccount's token with " +
		"--audience " + authn.DashboardAudience + " --audience " + aud
	return m
}

// quietBundle is b with its notices discarded: they went out once at startup.
func quietBundle(b *pactomcp.BundleMode) *pactomcp.BundleMode {
	if b == nil {
		return nil
	}
	quiet := *b
	quiet.Stderr = io.Discard
	return &quiet
}

// perCallerBuild builds each request's server with its caller's delivery
// tools. The bundle's notices went out once at startup, so the per-request
// builds discard them.
func perCallerBuild(version string, a pactomcp.Authoring, m pactomcp.Modes, reads func(*app.Delivery, string) pactomcp.DeliveryReads,
	installed func(context.Context) (bool, error), installedAt func() time.Time) func(context.Context) *mcpsdk.Server {
	m.Bundle = quietBundle(m.Bundle)
	return func(ctx context.Context) *mcpsdk.Server {
		mm := m
		if who := reviewedFrom(ctx); who.delivery != nil {
			mm.Delivery = &pactomcp.DeliveryMode{API: mcpDelivery{who.delivery}, Writes: who.writes, Caller: who.caller,
				Reads: reads(who.delivery, remoteCallerKey(who.person)), CallerAt: time.Now(), Installed: installed, InstalledAt: installedAt}
		}
		srv, _ := pactomcp.New(version, a, mm) // registered once at startup
		return srv
	}
}

// remoteDeliveryClients builds each caller's delivery client over their
// exchanged token, with the trust and policy namespace the flags name.
func remoteDeliveryClients(cmd *cobra.Command, d deps) (func(dyn dynamic.Interface) *app.Delivery, error) {
	policyNS, _ := cmd.Flags().GetString("delivery-namespace")
	path, _ := cmd.Flags().GetString("delivery-trust")
	trust, err := app.DeliveryTrust(path)
	if err != nil {
		return nil, err
	}
	return deliveryClientsFor(trust, policyNS, d), nil
}

// deliveryClientsFor builds each caller's delivery client over their own
// token with trust and the policy namespace policyNS.
func deliveryClientsFor(trust *delivery.TrustConfig, policyNS string, d deps) func(dyn dynamic.Interface) *app.Delivery {
	return func(dyn dynamic.Interface) *app.Delivery {
		return &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry, Records: d.newRecordRepo, Trust: trust,
			Policy: delivery.DefaultClassPolicy(), PolicyNamespace: policyNS, Now: time.Now}
	}
}
