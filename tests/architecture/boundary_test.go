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
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// corePackages are the platform-neutral packages that must stay k8s-free.
//
// This is the full set of engine packages the k8s integration consumes (its
// v2/pkg/... import closure). Everything here MUST remain consumable by an
// external collector without pulling k8s.
//
// pkg/dashboard used to be excluded because it embedded its own client-go
// ingestion stack. That stack is gone: the dashboard now reads a fleet snapshot
// its caller builds, so the cluster client lives on the caller's side of the
// seam and the dashboard is a rendering layer over the same platform-neutral
// core as everything else here. Keeping it gated is what stops that from
// silently reverting.
//
// pkg/contractview is the rendering half of that layer, split out so pkg/doc can
// render one bundle offline without linking a server. It is gated for the same
// reason and more strictly: it is the leaf both the dashboard and the offline
// exporter build on, so a k8s edge here would reach every consumer of either.
var corePackages = []string{
	"github.com/trianalab/pacto/v4/internal/fleetgen",
	"github.com/trianalab/pacto/v4/pkg/backstage/...",
	"github.com/trianalab/pacto/v4/pkg/catalog/...",
	"github.com/trianalab/pacto/v4/pkg/contract/...",
	"github.com/trianalab/pacto/v4/pkg/contractview/...",
	"github.com/trianalab/pacto/v4/pkg/dashboard/...",
	"github.com/trianalab/pacto/v4/pkg/delivery/...",
	"github.com/trianalab/pacto/v4/pkg/detect/...",
	"github.com/trianalab/pacto/v4/pkg/evidence/...",
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope/...",
	"github.com/trianalab/pacto/v4/pkg/evidenceingest/...",
	"github.com/trianalab/pacto/v4/pkg/externalcatalog/...",
	"github.com/trianalab/pacto/v4/pkg/externalid/...",
	"github.com/trianalab/pacto/v4/pkg/finding/...",
	"github.com/trianalab/pacto/v4/pkg/fleet/...",
	"github.com/trianalab/pacto/v4/pkg/graph/...",
	"github.com/trianalab/pacto/v4/pkg/impact/...",
	"github.com/trianalab/pacto/v4/pkg/managedres/...",
	"github.com/trianalab/pacto/v4/pkg/memberlink/...",
	"github.com/trianalab/pacto/v4/pkg/oci/...",
	"github.com/trianalab/pacto/v4/pkg/orgindex/...",
	"github.com/trianalab/pacto/v4/pkg/otelobserver/...",
	"github.com/trianalab/pacto/v4/pkg/probe/...",
	"github.com/trianalab/pacto/v4/pkg/readiness/...",
	"github.com/trianalab/pacto/v4/pkg/reconcile/...",
	"github.com/trianalab/pacto/v4/pkg/runrecord",       // exact: its registry subpackage needs oras
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats", // exact: the registry subpackage stays outside
	"github.com/trianalab/pacto/v4/pkg/runtimefacts/...",
	"github.com/trianalab/pacto/v4/pkg/schemax/...",
	"github.com/trianalab/pacto/v4/pkg/semver/...",
	"github.com/trianalab/pacto/v4/pkg/validation/...",
	"github.com/trianalab/pacto/v4/pkg/workloadmanager/...",
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
	// The kernel collector lives in internal/runtimewatch/ebpf, behind `pacto
	// agent`; pkg/runtimefacts carries its facts without linking the loader.
	"github.com/cilium/ebpf",
}

// evidenceConsumers read accepted evidence. They must reach it ONLY through the
// Evidence Server's HTTP DTO: no registry client, no credentials, no referrers
// enumeration of their own.
var evidenceConsumers = []string{
	"github.com/trianalab/pacto/v4/pkg/dashboard/...",
	"github.com/trianalab/pacto/v4/internal/fleetsrc/...",
}

