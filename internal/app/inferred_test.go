package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// leaks are the values the completion fixture holds that no record may.
var leaks = []string{"hunter2", "u4l-s3cret", "v4lues-s3cret", "c0nfigmap-s3cret"}

// completionFolder copies discover's completion fixture into a new folder
// whose pacto.yaml declares header and returns it.
func completionFolder(t *testing.T, header string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "discover", "testdata", "complete")
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"pacto.yaml": header + "service: {name: app, version: 1.0.0}\n"})
	return dir
}

// packed packs dir and returns the archive's files.
func packed(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if _, err := NewService(nil, nil).Pack(context.Background(), PackOptions{Path: dir, Output: out}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for tr := tar.NewReader(gz); ; {
		h, err := tr.Next()
		if err == io.EOF {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		files[h.Name] = string(body)
	}
}

// completed is what discover.Complete writes for dir at commit.
func completed(t *testing.T, dir, commit string) string {
	t.Helper()
	b, err := readLocalBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := discover.Complete(dir, b.Contract, contract.InferredInputs{Commit: commit, Pacto: "dev"})
	if err != nil || rec == nil {
		t.Fatalf("complete: %v %v", rec, err)
	}
	return string(rec.Marshal())
}

func TestPackEmbedsInferredRecord(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	writeGit(t, dir, map[string]string{"HEAD": commitA + "\n"})
	files := packed(t, dir)
	if got, want := files[contract.InferredRecordFile], completed(t, dir, commitA); got != want {
		t.Fatalf("packed record:\n%s\nwant:\n%s", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, contract.InferredRecordFile)); err == nil {
		t.Fatal("pack wrote the record into the folder")
	}
}

func TestPackTwiceSameDigest(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	digest := func() string {
		c, _, fsys, err := loadAndValidateLocal(context.Background(), dir, override.Overrides{}, contract.InferredInputs{Commit: commitA, Pacto: "dev"})
		if err != nil {
			t.Fatal(err)
		}
		d, err := oci.BundleDigest(&contract.Bundle{Contract: c, FS: fsys})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if a, b := digest(), digest(); a != b {
		t.Fatalf("two packs: %s %s", a, b)
	}
}

func TestPackOlderContractHasNoRecord(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.4"`+"\n")
	c, _, fsys, err := loadAndValidateLocal(context.Background(), dir, override.Overrides{}, contract.InferredInputs{Commit: commitA})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := oci.BundleDigest(&contract.Bundle{Contract: c, FS: fsys})
	base, err := readLocalBundle(dir) // what pack bundled before the record
	if err != nil {
		t.Fatal(err)
	}
	want, _ := oci.BundleDigest(base)
	if got != want {
		t.Fatalf("2.4 digest %s, base %s", got, want)
	}
	if _, ok := packed(t, dir)[contract.InferredRecordFile]; ok {
		t.Fatal("a 2.4 bundle holds a record")
	}
}

func TestPackReplacesFolderRecord(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	writeFiles(t, dir, map[string]string{contract.InferredRecordFile: "format: pacto.inferred/v1\ninputs: {pacto: old, discovery: \"0\", sources: []}\nfields: []\n"})
	if got := packed(t, dir)[contract.InferredRecordFile]; got != completed(t, dir, "") {
		t.Fatalf("the folder copy was packed: %s", got)
	}
	empty := writeTree(t, "svc", map[string]string{
		"svc/pacto.yaml":                     "pactoVersion: \"2.5\"\nservice: {name: s, version: 1.0.0}\n",
		"svc/" + contract.InferredRecordFile: "stale",
	})
	if _, ok := packed(t, empty)[contract.InferredRecordFile]; ok {
		t.Fatal("nothing inferred, yet the bundle holds a record")
	}
}

func TestPackedRecordNeverHoldsValues(t *testing.T) {
	rec := packed(t, completionFolder(t, ""))[contract.InferredRecordFile]
	if rec == "" {
		t.Fatal("no record packed")
	}
	for _, leak := range leaks {
		if strings.Contains(rec, leak) {
			t.Errorf("the packed record holds %q", leak)
		}
	}
}

func TestLocalReadCarriesRecord(t *testing.T) {
	dir := completionFolder(t, "")
	writeGit(t, dir, map[string]string{"HEAD": commitA + "\n"})
	named, err := loadLocalBundleNamed(dir, "app", override.Overrides{}, contract.InferredInputs{Pacto: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := NewService(nil, nil).ResolveBundle(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	records := map[string]*contract.InferredRecord{}
	for name, b := range map[string]*contract.Bundle{"loadLocalBundleNamed": named, "resolveBundleAsWritten": resolved} {
		rec, err := contract.ReadInferredRecord(b.FS)
		if rec == nil || err != nil {
			t.Fatalf("%s: record %v, %v", name, rec, err)
		}
		records[name] = rec
	}
	if records["resolveBundleAsWritten"].Inputs.Commit != commitA || records["loadLocalBundleNamed"].Inputs.Commit != "" {
		t.Errorf("commits: %+v", records)
	}
}

// TestIncompleteFolder: pack, push and publish refuse a folder Pacto cannot
// complete; a read keeps its declared bundle, with no record and a warning.
func TestIncompleteFolder(t *testing.T) {
	dir := completionFolder(t, "")
	if err := os.Chmod(filepath.Join(dir, "api"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "api"), 0o755) })
	// Pack, push and publish refuse a folder Pacto cannot complete.
	if _, err := loadLocalBundleNamed(dir, "app", override.Overrides{}, contract.InferredInputs{}); err == nil || !strings.Contains(err.Error(), "completing") {
		t.Errorf("an unreadable cited file: %v", err)
	}
	if _, err := NewService(nil, nil).Pack(context.Background(), PackOptions{Path: dir, Output: filepath.Join(t.TempDir(), "b.tar.gz")}); err == nil || !strings.Contains(err.Error(), "completing") {
		t.Errorf("pack of an incomplete folder: %v", err)
	}
	// A read keeps the declared bundle, with no record and a warning.
	var logs bytes.Buffer
	ctx := logging.WithLogger(context.Background(), logging.New(&logs, false))
	b, err := NewService(nil, nil).ResolveBundle(ctx, dir)
	if err != nil || b.Contract.Service.Name != "app" || !strings.Contains(logs.String(), "its inferred values are unknown, not absent") {
		t.Fatalf("read of an incomplete folder: %v %q", err, logs.String())
	}
	if _, err := fs.Stat(b.FS, contract.InferredRecordFile); err == nil {
		t.Error("an incomplete read serves a record")
	}
	if b, err := loadLocalBundle(dir); err != nil || b == nil {
		t.Errorf("a local dependency Pacto cannot complete: %v", err)
	}
}

// TestExplainLocalCarriesRecord: explain reads a local folder through the
// completing read and sees what it sees; a folder it cannot complete still
// explains its declared contract.
func TestExplainLocalCarriesRecord(t *testing.T) {
	dir := completionFolder(t, "")
	res, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil || len(res.Inferred) == 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if err := os.Chmod(filepath.Join(dir, "api"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "api"), 0o755) })
	if res, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir}); err != nil || res.Name != "app" {
		t.Fatalf("explain of an incomplete folder: %v %+v", err, res)
	}
}

