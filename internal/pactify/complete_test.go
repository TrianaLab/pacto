package pactify

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/discover"
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
		"configuration.DB_PASSWORD", "configuration.PORT", "dependencies[billing]", "dependencies[db]", "image", "service.owner", "workload"}
	if !slices.Equal(fields, want) {
		t.Fatalf("fields = %q, want %q", fields, want)
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == contract.InferredRecordFile }) {
		t.Fatalf("the folder never gets the record: %+v", r.Files)
	}
	if !hasLine(r.Found, "pacto.yaml declares 1 of the values Pacto reads in this folder's manifests and specs; Pacto completed 12 in pacto.inferred.yaml, published with the bundle") {
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

// A folder that adds no value but holds a file Pacto cannot read keeps the
// record for the finding and never says it completed 0 values.
func TestInitFindingsOnlyCompletesNothing(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: p}\n",
		"bad.yaml":   "a: [\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Inferred == nil || len(r.Inferred.Fields) != 0 || len(r.Inferred.Findings) != 1 ||
		!hasLine(r.Found, "pacto.yaml declares every value Pacto reads in this folder's manifests and specs, so Pacto infers nothing") ||
		slices.ContainsFunc(r.Found, func(l string) bool { return strings.Contains(l, "completed 0") }) {
		t.Fatalf("plan: %+v %v", r, err)
	}
}

// Completion names each chart it read statically, as a new contract does.
func TestInitCompletionNamesChartRead(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"pacto.yaml":                      "pactoVersion: \"2.5\"\nservice: {name: p}\n",
		"chart/Chart.yaml":                "apiVersion: v2\nname: p\nversion: 1.0.0\n",
		"chart/templates/deployment.yaml": "kind: Deployment\nmetadata: {name: p}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil || r.Mode != modeComplete || !slices.Contains(r.Found, "Helm chart chart read statically") {
		t.Fatalf("plan: %+v %v", r, err)
	}
}

// unreadableRepo is a folder named payments, holding files, in a repository
// root Pacto finds but cannot open.
func unreadableRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	dir := writeTree(t, filepath.Join(repo, "payments"), files)
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(repo, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(repo, 0o755) })
	if _, err := os.ReadDir(repo); err == nil {
		t.Skip("running with permission to read every folder")
	}
	return dir
}

// A folder Pacto cannot read is an error, never a partial record.
func TestInitCompletionUnreadableSource(t *testing.T) {
	dir := unreadableRepo(t, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\n" + completeContract})
	if _, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile); err == nil || !strings.Contains(err.Error(), "completing pacto.yaml") {
		t.Fatalf("err = %v", err)
	}
	dir = unreadableRepo(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	if _, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("new contract: err = %v", err)
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

// A kept pactoVersion 2.5 contract with cluster facts completes through
// discover.CompleteCluster: the folder's fields plus the cluster's, with the
// cluster input, and pacto.yaml unchanged.
func TestInitWithClusterFactsCompletes(t *testing.T) {
	text := "pactoVersion: \"2.5\"\n" + completeContract
	dir := completeFixture(t, text)
	facts := &discover.ClusterFacts{Object: "shop/Deployment/payments", AsOf: time.Date(2026, 11, 14, 10, 2, 0, 0, time.UTC), CallWindow: time.Hour,
		Calls: []discover.ClusterCall{{Name: "ledger", Ref: "oci://ghcr.io/acme/ledger"}}}
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true, Cluster: facts}, os.WriteFile)
	if err != nil || r.Mode != modeComplete || r.Inferred == nil || r.Inferred.Inputs.Cluster == nil {
		t.Fatalf("%+v %v", r, err)
	}
	i := slices.IndexFunc(r.Inferred.Fields, func(f contract.Inferred) bool { return f.Field == "dependencies[ledger]" })
	if i < 0 || r.Inferred.Fields[i].Source != "cluster:runtime calls from shop/Deployment/payments" ||
		!slices.ContainsFunc(r.Inferred.Fields, func(f contract.Inferred) bool { return f.Field == "dependencies[billing]" }) {
		t.Fatalf("fields = %+v", r.Inferred.Fields)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(got) != text {
		t.Fatalf("pacto.yaml changed:\n%s", got)
	}
}

// completePlanned fails, never writing a record, when the contract it would
// plan does not parse or the folder cannot be read.
func TestCompletePlannedErrors(t *testing.T) {
	bad := &planner{r: &Result{Dir: t.TempDir(), Files: []File{{Path: "pacto.yaml", Action: Add, Content: "service: ["}}}}
	if err := bad.completePlanned(contract.InferredInputs{}); err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("unparsable plan: %v", err)
	}
	gone := writeTree(t, t.TempDir(), map[string]string{"file": "x"}) + "/file" // a file, never a folder
	p := &planner{r: &Result{Dir: gone, Files: []File{{Path: "pacto.yaml", Action: Add, Content: completeContract}}}}
	if err := p.completePlanned(contract.InferredInputs{}); err == nil || !strings.Contains(err.Error(), "reading "+gone) || p.r.Inferred != nil {
		t.Errorf("unreadable folder: %v, %+v", err, p.r.Inferred)
	}
}
