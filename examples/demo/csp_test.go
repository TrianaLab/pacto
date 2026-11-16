package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
)

// The demo's <meta> policy is the dashboard's, plus 'wasm-unsafe-eval' for the
// engine and worker-src for the worker it runs in, and minus frame-ancestors,
// which a <meta> tag cannot carry.
func TestDemoCSPMatchesDashboard(t *testing.T) {
	mk, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^DEMO_CSP\s*:=\s*(.+)$`).FindSubmatch(mk)
	if m == nil {
		t.Fatal("Makefile has no DEMO_CSP")
	}
	want := strings.Replace(dashboard.ContentSecurityPolicy, "script-src 'self';", "script-src 'self' 'wasm-unsafe-eval';", 1)
	want = strings.Replace(want, "connect-src 'self';", "connect-src 'self'; worker-src 'self';", 1)
	want = strings.TrimSuffix(want, "; frame-ancestors 'none'")
	if got := string(m[1]); got != want {
		t.Fatalf("DEMO_CSP drifted from the dashboard policy:\n got %s\nwant %s", got, want)
	}
}

// The demo's dashboard bundle carries its own copy of the configuration
// schema (a bundle ref cannot leave its directory), so it follows the flags.
func TestDemoDashboardConfigSchemaMatchesDashboard(t *testing.T) {
	want, err := dashboard.ExportConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("pacto-dashboard/configuration/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != string(want) {
		t.Fatal("pacto-dashboard/configuration/schema.json drifted: go run ./cmd/genbundle config-schema > examples/demo/pacto-dashboard/configuration/schema.json")
	}
}
