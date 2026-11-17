package pactify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// unlockedRedis is the finding the payments fixture's chart gives.
const unlockedRedis = "chart dependency 'redis' has no Chart.lock digest, not read"

func TestPactifyChartReadStatically(t *testing.T) {
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "discovery/payments"), Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != modePactify || hasLine(r.Unknown, "render it") || hasLine(r.Unknown, "is not read: render") {
		t.Fatalf("mode %s, unknown %q", r.Mode, r.Unknown)
	}
	if !slices.Contains(r.Found, "Helm chart chart read statically: image ghcr.io/acme/payments:1.4.0, port 8080, 1 env key") {
		t.Errorf("found %q", r.Found)
	}
}

func TestPactifyOpenAPIAnyPath(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "orders"), map[string]string{
		"Dockerfile":          "FROM scratch\nEXPOSE 8080\n",
		"api/openapi.v1.yaml": "openapi: 3.0.0\ninfo: {title: orders, version: 1.0.0}\npaths: {}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if text := file(t, r, "pacto.yaml").Content; !strings.Contains(text, "ref: api/openapi.v1.yaml\n") {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
}

func TestPactifyBlankUsesDiscoveredOpenAPI(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "orders"), map[string]string{
		"chart/Chart.yaml":    "apiVersion: v2\nname: orders\nversion: 0.1.0\n",
		"api/openapi.v1.yaml": "openapi: 3.0.0\ninfo: {title: orders, version: 1.0.0}\npaths: {}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if r.Mode != modeBlank || !strings.Contains(file(t, r, "pacto.yaml").Content, "ref: api/openapi.v1.yaml\n") {
		t.Fatalf("mode %s, pacto.yaml:\n%s", r.Mode, file(t, r, "pacto.yaml").Content)
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == "interfaces/openapi.yaml" }) {
		t.Errorf("a placeholder is planned beside the discovered spec: %+v", r.Files)
	}
	// Without a spec the placeholder stays.
	empty, err := Init(context.Background(), Options{Dir: t.TempDir(), Name: "orders", Plan: true}, os.WriteFile)
	if err != nil || file(t, empty, "interfaces/openapi.yaml").Action != Add || empty.Inferred != nil {
		t.Fatalf("empty folder: %+v %v", empty, err)
	}
}

func TestPactifyDiscoveryFindingsUnknown(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"new contract": nil,
		"complete":     {"pacto.yaml": "pactoVersion: \"2.5\"\n"},
	} {
		dir := writeTree(t, fixtureDir(t, "discovery/payments"), files)
		r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(r.Unknown, unlockedRedis) || r.Inferred == nil ||
			!slices.ContainsFunc(r.Inferred.Findings, func(f contract.InferredFinding) bool { return f.Message == unlockedRedis }) {
			t.Errorf("%s: unknown %q, record %+v", name, r.Unknown, r.Inferred)
		}
	}
}

// discoveryLeakFixtures are the discover fixtures whose files hold values
// (hunter2, s3cr3t-values) or URL credentials (tok3n).
var discoveryLeakFixtures = []string{"helm/values", "helm/creds", "compose/payments", "kustomize/payments", "templates", "docker", "manifests"}

// discoveryLeakDir is a copy of the discover fixture in a folder named
// payments.
func discoveryLeakDir(t *testing.T, fixture string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "payments")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "discover", "testdata", fixture))); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPactifyDiscoveryNoValueBytes(t *testing.T) {
	for _, fixture := range discoveryLeakFixtures {
		r, err := Init(context.Background(), Options{Dir: discoveryLeakDir(t, fixture), Plan: true}, os.WriteFile)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(r)
		for _, leak := range []string{"hunter2", "s3cr3t-values", "tok3n"} {
			if strings.Contains(string(data), leak) {
				t.Errorf("%s: the result holds %q", fixture, leak)
			}
		}
	}
}

// TestPortNeverWrittenToInterfaces: a discovered port reaches the record
// and the plan, never interfaces[].port, whichever contract init writes.
func TestPortNeverWrittenToInterfaces(t *testing.T) {
	for name, dir := range map[string]string{
		"pactify": fixtureDir(t, "discovery/payments"),
		"blank": writeTree(t, filepath.Join(t.TempDir(), "orders"), map[string]string{
			"chart/Chart.yaml": "apiVersion: v2\nname: orders\nversion: 0.1.0\n", "chart/values.yaml": "service:\n  port: 8080\n",
			"api/openapi.v1.yaml": "openapi: 3.0.0\ninfo: {title: orders, version: 1.0.0}\npaths: {}\n"}),
	} {
		r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(file(t, r, "pacto.yaml").Content, "port:") || r.Inferred == nil ||
			!slices.ContainsFunc(r.Inferred.Fields, func(f contract.Inferred) bool { return f.Field == "ports[8080]" }) {
			t.Errorf("%s: pacto.yaml:\n%s\nrecord %+v", name, file(t, r, "pacto.yaml").Content, r.Inferred)
		}
	}
}

// Findings past the record's bound are counted in Unknown.
func TestPactifyDiscoveryFindingsDropped(t *testing.T) {
	p := &planner{r: &Result{}, sc: &discover.Scan{}}
	p.discoveryLines(&contract.InferredRecord{FindingsDropped: 3})
	if !slices.Equal(p.r.Unknown, []string{"and 3 more discovery findings"}) {
		t.Errorf("unknown %q", p.r.Unknown)
	}
}

// A new contract's record holds only what the pacto.yaml init writes leaves
// out: the owner it declares from the team label is not listed again, and a
// contract below 2.5, which publishes no record, says so.
func TestPactifyNewContractRecordAgainstPlanned(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "fraud"), map[string]string{
		"d.yaml":            "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fraud, labels: {team: risk}}\nspec: {template: {spec: {containers: [{name: fraud, image: 'reg.example/fraud:1.0.0'}]}}}\n",
		"catalog-info.yaml": "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata: {name: fraud}\nspec: {owner: group:other-team}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file(t, r, "pacto.yaml").Content, "team: risk") || r.Inferred == nil {
		t.Fatalf("pacto.yaml:\n%s\nrecord %+v", file(t, r, "pacto.yaml").Content, r.Inferred)
	}
	if slices.ContainsFunc(r.Inferred.Fields, func(f contract.Inferred) bool { return f.Field == "service.owner" || f.Field == "workload" }) {
		t.Errorf("the record lists what pacto.yaml declares: %+v", r.Inferred.Fields)
	}
	if !hasLine(r.Next, "pacto pack publishes none of what Pacto inferred") {
		t.Errorf("next %q", r.Next)
	}
}

// A pacto.yaml below 2.5 is not completed, yet its chart and what Pacto
// could not read in the folder are still named.
func TestPactifyOldContractStillNamesFolder(t *testing.T) {
	dir := writeTree(t, fixtureDir(t, "discovery/payments"), map[string]string{"pacto.yaml": "pactoVersion: \"2.4\"\nservice: {name: payments, version: 1.0.0}\n"})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if r.Inferred != nil || !slices.Contains(r.Unknown, unlockedRedis) || !hasLine(r.Found, "Helm chart chart read statically") {
		t.Errorf("found %q, unknown %q, record %+v", r.Found, r.Unknown, r.Inferred)
	}
}
