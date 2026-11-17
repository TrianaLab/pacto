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
	if !hasLine(r.Found, "pacto.yaml declares 1 of the values this folder shows; Pacto completed 11 in pacto.inferred.yaml, published with the bundle") {
		t.Fatalf("found: %q", r.Found)
	}
	if hasLine(r.Unknown, "pacto.yaml") {
		t.Fatalf("unknown: %q", r.Unknown)
	}
	if _, err := os.Stat(filepath.Join(dir, contract.InferredRecordFile)); err == nil {
		t.Fatal("apply wrote the record into the folder")
	}
}

// A folder whose contract declares everything it shows completes to nothing.
func TestInitCompletesNothingWhenAllDeclared(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "service: {name: p, version: 1.0.0}\n"})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Mode != modeComplete || r.Inferred != nil || !hasLine(r.Found, "pacto.yaml declares every value this folder shows, so Pacto infers nothing") {
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
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true, Inferred: []Inferred{{Name: "ledger", Where: "calls seen"}}}, os.WriteFile)
	if err != nil || r.Inferred != nil || r.Mode == modeComplete || !hasLine(r.Next, "run pacto init "+dir+" without --cluster to complete it") {
		t.Fatalf("plan: %+v %v", r, err)
	}
}
