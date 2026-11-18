package fleetsrc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// writeBundle writes a minimal parseable pacto.yaml for service name at dir.
func writeBundle(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSource_IDAndKind(t *testing.T) {
	if got := NewLocalSource("", "/x").ID(); got != "local" {
		t.Errorf("default id = %q, want local", got)
	}
	if got := NewLocalSource("custom", "/x").ID(); got != "custom" {
		t.Errorf("custom id = %q, want custom", got)
	}
	if got := NewLocalSource("", "/x").Kind(); got != "local" {
		t.Errorf("kind = %q, want local", got)
	}
}

func TestLocalSource_Collect(t *testing.T) {
	root := t.TempDir()

	// Two valid bundles: one at a top-level subdir, one nested deeper.
	writeBundle(t, filepath.Join(root, "svc-a"), "svc-a")
	writeBundle(t, filepath.Join(root, "nested", "svc-b"), "svc-b")

	// Skipped: hidden dir, node_modules, vendor.
	writeBundle(t, filepath.Join(root, ".git", "svc-x"), "svc-x")
	writeBundle(t, filepath.Join(root, "node_modules", "svc-y"), "svc-y")
	writeBundle(t, filepath.Join(root, "vendor", "svc-v"), "svc-v")

	// Skipped: deeper than maxScanDepth (rel depth >= 8).
	deep := filepath.Join(root, "l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9")
	writeBundle(t, deep, "svc-deep")

	// Skipped: pacto.yaml that fails contract.Parse (unsupported version).
	brokenDir := filepath.Join(root, "broken")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "pacto.yaml"),
		[]byte("pactoVersion: \"9.9\"\nservice:\n  name: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A non-pacto file at the root exercises the name filter.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	col, err := NewLocalSource("", root).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	names := map[string]bool{}
	for _, rev := range col.Revisions {
		if rev.Bundle == nil || rev.Bundle.Contract == nil {
			t.Fatal("revision missing bundle/contract")
		}
		names[rev.Bundle.Contract.Service.Name] = true
		if rev.Digest == "" {
			t.Errorf("revision %s: empty Digest", rev.Bundle.Contract.Service.Name)
		}
		if rev.FetchedAt == nil {
			t.Errorf("revision %s: nil FetchedAt", rev.Bundle.Contract.Service.Name)
		}
	}
	if len(col.Revisions) != 2 || !names["svc-a"] || !names["svc-b"] {
		t.Errorf("got revisions %v, want exactly svc-a and svc-b", names)
	}
	// The broken bundle is not silently dropped: it surfaces as a record-level
	// limitation so the source is reported partial instead of complete.
	if !hasLimitation(col.Limitations, fleet.LimitationSourceRecordInvalid) {
		t.Errorf("broken bundle should add a SOURCE_RECORD_INVALID limitation: %+v", col.Limitations)
	}
}

func hasLimitation(ls []fleet.Limitation, code string) bool {
	for _, l := range ls {
		if l.Code == code {
			return true
		}
	}
	return false
}

// TestLocalSourceKeepsScanningPastAnUnreadableDir is the whole reason
// [unreadableDirs] exists: a scan rooted at a home directory meets a directory
// the operating system will not open long before it meets the bundles, and
// aborting there answered "no services" for a machine full of them.
func TestLocalSourceKeepsScanningPastAnUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a refused directory is not reproducible as root")
	}
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "svc-a"), "svc-a")
	// Sorts before svc-a, so the walk meets it first and any abort would take
	// the readable bundle with it.
	blocked := filepath.Join(root, "locked")
	writeBundle(t, filepath.Join(blocked, "svc-hidden"), "svc-hidden")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	col, err := NewLocalSource("", root).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %d, want the one readable bundle", len(col.Revisions))
	}
	if len(col.Limitations) != 1 {
		t.Fatalf("limitations = %+v, want exactly one", col.Limitations)
	}
	lim := col.Limitations[0]
	if lim.Code != fleet.LimitationSourcePartial || lim.Source != "local" {
		t.Errorf("limitation = %+v, want SOURCE_PARTIAL from local", lim)
	}
	// The gap has to name the directory it is about, and name it relative to the
	// root rather than echoing the caller's absolute path back at them.
	if !strings.Contains(lim.Message, "locked") || strings.Contains(lim.Message, root) {
		t.Errorf("message %q should name locked relative to the root", lim.Message)
	}
}

