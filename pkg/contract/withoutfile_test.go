package contract

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestWithoutFileHidesOneName(t *testing.T) {
	base := fstest.MapFS{"pacto.yaml": {Data: []byte("a")}, "openapi.yaml": {Data: []byte("b")}, "sub/pacto.yaml": {Data: []byte("c")}}
	fsys := WithoutFile(base, "pacto.yaml")
	if _, err := fs.Stat(fsys, "pacto.yaml"); err == nil {
		t.Fatal("pacto.yaml is still served")
	}
	if _, err := fs.ReadFile(fsys, "pacto.yaml"); err == nil {
		t.Fatal("pacto.yaml still opens")
	}
	for _, keep := range []string{"openapi.yaml", "sub/pacto.yaml"} {
		if _, err := fs.ReadFile(fsys, keep); err != nil {
			t.Fatalf("%s hidden: %v", keep, err)
		}
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil || len(entries) != 2 || entries[0].Name() != "openapi.yaml" || entries[1].Name() != "sub" {
		t.Fatalf("root entries = %v, %v", entries, err)
	}
	if _, err := fs.ReadFile(WithoutInferredRecord(fstest.MapFS{InferredRecordFile: {Data: []byte("r")}}), InferredRecordFile); err == nil {
		t.Fatal("WithoutInferredRecord still serves the record")
	}
}
