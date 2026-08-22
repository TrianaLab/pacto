package fleet

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// docsSnapshot builds two SAME-NAMED services in different domains, each with a
// docs/overview.md of its own. It is the fixture that proves a document read is
// keyed by canonical revision identity, not by a service name.
func docsSnapshot(t *testing.T, aBody, bBody string) *FleetSnapshot {
	t.Helper()
	rev := func(domain, digest, body string) RawRevision {
		c := &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: "payments", Version: "1.0.0", Owner: contract.Owner{Team: "team-" + domain}},
		}
		return RawRevision{
			Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{
				"docs/overview.md":         {Data: []byte(body)},
				"docs/guides/deep.md":      {Data: []byte("# Deep\n")},
				"docs/not-markdown.txt":    {Data: []byte("ignored")},
				"secrets/private-key.pem":  {Data: []byte("TOP SECRET")},
				"interfaces/openapi.json":  {Data: []byte(smallOpenAPI)},
				"docs/binary-disguised.md": {Data: []byte{0xff, 0xfe, 0x00}},
			}},
			Domain: domain, Digest: digest,
		}
	}
	col := &Collection{Revisions: []RawRevision{
		rev("domain-a", "sha256:a-pay", aBody),
		rev("domain-b", "sha256:b-pay", bBody),
	}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func docsRevisionKey(t *testing.T, snap *FleetSnapshot, domain string) string {
	t.Helper()
	svc := snap.Services[NewServiceKeyDomain(domain, "payments")]
	if svc == nil || len(svc.Revisions) != 1 {
		t.Fatalf("expected exactly one revision for %s/payments", domain)
	}
	return string(svc.Revisions[0])
}

func TestRevisionDocument_ReadsTheExactRevisionsDocument(t *testing.T) {
	snap := docsSnapshot(t, "# Domain A\n", "# Domain B\n")
	q := NewQuery(snap)

	for _, tc := range []struct{ domain, want string }{
		{"domain-a", "# Domain A\n"},
		{"domain-b", "# Domain B\n"},
	} {
		key := docsRevisionKey(t, snap, tc.domain)
		doc, err := q.RevisionDocument(key, "docs/overview.md")
		if err != nil {
			t.Fatalf("%s: %v", tc.domain, err)
		}
		if doc.Document.Content != tc.want {
			t.Errorf("%s: content = %q, want %q", tc.domain, doc.Document.Content, tc.want)
		}
		if doc.Document.Bytes != len(tc.want) {
			t.Errorf("%s: bytes = %d, want %d", tc.domain, doc.Document.Bytes, len(tc.want))
		}
		if doc.Document.Title != "overview" {
			t.Errorf("%s: title = %q", tc.domain, doc.Document.Title)
		}
		if string(doc.Revision.Key) != key || doc.Revision.Domain != tc.domain {
			t.Errorf("%s: document attributed to revision %+v", tc.domain, doc.Revision)
		}
		if doc.Meta.SchemaVersion != ProductSchemaVersion || doc.Meta.SnapshotID != snap.SnapshotID {
			t.Errorf("%s: document must carry the product completeness envelope, got %+v", tc.domain, doc.Meta)
		}
	}
}

// The two revisions have identical service names and identical doc paths. Asking
// one for the other's document must return that revision's OWN body -- never the
// neighbour's -- because the key selects the bundle.
func TestRevisionDocument_SameNameDifferentDomainsCannotCrossRead(t *testing.T) {
	snap := docsSnapshot(t, "# Domain A\n", "# Domain B\n")
	q := NewQuery(snap)
	a, err := q.RevisionDocument(docsRevisionKey(t, snap, "domain-a"), "docs/overview.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.Document.Content, "Domain B") {
		t.Fatalf("domain-a read domain-b's document: %q", a.Document.Content)
	}
}

func TestRevisionDocument_RejectsUnknownRevisionAndUnlistedPaths(t *testing.T) {
	snap := docsSnapshot(t, "# A\n", "# B\n")
	q := NewQuery(snap)
	key := docsRevisionKey(t, snap, "domain-a")

	if _, err := q.RevisionDocument("no-such-revision", "docs/overview.md"); !isNotFound(err, "revision") {
		t.Errorf("unknown revision: want revision NotFoundError, got %v", err)
	}

	// Every one of these is absent from the revision's published docs list, so
	// none of them reaches the filesystem: traversal, absolute paths, a real file
	// outside docs/, a non-markdown file inside docs/, and a Windows-style escape.
	for _, p := range []string{
		"../secrets/private-key.pem",
		"docs/../secrets/private-key.pem",
		"docs/../../etc/passwd",
		"/etc/passwd",
		"secrets/private-key.pem",
		"docs/not-markdown.txt",
		"..\\secrets\\private-key.pem",
		"docs/overview.md/",
		"",
	} {
		if _, err := q.RevisionDocument(key, p); !isNotFound(err, "document") {
			t.Errorf("path %q: want document NotFoundError, got %v", p, err)
		}
	}
}

func isNotFound(err error, kind string) bool {
	var nf *NotFoundError
	return errors.As(err, &nf) && nf.Kind == kind
}

func TestRevisionDocument_UnavailableIsExplicitNeverEmptyContent(t *testing.T) {
	oversized := strings.Repeat("x", MaxDocumentBytes+1)
	snap := docsSnapshot(t, oversized, "# B\n")
	q := NewQuery(snap)
	key := docsRevisionKey(t, snap, "domain-a")

	doc, err := q.RevisionDocument(key, "docs/overview.md")
	if doc != nil {
		t.Fatalf("an oversized document must not be served at all, got %d bytes", doc.Document.Bytes)
	}
	var du *DocumentUnavailableError
	if !errors.As(err, &du) {
		t.Fatalf("want DocumentUnavailableError, got %v", err)
	}
	if !strings.Contains(du.Error(), "exceeds") || !strings.Contains(du.Error(), "docs/overview.md") {
		t.Errorf("the error must name the document and the reason, got %q", du.Error())
	}

	// A document at EXACTLY the bound is still served: the cap is inclusive.
	atBound := docsSnapshot(t, strings.Repeat("y", MaxDocumentBytes), "# B\n")
	if _, err := NewQuery(atBound).RevisionDocument(docsRevisionKey(t, atBound, "domain-a"), "docs/overview.md"); err != nil {
		t.Errorf("a document exactly at the bound must be served: %v", err)
	}

	// Non-text is refused rather than transported as replacement characters.
	if _, err := q.RevisionDocument(key, "docs/binary-disguised.md"); !errors.As(err, &du) {
		t.Errorf("non-UTF-8 body: want DocumentUnavailableError, got %v", err)
	} else if !strings.Contains(du.Error(), "UTF-8") {
		t.Errorf("want a UTF-8 reason, got %q", du.Error())
	}
}

// A revision built from a runtime-only source has doc refs from one contributor
// and no filesystem of its own; it must say so instead of returning nothing.
func TestRevisionDocument_MissingBundleFilesystemIsReported(t *testing.T) {
	snap := docsSnapshot(t, "# A\n", "# B\n")
	key := RevisionKey(docsRevisionKey(t, snap, "domain-a"))
	snap.Revisions[key].bundle = nil

	_, err := NewQuery(snap).RevisionDocument(string(key), "docs/overview.md")
	var du *DocumentUnavailableError
	if !errors.As(err, &du) || !strings.Contains(du.Error(), "not retained") {
		t.Fatalf("want a retained-filesystem DocumentUnavailableError, got %v", err)
	}
}

// A listed document whose body cannot be produced -- the file is gone, or it
// opens and then fails mid-read (a truncated or corrupt bundle layer) -- is an
// explicit error, never a silently empty document.
func TestRevisionDocument_UnreadableBodyIsReported(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"open fails": fstest.MapFS{},
		"read fails": failingReadFS{},
	} {
		snap := docsSnapshot(t, "# A\n", "# B\n")
		key := RevisionKey(docsRevisionKey(t, snap, "domain-a"))
		rev := snap.Revisions[key]
		rev.bundle = &contract.Bundle{Contract: rev.Contract, FS: fsys}

		_, err := NewQuery(snap).RevisionDocument(string(key), "docs/overview.md")
		var du *DocumentUnavailableError
		if !errors.As(err, &du) {
			t.Errorf("%s: want DocumentUnavailableError, got %v", name, err)
		}
	}
}

