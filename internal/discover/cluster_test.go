package discover

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	clusterDir    = "testdata/cluster"
	clusterObject = "shop/Deployment/invoices"
)

var clusterAsOf = time.Date(2026, 11, 14, 10, 2, 0, 0, time.UTC)

func clusterDeclared() *contract.Contract {
	return &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "invoices"}}
}

func clusterInputs() contract.InferredInputs {
	return contract.InferredInputs{Commit: "4e1a9c0", Pacto: "4.6.0"}
}

func clusterFacts() ClusterFacts {
	return ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: 24 * time.Hour, MaxItems: 10000,
		Images: []ClusterImage{{Container: "app", Digest: "sha256:" + strings.Repeat("a", 64)}},
		Calls:  []ClusterCall{{Name: "ledger", Ref: "oci://ghcr.io/acme/ledger", AsOf: clusterAsOf.Add(-time.Minute)}},
	}
}

func completeCluster(t *testing.T, dir string, declared *contract.Contract, facts ClusterFacts) *contract.InferredRecord {
	t.Helper()
	rec, err := CompleteCluster(dir, declared, nil, clusterInputs(), nil, facts)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func fieldByName(rec *contract.InferredRecord, name string) (contract.Inferred, bool) {
	i := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == name })
	if i < 0 {
		return contract.Inferred{}, false
	}
	return rec.Fields[i], true
}

// parses fails the test unless rec's YAML parses with the strict decoder.
func parses(t *testing.T, rec *contract.InferredRecord) {
	t.Helper()
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatalf("%v\n%s", err, rec.Marshal())
	}
}

func TestCompleteClusterMatchesCIForFolderFields(t *testing.T) {
	ci, err := CompleteIn(clusterDir, clusterDir, clusterDeclared(), clusterInputs(), nil)
	if err != nil || ci == nil {
		t.Fatal(ci, err)
	}
	cl := completeCluster(t, clusterDir, clusterDeclared(), clusterFacts())
	for _, f := range ci.Fields {
		if !slices.ContainsFunc(cl.Fields, func(g contract.Inferred) bool { return reflect.DeepEqual(f, g) }) {
			t.Errorf("folder field %+v is missing or changed", f)
		}
	}
	if len(cl.Fields) <= len(ci.Fields) {
		t.Fatalf("cluster record adds nothing: %q", fieldNames(cl))
	}
	for _, g := range cl.Fields {
		if !slices.ContainsFunc(ci.Fields, func(f contract.Inferred) bool { return reflect.DeepEqual(f, g) }) && !strings.HasPrefix(g.Source, "cluster:") {
			t.Errorf("added field %+v is not a cluster fact", g)
		}
	}
	if !reflect.DeepEqual(ci.Inputs.Sources, cl.Inputs.Sources) || !reflect.DeepEqual(ci.Findings, cl.Findings) {
		t.Errorf("inputs or findings differ: %+v %+v", ci.Inputs.Sources, cl.Inputs.Sources)
	}
	if c := cl.Inputs.Cluster; c == nil || *c != (contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: clusterObject, CallWindow: "24h", MaxItems: 10000}) {
		t.Errorf("cluster input = %+v", c)
	}
	parses(t, cl)
}

func TestCompleteClusterSnapshotKeepsValues(t *testing.T) {
	plain := completeCluster(t, clusterDir, clusterDeclared(), ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: time.Hour})
	facts := ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: time.Hour, Snapshots: []string{"deploy.yaml"}}
	snap := completeCluster(t, clusterDir, clusterDeclared(), facts)
	if len(plain.Fields) != len(snap.Fields) {
		t.Fatalf("fields %q vs %q", fieldNames(plain), fieldNames(snap))
	}
	stamped := 0
	for i, f := range plain.Fields {
		g := snap.Fields[i]
		if strings.HasPrefix(f.Source, "deploy.yaml:") {
			stamped++
			if g.AsOf != "2026-11-14T10:02:00Z" || !strings.HasPrefix(g.Source, "cluster:") {
				t.Errorf("%s: %+v", f.Field, g)
			}
			g.Source, g.AsOf = f.Source, ""
		}
		if !reflect.DeepEqual(f, g) {
			t.Errorf("%s: %+v, want %+v", f.Field, g, f)
		}
	}
	if stamped == 0 || slices.ContainsFunc(snap.Inputs.Sources, func(s contract.InferredSourceFile) bool { return s.Path == "deploy.yaml" }) ||
		!slices.ContainsFunc(plain.Inputs.Sources, func(s contract.InferredSourceFile) bool { return s.Path == "deploy.yaml" }) {
		t.Errorf("stamped %d, sources %+v", stamped, snap.Inputs.Sources)
	}
	if f, _ := fieldByName(snap, "workload"); !strings.HasPrefix(f.Source, "cluster:manifest "+clusterObject+" deploy.yaml:") {
		t.Errorf("workload source = %q", f.Source)
	}
	parses(t, snap)
}

