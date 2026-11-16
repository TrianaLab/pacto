package contract

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBundleRaw(t *testing.T) {
	tests := []struct {
		name    string
		bundle  Bundle
		want    string
		wantErr bool
	}{
		{
			name:   "prefers RawYAML",
			bundle: Bundle{RawYAML: []byte("from-raw"), FS: fstest.MapFS{"pacto.yaml": {Data: []byte("from-fs")}}},
			want:   "from-raw",
		},
		{
			name:   "falls back to the filesystem",
			bundle: Bundle{FS: fstest.MapFS{"pacto.yaml": {Data: []byte("from-fs")}}},
			want:   "from-fs",
		},
		{
			name:    "empty RawYAML with no filesystem is an error",
			bundle:  Bundle{},
			wantErr: true,
		},
		{
			name:    "missing document in the filesystem is an error",
			bundle:  Bundle{FS: fstest.MapFS{}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.bundle.Raw()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Raw() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Raw() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Raw() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWalkDocs(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/b.md":      {Data: []byte("b")},
		"docs/a.MD":      {Data: []byte("a")},
		"docs/sub/c.md":  {Data: []byte("c")},
		"docs/notes.txt": {Data: []byte("x")},
		"README.md":      {Data: []byte("r")},
		"docs/sub/d.md":  {Data: []byte("d")},
	}
	var all []string
	WalkDocs(fsys, func(p string) bool { all = append(all, p); return true })
	if strings.Join(all, ",") != "docs/a.MD,docs/b.md,docs/sub/c.md,docs/sub/d.md" {
		t.Errorf("walked %v", all)
	}
	var first []string
	WalkDocs(fsys, func(p string) bool { first = append(first, p); return len(first) < 2 })
	if len(first) != 2 {
		t.Errorf("stop after false: walked %v", first)
	}
	WalkDocs(nil, func(string) bool { t.Error("nil FS has no docs"); return true })
	WalkDocs(fstest.MapFS{}, func(string) bool { t.Error("no docs/ has no docs"); return true })
}

func TestBundleCompleteRecordsImplicitVersion(t *testing.T) {
	b := &Bundle{Contract: &Contract{}, FS: fstest.MapFS{"chart/Chart.yaml": {Data: []byte("apiVersion: v2\nname: payments\n")}}}
	b.Complete("dir")
	want := []Inferred{
		{Field: "pactoVersion", Value: Current(), Source: "this CLI"},
		{Field: "service.name", Value: "payments", Source: "chart/Chart.yaml:2"},
	}
	if !reflect.DeepEqual(b.Inferred, want) || b.Contract.Service.Name != "payments" {
		t.Fatalf("got %+v", b.Inferred)
	}
	if inf, ok := b.NameInferred(); !ok || inf.Value != "payments" {
		t.Fatalf("NameInferred = %+v, %v", inf, ok)
	}
	// A directory with no usable name leaves the name empty.
	b = &Bundle{Contract: &Contract{}, RawYAML: []byte("pactoVersion: \"2.5\"\n")}
	b.Complete("!!")
	if len(b.Inferred) != 0 || b.Contract.Service.Name != "" {
		t.Fatalf("got %+v", b.Inferred)
	}
}

func TestBundleCompleteKeepsDeclaredName(t *testing.T) {
	b := &Bundle{Contract: &Contract{Service: Service{Name: "declared"}}, RawYAML: []byte("pactoVersion: \"2.5\"\nservice: {name: declared}\n")}
	b.Complete("dir")
	if len(b.Inferred) != 0 || b.Contract.Service.Name != "declared" {
		t.Fatalf("got %+v", b.Inferred)
	}
	if _, ok := b.NameInferred(); ok {
		t.Fatal("a declared name is not inferred")
	}
}
