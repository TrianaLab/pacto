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
	"github.com/trianalab/pacto/v3/pkg/catalog/...",
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

// catalogForbidden are the delivery mechanisms the contract-catalog core must
// never reach. The catalog answers what a set of contract roots and their
// closure contain; the moment it depends on a protocol, a command line, an HTTP
// server or a cluster, that answer becomes a property of one delivery mechanism
// instead of a property of the contracts.
var catalogForbidden = []string{
	"github.com/trianalab/pacto/v3/internal/mcp",
	"github.com/trianalab/pacto/v3/internal/cli",
	"github.com/trianalab/pacto/v3/pkg/dashboard",
	"github.com/trianalab/pacto/v3/integrations/",
	"github.com/trianalab/pacto/integrations/",
	"k8s.io/",
	"sigs.k8s.io/",
}

// catalogAllowedPacto is everything inside this repository the catalog core may
// import. Reference parsing, credentials, caching and registry access reach it
// through the caller-supplied Resolver port instead, which is what keeps the
// catalog model reusable and keeps its tests hermetic.
var catalogAllowedPacto = map[string]bool{
	"github.com/trianalab/pacto/v3/pkg/catalog":  true,
	"github.com/trianalab/pacto/v3/pkg/contract": true,
}

func TestCatalogCoreIsFrameworkIndependent(t *testing.T) {
	for _, dep := range deps(t, "github.com/trianalab/pacto/v3/pkg/catalog/...") {
		for _, bad := range catalogForbidden {
			if strings.HasPrefix(dep, bad) {
				t.Errorf("boundary violation: the catalog core reaches %q (prefix %q); catalog semantics must not depend on a delivery mechanism", dep, bad)
			}
		}
		if strings.HasPrefix(dep, "github.com/trianalab/pacto/") && !catalogAllowedPacto[dep] {
			t.Errorf("boundary violation: the catalog core reaches Pacto package %q; it may reuse only pkg/contract, and everything else belongs behind the Resolver port", dep)
		}
	}
}

// TestTheFleetNeverReachesTheCatalog keeps the two knowledge models apart.
//
// pkg/catalog answers what a set of contract roots and their closure DECLARE. It
// is a frozen discovery session: no runtime observation, no source health, no
// staleness, and nothing in it outlives the call that produced it. pkg/fleet
// answers what is actually RUNNING, from live sources that each carry their own
// availability and completeness.
//
// An import edge would dissolve that. A declaration-only closure would become
// reachable as operational truth: a revision that exists only because someone
// pointed the catalog at a repository would appear as a fleet record, and a
// catalog closure that is complete (every declared reference resolved) would be
// readable as a fleet that is complete (every source answered), which is a
// different claim about a different world.
//
// The opposite direction is already closed by TestCatalogCoreIsFrameworkIndependent:
// pkg/fleet is absent from catalogAllowedPacto, so the catalog cannot reach the
// fleet either. This is the half that was unguarded.
func TestTheFleetNeverReachesTheCatalog(t *testing.T) {
	const catalogRoot = "github.com/trianalab/pacto/v3/pkg/catalog"
	for _, dep := range deps(t, "github.com/trianalab/pacto/v3/pkg/fleet/...") {
		if dep == catalogRoot || strings.HasPrefix(dep, catalogRoot+"/") {
			t.Errorf("boundary violation: pkg/fleet reaches %q; a frozen contract-catalog session is declaration-only knowledge and must never be readable as operational fleet truth", dep)
		}
	}
}

// TestEvidenceAndFindingNeverImportEachOther keeps an observation apart from a
// judgement about one.
//
// pkg/evidence records what a collector saw, and deliberately has no way to say
// something is absent: evidence.Outcome is Observed/Unsupported/Failed/Stale/
// Insufficient and there is no OutcomeAbsent. pkg/finding records a verdict
// reached by comparing a contract against evidence, and distinguishes
// CategoryMissingEvidence (we could not look) from CategoryInconclusive (we
// looked and could not decide). validation.Evaluate is the single bridge.
//
// Either edge would collapse the distinction:
//
//   - pkg/evidence importing pkg/finding lets a collector ship verdicts alongside
//     its observations, so "we could not reach the workload" arrives already
//     rendered as a violation -- absence of evidence delivered as evidence of
//     absence, from the one layer that is defined to have no opinion.
//   - pkg/finding importing pkg/evidence lets a Finding carry an observation
//     payload instead of the metadata-only finding.EvidenceRef (source and
//     timestamp). A consumer could then re-derive the verdict from the embedded
//     payload, and its answer and Evaluate's answer would drift apart with no
//     way to tell which one the user is reading.
func TestEvidenceAndFindingNeverImportEachOther(t *testing.T) {
	const (
		evidencePkg = "github.com/trianalab/pacto/v3/pkg/evidence"
		findingPkg  = "github.com/trianalab/pacto/v3/pkg/finding"
	)
	for _, c := range []struct{ from, to, why string }{
		{evidencePkg, findingPkg, "a collector records what it observed, never a verdict about it"},
		{findingPkg, evidencePkg, "a finding cites evidence by metadata (finding.EvidenceRef), never by carrying an observation payload"},
	} {
		for _, dep := range deps(t, c.from) {
			if dep == c.to {
				t.Errorf("boundary violation: %s reaches %s; %s. The only bridge is validation.Evaluate.", c.from, c.to, c.why)
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