func TestCompleteClusterLabelSource(t *testing.T) {
	facts := clusterFacts()
	facts.Snapshots = []string{"deploy.yaml"}
	rec := completeCluster(t, clusterDir, clusterDeclared(), facts)
	f, ok := fieldByName(rec, "service.owner")
	if !ok || f.Source != "cluster:label "+clusterObject+" team" || f.Value != "payments" || f.Completeness != "" {
		t.Fatalf("owner = %+v", f)
	}
}

func TestCompleteClusterImageDigests(t *testing.T) {
	a, b, c := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64)
	facts := ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: time.Hour, Images: []ClusterImage{
		{Container: "app", Digest: b}, {Container: "app", Digest: a}, {Container: "app", Digest: b}, {Container: "sidecar", Digest: c, Partial: true},
	}}
	rec := completeCluster(t, clusterDir, clusterDeclared(), facts)
	app, _ := fieldByName(rec, "image.digest[app]")
	side, _ := fieldByName(rec, "image.digest[sidecar]")
	src := "cluster:runtime pods of " + clusterObject
	if app.Value != a+", "+b || app.Source != src || app.Declare != "" || app.Completeness != "complete" || app.AsOf != "2026-11-14T10:02:00Z" {
		t.Errorf("app = %+v", app)
	}
	if side.Value != c || side.Completeness != "partial" || side.Declare != "" {
		t.Errorf("sidecar = %+v", side)
	}
	parses(t, rec)
}

func TestCompleteClusterRuntimeDependency(t *testing.T) {
	facts := clusterFacts()
	facts.Calls[0].Partial = true
	rec := completeCluster(t, clusterDir, clusterDeclared(), facts)
	f, ok := fieldByName(rec, "dependencies[ledger]")
	if !ok || f.Source != "cluster:runtime calls from "+clusterObject || f.Value != "oci://ghcr.io/acme/ledger" ||
		f.AsOf != "2026-11-14T10:01:00Z" || f.Completeness != "partial" || f.Confidence != contract.ConfidenceLikely ||
		!strings.Contains(f.Declare, "name: ledger") || f.Proposed != "" {
		t.Fatalf("dependency = %+v", f)
	}
	if _, ok := fieldByName(rec, "dependencies[ledger].compatibility"); ok {
		t.Error("a call with no proposal proposes a range")
	}
	parses(t, rec)
}

func TestCompleteClusterProposal(t *testing.T) {
	facts := clusterFacts()
	facts.Calls[0].Proposed, facts.Calls[0].Versions, facts.Calls[0].Partial = "^2.3.0", "2.3.1", true
	facts.Calls[0].AsOf = time.Time{}
	rec := completeCluster(t, clusterDir, clusterDeclared(), facts)
	f, ok := fieldByName(rec, "dependencies[ledger].compatibility")
	if !ok || f.Value != "compatibility unknown" || f.Proposed != "^2.3.0" || f.Source != "cluster:fleet reported versions 2.3.1" ||
		f.Completeness != "partial" || f.Declare != `compatibility: "^2.3.0"` || f.AsOf != "2026-11-14T10:02:00Z" {
		t.Fatalf("proposal = %+v", f)
	}
	parses(t, rec)
}

