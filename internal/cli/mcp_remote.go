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
	// server answers every request; perRequest, when set, builds the server
	// for each request instead (remote delivery: one caller per request).
	server     *mcpsdk.Server
	perRequest func(http.Handler) http.Handler
	auth       mcpAuth
	issuer     string
	resource   string
}

// errServiceCredsBeyondLoopback refuses to lend this process's service
// credential, or its write tools, to remote callers: every caller would act
// as the server, and the service could not tell them apart.
var errServiceCredsBeyondLoopback = errors.New("--auth and --allow-writes serve loopback only: beyond it every caller would " +
	"call the service with this process's credential; run `pacto mcp` beside the agent, under the agent's own credentials")

var errAuthBeyondLoopback = errors.New("binding the MCP server beyond loopback needs --auth-issuer and --resource: " +
	"every request must carry a token the server verifies")

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
	mux.Handle("/mcp", h)
	return mux
}

// remoteDelivery builds the delivery tools for each request as the caller:
// their verified token is exchanged for one the apiserver accepts, the
// apiserver says who they are, and only an agent gets propose and cancel.
// Nobody gets approve.
type remoteDelivery struct {
	auth      mcpAuth
	cluster   *rest.Config
	newClient func(*rest.Config) (dynamic.Interface, error)
	registry  func(...string) app.DeliveryRegistry
	records   app.RecordRepo
	trust     *delivery.TrustConfig
	policyNS  string
	// claims names a caller in a refusal logged before the cluster could
	// (see claimsFlags).
	claims authn.Claims
	build  func(api pactomcp.DeliveryAPI, writes bool) *mcpsdk.Server
}

func (rd remoteDelivery) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := auth.TokenInfoFromContext(r.Context())
		// Before the cluster names the caller, a refusal can only say who the
		// claim mapping says they are.
		claimed := rd.claims.PersonFromTokenInfo(info).User
		refuse := func(code int, what string, err error) {
			authn.Refused(r.Context(), r, what, "claimed", claimed, "reason", err.Error())
			http.Error(w, err.Error(), code)
		}
		tok, err := rd.auth.Exchange(r.Context(), info)
		if err != nil {
			refuse(http.StatusForbidden, "remote MCP could not exchange a caller's token", err)
			return
		}
		dyn, err := rd.newClient(authn.RESTConfig(rd.cluster, tok))
		if err != nil {
			refuse(http.StatusInternalServerError, "remote MCP could not build a caller's client", err)
			return
		}
		c := &app.Delivery{Dynamic: dyn, Registry: rd.registry, Records: rd.records, Trust: rd.trust,
			Policy: delivery.DefaultClassPolicy(), PolicyNamespace: rd.policyNS, Now: time.Now}
		caller, err := c.WhoAmI(r.Context())
		if err != nil {
			refuse(http.StatusForbidden, "remote MCP: the apiserver did not say who a caller is", err)
			return
		}
		writes := caller.Class == delivery.ClassAgent
		logging.LoggerFromContext(r.Context()).Info("remote MCP delivery request", "user", caller.User,
			"class", caller.Class, "writeTools", writes)
		srv := rd.build(mcpDelivery{c}, writes)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpServerKey{}, srv)))
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

// remoteDeliveryFor wires per-request delivery for an authenticated HTTP
// server; without token exchange there is none.
func remoteDeliveryFor(cmd *cobra.Command, svc *app.Service, version string, d deps, s *mcpServe) error {
	if s.auth == nil {
		return errDeliveryOverHTTP
	}
	if !s.auth.CanExchange() {
		return errDeliveryNoExchange
	}
	cluster, err := d.clusterConfig()
	if err != nil {
		return fmt.Errorf("remote delivery needs the cluster's address: %w", err)
	}
	policyNS, _ := cmd.Flags().GetString("delivery-namespace")
	path, _ := cmd.Flags().GetString("delivery-trust")
	trust, err := deliveryTrust(path)
	if err != nil {
		return err
	}
	rd := remoteDelivery{auth: s.auth, cluster: cluster, registry: d.newDeliveryRegistry, newClient: d.dynamicFor,
		records: d.newRecordRepo, trust: trust,
		policyNS: policyNS, claims: claimsFlags(flagString(cmd)),
		build: func(api pactomcp.DeliveryAPI, writes bool) *mcpsdk.Server {
			return pactomcp.NewDeliveryServer(version, api, writes, svc.ValidationReport)
		}}
	s.perRequest = rd.middleware
	return nil
}