// TestLocalSourceSummarisesManyUnreadableDirs: a home directory refuses around a
// hundred privacy-guarded paths, and a limitation each would bury the gaps a
// reader can act on.
func TestLocalSourceSummarisesManyUnreadableDirs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a refused directory is not reproducible as root")
	}
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "zz-svc"), "zz-svc")
	const blocked = maxUnreadableNotes + 2
	for i := range blocked {
		dir := filepath.Join(root, fmt.Sprintf("locked-%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	}

	col, err := NewLocalSource("", root).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %d, want the one readable bundle", len(col.Revisions))
	}
	if len(col.Limitations) != maxUnreadableNotes+1 {
		t.Fatalf("limitations = %d, want %d named plus one summary", len(col.Limitations), maxUnreadableNotes+1)
	}
	last := col.Limitations[len(col.Limitations)-1].Message
	if !strings.Contains(last, fmt.Sprintf("%d directories could not be read", blocked)) {
		t.Errorf("summary = %q, want the total %d", last, blocked)
	}
}

func TestLocalSource_MissingRoot(t *testing.T) {
	_, err := NewLocalSource("", filepath.Join(t.TempDir(), "does-not-exist")).Collect(context.Background())
	if err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestLocalSource_ContextCancelled(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "svc-a"), "svc-a")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewLocalSource("", root).Collect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// fakeDirEntry is a synthetic fs.DirEntry for exercising skipDir directly.
type fakeDirEntry struct{ name string }

func (f fakeDirEntry) Name() string             { return f.name }
func (fakeDirEntry) IsDir() bool                { return true }
func (fakeDirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

func TestSkipDir(t *testing.T) {
	sep := string(filepath.Separator)
	deep := "a" + sep + "b" + sep + "c" + sep + "d" + sep + "e" + sep + "f" + sep + "g" + sep + "h" + sep + "i"
	cases := []struct {
		name string
		root string
		p    string
		dir  string
		want bool
	}{
		{"root itself", "/r", "/r", "r", false},
		{"hidden", "/r", "/r/.git", ".git", true},
		{"node_modules", "/r", "/r/node_modules", "node_modules", true},
		{"vendor", "/r", "/r/vendor", "vendor", true},
		{"rel error (abs root, rel path)", "/r", "relative/child", "child", true},
		{"too deep", "/r", "/r/" + deep, "i", true},
		{"shallow ok", "/r", "/r/sub", "sub", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipDir(tc.root, tc.p, fakeDirEntry{name: tc.dir}); got != tc.want {
				t.Errorf("skipDir(%q,%q,%q) = %v, want %v", tc.root, tc.p, tc.dir, got, tc.want)
			}
		})
	}
}

func TestLoadRevision_ReadError(t *testing.T) {
	// A directory with no pacto.yaml → ReadFile fails → error.
	if _, err := loadRevision(t.TempDir(), nil); err == nil {
		t.Error("expected an error when pacto.yaml is absent")
	}
}

func TestLoadRevision_ParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("::: not yaml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRevision(dir, nil); err == nil {
		t.Error("expected an error for an unparseable contract")
	}
}

