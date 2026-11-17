package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestExplainListsInferred(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	got, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := contract.ParseInferredRecord([]byte(completed(t, dir, "")))
	if len(got.Inferred) == 0 || !reflect.DeepEqual(got.Inferred, rec.Fields) || got.InferredUnreadable {
		t.Fatalf("inferred %+v, want %+v", got.Inferred, rec.Fields)
	}
}

func TestExplainNeverPrintsValues(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	got, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(got)
	for _, l := range leaks {
		if strings.Contains(string(data), l) {
			t.Fatalf("explain JSON holds %q: %s", l, data)
		}
	}
}

// registryBundle is dir as a pulled bundle whose files hold record.
func registryBundle(t *testing.T, dir, record string) *mockBundleStore {
	t.Helper()
	b, err := readLocalBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	if err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(dir + "/" + p)
		files[p] = &fstest.MapFile{Data: data}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	files[contract.InferredRecordFile] = &fstest.MapFile{Data: []byte(record)}
	b.FS = files
	return &mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) { return b, nil }}
}

func TestExplainRegistryBundleRecord(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	record := completed(t, dir, commitA)
	got, err := NewService(registryBundle(t, dir, record), nil).Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/app:1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := contract.ParseInferredRecord([]byte(record))
	if len(got.Inferred) == 0 || !reflect.DeepEqual(got.Inferred, rec.Fields) {
		t.Fatalf("inferred %+v, want %+v", got.Inferred, rec.Fields)
	}
}

func TestExplainUnreadableRecord(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	for name, record := range map[string]string{
		"malformed": "format: other\n",
		"stale":     staleRecord(t, dir),
	} {
		got, err := NewService(registryBundle(t, dir, record), nil).Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/app:1.0.0"})
		if err != nil {
			t.Fatal(err)
		}
		if !got.InferredUnreadable || got.Inferred != nil {
			t.Fatalf("%s: unreadable %v, inferred %+v", name, got.InferredUnreadable, got.Inferred)
		}
	}
	// Below 2.5 a record is ignored, never read.
	old := completionFolder(t, `pactoVersion: "2.4"`+"\n")
	got, err := NewService(registryBundle(t, old, "format: other\n"), nil).Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/app:1.0.0"})
	if err != nil || got.InferredUnreadable || got.Inferred != nil {
		t.Fatalf("2.4: %+v %v", got, err)
	}
}

// staleRecord is dir's record citing a digest no file has.
func staleRecord(t *testing.T, dir string) string {
	t.Helper()
	rec, _ := contract.ParseInferredRecord([]byte(completed(t, dir, "")))
	rec.Inputs.Sources[0].Digest = "sha256:" + strings.Repeat("0", 64)
	return string(rec.Marshal())
}
