package discover

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const completeDir = "testdata/complete"

func complete(t *testing.T, dir string, declared *contract.Contract) *contract.InferredRecord {
	t.Helper()
	rec, err := Complete(dir, declared, contract.InferredInputs{Commit: "4e1a9c0", Pacto: "4.6.0"})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func fieldNames(rec *contract.InferredRecord) []string {
	var out []string
	for _, f := range rec.Fields {
		out = append(out, f.Field)
	}
	return out
}

// lineAt is the text of the "<path>:<line>" source under dir.
func lineAt(t *testing.T, dir, source string) string {
	t.Helper()
	file, n, ok := strings.Cut(source, ":")
	if !ok {
		t.Fatalf("source %q has no line", source)
	}
	i, _ := strconv.Atoi(n)
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(data), "\n")[i-1]
}

func TestCompleteFieldsAndSources(t *testing.T) {
	rec := complete(t, completeDir, &contract.Contract{})
	want := []string{"capabilities[health]", "configuration.API_TOKEN", "configuration.BILLING_URL", "configuration.CLIENT_SECRET", "configuration.DATABASE_URL",
		"configuration.DB_PASSWORD", "configuration.PORT", "dependencies[billing]", "dependencies[db]", "interfaces[api]", "service.owner", "workload"}
	if got := fieldNames(rec); !slices.Equal(got, want) {
		t.Fatalf("fields = %q, want %q", got, want)
	}
	lines := map[string]string{
		"capabilities[health]":        "readinessProbe:",
		"configuration.API_TOKEN":     "name: API_TOKEN",
		"configuration.BILLING_URL":   "name: BILLING_URL",
		"configuration.CLIENT_SECRET": "CLIENT_SECRET:",
		"configuration.DATABASE_URL":  "name: DATABASE_URL",
		"configuration.DB_PASSWORD":   "name: DB_PASSWORD",
		"configuration.PORT":          "name: PORT",
		"dependencies[billing]":       "name: BILLING_URL",
		"dependencies[db]":            "name: DATABASE_URL",
		"service.owner":               "team: payments",
		"workload":                    "kind: Deployment",
	}
	values := map[string]string{"configuration.DB_PASSWORD": "string, writeOnly", "configuration.PORT": "integer", "dependencies[db]": "db",
		"capabilities[health]": "GET /healthz (readinessProbe)", "service.owner": "payments", "workload": "service (Deployment app)"}
	for _, f := range rec.Fields {
		if f.Confidence != contract.ConfidenceLikely || f.Declare == "" {
			t.Errorf("%s: confidence %q, declare %q", f.Field, f.Confidence, f.Declare)
		}
		if f.Field == "interfaces[api]" {
			if f.Source != "api/openapi.yaml" {
				t.Errorf("interfaces[api] source = %q", f.Source)
			}
			continue
		}
		if got := lineAt(t, completeDir, f.Source); !strings.Contains(got, lines[f.Field]) {
			t.Errorf("%s: source %s reads %q, want it to hold %q", f.Field, f.Source, got, lines[f.Field])
		}
		if want, ok := values[f.Field]; ok && f.Value != want {
			t.Errorf("%s: value = %q, want %q", f.Field, f.Value, want)
		}
	}
}

func TestCompleteNeverTouchesDeclared(t *testing.T) {
	declared := &contract.Contract{
		Workload:       "service",
		Interfaces:     []contract.Interface{{Name: "http", Type: "openapi", Ref: "api/openapi.yaml"}},
		Configurations: []contract.Configuration{{Name: "env", Schema: "configuration/schema.json", Values: map[string]any{"API_TOKEN": "x"}}},
		Dependencies:   []contract.Dependency{{Name: "billing"}, {Name: "database", Ref: "oci://r.io/team/db:1.0.0"}},
		Capabilities:   []contract.Capability{{Type: "metrics"}, {Type: "health"}},
		Service:        contract.Service{Owner: contract.Owner{Team: "payments"}},
	}
	got := fieldNames(complete(t, completeDir, declared))
	want := []string{"configuration.BILLING_URL", "configuration.CLIENT_SECRET", "configuration.DATABASE_URL"}
	if !slices.Equal(got, want) {
		t.Fatalf("fields = %q, want %q", got, want)
	}
}

