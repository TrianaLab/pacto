package fleet

import (
	"crypto/sha256"
	"fmt"
	"io"
	"unicode/utf8"
)

// Lazy, content-verified in-bundle document reads.
//
// A revision's DocRefs are collected at Build time without RETAINING a single
// body (see docsFrom): a snapshot holds every revision of every service and
// eagerly inlining documentation would make the index grow with the prose in the
// fleet. The bodies stay where they are — in the revision's own bundle
// filesystem — and are read one at a time, on demand, through
// [Query.RevisionDocument].
//
// What Build DOES retain is a SHA-256 fingerprint per document. That is the
// difference between a lazy read and a live one. A bundle filesystem is not
// necessarily immutable storage: a local source's FS is an os.DirFS over a
// working directory, and the file behind docs/overview.md can be edited, replaced
// or deleted while the snapshot is still being queried. A read that simply
// re-opened the path would let one
//
//	(SnapshotID, RevisionKey, document path)
//
// name different bytes at different times, which is not what an immutable
// revision means. Every read therefore re-derives the fingerprint and refuses to
// serve anything that does not match: the caller gets the revision's content, or
// an explicit statement that it is no longer available. Never new bytes under the
// old identity.
//
// The read is deliberately narrow:
//
//   - It is keyed by canonical RevisionKey, so a bare service name can never
//     select a document and two same-named services in different domains have
//     different keys and different bundles.
//   - The requested path must be one the snapshot already recorded for THAT
//     revision. That allow-list is the whole traversal and cross-revision
//     defense: a "../" path, an absolute path or another revision's path is not
//     in the list, so it is simply not found. No string sanitizing is involved,
//     and nothing outside docs/*.md is reachable.
//   - The body is bounded by [MaxDocumentBytes] and must be text. An oversized,
//     unreadable or non-text document is an explicit error, never a silently
//     truncated half-document.
//   - The body must hash to the fingerprint recorded for that path at Build.
//   - The revision's documents must not be in conflict: when two sources
//     contributed the same revision with different document content, neither is
//     served (see mergeRevisionDocs), because picking one would make the answer
//     depend on source ordering.
//
// The read returns a fresh copy; the snapshot is never mutated and no
// snapshot-owned memory is handed out.

// MaxDocumentBytes is the hard cap on one lazily-read document body. A document
// larger than this is reported as unavailable rather than truncated: a Markdown
// file cut mid-fence renders as garbage and would be indistinguishable from the
// real document.
const MaxDocumentBytes = 512 << 10

// DocumentUnavailableError is returned when a document the snapshot DOES list
// cannot be served: its bundle filesystem is not retained, it could not be read,
// it exceeds [MaxDocumentBytes], it is not text, its bytes no longer match the
// content recorded for this revision at Build, or its sources conflict. It is
// distinct from [NotFoundError], which means the revision or the path is unknown.
//
// It is deliberately the ONLY alternative to serving the revision's own content:
// the two acceptable outcomes for a document whose backing bytes changed are the
// immutable content or an explicit failure, never a fresh body under the old
// revision identity.
type DocumentUnavailableError struct {
	Revision string
	Path     string
	Reason   string
}

func (e *DocumentUnavailableError) Error() string {
	return fmt.Sprintf("document %q of revision %q is unavailable: %s", e.Path, e.Revision, e.Reason)
}

// DocumentBody is one in-bundle document: the reference the revision already
// published plus its text. Bytes is the exact size read, so a consumer can show
// the document's weight without measuring the transported string.
type DocumentBody struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Bytes   int    `json:"bytes"`
	Content string `json:"content"`
}

// RevisionDocument is the product answer for one lazily-read document: the usual
// completeness envelope, the canonical revision it belongs to, and the body.
type RevisionDocument struct {
	Meta     ProductMeta  `json:"meta"`
	Revision EntityRef    `json:"revision"`
	Document DocumentBody `json:"document"`
}

// RevisionDocument reads one document belonging to exactly the given revision.
//
// It is the only query that touches the bundle filesystem after Build, and it
// does so under an explicit bound. See the package comment above for the
// identity, traversal and size rules it enforces.
func (q *Query) RevisionDocument(revisionKey, path string) (*RevisionDocument, error) {
	rev := q.snap.Revisions[RevisionKey(revisionKey)]
	if rev == nil {
		return nil, &NotFoundError{Kind: "revision", ID: revisionKey}
	}
	// Exact membership in the revision's OWN recorded doc list. Nothing else is
	// reachable, whatever the caller sends.
	var doc *DocRef
	for i := range rev.Docs {
		if rev.Docs[i].Path == path {
			doc = &rev.Docs[i]
			break
		}
	}
	if doc == nil {
		return nil, &NotFoundError{Kind: "document", ID: path}
	}
	unavailable := func(reason string) error {
		return &DocumentUnavailableError{Revision: revisionKey, Path: doc.Path, Reason: reason}
	}
	if rev.docConflict != "" {
		return nil, unavailable(rev.docConflict)
	}
	if rev.bundle == nil || rev.bundle.FS == nil {
		return nil, unavailable("this revision's bundle filesystem is not retained in the snapshot")
	}
	f, err := rev.bundle.FS.Open(doc.Path)
	if err != nil {
		return nil, unavailable(err.Error())
	}
	defer f.Close() //nolint:errcheck // read-only
	// One byte past the bound, so "exactly at the bound" and "over it" are
	// distinguishable without trusting a reported file size.
	body, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return nil, unavailable(err.Error())
	}
	if len(body) > MaxDocumentBytes {
		return nil, unavailable(fmt.Sprintf("it exceeds the %d-byte read bound", MaxDocumentBytes))
	}
	// The immutability check. Everything above proves the bytes are servable; this
	// proves they are THIS revision's. A document Build could not fingerprint has
	// no immutable content to serve, so it fails here too rather than being served
	// unverified.
	switch {
	case doc.digest == "":
		return nil, unavailable("no immutable content was recorded for it when the snapshot was built, so the bytes on disk cannot be attributed to this revision")
	case fmt.Sprintf("%x", sha256.Sum256(body)) != doc.digest:
		return nil, unavailable("its content changed after the snapshot was built; this revision's document is no longer available from its backing storage")
	}
	if !utf8.Valid(body) {
		return nil, unavailable("it is not valid UTF-8 text")
	}
	return &RevisionDocument{
		Meta:     q.productMeta(),
		Revision: revisionEntityRef(rev),
		Document: DocumentBody{
			Path: doc.Path, Title: doc.Title, Bytes: len(body), Content: string(body),
		},
	}, nil
}
