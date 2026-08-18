// Package architecture holds static import-boundary gates for the Pacto monorepo.
//
// The dependency direction MUST be:
//
//	k8s operator -> k8s collector -> Pacto evidence/evaluation APIs -> Pacto core
//
// The platform-neutral core packages below must NEVER acquire a Kubernetes,
// controller-runtime, or integration dependency (directly or transitively), so
// that external/third-party collectors can consume the core without pulling k8s.
package architecture

import (
	"os/exec"
	"strings"
	"testing"
)

// corePackages are the platform-neutral packages that must stay k8s-free.
//
// This is the full set of engine packages the k8s integration consumes (its
// v2/pkg/... import closure) minus pkg/dashboard, which legitimately embeds a
// k8s runtime source (client-go) and so is intentionally excluded. Everything
// here MUST remain consumable by an external collector without pulling k8s.
var corePackages = []string{
	"github.com/trianalab/pacto/v3/pkg/contract/...",
	"github.com/trianalab/pacto/v3/pkg/evidence/...",
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope/...",
	"github.com/trianalab/pacto/v3/pkg/evidenceingest/...",
	"github.com/trianalab/pacto/v3/pkg/finding/...",
	"github.com/trianalab/pacto/v3/pkg/fleet/...",
	"github.com/trianalab/pacto/v3/pkg/graph/...",
	"github.com/trianalab/pacto/v3/pkg/impact/...",
	"github.com/trianalab/pacto/v3/pkg/oci/...",
	"github.com/trianalab/pacto/v3/pkg/otelobserver/...",
	"github.com/trianalab/pacto/v3/pkg/readiness/...",
	"github.com/trianalab/pacto/v3/pkg/reconcile/...",
	"github.com/trianalab/pacto/v3/pkg/schemax/...",
	"github.com/trianalab/pacto/v3/pkg/semver/...",
	"github.com/trianalab/pacto/v3/pkg/validation/...",
}

// forbiddenPrefixes are import-path prefixes a core package may never reach.
var forbiddenPrefixes = []string{
	"k8s.io/",
	"sigs.k8s.io/",
	"github.com/trianalab/pacto/integrations/", // no core -> integration edge
	// The registry client belongs behind the evidence Store adapter
	// (internal/evidenceoci). pkg/evidenceingest owns the port and the HTTP host;
	// if it could reach a registry directly the store would stop being swappable
	// and an external collector would inherit registry credentials it never needs.
	"oras.land/",
}

// evidenceConsumers read accepted evidence. They must reach it ONLY through the
// Evidence Server's HTTP DTO: no registry client, no credentials, no referrers
// enumeration of their own. pkg/dashboard is exempt from the k8s rule above but
// not from this one, so it is gated here.
var evidenceConsumers = []string{
	"github.com/trianalab/pacto/v3/pkg/dashboard/...",
	"github.com/trianalab/pacto/v3/internal/fleetsrc/...",
}

func TestEvidenceConsumersNeverReachTheRegistryStore(t *testing.T) {
	const store = "github.com/trianalab/pacto/v3/internal/evidenceoci"
	for _, pkg := range evidenceConsumers {
		for _, dep := range deps(t, pkg) {
			if dep == store || strings.HasPrefix(dep, "oras.land/") {
				t.Errorf("boundary violation: evidence consumer %q reaches %q; it must read evidence over the Evidence Server HTTP DTO", pkg, dep)
			}
		}
	}
}

// deps returns the full transitive import closure of a package pattern.
func deps(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func TestCorePackagesHaveNoKubernetesOrIntegrationDeps(t *testing.T) {
	for _, pkg := range corePackages {
		for _, dep := range deps(t, pkg) {
			for _, bad := range forbiddenPrefixes {
				if strings.HasPrefix(dep, bad) {
					t.Errorf("boundary violation: core package pattern %q transitively imports forbidden %q (prefix %q)", pkg, dep, bad)
				}
			}
		}
	}
}