// TestCompleteUnlistedConfigurationInfersNoKey: a configuration whose keys
// Pacto cannot list may declare any key, so no configuration key is inferred
// rather than one reading as not declared.
func TestCompleteUnlistedConfigurationInfersNoKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(completeDir)); err != nil {
		t.Fatal(err)
	}
	writeTree(t, dir, map[string]string{
		"allof.json":   `{"allOf": [{"properties": {"PORT": {"type": "integer"}}}]}`,
		"broken.json":  `{`,
		"pattern.json": `{"patternProperties": {"^DB_": {"type": "string"}}}`,
	})
	for name, cfg := range map[string]contract.Configuration{
		"ref":        {Name: "base", Ref: "oci://reg/platform/base-config", Values: map[string]any{"PORT": 8080}},
		"unreadable": {Name: "env", Schema: "nope.json"},
		"broken":     {Name: "env", Schema: "broken.json"},
		"allOf":      {Name: "env", Schema: "allof.json"},
		"pattern":    {Name: "env", Schema: "pattern.json"},
	} {
		for _, f := range complete(t, dir, &contract.Contract{Configurations: []contract.Configuration{cfg}}).Fields {
			if strings.HasPrefix(f.Field, "configuration.") {
				t.Errorf("%s: inferred %s", name, f.Field)
			}
		}
	}
}

// TestCompleteKeysAndTypesOnly: the record names keys and types, never a value
// the folder sets, and a dependency read from a credentialed URL names the
// host alone.
func TestCompleteKeysAndTypesOnly(t *testing.T) {
	rec := complete(t, completeDir, nil)
	out := rec.Marshal()
	for _, leak := range []string{"hunter2", "u4l-s3cret", "v4lues-s3cret", "c0nfigmap-s3cret", "default"} {
		if bytes.Contains(out, []byte(leak)) {
			t.Errorf("the record holds %q:\n%s", leak, out)
		}
	}
	for _, f := range rec.Fields {
		if f.Field == "dependencies[db]" && (f.Value != "db" || strings.Contains(f.Declare, "u4l-s3cret") || strings.Contains(f.Declare, "app:")) {
			t.Errorf("dependencies[db] = %+v", f)
		}
	}
}

func TestCompleteInputs(t *testing.T) {
	rec := complete(t, completeDir, nil)
	var paths []string
	for _, s := range rec.Inputs.Sources {
		paths = append(paths, s.Path)
		data, err := os.ReadFile(filepath.Join(completeDir, s.Path))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if s.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
			t.Errorf("%s digest %s", s.Path, s.Digest)
		}
	}
	if !slices.Equal(paths, []string{"api/openapi.yaml", "deploy/app.yaml"}) {
		t.Errorf("sources = %q", paths)
	}
	if rec.Inputs.Discovery != Version || rec.Inputs.Commit != "4e1a9c0" || rec.Inputs.Pacto != "4.6.0" || rec.Format != contract.InferredRecordFormat {
		t.Errorf("inputs = %+v", rec.Inputs)
	}
	// The record verifies against the folder it was completed from.
	if err := contract.VerifyInferredRecord(os.DirFS(completeDir), rec); err != nil {
		t.Error(err)
	}
}

func TestCompleteDeterministic(t *testing.T) {
	a, b := complete(t, completeDir, nil).Marshal(), complete(t, completeDir, nil).Marshal()
	if !bytes.Equal(a, b) {
		t.Errorf("two runs differ:\n%s\n%s", a, b)
	}
	if _, err := contract.ParseInferredRecord(a); err != nil {
		t.Error(err)
	}
}

func TestCompleteNothingInferred(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: x, version: 1.0.0}\n"})
	if rec := complete(t, dir, nil); rec != nil {
		t.Errorf("record = %+v, want nil", rec)
	}
}

// TestCompleteHealthWithoutPath: a probe that calls no HTTP path is a health
// capability bound to nothing.
func TestCompleteHealthWithoutPath(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"k8s/app.yaml": "apiVersion: apps/v1\nkind: Job\nmetadata: {name: x}\nspec:\n  template:\n    spec:\n      containers:\n        - name: x\n          livenessProbe: {tcpSocket: {port: 80}}\n"})
	rec := complete(t, dir, nil)
	if got := fieldNames(rec); !slices.Equal(got, []string{"capabilities[health]", "workload"}) {
		t.Fatalf("fields = %q", got)
	}
	if h := rec.Fields[0]; h.Value != "livenessProbe" || strings.Contains(h.Declare, "binding") {
		t.Errorf("health = %+v", h)
	}
}