func TestEvidenceConsumersNeverReachTheRegistryStore(t *testing.T) {
	const store = "github.com/trianalab/pacto/v4/internal/evidenceoci"
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
	"github.com/trianalab/pacto/v4/internal/mcp",
	"github.com/trianalab/pacto/v4/internal/cli",
	"github.com/trianalab/pacto/v4/pkg/dashboard",
	"github.com/trianalab/pacto/v4/integrations/",
	"github.com/trianalab/pacto/integrations/",
	"k8s.io/",
	"sigs.k8s.io/",
}

// catalogAllowedPacto is everything inside this repository the catalog core may
// import. Reference parsing, credentials, caching and registry access reach it
// through the caller-supplied Resolver port instead, which is what keeps the
// catalog model reusable and keeps its tests hermetic.
var catalogAllowedPacto = map[string]bool{
	"github.com/trianalab/pacto/v4/pkg/catalog":  true,
	"github.com/trianalab/pacto/v4/pkg/contract": true,
}

func TestCatalogCoreIsFrameworkIndependent(t *testing.T) {
	for _, dep := range deps(t, "github.com/trianalab/pacto/v4/pkg/catalog/...") {
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
	const catalogRoot = "github.com/trianalab/pacto/v4/pkg/catalog"
	for _, dep := range deps(t, "github.com/trianalab/pacto/v4/pkg/fleet/...") {
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
		evidencePkg = "github.com/trianalab/pacto/v4/pkg/evidence"
		findingPkg  = "github.com/trianalab/pacto/v4/pkg/finding"
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

// TestContractIsTheLeaf keeps pkg/contract the domain layer every other
// package builds on (ARCHITECTURE.md, strict layering): it may reach no other
// Pacto package, or the layering inverts and an import cycle is one edit away.
func TestContractIsTheLeaf(t *testing.T) {
	const self = "github.com/trianalab/pacto/v4/pkg/contract"
	for _, dep := range deps(t, self) {
		if strings.HasPrefix(dep, "github.com/trianalab/pacto/") && dep != self {
			t.Errorf("boundary violation: pkg/contract reaches %q; the domain model imports no other Pacto package", dep)
		}
	}
}

// TestWorkloadShapeReadsNoCluster keeps pkg/workloadshape a standard
// library leaf: it reads a decoded workload object, so a folder reader that
// must never reach a cluster may share it with the cluster source.
func TestWorkloadShapeReadsNoCluster(t *testing.T) {
	const self = "github.com/trianalab/pacto/v4/pkg/workloadshape"
	for _, dep := range deps(t, self) {
		for _, bad := range []string{"k8s.io/", "sigs.k8s.io/", "github.com/trianalab/pacto/"} {
			if strings.HasPrefix(dep, bad) && dep != self {
				t.Errorf("boundary violation: pkg/workloadshape reaches %q; it imports only the standard library", dep)
			}
		}
	}
}

// TestPublicPackagesNeverReachInternal keeps pkg/* importable by external Go
// programs: a pkg/* -> internal/* edge would compile here and fail for every
// outside caller, and drag the CLI's wiring into the public API.
func TestPublicPackagesNeverReachInternal(t *testing.T) {
	for _, dep := range deps(t, "github.com/trianalab/pacto/v4/pkg/...") {
		if strings.HasPrefix(dep, "github.com/trianalab/pacto/v4/internal/") {
			t.Errorf("boundary violation: a pkg/* package reaches %q; public packages must not depend on internal/*", dep)
		}
	}
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

// TestInferredContractIsKubernetesFree keeps pkg/inferredcontract usable by
// the CLI and the operator alike: it publishes through pkg/publish (which
// reaches the registry, so it is not a core package) and never reaches a
// Kubernetes client, an integration or the CLI's internal packages. Only
// k8s.io/klog, the logger pkg/signing's sigstore dependencies log through,
// is allowed.
func TestInferredContractIsKubernetesFree(t *testing.T) {
	const mod = "github.com/trianalab/pacto/v4/"
	for _, dep := range deps(t, mod+"pkg/inferredcontract") {
		if strings.HasPrefix(dep, "k8s.io/klog/") {
			continue
		}
		for _, bad := range []string{"k8s.io/", "sigs.k8s.io/", "github.com/trianalab/pacto/integrations/", mod + "integrations/", mod + "internal/"} {
			if strings.HasPrefix(dep, bad) {
				t.Errorf("boundary violation: pkg/inferredcontract reaches %q; it stays Kubernetes-free", dep)
			}
		}
	}
}

// TestDiscoverReadsNoClusterOrRuntime keeps folder discovery a pure read of
// the repository: what Pacto infers for a contract comes from files, never
// from a cluster or runtime observation, so internal/discover reaches no
// Kubernetes client, runtime watcher, fleet source or the pactify package
// that drafts from a cluster. internal/ci, which plans a repository for a CI
// run, is held to the same fence.
func TestDiscoverReadsNoClusterOrRuntime(t *testing.T) {
	const mod = "github.com/trianalab/pacto/v4/"
	forbidden := []string{"k8s.io/", "sigs.k8s.io/", mod + "internal/k8sclient", mod + "internal/runtimewatch", mod + "pkg/runtimefacts", mod + "internal/fleetsrc", mod + "internal/pactify"}
	for _, pkg := range []string{"internal/discover", "internal/ci"} {
		for _, dep := range deps(t, mod+pkg) {
			for _, bad := range forbidden {
				if strings.HasPrefix(dep, bad) {
					t.Errorf("boundary violation: %s reaches %q; discovery and the CI plan read the repository only", pkg, dep)
				}
			}
		}
	}
}

// clusterViewSymbols are the pkg/fleet names that carry what a restricted
// reader may see of each cluster: access marks, per-cluster summaries and the
// cluster records they come from.
var clusterViewSymbols = []string{"ClusterSummary", "ClusterRecord", "ClusterNoAccess", "ClusterReviewFailed", "ClusterNotReviewable"}

// joinListReaders are the gate-path files whose Clusters selectors name the
// hub's member join list (delivery.ClusterJoinList.Clusters) and the
// candidate inventory (detect.Clusters), never a fleet cluster view; every
// other check still applies to them.
var joinListReaders = map[string]string{
	"pkg/delivery/clustercandidates.go": "matches the hub's candidate clusters to its join list by stated UID; it reads no fleet view",
}

// TestNoGateReadsClusterAccess keeps cluster access marks, per-cluster
// summaries and the Cluster list filter out of every gate: they describe what
// one reader may see, so a gate that read them would pass or fail by who asked.
// It matches by name (see deadcode_test.go for why), so a red run is always real.
func TestNoGateReadsClusterAccess(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range []string{"pkg/impact", "pkg/delivery", "internal/ci"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, at := range clusterViewReads(t, path, filepath.ToSlash(rel)) {
				t.Errorf("%s: a gate reads cluster access, a cluster summary or the Cluster filter", at)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// clusterViewReads lists, as rel:line, every read of a cluster view fact in
// the file at path.
func clusterViewReads(t *testing.T, path, rel string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	var out []string
	at := func(n ast.Node) { out = append(out, rel+":"+strconv.Itoa(fset.Position(n.Pos()).Line)) }
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			x, ok := n.X.(*ast.Ident)
			joinList := joinListReaders[rel] != "" && n.Sel.Name == "Clusters"
			if (ok && x.Name == "fleet" && slices.Contains(clusterViewSymbols, n.Sel.Name)) || (n.Sel.Name == "Clusters" && !joinList) || n.Sel.Name == "ClustersTruncated" {
				at(n)
			}
		case *ast.CompositeLit:
			if sel, ok := n.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "EntityFilter" && slices.ContainsFunc(n.Elts, func(e ast.Expr) bool {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					return false
				}
				k, ok := kv.Key.(*ast.Ident)
				return ok && k.Name == "Cluster"
			}) {
				at(n)
			}
		}
		return true
	})
	return out
}