func TestLockIgnoresInferredRecord(t *testing.T) {
	repo := t.TempDir()
	dep := completionFolder(t, "")
	if err := os.Rename(dep, filepath.Join(repo, "app")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, repo, map[string]string{"root/pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: root, version: 1.0.0}\n" +
		"dependencies:\n  - {name: app, ref: ../app, compatibility: ^1.0.0}\n"})
	root := filepath.Join(repo, "root")
	s := NewService(nil, nil)
	writeGit(t, repo, map[string]string{"HEAD": commitA + "\n"})
	if _, err := s.Lock(context.Background(), LockOptions{Path: root}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(root, "pacto.lock"))
	// The entry pins the folder as written, record or not.
	l, _ := lock.Parse(first)
	folder, _ := lock.HashFS(os.DirFS(filepath.Join(repo, "app")))
	if e, ok := l.Dependency("app"); !ok || e.ContentHash != folder {
		t.Fatalf("app entry %+v, folder hash %s", e, folder)
	}
	writeGit(t, repo, map[string]string{"HEAD": commitB + "\n"})
	if res, err := s.Lock(context.Background(), LockOptions{Path: root, Check: true}); err != nil || !res.UpToDate {
		t.Fatalf("lock --check at another commit: %v %+v", err, res)
	}
	if _, err := s.Lock(context.Background(), LockOptions{Path: root, Update: true}); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(filepath.Join(root, "pacto.lock")); !bytes.Equal(first, second) {
		t.Fatalf("the lock moved with the commit:\n%s\n%s", first, second)
	}
}

