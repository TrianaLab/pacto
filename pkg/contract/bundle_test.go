package contract

import (
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
