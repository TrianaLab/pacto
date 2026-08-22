package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Counterexamples for the revision-content immutability invariant.
//
// The tuple (SnapshotID, RevisionKey, document path) is an IDENTITY. It must
// name one set of bytes for as long as the snapshot exists. A bundle filesystem
// is not immutable storage — a local source's FS is an os.DirFS over a working
// directory an author is editing right now — so a read that merely re-opened the
// path would hand back today's draft under yesterday's revision identity.
//
// Every test below mutates the backing storage AFTER Build and asserts one of the
// only two honest outcomes: the revision's own content, or an explicit
// unavailability. Never new bytes under the old identity.

// tempDirBundleFS writes files into a real temp directory and returns an
// os.DirFS over it plus the directory path, so a test can edit the files on disk
// exactly as an author would while the dashboard is open.
func tempDirBundleFS(t *testing.T, files map[string]string) (string, *contract.Bundle) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, &contract.Bundle{Contract: docContract(), FS: os.DirFS(dir)}
}

// docContract is the minimal contract every immutability fixture shares. The
// contract body is IDENTICAL across fixtures on purpose: the revision key is
// content-addressed off the contract, not off the docs, which is exactly why a
// document body can drift out from under a stable key.
func docContract() *contract.Contract {
	return &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "payments", Version: "1.0.0", Owner: contract.Owner{Team: "platform"}},
	}
}

