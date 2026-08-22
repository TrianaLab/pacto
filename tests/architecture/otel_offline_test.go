package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOTelObserverStaysOffline guards the section 9 narrowing: pkg/otelobserver is an
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

// TestOperatorObservationPackagingStaysOffline extends the same guard to the
// packaging layer. The operator can MOUNT an offline trace export read-only and
// tell the dashboard where it is; it must never grow into deploying a receiver.
// A collector sidecar, an OTLP port or an ingest path in the operator's dashboard
// wiring or in the chart would make the "no live OTLP receiver" claim false
// without a single line of pkg/otelobserver changing.
func TestOperatorObservationPackagingStaysOffline(t *testing.T) {
	root := docsRoot(t)
	var files []string
	for _, pattern := range []string{
		filepath.Join(root, "integrations", "kubernetes", "internal", "dashboard", "*.go"),
		filepath.Join(root, "integrations", "kubernetes", "charts", "pacto-operator", "values.yaml"),
		filepath.Join(root, "integrations", "kubernetes", "charts", "pacto-operator", "templates", "_helpers.tpl"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no operator dashboard packaging sources found — moved or renamed?")
	}

	// Tokens that would signal ingest infrastructure rather than a mounted file.
	forbidden := []string{
		"/v1/traces", // the OTLP/HTTP trace ingest path
		"4317",       // OTLP/gRPC default port
		"4318",       // OTLP/HTTP default port
		"otel-collector",
		"opentelemetry-collector",
	}

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(b)
		for _, tok := range forbidden {
			if strings.Contains(src, tok) {
				t.Errorf("%s contains %q — the operator configures OFFLINE trace files (a read-only mount plus a source name), never a receiver or a deployed collector. Update docs/operational-graph.md and the chart's honesty about what is deployed before removing this guard.", filepath.Base(f), tok)
			}
		}
	}
}
