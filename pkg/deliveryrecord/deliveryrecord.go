// Package deliveryrecord is where delivery records live in a registry: the
// stream anchor every record of a Stage is attached to, the annotations on a
// record's referrer manifest, and reading a subject's records back verified.
// The recorder writes through it and every reader (CLI, MCP, dashboard) reads
// through it, so both agree on the layout.
//
// Experimental: not covered by semantic versioning. Delivery is Alpha, and
// this API may change in a minor release.
package deliveryrecord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// Referrer annotations on every record manifest. The predicate type is what
// readers filter on, because the artifact type matches every sigstore bundle;
// the dev.pacto ones are unsigned conveniences.
const (
	AnnBundleContent = "dev.sigstore.bundle.content"
	AnnPredicateType = "dev.sigstore.bundle.predicateType"
	AnnUID           = "dev.pacto.uid"
	AnnStream        = "dev.pacto.stream"
	AnnSeq           = "dev.pacto.seq"
	// AnchorType is the artifact and layer type of a stream anchor.
	AnchorType = "application/vnd.pacto.delivery.stream.v1+json"
)

// MaxBytes bounds one record manifest or bundle a reader fetches.
var MaxBytes int64 = 4 << 20

// Anchor is a stream's anchor: a deterministic manifest whose one layer names
// the stream, so anyone who knows the stream id knows its digest.
func Anchor(stream string) ocireferrer.Artifact {
	payload, _ := json.Marshal(map[string]string{"stream": stream}) // a string map; marshalling cannot fail
	return ocireferrer.Build(AnchorType, AnchorType, payload, nil, nil)
}

// AnchorRef is where a stream's anchor lives: "<repository>@<digest>".
func AnchorRef(repository, stream string) string {
	return repository + "@" + Anchor(stream).ManifestDesc.Digest.String()
}

// ParseAnchorRef splits an anchor reference and checks it is the anchor of
// stream, so a reference pointing at another stream's records is refused.
func ParseAnchorRef(ref, stream string) (repository string, desc ocispec.Descriptor, err error) {
	repository, digest, ok := strings.Cut(ref, "@")
	want := Anchor(stream).ManifestDesc
	if !ok || repository == "" || digest != want.Digest.String() {
		return "", ocispec.Descriptor{}, fmt.Errorf("%q is not the anchor of stream %q (want <repository>@%s)", ref, stream, want.Digest)
	}
	return repository, want, nil
}

// Listing is what a subject's records read back as. Unreadable names every
// record referrer that could not be verified, with why: a reader reports
// them, and the recorder refuses to extend a stream that has any.
type Listing struct {
	Records    []delivery.StoredRecord
	Unreadable []string
}

// ListStream reads the records on stream's anchor. A record the anchor holds
// that names another stream is unreadable, however validly it is signed: keys
// are trusted per stream by the stream a record names, so without this check a
// key trusted only for dev could sign a record and attach it to prod.
func ListStream(ctx context.Context, repo *remote.Repository, stream string, verify delivery.Verifier) (Listing, error) {
	return (&Reader{Verify: verify}).ListStream(ctx, repo, stream)
}

// List reads every record attached to subject in repo and verifies each.
// Referrers that are not records are skipped, and so is a second manifest
// carrying a record already listed (a re-signed retry: same payload, new
// signature). A listing or fetch failure is an error: the registry could not
// be read, which is never the same as no record.
func List(ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor, verify delivery.Verifier) (Listing, error) {
	return (&Reader{Verify: verify}).List(ctx, repo, subject)
}

// Reader reads records verified with Verify and remembers what each referrer
// manifest read as, by digest: a manifest never changes, so a later read of
// the same subject lists its referrers and fetches and verifies only the new
// ones. It forgets a manifest the subject no longer lists, so it holds no more
// than the registry does. Use one Reader per trust config; it is safe for
// concurrent use.
type Reader struct {
	Verify delivery.Verifier

	mu   sync.Mutex
	read map[string]map[string]referrer // subject digest -> manifest digest -> what it read as
}

// referrer is what one referrer manifest read as: a record, why it is
// unreadable, or neither (another tool's referrer, or one too large). until
// is set on a record only a revoked key signs: the last record it signed.
type referrer struct {
	rec   *delivery.StoredRecord
	bad   string
	until string
}

// ListStream is the package ListStream through r's cache.
func (r *Reader) ListStream(ctx context.Context, repo *remote.Repository, stream string) (Listing, error) {
	l, held, err := r.list(ctx, repo, Anchor(stream).ManifestDesc)
	l.Records = append(l.Records, history(l.Records, held, &l.Unreadable)...)
	ours := l.Records[:0]
	for _, rec := range l.Records {
		if got := rec.Statement.Predicate.Chain.Stream; got != stream {
			l.Unreadable = append(l.Unreadable, fmt.Sprintf("%s: names stream %q, read from the anchor of %q", rec.Manifest, got, stream))
			continue
		}
		ours = append(ours, rec)
	}
	l.Records = ours
	return l, err
}

// held is a record only a revoked key signs, which is history only if the
// last record the key signed (until) is it or chains from it.
type held struct {
	rec   delivery.StoredRecord
	until string
}