// buildOneRevision builds a snapshot from a single bundle and returns it with the
// key of its only revision.
func buildOneRevision(t *testing.T, b *contract.Bundle) (*FleetSnapshot, string) {
	t.Helper()
	col := &Collection{Revisions: []RawRevision{{Bundle: b, Domain: "domain-a", Digest: "sha256:pay"}}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	svc := snap.Services[NewServiceKeyDomain("domain-a", "payments")]
	if svc == nil || len(svc.Revisions) != 1 {
		t.Fatalf("fixture: expected one revision, got %+v", svc)
	}
	return snap, string(svc.Revisions[0])
}

// wantContent asserts the document reads back with exactly the expected body.
func wantContent(t *testing.T, q *Query, key, path, want string) {
	t.Helper()
	doc, err := q.RevisionDocument(key, path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if doc.Document.Content != want {
		t.Fatalf("%s: content = %q, want %q", path, doc.Document.Content, want)
	}
}

// wantUnavailable asserts the read failed explicitly, and returns the reason so a
// caller can check it names the right cause.
func wantUnavailable(t *testing.T, q *Query, key, path string) string {
	t.Helper()
	doc, err := q.RevisionDocument(key, path)
	if doc != nil {
		t.Fatalf("%s: served %d bytes (%q) under a revision identity whose content changed", path, doc.Document.Bytes, doc.Document.Content)
	}
	var du *DocumentUnavailableError
	if !errors.As(err, &du) {
		t.Fatalf("%s: want DocumentUnavailableError, got %v", path, err)
	}
	return du.Reason
}

// Counterexample 1: an in-memory bundle filesystem mutated after Build.
func TestRevisionDocument_InMemoryBodyChangedAfterBuildIsNotServed(t *testing.T) {
	fsys := fstest.MapFS{"docs/overview.md": {Data: []byte("A")}}
	snap, key := buildOneRevision(t, &contract.Bundle{Contract: docContract(), FS: fsys})
	q := NewQuery(snap)
	wantContent(t, q, key, "docs/overview.md", "A")

	fsys["docs/overview.md"] = &fstest.MapFile{Data: []byte("B")}

	reason := wantUnavailable(t, q, key, "docs/overview.md")
	if !strings.Contains(reason, "changed") {
		t.Errorf("the reason must say the content changed, got %q", reason)
	}
}

// Counterexample 2: the local-source shape. A real temp directory behind an
// os.DirFS, edited on disk while the snapshot is live -- exactly what happens when
// an author saves docs/overview.md with the dashboard open.
func TestRevisionDocument_LocalDirBodyChangedAfterBuildIsNotServed(t *testing.T) {
	dir, bundle := tempDirBundleFS(t, map[string]string{"docs/overview.md": "# A\n"})
	snap, key := buildOneRevision(t, bundle)
	q := NewQuery(snap)
	wantContent(t, q, key, "docs/overview.md", "# A\n")
	before := snap.SnapshotID

	if err := os.WriteFile(filepath.Join(dir, "docs", "overview.md"), []byte("# B\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wantUnavailable(t, q, key, "docs/overview.md")
	if snap.SnapshotID != before {
		t.Errorf("the snapshot id must not move when backing storage does: %q -> %q", before, snap.SnapshotID)
	}
}

// Counterexample 3: the file is deleted after Build. Unavailable, never empty.
func TestRevisionDocument_DeletedAfterBuildIsExplicitlyUnavailable(t *testing.T) {
	dir, bundle := tempDirBundleFS(t, map[string]string{"docs/overview.md": "# A\n"})
	snap, key := buildOneRevision(t, bundle)
	q := NewQuery(snap)
	wantContent(t, q, key, "docs/overview.md", "# A\n")

	if err := os.Remove(filepath.Join(dir, "docs", "overview.md")); err != nil {
		t.Fatal(err)
	}

	wantUnavailable(t, q, key, "docs/overview.md")
	// The document is still LISTED: the revision did publish it. Absence of bytes
	// is not absence of the reference.
	if _, err := q.RevisionDocument(key, "docs/overview.md"); errors.Is(err, errors.ErrUnsupported) {
		t.Fatal("unreachable")
	}
	rev := snap.Revisions[RevisionKey(key)]
	if len(rev.Docs) != 1 || rev.Docs[0].Path != "docs/overview.md" {
		t.Errorf("the published doc list must be unchanged, got %+v", rev.Docs)
	}
}

// Counterexample 4: the file is replaced by ANOTHER perfectly valid UTF-8
// Markdown document. Nothing about the new bytes is wrong -- they are just not
// this revision's.
func TestRevisionDocument_ReplacedByAnotherValidDocumentIsNotServed(t *testing.T) {
	dir, bundle := tempDirBundleFS(t, map[string]string{"docs/overview.md": "# Payments overview\n"})
	snap, key := buildOneRevision(t, bundle)
	q := NewQuery(snap)

	if err := os.WriteFile(filepath.Join(dir, "docs", "overview.md"), []byte("# Billing overview\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	reason := wantUnavailable(t, q, key, "docs/overview.md")
	if strings.Contains(reason, "UTF-8") {
		t.Errorf("the replacement is valid text; the reason must be identity, not encoding: %q", reason)
	}
}

// Counterexample 5: the SAME RevisionKey, asked repeatedly across a mutation,
// must never yield two different bodies. This is the invariant itself, stated
// directly.
func TestRevisionDocument_SameRevisionKeyNeverReturnsChangedBytes(t *testing.T) {
	dir, bundle := tempDirBundleFS(t, map[string]string{"docs/overview.md": "one\n"})
	snap, key := buildOneRevision(t, bundle)
	q := NewQuery(snap)

	first, err := q.RevisionDocument(key, "docs/overview.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"two\n", "three\n", "one\n"} {
		if err := os.WriteFile(filepath.Join(dir, "docs", "overview.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		doc, err := q.RevisionDocument(key, "docs/overview.md")
		switch {
		case err != nil:
			continue // an honest refusal is always acceptable
		case doc.Document.Content != first.Document.Content:
			t.Fatalf("the same revision key served %q and then %q", first.Document.Content, doc.Document.Content)
		}
	}
	// Restoring the original bytes restores the original document: the identity is
	// the CONTENT, not the moment of the read.
	wantContent(t, q, key, "docs/overview.md", "one\n")
}

// docPermutationSources returns two sources contributing the SAME revision with
// the SAME contract, each with its own bundle filesystem holding `aBody`/`bBody`
// at docs/overview.md.
func docPermutationSources(aBody, bBody string) (Source, Source) {
	mk := func(id, body string) Source {
		return NewMemorySource(id, "local", &Collection{Revisions: []RawRevision{{
			Bundle: &contract.Bundle{Contract: docContract(), FS: fstest.MapFS{
				"docs/overview.md": {Data: []byte(body)},
			}},
			Domain: "domain-a", Digest: "sha256:pay",
		}}})
	}
	return mk("src-a", aBody), mk("src-b", bBody)
}

// Counterexample 6: two sources that AGREE. Whichever order they are collected
// in, the document read is identical -- source ordering is not allowed to select
// a hidden bundle handle whose bytes differ.
func TestRevisionDocument_SourceOrderCannotChangeTheAnswer(t *testing.T) {
	a, b := docPermutationSources("# Same\n", "# Same\n")
	var got []string
	for _, order := range [][]Source{{a, b}, {b, a}} {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, order...)
		if err != nil {
			t.Fatal(err)
		}
		svc := snap.Services[NewServiceKeyDomain("domain-a", "payments")]
		doc, err := NewQuery(snap).RevisionDocument(string(svc.Revisions[0]), "docs/overview.md")
		if err != nil {
			t.Fatalf("%s first: %v", order[0].ID(), err)
		}
		got = append(got, doc.Document.Content)
	}
	if got[0] != got[1] {
		t.Fatalf("source order changed the document: %q vs %q", got[0], got[1])
	}
}

// Counterexample 7: two sources claiming the SAME immutable revision with
// INCOMPATIBLE document content. There is no honest winner, so neither is served
// and the snapshot says why -- in both collection orders.
func TestRevisionDocument_ConflictingContributorsHaveNoArbitraryWinner(t *testing.T) {
	a, b := docPermutationSources("# From A\n", "# From B\n")
	for _, order := range [][]Source{{a, b}, {b, a}} {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, order...)
		if err != nil {
			t.Fatal(err)
		}
		svc := snap.Services[NewServiceKeyDomain("domain-a", "payments")]
		key := string(svc.Revisions[0])
		q := NewQuery(snap)

		reason := wantUnavailable(t, q, key, "docs/overview.md")
		if !strings.Contains(reason, "different document content") {
			t.Errorf("%s first: the reason must name the disagreement, got %q", order[0].ID(), reason)
		}
		if !hasLimitation(snap.Limitations, LimitationRevisionDocConflict) {
			t.Errorf("%s first: the conflict must be reported as a snapshot limitation, got %+v", order[0].ID(), snap.Limitations)
		}
	}
}

// A revision one source knows only from runtime (no bundle at all) still reads
// its documents from the contributor that DOES have a bundle: silence is not
// disagreement.
func TestRevisionDocument_RuntimeOnlyContributorDoesNotConflict(t *testing.T) {
	withDocs := NewMemorySource("bundle-src", "local", &Collection{Revisions: []RawRevision{{
		Bundle: &contract.Bundle{Contract: docContract(), FS: fstest.MapFS{"docs/overview.md": {Data: []byte("# Only copy\n")}}},
		Domain: "domain-a", Digest: "sha256:pay",
	}}})
	noFS := NewMemorySource("runtime-src", "k8s", &Collection{Revisions: []RawRevision{{
		Bundle: &contract.Bundle{Contract: docContract()},
		Domain: "domain-a", Digest: "sha256:pay",
	}}})

	for _, order := range [][]Source{{withDocs, noFS}, {noFS, withDocs}} {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, order...)
		if err != nil {
			t.Fatal(err)
		}
		svc := snap.Services[NewServiceKeyDomain("domain-a", "payments")]
		wantContent(t, NewQuery(snap), string(svc.Revisions[0]), "docs/overview.md", "# Only copy\n")
		if hasLimitation(snap.Limitations, LimitationRevisionDocConflict) {
			t.Errorf("%s first: a contributor with no bundle must not create a document conflict", order[0].ID())
		}
	}
}

// Two contributors whose doc LISTS differ in size disagree just as loudly as two
// whose bodies differ: one of them is missing a document the revision has, so
// neither list can be trusted to describe it.
func TestRevisionDocument_ContributorsWithDifferentDocSetsConflict(t *testing.T) {
	mk := func(id string, files fstest.MapFS) Source {
		return NewMemorySource(id, "local", &Collection{Revisions: []RawRevision{{
			Bundle: &contract.Bundle{Contract: docContract(), FS: files},
			Domain: "domain-a", Digest: "sha256:pay",
		}}})
	}
	one := mk("one-doc", fstest.MapFS{"docs/overview.md": {Data: []byte("# Same\n")}})
	two := mk("two-docs", fstest.MapFS{
		"docs/overview.md": {Data: []byte("# Same\n")},
		"docs/extra.md":    {Data: []byte("# Extra\n")},
	})
	for _, order := range [][]Source{{one, two}, {two, one}} {
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, order...)
		if err != nil {
			t.Fatal(err)
		}
		svc := snap.Services[NewServiceKeyDomain("domain-a", "payments")]
		wantUnavailable(t, NewQuery(snap), string(svc.Revisions[0]), "docs/overview.md")
		if !hasLimitation(snap.Limitations, LimitationRevisionDocConflict) {
			t.Errorf("%s first: a differing document SET is a conflict too, got %+v", order[0].ID(), snap.Limitations)
		}
	}
}

// A path the walk listed but the read cannot open carries no fingerprint, so it
// can never be served as this revision's content.
func TestDocDigest_UnopenablePathHasNoFingerprint(t *testing.T) {
	if got := docDigest(fstest.MapFS{}, "docs/vanished.md"); got != "" {
		t.Errorf("an unopenable document must not be fingerprinted, got %q", got)
	}
}

// A document Build could not fingerprint (it was over the bound then) is never
// served later just because it happens to be readable now.
func TestRevisionDocument_UnfingerprintableAtBuildIsNeverServedLater(t *testing.T) {
	dir, bundle := tempDirBundleFS(t, map[string]string{
		"docs/overview.md": strings.Repeat("x", MaxDocumentBytes+1),
	})
	snap, key := buildOneRevision(t, bundle)
	q := NewQuery(snap)
	if reason := wantUnavailable(t, q, key, "docs/overview.md"); !strings.Contains(reason, "exceeds") {
		t.Errorf("while oversized, the reason must be the bound: %q", reason)
	}

	// Now it fits. It is still not this revision's document.
	if err := os.WriteFile(filepath.Join(dir, "docs", "overview.md"), []byte("# Small now\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reason := wantUnavailable(t, q, key, "docs/overview.md"); !strings.Contains(reason, "no immutable content") {
		t.Errorf("shrinking a document must not make it servable, got %q", reason)
	}
}