func TestCompleteErrors(t *testing.T) {
	// An unreadable .pactoignore stops the scan.
	dir := writeTree(t, t.TempDir(), map[string]string{".pactoignore/x": ""})
	if _, err := Complete(dir, nil, contract.InferredInputs{}); err == nil {
		t.Error("an unreadable .pactoignore was not an error")
	}
	// A cited file that cannot be read has no digest.
	dir = writeTree(t, t.TempDir(), map[string]string{"openapi.yaml": "openapi: 3.1.0\n"})
	if err := os.Chmod(filepath.Join(dir, "openapi.yaml"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "openapi.yaml"), 0o644) })
	if _, err := Complete(dir, nil, contract.InferredInputs{}); err == nil {
		t.Error("an unreadable cited file was not an error")
	}
}

func TestSourceFirstMatch(t *testing.T) {
	sc := &Scan{files: map[string][]byte{"a.yaml": []byte("kind: A\nkey: 1\n---\nkind: B\nother: 2\n")}}
	if got := sc.source("a.yaml", 4, "other:"); got != "a.yaml:5" {
		t.Errorf("second document = %q", got)
	}
	if got := sc.source("a.yaml", 4, "key:"); got != "a.yaml" {
		t.Errorf("a needle only before the document = %q, want the bare file", got)
	}
	if got := sc.source("a.yaml", 1, "missing"); got != "a.yaml" {
		t.Errorf("missing needle = %q", got)
	}
}

// TestManifestDocumentLines: each document starts where its node says, so a
// needle repeated across documents is found in its own one.
func TestManifestDocumentLines(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"k8s/app.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\ndata: {K: one}\n---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: b}\ndata:\n  K: two\n"})
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := sc.source("k8s/app.yaml", sc.ConfigMaps["b"].line, "K:"); got != "k8s/app.yaml:10" {
		t.Errorf("b's key = %q", got)
	}
}

func TestCompleteBundle(t *testing.T) {
	bundleOf := func(dir, version string) *contract.Bundle {
		return &contract.Bundle{Contract: &contract.Contract{PactoVersion: version}, FS: os.DirFS(dir)}
	}
	// Before 2.5 the bundle stays as read, a folder copy included.
	old := writeTree(t, t.TempDir(), map[string]string{"openapi.yaml": "openapi: 3.1.0\n", contract.InferredRecordFile: "stale"})
	b := bundleOf(old, "2.4")
	if err := CompleteBundle(b, old, contract.InferredInputs{}); err != nil {
		t.Fatalf("2.4: %v", err)
	}
	if data, _ := fs.ReadFile(b.FS, contract.InferredRecordFile); string(data) != "stale" {
		t.Fatalf("2.4 record = %q", data)
	}
	// From 2.5 (absent is this CLI's) the record is computed and served.
	b = bundleOf(old, "")
	if err := CompleteBundle(b, old, contract.InferredInputs{Pacto: "4.6.0"}); err != nil {
		t.Fatalf("2.5: %v", err)
	}
	want, _ := Complete(old, b.Contract, contract.InferredInputs{Pacto: "4.6.0"})
	if data, _ := fs.ReadFile(b.FS, contract.InferredRecordFile); want == nil || !bytes.Equal(data, want.Marshal()) {
		t.Fatalf("served %q", data)
	}
	// Nothing inferred hides the folder copy.
	empty := writeTree(t, t.TempDir(), map[string]string{contract.InferredRecordFile: "stale"})
	b = bundleOf(empty, "2.5")
	if err := CompleteBundle(b, empty, contract.InferredInputs{}); err != nil {
		t.Fatalf("nothing inferred: %v", err)
	}
	if _, err := fs.Stat(b.FS, contract.InferredRecordFile); err == nil {
		t.Fatal("the stale copy is still served")
	}
	// A folder Pacto cannot complete is an error, and its copy is hidden too.
	bad := writeTree(t, t.TempDir(), map[string]string{".pactoignore/x": "", contract.InferredRecordFile: "stale"})
	b = bundleOf(bad, "2.5")
	if err := CompleteBundle(b, bad, contract.InferredInputs{}); err == nil {
		t.Fatal("an unreadable folder was not an error")
	}
	if _, err := fs.Stat(b.FS, contract.InferredRecordFile); err == nil {
		t.Fatal("a failed completion still serves the stale copy")
	}
}
