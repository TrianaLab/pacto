package contract

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func overlayBase() fstest.MapFS {
	return fstest.MapFS{
		"pacto.yaml":       {Data: []byte("service: {name: x}\n")},
		InferredRecordFile: {Data: []byte("stale")},
		"api/openapi.yaml": {Data: []byte("openapi: 3.0.0\n")},
	}
}

func TestInferredOverlay(t *testing.T) {
	base := overlayBase()
	fresh := []byte("format: pacto.inferred/v1\n")
	with := WithInferredRecord(base, fresh)
	if data, err := fs.ReadFile(with, InferredRecordFile); err != nil || string(data) != string(fresh) {
		t.Fatalf("served %q, %v", data, err)
	}
	if data, _ := fs.ReadFile(with, "api/openapi.yaml"); string(data) != "openapi: 3.0.0\n" {
		t.Fatalf("other files: %q", data)
	}
	entries, err := fs.ReadDir(with, ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, " "); got != "api pacto.inferred.yaml pacto.yaml" {
		t.Fatalf("ReadDir(.) = %s", got)
	}
	if got := walked(t, with); got != "api api/openapi.yaml pacto.inferred.yaml pacto.yaml" {
		t.Fatalf("WalkDir = %s", got)
	}
	if info, err := fs.Stat(with, InferredRecordFile); err != nil || info.Size() != int64(len(fresh)) {
		t.Fatalf("Stat = %v, %v", info, err)
	}
	if info, err := fs.Stat(with, "pacto.yaml"); err != nil || info.IsDir() {
		t.Fatalf("Stat(pacto.yaml) = %v, %v", info, err)
	}
	if sub, err := fs.ReadDir(with, "api"); err != nil || len(sub) != 1 {
		t.Fatalf("ReadDir(api) = %v, %v", sub, err)
	}
	if _, err := fs.ReadDir(with, "missing"); err == nil {
		t.Fatal("ReadDir of a missing directory must fail")
	}
}

func TestInferredOverlayHides(t *testing.T) {
	without := WithoutInferredRecord(overlayBase())
	if _, err := without.Open(InferredRecordFile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Open = %v", err)
	}
	if _, err := fs.Stat(without, InferredRecordFile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat = %v", err)
	}
	if got := walked(t, without); got != "api api/openapi.yaml pacto.yaml" {
		t.Fatalf("WalkDir = %s", got)
	}
}

func walked(t *testing.T, fsys fs.FS) string {
	t.Helper()
	var seen []string
	if err := fs.WalkDir(fsys, ".", func(p string, _ fs.DirEntry, err error) error {
		if p != "." {
			seen = append(seen, p)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return strings.Join(seen, " ")
}

func TestInferredUnchangedWhat(t *testing.T) {
	// bundle holds the files sampleRecord cites, so its records verify.
	bundle := func(pactoYAML string, rec *InferredRecord) *Bundle {
		fsys := fstest.MapFS{
			"pacto.yaml":      {Data: []byte(pactoYAML)},
			"openapi/v1.yaml": {Data: []byte("openapi: 3.0.0\n")},
			"deploy/app.yaml": {Data: []byte("kind: Deployment\n")},
		}
		if rec != nil {
			fsys[InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
		}
		return &Bundle{Contract: &Contract{}, FS: fsys}
	}
	digest := func(b *Bundle) (string, error) {
		data, err := fs.ReadFile(b.FS, "pacto.yaml")
		if _, serr := fs.Stat(b.FS, InferredRecordFile); serr == nil {
			t.Fatal("the digest saw the record")
		}
		return string(data), err
	}
	rec := func(discovery, pacto, commit string) *InferredRecord {
		r := sampleRecord()
		r.Inputs.Discovery, r.Inputs.Pacto, r.Inputs.Commit = discovery, pacto, commit
		return r
	}
	moreSources := rec("1", "4.6.0", "")
	moreSources.Inputs.Sources = append(moreSources.Inputs.Sources, InferredSourceFile{Path: "pacto.yaml", Digest: digestOf("x")})
	forged := rec("1", "4.6.0", "")
	forged.Inputs.Sources[0].Digest = digestOf("not the file")
	cases := []struct {
		name      string
		prev      *Bundle
		next      *Bundle
		unchanged bool
		what      string
	}{
		{"inputs differ", bundle("x", rec("1", "4.5.0", "4e1a9c0d0000")), bundle("x", rec("2", "4.6.0", "77c0b2e1111")), true,
			"discovery 1 -> 2, pacto 4.5.0 -> 4.6.0, commit 4e1a9c0 -> 77c0b2e"},
		{"no commit before", bundle("x", rec("1", "4.6.0", "")), bundle("x", rec("1", "4.6.0", "abc")), true, "commit none -> abc"},
		{"only fields", bundle("x", rec("1", "4.6.0", "")), bundle("x", func() *InferredRecord {
			r := rec("1", "4.6.0", "")
			r.Fields = r.Fields[:1]
			return r
		}()), true, "fields"},
		{"cited set differs", bundle("x", rec("1", "4.6.0", "")), bundle("x", moreSources), false, "sources"},
		{"no record before", bundle("x", nil), bundle("x", rec("1", "4.6.0", "")), false, "discovery none -> 1, pacto none -> 4.6.0, sources"},
		{"declared differs", bundle("x", rec("1", "4.6.0", "")), bundle("y", rec("1", "4.6.0", "")), false, ""},
		{"unreadable record", bundle("x", rec("1", "4.6.0", "")), &Bundle{Contract: &Contract{}, FS: fstest.MapFS{"pacto.yaml": {Data: []byte("x")}, InferredRecordFile: {Data: []byte("format: other\n")}}}, false, ""},
		// A record whose digests do not match its own bundle is stale or
		// forged: never a record-only change, on either side.
		{"forged record before", bundle("x", forged), bundle("x", rec("1", "4.6.0", "")), false, ""},
		{"forged record after", bundle("x", rec("1", "4.6.0", "")), bundle("x", forged), false, ""},
	}
	for _, tc := range cases {
		unchanged, what := InferredUnchanged(tc.prev, tc.next, digest)
		if unchanged != tc.unchanged || what != tc.what {
			t.Errorf("%s: got %v %q, want %v %q", tc.name, unchanged, what, tc.unchanged, tc.what)
		}
	}
}
