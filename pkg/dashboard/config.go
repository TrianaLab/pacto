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
	Host             string `json:"PACTO_DASHBOARD_HOST" default:"127.0.0.1" doc:"Bind address for the server"`
	Port             int    `json:"PACTO_DASHBOARD_PORT" default:"3000" doc:"HTTP server port"`
	Namespace        string `json:"PACTO_DASHBOARD_NAMESPACE,omitempty" doc:"Kubernetes namespace filter (empty = all)"`
	Repo             string `json:"PACTO_DASHBOARD_REPO,omitempty" doc:"Comma-separated OCI repositories to scan"`
	CacheDir         string `json:"PACTO_CACHE_DIR,omitempty" doc:"OCI bundle cache directory (default: ~/.cache/pacto/oci)"`
	NoCache          bool   `json:"PACTO_NO_CACHE" default:"false" doc:"Disable OCI bundle caching"`
	NoUpdateCheck    bool   `json:"PACTO_NO_UPDATE_CHECK" default:"false" doc:"Disable update checks"`
	RegistryUsername string `json:"PACTO_REGISTRY_USERNAME,omitempty" doc:"Registry authentication username"`
	RegistryPassword string `json:"PACTO_REGISTRY_PASSWORD,omitempty" doc:"Registry authentication password"`
	RegistryToken    string `json:"PACTO_REGISTRY_TOKEN,omitempty" doc:"Registry authentication token"`
	OIDCIssuerURL    string `json:"PACTO_DASHBOARD_OIDC_ISSUER_URL,omitempty" doc:"OpenID Connect issuer people sign in with; delivery then runs as the signed-in person"`
	OIDCClientID     string `json:"PACTO_DASHBOARD_OIDC_CLIENT_ID,omitempty" doc:"OpenID Connect client id of the dashboard"`
	OIDCClientSecret string `json:"PACTO_DASHBOARD_OIDC_CLIENT_SECRET,omitempty" doc:"OpenID Connect client secret of the dashboard"`
	OIDCRedirectURL  string `json:"PACTO_DASHBOARD_OIDC_REDIRECT_URL,omitempty" doc:"The dashboard's /auth/callback as browsers reach it"`
	OIDCExtraScopes  string `json:"PACTO_DASHBOARD_OIDC_EXTRA_SCOPES,omitempty" doc:"Space-separated scopes beyond openid, profile and groups"`
	TeamGroupPrefix  string `json:"PACTO_DASHBOARD_TEAM_GROUP_PREFIX,omitempty" doc:"A contract owner team T is group T or this prefix plus T"`
	MiraURL          string `json:"PACTO_DASHBOARD_MIRA_URL,omitempty" doc:"Mira's address, to link health-check citations"`
}

// ExportConfigSchema generates a JSON Schema for DashboardConfig using Huma's
// schema registry. The output includes defaults and descriptions from struct tags.
func ExportConfigSchema() ([]byte, error) {
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	s := huma.SchemaFromType(r, reflect.TypeOf(DashboardConfig{}))
	s.Title = "Pacto Dashboard Configuration"
	return json.MarshalIndent(s, "", "  ")
}
