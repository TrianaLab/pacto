package cli

import (
	"context"
	"errors"
	"fmt"
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
		u, _ := url.Parse(s.resource) // validated at start
		meta := *u
		meta.Path = "/.well-known/oauth-protected-resource" + u.Path
		h = auth.RequireBearerToken(s.auth.Verify, &auth.RequireBearerTokenOptions{ResourceMetadataURL: meta.String()})(h)
		prm := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{Resource: s.resource,
			AuthorizationServers: []string{s.issuer}, BearerMethodsSupported: []string{"header"}})
		mux.Handle("/.well-known/oauth-protected-resource", prm)
		mux.Handle(meta.Path, prm)
	}
	if s.launchVerify != nil {
		h = auth.RequireBearerToken(s.launchVerify, nil)(h)
	}
	mux.Handle("/mcp", h)
	return mux
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
	build  func(ctx context.Context) *mcpsdk.Server
}

// reviewed is who the apiserver said a request's caller is, and with
// delivery their client and whether they get the write tools.
type reviewed struct {
	person   authn.Person
	delivery *app.Delivery
	writes   bool
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
		return reviewed{person: p}, []any{"user", p.User, "writeTools", false}, err
	}
	c := rc.delivery(dyn)
	caller, err := c.WhoAmI(ctx)
	if err == nil && caller.User == "" {
		err = errNobodyReviewed
	}
	writes := caller.Class == delivery.ClassAgent
	return reviewed{person: authn.Person{User: caller.User, Groups: caller.Groups}, delivery: c, writes: writes},
		[]any{"user", caller.User, "class", caller.Class, "writeTools", writes}, err
}

func (rc remoteCaller) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := auth.TokenInfoFromContext(r.Context())
		// Before the cluster names the caller, a refusal can only say who the
		// claim mapping says they are.
		claimed := rc.claims.PersonFromTokenInfo(info).User
		refuse := func(code int, what string, err error) {
			authn.Refused(r.Context(), r, what, "claimed", claimed, "reason", err.Error())
			http.Error(w, err.Error(), code)
		}
		tok, err := rc.auth.Exchange(r.Context(), info)
		if err != nil {
			refuse(http.StatusForbidden, "remote MCP could not exchange a caller's token", err)
			return
		}
		dyn, err := rc.newClient(authn.RESTConfig(rc.cluster, tok))
		if err != nil {
			refuse(http.StatusInternalServerError, "remote MCP could not build a caller's client", err)
			return
		}
		who, attrs, err := rc.review(r.Context(), dyn)
		if err != nil {
			refuse(http.StatusForbidden, "remote MCP: the apiserver did not say who a caller is", err)
			return
		}
		logging.LoggerFromContext(r.Context()).Info("remote MCP request", attrs...)
		// The audit line names the reviewed user, the name admission records.
		ctx := pactomcp.WithCaller(context.WithValue(r.Context(), reviewedKey{}, who), who.person.User)
		ctx = context.WithValue(ctx, mcpServerKey{}, rc.build(ctx))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
// token exchange there is none.
func remoteCallerFor(cmd *cobra.Command, svc *app.Service, version string, d deps, s *mcpServe) error {
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
	rc := remoteCaller{auth: s.auth, cluster: cluster, newClient: d.dynamicFor, claims: claimsFlags(flagString(cmd))}
	if deliveryMode {
		err = remoteDeliveryTools(cmd, svc, version, d, &rc)
	} else {
		err = remoteFleetTools(cmd, svc, version, d, &rc)
	}
	if err != nil {
		return err
	}
	s.perRequest = rc.middleware
	s.remoteAuthoring = true
	return nil
}

// remoteDeliveryTools builds the delivery tools for each request as its
// reviewed caller.
func remoteDeliveryTools(cmd *cobra.Command, svc *app.Service, version string, d deps, rc *remoteCaller) error {
	policyNS, _ := cmd.Flags().GetString("delivery-namespace")
	path, _ := cmd.Flags().GetString("delivery-trust")
	trust, err := deliveryTrust(path)
	if err != nil {
		return err
	}
	rc.delivery = func(dyn dynamic.Interface) *app.Delivery {
		return &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry, Records: d.newRecordRepo, Trust: trust,
			Policy: delivery.DefaultClassPolicy(), PolicyNamespace: policyNS, Now: time.Now}
	}
	rc.build = func(ctx context.Context) *mcpsdk.Server {
		who := reviewedFrom(ctx)
		return pactomcp.NewDeliveryServer(version, mcpDelivery{who.delivery}, who.writes, pactomcp.Authoring{Validate: svc.ValidationReport, Remote: true})
	}
	return nil
}

// remoteFleetTools serves one fleet server to every request: its view reads
// the reviewed caller from each request's context.
func remoteFleetTools(cmd *cobra.Command, svc *app.Service, version string, d deps, rc *remoteCaller) error {
	srv, err := buildFleetServer(cmd, svc, version, d)
	if err != nil {
		return err
	}
	rc.build = func(context.Context) *mcpsdk.Server { return srv }
	return nil
}