func TestPublishRecordCommitMatchesRevision(t *testing.T) {
	const revision = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	for name, opts := range map[string]PublishOptions{"--revision": {Revision: revision}, "git": {}} {
		t.Run(name, func(t *testing.T) {
			host, reg := publishRegistry(t)
			dir := completionFolder(t, "")
			writeFiles(t, dir, map[string]string{"pacto.yaml": "pactoVersion: \"2.5\"\nservice: {name: app, version: 1.0.0}\n" +
				"publish:\n  bundle: oci://" + host + "/pacto/app\n  sign: {key: cosign.key, transparencyLog: false}\n"})
			signingKey(t, dir)
			writeGit(t, dir, map[string]string{"HEAD": commitA + "\n"})
			opts.Path = dir
			res, err := publishService(&fakeRun{}, reg).Publish(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			b, err := reg.Pull(context.Background(), host+"/pacto/app@"+res.Digests[publish.BundleItem])
			if err != nil {
				t.Fatal(err)
			}
			rec, err := contract.ReadInferredRecord(b.FS)
			if err != nil || rec == nil || rec.Inputs.Commit != res.Plan.Revision || res.Plan.Revision != map[string]string{"--revision": revision, "git": commitA}[name] {
				t.Fatalf("record %+v (%v), revision %s", rec, err, res.Plan.Revision)
			}
		})
	}
}

// TestPushFromAnotherCommitIsUnchanged: pushing the same content from a new
// commit changes only the record's inputs, so push stays a no-op success.
func TestPushFromAnotherCommitIsUnchanged(t *testing.T) {
	dir := completionFolder(t, `pactoVersion: "2.5"`+"\n")
	writeGit(t, dir, map[string]string{"HEAD": commitA + "\n"})
	var first *contract.Bundle
	svc := NewService(&mockBundleStore{
		ResolveFn: func(context.Context, string) (string, error) { return "", &oci.ArtifactNotFoundError{} },
		PushFn: func(_ context.Context, _ string, b *contract.Bundle) (string, error) {
			first = b
			return oci.BundleDigest(b)
		},
	}, nil)
	pushed, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/app", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	writeGit(t, dir, map[string]string{"HEAD": commitB + "\n"})
	var pulled string
	svc.BundleStore = &mockBundleStore{
		ResolveFn: func(context.Context, string) (string, error) { return pushed.Digest, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			pulled = ref
			return first, nil
		},
		PushFn: func(context.Context, string, *contract.Bundle) (string, error) {
			t.Fatal("an inferred-only change writes nothing")
			return "", nil
		},
	}
	res, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/app", Path: dir})
	if err != nil || !res.Unchanged || res.Digest != pushed.Digest || pulled != "ghcr.io/acme/app@"+pushed.Digest {
		t.Fatalf("%+v %v pulled %q", res, err, pulled)
	}
}

func TestRefRepo(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/acme/app:1.0.0":        "ghcr.io/acme/app",
		"ghcr.io/acme/app@sha256:ab":    "ghcr.io/acme/app",
		"localhost:5000/acme/app":       "localhost:5000/acme/app",
		"localhost:5000/acme/app:1.0.0": "localhost:5000/acme/app",
	} {
		if got := refRepo(ref); got != want {
			t.Errorf("refRepo(%q) = %q, want %q", ref, got, want)
		}
	}
}
