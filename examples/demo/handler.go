package main

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
)

// demoMCPSource is installation.mcp's source in the demo: it serves no MCP
// endpoint, and says where an installed Pacto does.
const demoMCPSource = "This demo runs in your browser and serves no MCP endpoint. An installed Pacto serves it at /mcp."

// demoHandler is the demo's in-memory API: every huma operation plus the raw
// routes, as a live dashboard serves them.
func demoHandler(opts dashboard.Options) http.Handler {
	if opts.MCPSource == "" {
		opts.MCPSource = demoMCPSource
	}
	mux := http.NewServeMux()
	srv := dashboard.NewServer(opts)
	srv.RegisterOperations(humago.New(mux, dashboard.APIConfig()))
	srv.RegisterRawRoutes(mux)
	return mux
}