func TestLoadRevision_IgnoreFileError(t *testing.T) {
	dir := t.TempDir()
	writeBundle(t, dir, "svc")
	// A directory where the ignore file should be: it exists, so Load does not
	// take the not-exist path, and reading it fails. A permission bit would not
	// do -- CI runs as root, where every mode is readable.
	if err := os.Mkdir(filepath.Join(dir, ".pactoignore"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := loadRevision(dir, nil)
	if err == nil || !strings.Contains(err.Error(), ".pactoignore") {
		t.Fatalf("err = %v, want one naming .pactoignore", err)
	}
}

// TestLoadRevision_DigestIgnoresUnpackagedFiles is the reason the FS is
// ignore-filtered. A content digest claims two bundles ARE the same bundle, and
// the lockfile, the catalog and a pushed artifact all hash the packaged file
// set. Hashing the raw directory instead would make one developer's .DS_Store
// into a second revision of one service at one version -- a content conflict
// reported against a fleet where nothing changed.
func TestLoadRevision_DigestIgnoresUnpackagedFiles(t *testing.T) {
	clean, littered := t.TempDir(), t.TempDir()
	writeBundle(t, clean, "svc")
	writeBundle(t, littered, "svc")
	if err := os.WriteFile(filepath.Join(littered, ".DS_Store"), []byte("finder"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := loadRevision(clean, nil)
	if err != nil {
		t.Fatalf("loadRevision(clean, nil): %v", err)
	}
	b, err := loadRevision(littered, nil)
	if err != nil {
		t.Fatalf("loadRevision(littered, nil): %v", err)
	}
	if a.Digest == "" || a.Digest != b.Digest {
		t.Errorf("digests %q and %q differ; an unpackaged file changed a bundle's identity", a.Digest, b.Digest)
	}
}

func TestLocalSourceInfersName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Pay API")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	col, err := NewLocalSource("", root).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %+v", col.Revisions)
	}
	b := col.Revisions[0].Bundle
	if inf, ok := b.NameInferred(); !ok || b.Contract.Service.Name != "pay-api" || inf.Source != "directory Pay API" {
		t.Fatalf("got %+v %+v", b.Contract.Service, b.Inferred)
	}
}

// A root of "." (the --local default) has no base name of its own; the name
// still comes from the folder, as `pacto validate .` infers it.
func TestLocalSourceInfersNameAtDotRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "payments-api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	col, err := NewLocalSource("", ".").Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Revisions) != 1 {
		t.Fatalf("revisions = %+v, limitations = %+v", col.Revisions, col.Limitations)
	}
	b := col.Revisions[0].Bundle
	if inf, ok := b.NameInferred(); !ok || b.Contract.Service.Name != "payments-api" || inf.Source != "directory payments-api" {
		t.Fatalf("got %+v %+v", b.Contract.Service, b.Inferred)
	}
}

// A contract that stays nameless after inference is a record gap, never a
// service keyed by "".
func TestLoadRevision_NamelessIsAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "---") // sanitizes to nothing
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRevision(dir, nil); err == nil || !strings.Contains(err.Error(), "service.name") {
		t.Fatalf("err = %v", err)
	}
}

// TestLocalReadCarriesRecord: a local revision carries its inferred record,
// and its digest is the folder's without any record, a folder copy included.
func TestLocalReadCarriesRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "discover", "testdata", "complete"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("service: {name: app, version: 1.0.0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	folder, err := lock.HashFS(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	rev, err := loadRevision(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(rev.Bundle.FS)
	if err != nil || rec == nil || rec.Inputs.Commit != "" || rev.Digest != folder {
		t.Fatalf("record %+v %v digest %s, folder %s", rec, err, rev.Digest, folder)
	}
	// A record copy in the folder never moves its identity.
	if err := os.WriteFile(filepath.Join(dir, contract.InferredRecordFile), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if withCopy, err := loadRevision(dir, nil); err != nil || withCopy.Digest != folder {
		t.Fatalf("with a folder copy: %v digest %s, folder %s", err, withCopy.Digest, folder)
	}
}

// TestLocalIncompleteKeepsRevision: a folder Pacto cannot complete keeps its
// declared revision, with no record and a limitation that makes it partial.
func TestLocalIncompleteKeepsRevision(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "app")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "discover", "testdata", "complete"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("service: {name: app, version: 1.0.0}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "api"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "api"), 0o755) })
	rev, err := loadRevision(dir, nil)
	if !errors.Is(err, errIncomplete) || rev.Bundle == nil {
		t.Fatalf("an unreadable cited file: %v %+v", err, rev)
	}
	if _, serr := fs.Stat(rev.Bundle.FS, contract.InferredRecordFile); serr == nil {
		t.Fatal("an incomplete revision serves a record")
	}
	col, err := NewLocalSource("local", root).Collect(context.Background())
	// The unreadable folder also leaves the source walk partial.
	if err != nil || len(col.Revisions) != 1 || !slices.ContainsFunc(col.Limitations, func(l fleet.Limitation) bool {
		return l.Code == fleet.LimitationInferredRecordUnreadable && strings.Contains(l.Message, "app/pacto.yaml: could not be completed")
	}) {
		t.Fatalf("collect: %v %+v", err, col)
	}
}