func TestCompleteClusterFolderKeyWins(t *testing.T) {
	ci, _ := CompleteIn(clusterDir, clusterDir, clusterDeclared(), clusterInputs(), nil)
	folder, ok := fieldByName(ci, "dependencies[billing]")
	if !ok {
		t.Fatalf("fixture has no folder dependency: %q", fieldNames(ci))
	}
	facts := clusterFacts()
	facts.Calls = []ClusterCall{{Name: "billing", Ref: "oci://ghcr.io/acme/billing"}}
	rec := completeCluster(t, clusterDir, clusterDeclared(), facts)
	n := 0
	for _, f := range rec.Fields {
		if f.Field == "dependencies[billing]" {
			n++
			if !reflect.DeepEqual(f, folder) {
				t.Errorf("folder field changed: %+v", f)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d billing entries", n)
	}
}

func TestCompleteClusterDeclaredFiltered(t *testing.T) {
	facts := clusterFacts()
	facts.Calls[0].Proposed, facts.Calls[0].Versions = "^2.3.0", "2.3.1"
	for compat, want := range map[string][]string{"^2.0.0": nil, "unknown": {"dependencies[ledger].compatibility"}, "": {"dependencies[ledger].compatibility"}} {
		d := clusterDeclared()
		d.Dependencies = []contract.Dependency{{Ref: "oci://ghcr.io/acme/ledger:2.3.1", Compatibility: compat}}
		rec := completeCluster(t, clusterDir, d, facts)
		var got []string
		for _, f := range rec.Fields {
			if strings.HasPrefix(f.Field, "dependencies[ledger]") {
				got = append(got, f.Field)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("compatibility %q: %q, want %q", compat, got, want)
		}
	}
}

func TestCompleteClusterCoverageOnly(t *testing.T) {
	dir := t.TempDir()
	cov := []contract.InferredCoverage{{Source: "workloads", State: "complete"}, {Source: "runtime-calls", State: "failed", Reason: "no node runtime observations"}}
	rec := completeCluster(t, dir, nil, ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: 90 * time.Minute,
		Coverage: cov, Hidden: contract.InferredHidden{Services: 1}})
	if rec == nil || len(rec.Fields) != 0 || !reflect.DeepEqual(rec.Coverage, cov) || rec.Hidden == nil || rec.Hidden.Services != 1 ||
		rec.Inputs.Discovery != Version || len(rec.Inputs.Catalog) == 0 || rec.Inputs.Cluster.CallWindow != "1h30m" {
		t.Fatalf("record = %+v", rec)
	}
	parses(t, rec)
}

func TestCompleteClusterNilWhenNothing(t *testing.T) {
	rec := completeCluster(t, t.TempDir(), nil, ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: time.Minute,
		Coverage: []contract.InferredCoverage{{Source: "workloads", State: "complete"}}})
	if rec != nil {
		t.Fatalf("record = %+v", rec)
	}
}

func TestCompleteClusterFolderError(t *testing.T) {
	if _, err := CompleteCluster("testdata/cluster/deploy.yaml", nil, nil, clusterInputs(), nil, clusterFacts()); err == nil {
		t.Fatal("a missing folder completes")
	}
}

func TestClusterDurationsAndTimes(t *testing.T) {
	for d, want := range map[time.Duration]string{24 * time.Hour: "24h", 10 * time.Minute: "10m", 70 * time.Minute: "1h10m", 90 * time.Second: "1m30s"} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
	if clusterTime(time.Time{}) != "" || clusterTime(clusterAsOf.Add(500*time.Millisecond)) != "2026-11-14T10:02:00Z" {
		t.Error("clusterTime")
	}
}

func TestClusterFactsCarryNoValue(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[ClusterFacts](), reflect.TypeFor[ClusterImage](), reflect.TypeFor[ClusterCall]()} {
		for f := range typ.Fields() {
			if f.Name == "Value" || f.Name == "Env" || f.Name == "Args" {
				t.Errorf("%s.%s can carry a value", typ.Name(), f.Name)
			}
		}
	}
}

// TestCompleteClusterCallWithoutRef: a call to a service no contract ref
// was read for adds no dependency, which would have nothing to declare.
func TestCompleteClusterCallWithoutRef(t *testing.T) {
	facts := ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: time.Hour, Calls: []ClusterCall{{Name: "fraud"}}}
	if rec := completeCluster(t, clusterDir, clusterDeclared(), facts); rec != nil {
		if _, ok := fieldByName(rec, "dependencies[fraud]"); ok {
			t.Fatalf("%s", rec.Marshal())
		}
	}
}

// TestCompleteClusterQuotedEnvNameHasLine: an env name YAML has to quote,
// such as NO, is found on its line, so its field is a cluster fact like the
// rest.
func TestCompleteClusterQuotedEnvNameHasLine(t *testing.T) {
	dir := t.TempDir()
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: invoices\nspec:\n  template:\n    spec:\n      containers:\n" +
		"        - name: app\n          image: reg.io/acme/invoices:1.0.0\n          env:\n            - name: \"NO\"\n              value: x\n            - name: 'ON'\n              value: y\n"
	if err := os.WriteFile(filepath.Join(dir, "manifests.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := ClusterFacts{Object: clusterObject, AsOf: clusterAsOf, CallWindow: time.Hour, Snapshots: []string{"manifests.yaml"}}
	rec := completeCluster(t, dir, clusterDeclared(), facts)
	for name, line := range map[string]string{"configuration.NO": "12", "configuration.ON": "14"} {
		if f, ok := fieldByName(rec, name); !ok || f.Source != "cluster:manifest "+clusterObject+" manifests.yaml:"+line {
			t.Errorf("%s = %+v", name, f)
		}
	}
	parses(t, rec)
}

// TestStampSnapshotsBareSource: a field whose line was not found still
// becomes a cluster fact, so no field cites a snapshot the inputs no longer
// list.
func TestStampSnapshotsBareSource(t *testing.T) {
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: Version, Sources: []contract.InferredSourceFile{{Path: "manifests.yaml", Digest: "sha256:" + strings.Repeat("b", 64)}},
			Cluster: &contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: clusterObject, CallWindow: "1h"}},
		Fields: []contract.Inferred{{Field: "configuration.X", Value: "string", Source: "manifests.yaml", Confidence: contract.ConfidenceLikely}}}
	stampSnapshots(rec, ClusterFacts{Object: clusterObject, Snapshots: []string{"manifests.yaml"}}, "2026-11-14T10:02:00Z")
	if f := rec.Fields[0]; f.Source != "cluster:manifest "+clusterObject+" manifests.yaml" || f.AsOf != "2026-11-14T10:02:00Z" || len(rec.Inputs.Sources) != 0 {
		t.Fatalf("%+v", rec)
	}
	parses(t, rec)
}
