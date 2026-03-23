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
	Port        int    `json:"PACTO_DASHBOARD_PORT" default:"3000" doc:"HTTP port for the dashboard server"`
	Namespace   string `json:"PACTO_DASHBOARD_NAMESPACE" default:"" doc:"Kubernetes namespace filter (empty = all)"`
	Diagnostics bool   `json:"PACTO_DASHBOARD_DIAGNOSTICS" default:"false" doc:"Enable diagnostics debug endpoints"`
	NoCache     bool   `json:"PACTO_NO_CACHE" default:"false" doc:"Disable OCI bundle cache"`
	Verbose     bool   `json:"PACTO_VERBOSE" default:"false" doc:"Enable verbose logging"`
}

// ExportConfigSchema generates a JSON Schema for DashboardConfig using Huma's
// schema registry. The output includes defaults and descriptions from struct tags.
func ExportConfigSchema() ([]byte, error) {
	r := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	s := huma.SchemaFromType(r, reflect.TypeOf(DashboardConfig{}))
	s.Title = "Pacto Dashboard Configuration"
	return json.MarshalIndent(s, "", "  ")
}
