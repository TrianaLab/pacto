package architecture

import (
	"os/exec"
	"slices"
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
	runRecordImporters = []string{modRoot + "pkg/runrecord/registry", modRoot + "internal/cli"}
	// runRecordRegistryImporters may import pkg/runrecord/registry directly.
	runRecordRegistryImporters = []string{modRoot + "internal/cli"}
	// runRecordReachers may reach either package transitively.
	runRecordReachers = []string{runRecordPkg, runRecordRegistry, modRoot + "internal/cli", modRoot + "cmd/pacto", modRoot + "cmd/gendocs"}
)

// operatorRunRecordEmitters may import pkg/runrecord/... from the operator
// module. S2 adds exactly its emitter package; it writes and never reads.
var operatorRunRecordEmitters = []string{}

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
