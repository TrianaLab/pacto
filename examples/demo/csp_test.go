package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
)

// The demo's <meta> policy is the dashboard's, plus 'wasm-unsafe-eval' for the
// engine and minus frame-ancestors, which a <meta> tag cannot carry.
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
	want = strings.TrimSuffix(want, "; frame-ancestors 'none'")
	if got := string(m[1]); got != want {
		t.Fatalf("DEMO_CSP drifted from the dashboard policy:\n got %s\nwant %s", got, want)
	}
}
