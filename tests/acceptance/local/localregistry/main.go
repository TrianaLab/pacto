// Command localregistry runs an ephemeral, in-memory OCI registry (no Docker) so
// the cluster-free acceptance can push a contract revision and reference it by an
// immutable digest — the shape the evidence ingestion policy requires. Referrers
// support is on, because accepted evidence is published as an OCI 1.1 referrer of
// that revision and the registry IS the evidence store. It serves on
// 127.0.0.1:<--port> until killed. Go-only, so it runs anywhere `go` runs.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/google/go-containerregistry/pkg/registry"
)

func main() {
	port := flag.Int("port", 5000, "port to listen on (127.0.0.1)")
	flag.Parse()
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	// registry.New returns an http.Handler backed by an in-memory blob store.
	handler := registry.New(registry.WithReferrersSupport(true))
	if err := http.ListenAndServe(addr, handler); err != nil { //nolint:gosec // local test-only registry
		fmt.Fprintln(os.Stderr, "localregistry:", err)
		os.Exit(1)
	}
}
