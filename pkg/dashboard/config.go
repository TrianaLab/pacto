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
	Diagnostics      bool   `json:"PACTO_DASHBOARD_DIAGNOSTICS" default:"false" doc:"Enable source diagnostics panel"`
	CacheDir         string `json:"PACTO_CACHE_DIR,omitempty" doc:"OCI bundle cache directory (default: ~/.cache/pacto/oci)"`
	NoCache          bool   `json:"PACTO_NO_CACHE" default:"false" doc:"Disable OCI bundle caching"`
	NoUpdateCheck    bool   `json:"PACTO_NO_UPDATE_CHECK" default:"false" doc:"Disable update checks"`
	RegistryUsername string `json:"PACTO_REGISTRY_USERNAME,omitempty" doc:"Registry authentication username"`
	RegistryPassword string `json:"PACTO_REGISTRY_PASSWORD,omitempty" doc:"Registry authentication password"`
	RegistryToken    string `json:"PACTO_REGISTRY_TOKEN,omitempty" doc:"Registry authentication token"`
}

// ExportConfigSchema generates a JSON Schema for DashboardConfig using Huma's
// schema registry. The output includes defaults and descriptions from struct tags.
func ExportConfigSchema() ([]byte, error) {
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	s := huma.SchemaFromType(r, reflect.TypeOf(DashboardConfig{}))
	s.Title = "Pacto Dashboard Configuration"
	return json.MarshalIndent(s, "", "  ")
}
