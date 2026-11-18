package main

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
)

// demoHandler is the demo's in-memory API: every huma operation plus the raw
// routes, as a live dashboard serves them.
func demoHandler(opts dashboard.Options) http.Handler {
	mux := http.NewServeMux()
	srv := dashboard.NewServer(opts)
	srv.RegisterOperations(humago.New(mux, dashboard.APIConfig()))
	srv.RegisterRawRoutes(mux)
	return mux
}
