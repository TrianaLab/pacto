package pactify

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// completeFixture copies discover's completion fixture (DB_PASSWORD=hunter2
// and the other leak literals) into a folder named payments, with pacto.yaml.
func completeFixture(t *testing.T, pactoYAML string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "payments")
	if err := os.CopyFS(dir, os.DirFS("../discover/testdata/complete")); err != nil {
		t.Fatal(err)
	}
	return writeTree(t, dir, map[string]string{"pacto.yaml": pactoYAML})
}

const completeContract = "service: {name: payments, version: 1.0.0}\ninterfaces:\n  - {name: api, type: http, ref: api/openapi.yaml}\n"

func TestInitCompletesExistingContract(t *testing.T) {
	dir := completeFixture(t, completeContract)
	r, err := Init(context.Background(), Options{Dir: dir, Commit: "4e1a9c0", PactoVersion: "4.6.0"}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(got) != completeContract {
		t.Fatalf("pacto.yaml changed:\n%s", got)
	}
	if r.Mode != modeComplete || r.Inferred == nil || r.Inferred.Inputs.Commit != "4e1a9c0" || r.Inferred.Inputs.Pacto != "4.6.0" {
		t.Fatalf("mode %q, record %+v", r.Mode, r.Inferred)
	}
	var fields []string
	for _, f := range r.Inferred.Fields {
		fields = append(fields, f.Field)
	}
	want := []string{"capabilities[health]", "configuration.API_TOKEN", "configuration.BILLING_URL", "configuration.CLIENT_SECRET", "configuration.DATABASE_URL",
		"configuration.DB_PASSWORD", "configuration.PORT", "dependencies[billing]", "dependencies[db]", "service.owner", "workload"}
	if !slices.Equal(fields, want) {
		t.Fatalf("fields = %q, want %q", fields, want)
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == contract.InferredRecordFile }) {
		t.Fatalf("the folder never gets the record: %+v", r.Files)
	}
	if !hasLine(r.Found, "pacto.yaml declares 1 of the values Pacto reads in this folder's manifests and specs; Pacto completed 11 in pacto.inferred.yaml, published with the bundle") {
		t.Fatalf("found: %q", r.Found)
	}
	if hasLine(r.Unknown, "pacto.yaml") {
		t.Fatalf("unknown: %q", r.Unknown)
	}
	if _, err := os.Stat(filepath.Join(dir, contract.InferredRecordFile)); err == nil {
		t.Fatal("apply wrote the record into the folder")
	}
}

// A contract that names no service completes under the name pacto init
// reports (the workload's, app), never the folder's, so a folder named after
// a host the service calls still lists that host in both places.
func TestInitCompletionSelfIsReportedService(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "billing")
	if err := os.CopyFS(dir, os.DirFS("../discover/testdata/complete")); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\n"})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Service != "app" || r.Inferred == nil {
		t.Fatalf("plan: %+v %v", r, err)
	}
	inRecord := slices.ContainsFunc(r.Inferred.Fields, func(f contract.Inferred) bool { return f.Field == "dependencies[billing]" })
	inResult := slices.ContainsFunc(r.Dependencies, func(f Finding) bool { return f.Name == "billing" })
	if !inRecord || !inResult {
		t.Fatalf("record %v, dependencies %v: %+v", inRecord, inResult, r.Dependencies)
	}
}

// A folder whose contract declares everything it shows completes to nothing.
func TestInitCompletesNothingWhenAllDeclared(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "service: {name: p, version: 1.0.0}\n"})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Mode != modeComplete || r.Inferred != nil || !hasLine(r.Found, "pacto.yaml declares every value Pacto reads in this folder's manifests and specs, so Pacto infers nothing") {
		t.Fatalf("plan: %+v %v", r, err)
	}
}

// A chart Pacto does not read stays an unknown in completion mode, so a
// contract over an unrendered chart never reads as declaring everything.
func TestInitCompletionKeepsChartUnknown(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"pacto.yaml":                      "pactoVersion: \"2.5\"\nservice: {name: p}\n",
		"chart/Chart.yaml":                "apiVersion: v2\nname: p\nversion: 1.0.0\n",
		"chart/templates/deployment.yaml": "kind: Deployment\nmetadata: {name: p}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Mode != modeComplete || !slices.ContainsFunc(r.Unknown, func(u string) bool { return strings.HasPrefix(u, "the Helm chart in chart is not read") }) {
		t.Fatalf("plan: %+v %v", r, err)
	}
}

// A cited file that cannot be read is an error, never a partial record.
func TestInitCompletionUnreadableSource(t *testing.T) {
	dir := completeFixture(t, "service: {name: payments, version: 1.0.0}\n")
	if err := os.Chmod(filepath.Join(dir, "api", "openapi.yaml"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "api", "openapi.yaml")); err == nil {
		t.Skip("running with permission to read every file")
	}
	if _, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile); err == nil || !strings.Contains(err.Error(), "completing pacto.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitOlderContractNotCompleted(t *testing.T) {
	dir := completeFixture(t, "pactoVersion: \"2.4\"\n"+completeContract)
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Inferred != nil || r.Mode == modeComplete ||
		!hasLine(r.Next, `pacto.yaml declares pactoVersion 2.4; set pactoVersion: "2.5" to have Pacto complete it`) {
		t.Fatalf("plan: %+v %v", r, err)
	}
}

func TestInitClusterNeverCompletes(t *testing.T) {
	dir := completeFixture(t, "pactoVersion: \"2.5\"\n"+completeContract)
	// A cluster draft that saw no calls has no Inferred, so the cluster flag
	// alone has to keep it from completing.
	for name, opts := range map[string]Options{
		"calls seen":    {Dir: dir, Plan: true, Inferred: []Inferred{{Name: "ledger", Where: "calls seen"}}},
		"no calls seen": {Dir: dir, Plan: true, cluster: true},
	} {
		r, err := Init(context.Background(), opts, os.WriteFile)
		if err != nil || r.Inferred != nil || r.Mode == modeComplete || !hasLine(r.Next, "run pacto init "+dir+" without --cluster to complete it") {
			t.Fatalf("%s: %+v %v", name, r, err)
		}
	}
}
