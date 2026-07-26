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
	"github.com/trianalab/pacto/v2/pkg/contract/...",
	"github.com/trianalab/pacto/v2/pkg/evidence/...",
	"github.com/trianalab/pacto/v2/pkg/finding/...",
	"github.com/trianalab/pacto/v2/pkg/graph/...",
	"github.com/trianalab/pacto/v2/pkg/oci/...",
	"github.com/trianalab/pacto/v2/pkg/readiness/...",
	"github.com/trianalab/pacto/v2/pkg/schemax/...",
	"github.com/trianalab/pacto/v2/pkg/semver/...",
	"github.com/trianalab/pacto/v2/pkg/validation/...",
}

// forbiddenPrefixes are import-path prefixes a core package may never reach.
var forbiddenPrefixes = []string{
	"k8s.io/",
	"sigs.k8s.io/",
	"github.com/trianalab/pacto/integrations/", // no core -> integration edge
}

func TestCorePackagesHaveNoKubernetesOrIntegrationDeps(t *testing.T) {
	for _, pkg := range corePackages {
		// Full transitive import closure of the package pattern.
		out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
		}
		for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			for _, bad := range forbiddenPrefixes {
				if strings.HasPrefix(dep, bad) {
					t.Errorf("boundary violation: core package pattern %q transitively imports forbidden %q (prefix %q)", pkg, dep, bad)
				}
			}
		}
	}
}
