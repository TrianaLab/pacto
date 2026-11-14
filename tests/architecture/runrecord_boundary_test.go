package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Run records are unsigned statistics. Nothing that decides a gate, a
// compliance state or an admission may read them, so these rules are
// allowlists: a new package fails without anyone having to name it.
const (
	modRoot           = "github.com/trianalab/pacto/v4/"
	runRecordPkg      = modRoot + "pkg/runrecord"
	runRecordRegistry = modRoot + "pkg/runrecord/registry"
)

// Later stories add their readers in the same commit as the import: S3 adds
// pkg/runrecord/stats, internal/app, internal/mcp and internal/tui; S4 adds
// pkg/dashboard and its importers (examples/demo, cmd/genbundle, pkg/doc); S5
// adds examples/story and the acceptance harnesses that import it; nothing that
// decides a gate, a compliance state or an admission is ever added.
var (
	// runRecordImporters may import pkg/runrecord directly.
	runRecordImporters = []string{modRoot + "pkg/runrecord/registry", modRoot + "pkg/runrecord/stats", modRoot + "internal/cli", modRoot + "internal/app", modRoot + "examples/story"}
	// runRecordRegistryImporters may import pkg/runrecord/registry directly.
	runRecordRegistryImporters = []string{modRoot + "internal/cli", modRoot + "internal/app"}
	// runRecordReachers may reach either package transitively.
	runRecordReachers = []string{runRecordPkg, runRecordRegistry, modRoot + "pkg/runrecord/stats", modRoot + "internal/cli", modRoot + "internal/app", modRoot + "internal/mcp", modRoot + "internal/tui", modRoot + "cmd/pacto", modRoot + "cmd/gendocs",
		modRoot + "examples/story", modRoot + "examples/demo", modRoot + "tests/acceptance/kind/storybeats"}
)

// operatorRunRecordEmitters may import pkg/runrecord/... from the operator
// module. S2 adds exactly its emitter package; it writes and never reads.
var operatorRunRecordEmitters = []string{runStatsPkg}

const (
	operatorRoot   = "github.com/trianalab/pacto/integrations/kubernetes/v6/"
	runStatsPkg    = operatorRoot + "internal/runstats"
	runStatsDir    = "../../integrations/kubernetes/internal/runstats"
	ocireferrerPkg = modRoot + "pkg/ocireferrer"
)

// listGraph maps every package matching pattern to the packages in field
// (Imports for direct imports, Deps for the transitive closure).
func listGraph(t *testing.T, pattern, field string) map[string][]string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join ."+field+" \" \"}}", pattern).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", pattern, err, out)
	}
	graph := map[string][]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		graph[fields[0]] = fields[1:]
	}
	return graph
}

func isRunRecord(pkg string) bool {
	return pkg == runRecordPkg || strings.HasPrefix(pkg, runRecordPkg+"/")
}

func TestDecidersNeverReachRunRecords(t *testing.T) {
	for pkg, imports := range listGraph(t, modRoot+"...", "Imports") {
		if slices.Contains(imports, runRecordPkg) && !slices.Contains(runRecordImporters, pkg) {
			t.Errorf("statistics are never evidence: %s reaches %s", pkg, runRecordPkg)
		}
		if slices.Contains(imports, runRecordRegistry) && !slices.Contains(runRecordRegistryImporters, pkg) {
			t.Errorf("statistics are never evidence: %s reaches %s", pkg, runRecordRegistry)
		}
	}
	for pkg, closure := range listGraph(t, modRoot+"...", "Deps") {
		if slices.Contains(runRecordReachers, pkg) {
			continue
		}
		for _, dep := range closure {
			if isRunRecord(dep) {
				t.Errorf("statistics are never evidence: %s reaches %s", pkg, dep)
			}
		}
	}
}

func TestOperatorNeverImportsRunRecords(t *testing.T) {
	for pkg, imports := range listGraph(t, "github.com/trianalab/pacto/integrations/kubernetes/v6/...", "Imports") {
		if slices.Contains(operatorRunRecordEmitters, pkg) {
			continue
		}
		for _, dep := range imports {
			if isRunRecord(dep) {
				t.Errorf("statistics are never evidence: %s reaches %s", pkg, dep)
			}
		}
	}
}

// runStatsWrites is everything the emitter may use from the run record
// packages: it builds and publishes. S3 adds readers to those packages; the
// emitter never calls them.
var runStatsWrites = []string{
	runRecordPkg + ".FromOperation", runRecordPkg + ".OperationInput", runRecordPkg + ".Record",
	runRecordRegistry + ".Open", runRecordRegistry + ".Publish",
}

func TestRunStatsOnlyWrites(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(runStatsDir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no emitter sources in %s: %v", runStatsDir, err)
	}
	watched := []string{runRecordPkg, runRecordRegistry, ocireferrerPkg}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]string{}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(path)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			names[name] = path
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && slices.Contains(watched, names[id.Name]) {
				if use := names[id.Name] + "." + sel.Sel.Name; !slices.Contains(runStatsWrites, use) {
					t.Errorf("statistics are never evidence: %s uses %s, which is not a write", file, use)
				}
			}
			return true
		})
	}
}

func TestRunStatsNeverReadsRecords(t *testing.T) {
	forbidden := []string{modRoot + "pkg/deliveryrecord", modRoot + "pkg/signing", operatorRoot + "internal/delivery"}
	for _, dep := range deps(t, runStatsPkg) {
		if slices.Contains(forbidden, dep) {
			t.Errorf("statistics are never evidence: %s reaches %s", runStatsPkg, dep)
		}
	}
	for _, dep := range deps(t, operatorRoot+"internal/delivery") {
		if isRunRecord(dep) || dep == runStatsPkg {
			t.Errorf("statistics are never evidence: internal/delivery reaches %s", dep)
		}
	}
}