// failingReadFS opens every path and fails on the first read.
type failingReadFS struct{}

func (failingReadFS) Open(string) (fs.File, error) { return failingReadFile{}, nil }

type failingReadFile struct{}

func (failingReadFile) Stat() (fs.FileInfo, error) { return nil, errors.New("no stat") }
func (failingReadFile) Read([]byte) (int, error) {
	return 0, errors.New("the bundle layer is truncated")
}
func (failingReadFile) Close() error { return nil }

// mergeRevision may adopt another contributor's doc list; it must adopt the
// filesystem those paths point into, or every listed document is unreadable.
func TestMergeRevision_AdoptsTheFilesystemBehindAdoptedDocs(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "svc", Version: "1.0.0"}}
	bare := &contract.Bundle{Contract: c}
	withDocs := &contract.Bundle{Contract: c, FS: fstest.MapFS{"docs/overview.md": {Data: []byte("# hi\n")}}}

	existing, _ := revisionFrom(RawRevision{Bundle: bare, Digest: "sha256:x"}, "runtime", fixedNow())
	add, _ := revisionFrom(RawRevision{Bundle: withDocs, Digest: "sha256:x"}, "registry", fixedNow())
	if len(existing.Docs) != 0 || len(add.Docs) != 1 {
		t.Fatalf("fixture: existing=%d docs, add=%d docs", len(existing.Docs), len(add.Docs))
	}
	if lims := mergeRevision(existing, add); len(lims) != 0 {
		t.Fatalf("same content must not conflict: %+v", lims)
	}

	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{existing.Key: existing}}
	if _, err := NewQuery(snap).RevisionDocument(string(existing.Key), "docs/overview.md"); err != nil {
		t.Fatalf("an adopted doc list must be readable: %v", err)
	}
}
