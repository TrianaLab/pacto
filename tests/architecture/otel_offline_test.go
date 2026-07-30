package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOTelObserverStaysOffline guards the §9 narrowing: pkg/otelobserver is an
// OFFLINE OTLP/JSON trace-FILE analyzer. It has no OTLP/HTTP receiver, no live
// endpoint and nothing deployed. The docs (docs/operational-graph.md) claim
// exactly that. If someone re-introduces a receiver — an HTTP listener, the
// /v1/traces path, or the OTLP ports — without updating those claims, this test
// fails, forcing the narrative and the code back into agreement.
func TestOTelObserverStaysOffline(t *testing.T) {
	root := docsRoot(t) // repo root, defined in collector_docs_test.go
	files, err := filepath.Glob(filepath.Join(root, "pkg", "otelobserver", "*.go"))
	if err != nil {
		t.Fatalf("glob otelobserver: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no pkg/otelobserver/*.go source found — moved or renamed?")
	}

	// Tokens that would signal a live OTLP receiver rather than a file analyzer.
	forbidden := []string{
		"net.Listen",          // opening a socket
		"http.ListenAndServe", // an HTTP server
		"http.HandleFunc",     // an HTTP handler
		"http.Handle",         // an HTTP handler
		"/v1/traces",          // the OTLP/HTTP trace ingest path
		":4317",               // OTLP/gRPC default port
		":4318",               // OTLP/HTTP default port
	}

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(b)
		for _, tok := range forbidden {
			if strings.Contains(src, tok) {
				t.Errorf("%s contains %q — pkg/otelobserver must stay an offline trace-file analyzer; a receiver contradicts the docs' claims (docs/operational-graph.md). Add the receiver's shipped surface to the docs before removing this guard.", filepath.Base(f), tok)
			}
		}
	}
}
