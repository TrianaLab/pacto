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
	root := repoRoot(t)
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

// TestOperatorObservationPackagingStaysOffline guards the shipped receiver
// surface in the packaging layer. The dashboard receives OTLP/HTTP JSON on one
// port, OTLPPort in internal/dashboard/resources.go, exposed only by the
// operator-applied Service and admitted only from pods in the cluster by the
// chart's NetworkPolicy. The operator never deploys a collector, never opens
// OTLP/gRPC and never routes the chart Service (and so no Ingress or
// HTTPRoute) to the receiver. A collector image, an ingest path or a second
// copy of the port in the wiring would make docs/operational-graph.md wrong
// without a single line of pkg/otelobserver changing.
func TestOperatorObservationPackagingStaysOffline(t *testing.T) {
	root := repoRoot(t)
	chart := filepath.Join(root, "integrations", "kubernetes", "charts", "pacto-operator")
	var files []string
	for _, pattern := range []string{
		filepath.Join(root, "integrations", "kubernetes", "internal", "dashboard", "*.go"),
		filepath.Join(chart, "values.yaml"),
		filepath.Join(chart, "templates", "_helpers.tpl"),
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

	forbidden := []string{
		"/v1/traces", // the ingest path belongs to the dashboard binary, not its packaging
		"4317",       // OTLP/gRPC is not served
		"otel-collector",
		"opentelemetry-collector",
	}
	const why = " — the dashboard receives OTLP/HTTP JSON on OTLPPort only; the operator never deploys a collector or a second ingest surface. Update docs/operational-graph.md before changing this guard."

	var port int
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		src := string(b)
		for _, tok := range forbidden {
			if strings.Contains(src, tok) {
				t.Errorf("%s contains %q%s", filepath.Base(f), tok, why)
			}
		}
		n := strings.Count(src, "4318")
		if filepath.Base(f) == "resources.go" {
			port += n
		} else if n > 0 {
			t.Errorf("%s contains %q: use OTLPPort from resources.go%s", filepath.Base(f), "4318", why)
		}
	}
	if port != 1 {
		t.Errorf("resources.go holds 4318 %d times, want exactly once (OTLPPort)%s", port, why)
	}

	// The chart Service backs the Ingress and HTTPRoute: it never carries the receiver port.
	services, err := filepath.Glob(filepath.Join(chart, "templates", "*-service.yaml"))
	if err != nil || len(services) == 0 {
		t.Fatalf("no chart Service templates found (%v)", err)
	}
	for _, f := range append(services, filepath.Join(chart, "templates", "_resources.tpl")) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(b), "4318") {
			t.Errorf("%s contains %q: only the operator-applied Service exposes the receiver%s", filepath.Base(f), "4318", why)
		}
	}
}
