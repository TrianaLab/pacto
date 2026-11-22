package dashboard

import (
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// DashboardConfig describes the environment-variable-based configuration for
// the dashboard server. Huma's schema registry generates a JSON Schema from
// this struct, including defaults and descriptions via struct tags.
type DashboardConfig struct {
	Host                string `json:"PACTO_DASHBOARD_HOST" default:"127.0.0.1" doc:"Bind address for the server"`
	Port                int    `json:"PACTO_DASHBOARD_PORT" default:"3000" doc:"HTTP server port"`
	Namespace           string `json:"PACTO_DASHBOARD_NAMESPACE,omitempty" doc:"Kubernetes namespaces to read, comma-separated (empty = all)"`
	Repo                string `json:"PACTO_DASHBOARD_REPO,omitempty" doc:"Comma-separated OCI repositories to scan"`
	CacheDir            string `json:"PACTO_CACHE_DIR,omitempty" doc:"OCI bundle cache directory (default: ~/.cache/pacto/oci)"`
	NoCache             bool   `json:"PACTO_NO_CACHE" default:"false" doc:"Disable OCI bundle caching"`
	NoUpdateCheck       bool   `json:"PACTO_NO_UPDATE_CHECK" default:"false" doc:"Disable update checks"`
	RegistryUsername    string `json:"PACTO_REGISTRY_USERNAME,omitempty" doc:"Registry authentication username"`
	RegistryPassword    string `json:"PACTO_REGISTRY_PASSWORD,omitempty" doc:"Registry authentication password"`
	RegistryToken       string `json:"PACTO_REGISTRY_TOKEN,omitempty" doc:"Registry authentication token"`
	OIDCIssuerURL       string `json:"PACTO_DASHBOARD_OIDC_ISSUER_URL,omitempty" doc:"OpenID Connect issuer people sign in with before the dashboard shows anything; every view then follows their Kubernetes RBAC"`
	OIDCClientID        string `json:"PACTO_DASHBOARD_OIDC_CLIENT_ID,omitempty" doc:"OpenID Connect client id of the dashboard"`
	OIDCClientSecret    string `json:"PACTO_DASHBOARD_OIDC_CLIENT_SECRET,omitempty" doc:"OpenID Connect client secret of the dashboard"`
	OIDCRedirectURL     string `json:"PACTO_DASHBOARD_OIDC_REDIRECT_URL,omitempty" doc:"The dashboard's /auth/callback as browsers reach it"`
	OIDCDisplayName     string `json:"PACTO_DASHBOARD_OIDC_DISPLAY_NAME,omitempty" doc:"The identity provider's name on the sign-in button, as in Sign in with Keycloak (default OIDC)"`
	OIDCExtraScopes     string `json:"PACTO_DASHBOARD_OIDC_EXTRA_SCOPES,omitempty" doc:"Space-separated scopes beyond openid, profile and groups"`
	OIDCUsernameClaim   string `json:"PACTO_DASHBOARD_OIDC_USERNAME_CLAIM,omitempty" doc:"ID token claim the API server takes the username from (default sub); must match its claim mapping"`
	OIDCUsernamePrefix  string `json:"PACTO_DASHBOARD_OIDC_USERNAME_PREFIX,omitempty" doc:"Prefix the API server puts before the username (default the issuer URL and #, none for the email claim; - for none); must match its claim mapping"`
	OIDCGroupsClaim     string `json:"PACTO_DASHBOARD_OIDC_GROUPS_CLAIM,omitempty" doc:"ID token claim the API server takes the groups from (default groups); must match its claim mapping"`
	OIDCGroupsPrefix    string `json:"PACTO_DASHBOARD_OIDC_GROUPS_PREFIX,omitempty" doc:"Prefix the API server puts before every group; must match its claim mapping"`
	SessionIdleTimeout  string `json:"PACTO_DASHBOARD_SESSION_IDLE_TIMEOUT,omitempty" doc:"A session without a request for this long ends, as a Go duration (default 1h)"`
	SessionMaxAge       string `json:"PACTO_DASHBOARD_SESSION_MAX_AGE,omitempty" doc:"A session ends this long after sign-in, as a Go duration (default 12h)"`
	TokenLogin          bool   `json:"PACTO_DASHBOARD_TOKEN_LOGIN,omitempty" default:"false" doc:"Let people sign in with a Kubernetes token: a ServiceAccount token minted for the dashboard reads the fleet only, and a person's own token acts as them"`
	MCP                 bool   `json:"PACTO_DASHBOARD_MCP,omitempty" default:"false" doc:"Serve MCP to agents at /mcp with the dashboard's sign-in: a Kubernetes token (token sign-in) or, with an issuer and PACTO_DASHBOARD_MCP_EXCHANGE_AUDIENCE, an issuer token exchanged for the cluster"`
	MCPURL              string `json:"PACTO_DASHBOARD_MCP_URL,omitempty" doc:"The /mcp URL as agents reach it (absolute http(s), path exactly /mcp), the RFC 9728 resource; empty: the request's own address, token sign-in only"`
	MCPExchangeAudience string `json:"PACTO_DASHBOARD_MCP_EXCHANGE_AUDIENCE,omitempty" doc:"The audience asked for when exchanging an issuer token, one the API server accepts. With it, every token from the dashboard's issuer must name the MCP URL as its audience; without it every token is checked as a Kubernetes token"`
	AllowAnonymous      bool   `json:"PACTO_DASHBOARD_ALLOW_ANONYMOUS,omitempty" doc:"Serve beyond loopback without sign-in; everyone who reaches the address reads the whole fleet"`
	Review              string `json:"PACTO_DASHBOARD_REVIEW,omitempty" default:"cluster" enum:"cluster,local" doc:"How signed-in people are reviewed: cluster asks once about every namespace and needs a ClusterRole; local asks in each PACTO_DASHBOARD_NAMESPACE only, and then services without a namespace show to nobody"`
	DeliveryNamespace   string `json:"PACTO_DASHBOARD_DELIVERY_NAMESPACE,omitempty" default:"pacto-delivery" doc:"Namespace Pacto delivery runs in, whose published identity policy the dashboard reads"`
	DeliveryTrust       string `json:"PACTO_DASHBOARD_DELIVERY_TRUST,omitempty" doc:"Trust config delivery records are verified with, so History reads signed records (the recorder's PEM public key or a YAML trust config)"`
	MiraURL             string `json:"PACTO_DASHBOARD_MIRA_URL,omitempty" doc:"Mira's address, to link health-check citations and open a service in Mira"`
}

// ExportConfigSchema generates a JSON Schema for DashboardConfig using Huma's
// schema registry. The output includes defaults and descriptions from struct tags.
func ExportConfigSchema() ([]byte, error) {
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	s := huma.SchemaFromType(r, reflect.TypeOf(DashboardConfig{}))
	s.Title = "Pacto Dashboard Configuration"
	return json.MarshalIndent(s, "", "  ")
}