// history is the held records the stream's own chain vouches for: those on
// the parent chain from each revoked key's last record, walked through every
// record read. The rest go to unreadable: a leaked key that backdates a
// record to a seq it already used must not fork the stream.
func history(recs []delivery.StoredRecord, hs []held, unreadable *[]string) []delivery.StoredRecord {
	byID := map[string]delivery.StoredRecord{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	for _, h := range hs {
		byID[h.rec.ID] = h.rec
	}
	vouched := map[string]bool{}
	for _, h := range hs {
		for id := h.until; id != "" && !vouched[id]; {
			r, ok := byID[id]
			if !ok {
				break
			}
			vouched[id], id = true, r.Statement.Predicate.Chain.Parent
		}
	}
	var out []delivery.StoredRecord
	for _, h := range hs {
		if vouched[h.rec.ID] {
			out = append(out, h.rec)
			continue
		}
		*unreadable = append(*unreadable, h.rec.Manifest+": "+(&delivery.HistoryOnly{Until: h.until}).Error())
	}
	return out
}

// List is the package List through r's cache. A record only a revoked key
// signs is unreadable here: only its stream's chain can tell it is history.
func (r *Reader) List(ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor) (Listing, error) {
	l, hs, err := r.list(ctx, repo, subject)
	for _, h := range hs {
		l.Unreadable = append(l.Unreadable, h.rec.Manifest+": signed by a revoked key; read its stream to tell its history from a forgery")
	}
	return l, err
}

func (r *Reader) list(ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor) (Listing, []held, error) {
	var descs []ocispec.Descriptor
	if err := repo.Referrers(ctx, subject, "", func(page []ocispec.Descriptor) error {
		descs = append(descs, page...)
		return nil
	}); err != nil {
		return Listing{}, nil, err
	}
	key := subject.Digest.String()
	r.mu.Lock()
	known := r.read[key]
	r.mu.Unlock()
	now := make(map[string]referrer, len(descs))
	var out Listing
	var hs []held
	seen := map[string]bool{}
	for _, d := range descs {
		got, ok := known[d.Digest.String()]
		if !ok {
			var err error
			if got, err = r.readOne(ctx, repo, d); err != nil {
				return Listing{}, nil, err
			}
		}
		now[d.Digest.String()] = got
		switch {
		case got.bad != "":
			out.Unreadable = append(out.Unreadable, got.bad)
		case got.rec != nil && !seen[got.rec.ID]:
			seen[got.rec.ID] = true
			if got.until != "" {
				hs = append(hs, held{*got.rec, got.until})
				continue
			}
			out.Records = append(out.Records, *got.rec)
		}
	}
	r.mu.Lock()
	if r.read == nil {
		r.read = map[string]map[string]referrer{}
	}
	r.read[key] = now
	r.mu.Unlock()
	return out, hs, nil
}

// readOne fetches and verifies the referrer d. An error is a registry
// failure, which is never remembered.
func (r *Reader) readOne(ctx context.Context, repo *remote.Repository, d ocispec.Descriptor) (referrer, error) {
	bundle, bad, err := fetchBundle(ctx, repo, d, delivery.PredicateOperation)
	switch {
	case err != nil:
		return referrer{}, err
	case bad != "":
		return referrer{bad: bad}, nil
	case bundle == nil:
		return referrer{}, nil // another tool's referrer
	}
	st, id, err := delivery.OpenRecord(bundle, r.Verify)
	if h := (*delivery.HistoryOnly)(nil); errors.As(err, &h) {
		return referrer{rec: &delivery.StoredRecord{ID: id, Manifest: d.Digest.String(), Statement: st}, until: h.Until}, nil
	}
	if err != nil {
		return referrer{bad: d.Digest.String() + ": " + err.Error()}, nil
	}
	return referrer{rec: &delivery.StoredRecord{ID: id, Manifest: d.Digest.String(), Statement: st}}, nil
}

// fetchBundle reads the sigstore bundle a referrer manifest d carries when the
// manifest says it is a bundle of predicate: nil for anything else, bad when
// it is one but not one bundle layer, an error when the registry fails.
func fetchBundle(ctx context.Context, repo *remote.Repository, d ocispec.Descriptor, predicate string) (bundle []byte, bad string, err error) {
	if d.Size > MaxBytes {
		return nil, "", nil // too large to be a record manifest
	}
	raw, err := content.FetchAll(ctx, repo, d)
	if err != nil {
		return nil, "", err
	}
	var m ocispec.Manifest
	if json.Unmarshal(raw, &m) != nil || m.Annotations[AnnPredicateType] != predicate {
		return nil, "", nil
	}
	if len(m.Layers) != 1 || m.Layers[0].Size > MaxBytes {
		return nil, d.Digest.String() + ": not one bundle layer", nil
	}
	bundle, err = content.FetchAll(ctx, repo, m.Layers[0])
	return bundle, "", err
}

// Unchecked is a Verifier that accepts every signature: a reader with no trust
// config still lists records, and says they are unverified.
func Unchecked(string, int64, []byte) error { return nil }

// Upstream reads what a Stage in another cluster verified from its stream's
// records, verified with verify (a trust config naming the key that signs
// that stream). It is nil, with the reason, when the registry cannot be read
// or the verified stream is not whole: a downstream Stage never promotes on
// history it cannot check. A record that does not verify is skipped, so a
// referrer anyone with push access attaches cannot blind the follower. It is
// nil too when the stream no longer holds seen, the newest record the
// following Stage already read (delivery.UpstreamTruncated).
func Upstream(ctx context.Context, repo *remote.Repository, stream string, verify delivery.Verifier, seen delivery.RecordMark) (*delivery.StageStatus, string) {
	return (&Reader{Verify: verify}).Upstream(ctx, repo, stream, seen)
}

// Upstream is the package Upstream through r's cache.
func (r *Reader) Upstream(ctx context.Context, repo *remote.Repository, stream string, seen delivery.RecordMark) (*delivery.StageStatus, string) {
	l, err := r.ListStream(ctx, repo, stream)
	if err != nil {
		return nil, "reading the records: " + err.Error()
	}
	return delivery.UpstreamFromRecords(l.Records, seen)
}
