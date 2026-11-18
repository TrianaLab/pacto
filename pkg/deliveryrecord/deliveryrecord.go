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
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
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
	// AnnRegistryMode is how the record was written: "native" (a referrer)
	// or "write-once-tags" (one write-once tag per record in the streams
	// repository).
	AnnRegistryMode = "dev.pacto.registry-mode"
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

// AnchorTag is the tag that keeps a stream's anchor from being collected by a
// registry that removes untagged manifests.
func AnchorTag(stream string) string {
	sum := sha256.Sum256([]byte(stream))
	return "stream-" + hex.EncodeToString(sum[:])
}

// RecordTag is the write-once tag of the record of stream at seq whose
// manifest is manifest: the anchor tag, the seq and 12 hex of the manifest
// digest. Two writers racing for one seq write two tags, never one moved
// tag, and a reader reports the fork. At most 104 characters.
func RecordTag(stream string, seq int64, manifest digest.Digest) string {
	return AnchorTag(stream) + "-" + strconv.FormatInt(seq, 10) + "-" + manifest.Encoded()[:12]
}

// PutRecord writes art, a record attached to anchor, in mode: a referrer
// confirmed in the Referrers listing, or the write-once tag
// [RecordTag] in the streams repository repo where the registry serves no
// Referrers API.
func PutRecord(ctx context.Context, repo *remote.Repository, anchor ocispec.Descriptor, stream string, seq int64, art ocireferrer.Artifact, mode capabilities.ReferrersMode) error {
	return put(ctx, repo, anchor, art, mode, RecordTag(stream, seq, art.ManifestDesc.Digest))
}

func put(ctx context.Context, repo *remote.Repository, anchor ocispec.Descriptor, art ocireferrer.Artifact, mode capabilities.ReferrersMode, tag string) error {
	if mode == capabilities.ReferrersWriteOnceTags {
		// PushWriteOnce here writes only the delivery record repositories.
		return ocireferrer.PushWriteOnce(ctx, repo, art, tag)
	}
	if err := ocireferrer.Push(ctx, repo, art); err != nil {
		return err
	}
	return ocireferrer.Confirm(ctx, repo, anchor, art.ManifestDesc)
}

// Record kinds that carry no seq, named in their [KindTag].
const (
	KindApproval = "approval"
	KindIntent   = "intent"
)

// KindTag is the write-once tag of an approval or intent record (kind) of
// stream whose manifest is manifest: the anchor tag, the kind and 12 hex of
// the manifest digest. At most 93 characters.
func KindTag(stream, kind string, manifest digest.Digest) string {
	return AnchorTag(stream) + "-" + kind + "-" + manifest.Encoded()[:12]
}

// Push writes bundle, a signed approval/v1 or operation-intent/v1 record of
// stream, attached to the stream's anchor in repo in mode, and returns where
// it is: "<repository>@<manifest digest>". ann are added to the record's
// annotations. Operation records go through [PutRecord].
func Push(ctx context.Context, repo *remote.Repository, stream string, mode capabilities.ReferrersMode, predicate string, bundle []byte, ann map[string]string) (string, error) {
	kind := KindIntent
	if predicate == delivery.PredicateApproval {
		kind = KindApproval
	}
	a := map[string]string{
		AnnBundleContent: "dsse-envelope",
		AnnPredicateType: predicate,
		AnnStream:        stream,
		AnnRegistryMode:  string(mode),
	}
	maps.Copy(a, ann)
	anchor := Anchor(stream).ManifestDesc
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, bundle, &anchor, a)
	if err := put(ctx, repo, anchor, art, mode, KindTag(stream, kind, art.ManifestDesc.Digest)); err != nil {
		return "", err
	}
	ref := repo.Reference
	ref.Reference = art.ManifestDesc.Digest.String()
	return ref.String(), nil
}

// Fetched is one approval or intent record read but not opened: the
// manifest that carries it and its bundle.
type Fetched struct {
	Manifest string
	Bundle   []byte
}

// ListPredicate reads the bundles of predicate (approval/v1 or
// operation-intent/v1) attached to stream's anchor in repo, as referrers and
// as [KindTag] write-once tags. uid, when set, keeps the records whose AnnUID
// names that operation: an unsigned convenience that only narrows what is
// read. Nothing is verified: the caller opens each, and [Reader.Reject]s
// what does not verify so it never takes the bound again. It returns at most
// max, picked in listing order (the time a statement claims is unverified,
// so it never picks) and then sorted newest first by that claimed time, and
// says when more matched (cut), so a caller says the bound was reached. A
// listing or fetch failure is an error, never no record. What each manifest
// and tag names is remembered, as [Reader.List] does, so a later call
// fetches only new manifests and the bundles it returns.
func (r *Reader) ListPredicate(ctx context.Context, repo *remote.Repository, stream, predicate, uid string, max int) ([]Fetched, bool, error) {
	anchor := Anchor(stream).ManifestDesc
	key, kind := headsKey(stream, predicate)
	descs, tags, err := ocireferrer.Attached(ctx, repo, anchor, AnchorTag(stream)+"-"+kind+"-")
	if err != nil {
		return nil, false, err
	}
	heads, order, err := r.readHeads(ctx, repo, key, descs, tags)
	if err != nil {
		return nil, false, err
	}
	var matched []string
	for _, k := range order {
		if h := heads[k]; h.layer != nil && !h.rejected && h.predicate == predicate && h.subject == anchor.Digest.String() && (uid == "" || h.uid == uid) {
			matched = append(matched, k)
		}
	}
	cut := len(matched) > max
	out := make([]Fetched, 0, min(max, len(matched)))
	for _, k := range matched[:min(max, len(matched))] {
		bundle, err := content.FetchAll(ctx, repo, *heads[k].layer)
		if err != nil {
			return nil, false, err
		}
		out = append(out, Fetched{Manifest: k, Bundle: bundle})
	}
	slices.SortStableFunc(out, func(a, b Fetched) int { return cmp.Compare(claimedTime(b.Bundle), claimedTime(a.Bundle)) })
	return out, cut, nil
}

// headsKey is where ListPredicate remembers the heads of predicate records on
// stream's anchor, and the tag kind they are written under.
func headsKey(stream, predicate string) (key, kind string) {
	kind = KindIntent
	if predicate == delivery.PredicateApproval {
		kind = KindApproval
	}
	return Anchor(stream).ManifestDesc.Digest.String() + " " + kind, kind
}

// Reject remembers that the predicate record manifest on stream's anchor
// does not verify or answers nothing the caller asks, so ListPredicate never
// returns it again nor counts it against max. It is forgotten with the
// manifest, once the registry no longer lists it.
func (r *Reader) Reject(stream, predicate, manifest string) {
	key, _ := headsKey(stream, predicate)
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.heads[key][manifest]; ok {
		// A copy: a concurrent readHeads reads the map it took outside the lock.
		heads := maps.Clone(r.heads[key])
		h.rejected = true
		heads[manifest] = h
		r.heads[key] = heads
	}
}

// readHeads says what each manifest in descs and each tag in tags names,
// by manifest digest and in listing order, through what r remembers under
// key. A vanished or moved tag holds nothing to open.
func (r *Reader) readHeads(ctx context.Context, repo *remote.Repository, key string, descs []ocispec.Descriptor, tags []string) (map[string]head, []string, error) {
	r.mu.Lock()
	knownHeads, knownTags := r.heads[key], r.kindTags[key]
	r.mu.Unlock()
	tagged, nowTags, err := resolveTags(ctx, repo, tags, knownTags, new([]string))
	if err != nil {
		return nil, nil, err
	}
	heads := map[string]head{}
	var order []string
	for _, d := range append(descs, tagged...) {
		k := d.Digest.String()
		if _, dup := heads[k]; dup {
			continue
		}
		h, ok := knownHeads[k]
		if !ok {
			if h, err = readHead(ctx, repo, d); err != nil {
				return nil, nil, err
			}
		}
		heads[k], order = h, append(order, k)
	}
	r.mu.Lock()
	if r.heads == nil {
		r.heads, r.kindTags = map[string]map[string]head{}, map[string]map[string]ocispec.Descriptor{}
	}
	r.heads[key], r.kindTags[key] = heads, nowTags
	r.mu.Unlock()
	return heads, order, nil
}

// head is what a record manifest says before its bundle is fetched. layer
// is nil when it is not one bundle layer, or too large to be a record.
type head struct {
	predicate, uid, subject string
	layer                   *ocispec.Descriptor
	// rejected is set by [Reader.Reject].
	rejected bool
}

// readHead fetches the manifest d and says what it is. An error is a
// registry failure, which is never remembered.
func readHead(ctx context.Context, repo *remote.Repository, d ocispec.Descriptor) (head, error) {
	if d.Size > MaxBytes {
		return head{}, nil
	}
	raw, err := content.FetchAll(ctx, repo, d)
	if err != nil {
		return head{}, err
	}
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m) // a manifest that does not decode is no record
	h := head{predicate: m.Annotations[AnnPredicateType], uid: m.Annotations[AnnUID]}
	if m.Subject != nil {
		h.subject = m.Subject.Digest.String()
	}
	if len(m.Layers) == 1 && m.Layers[0].Size <= MaxBytes {
		h.layer = &m.Layers[0]
	}
	return h, nil
}

// Fetch reads the bundle of the predicate record whose manifest digest is
// manifest in repo, unverified: the caller opens it. Anything else at that
// digest is an error.
func Fetch(ctx context.Context, repo *remote.Repository, manifest, predicate string) ([]byte, error) {
	if _, err := digest.Parse(manifest); err != nil {
		return nil, fmt.Errorf("%q is not a manifest digest: %w", manifest, err)
	}
	d, err := repo.Resolve(ctx, manifest)
	if err != nil {
		return nil, err
	}
	bundle, _, bad, err := fetchRecord(ctx, repo, d, predicate)
	if err == nil && bundle == nil {
		err = fmt.Errorf("%s is not one %s record %s", manifest, predicate, bad)
	}
	return bundle, err
}

// ReadAt reads the record at reference in repo, a tag or a manifest
// digest, unverified, and says which predicate it carries: operation/v1,
// approval/v1 or operation-intent/v1. Anything else, a record larger than
// MaxBytes or a write-once tag that does not name the manifest it points at
// is an error.
func ReadAt(ctx context.Context, repo *remote.Repository, reference string) (predicate string, bundle []byte, err error) {
	d, err := repo.Resolve(ctx, reference)
	if err != nil {
		return "", nil, err
	}
	if strings.HasPrefix(reference, "stream-") { // a write-once tag, never a digest
		if suffix := reference[strings.LastIndex(reference, "-")+1:]; len(suffix) == 12 && !strings.HasPrefix(d.Digest.Encoded(), suffix) {
			return "", nil, fmt.Errorf("tag %s does not name the manifest it points at", reference)
		}
	}
	// ponytail: one small manifest read per predicate tried, at most three.
	for _, predicate = range []string{delivery.PredicateOperation, delivery.PredicateApproval, delivery.PredicateIntent} {
		var bad string
		if bundle, _, bad, err = fetchRecord(ctx, repo, d, predicate); err != nil || bad != "" {
			return "", nil, cmp.Or(err, errors.New(bad))
		}
		if bundle != nil {
			return predicate, bundle, nil
		}
	}
	return "", nil, fmt.Errorf("%s is not a delivery record of at most %d bytes", reference, MaxBytes)
}

// claimedTime is the time a bundle's statement claims, unverified: an order
// only.
func claimedTime(bundle []byte) string {
	var b struct {
		DSSEEnvelope struct {
			Payload []byte `json:"payload"`
		} `json:"dsseEnvelope"`
	}
	var st struct {
		Predicate struct {
			Time string `json:"time"`
		} `json:"predicate"`
	}
	_ = json.Unmarshal(bundle, &b) // an unreadable bundle sorts last
	_ = json.Unmarshal(b.DSSEEnvelope.Payload, &st)
	return st.Predicate.Time
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
// ones. It remembers what each write-once record tag resolved to as well: a
// write-once tag never moves. It forgets a manifest or tag the subject no
// longer lists, so it holds no more than the registry does. Use one Reader per
// trust config; it is safe for concurrent use.
type Reader struct {
	Verify delivery.Verifier

	mu   sync.Mutex
	read map[string]map[string]referrer           // subject digest -> manifest digest -> what it read as
	tags map[string]map[string]ocispec.Descriptor // subject digest -> tag -> the manifest it names
	// heads and kindTags are ListPredicate's: by anchor digest and kind.
	heads    map[string]map[string]head
	kindTags map[string]map[string]ocispec.Descriptor
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
	l, held, err := r.list(ctx, repo, Anchor(stream).ManifestDesc, AnchorTag(stream)+"-")
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
	l, hs, err := r.list(ctx, repo, subject, "")
	for _, h := range hs {
		l.Unreadable = append(l.Unreadable, h.rec.Manifest+": signed by a revoked key; read its stream to tell its history from a forgery")
	}
	return l, err
}

// list reads what is attached to subject: its native referrers and, with a
// tagPrefix, the write-once record tags with that prefix.
func (r *Reader) list(ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor, tagPrefix string) (Listing, []held, error) {
	descs, tags, err := ocireferrer.Attached(ctx, repo, subject, tagPrefix)
	if err != nil {
		return Listing{}, nil, err
	}
	key := subject.Digest.String()
	r.mu.Lock()
	known, knownTags := r.read[key], r.tags[key]
	r.mu.Unlock()
	var out Listing
	tagged, nowTags, err := resolveTags(ctx, repo, tags, knownTags, &out.Unreadable)
	if err != nil {
		return Listing{}, nil, err
	}
	now := make(map[string]referrer, len(descs)+len(tagged))
	var hs []held
	seen := map[string]bool{}
	for _, d := range append(descs, tagged...) {
		if _, dup := now[d.Digest.String()]; dup {
			continue // a record that is both a referrer and a tag
		}
		got, ok := known[d.Digest.String()]
		if !ok {
			var err error
			if got, err = r.readOne(ctx, repo, subject, d); err != nil {
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
		r.read, r.tags = map[string]map[string]referrer{}, map[string]map[string]ocispec.Descriptor{}
	}
	r.read[key], r.tags[key] = now, nowTags
	r.mu.Unlock()
	return out, hs, nil
}

// resolveTags resolves each record tag to the manifest it names, through
// known. A tag whose manifest digest does not start with the tag's 12-hex
// suffix, or that was listed and is gone, is unreadable.
func resolveTags(ctx context.Context, repo *remote.Repository, tags []string, known map[string]ocispec.Descriptor, unreadable *[]string) ([]ocispec.Descriptor, map[string]ocispec.Descriptor, error) {
	var out []ocispec.Descriptor
	now := make(map[string]ocispec.Descriptor, len(tags))
	for _, t := range tags {
		d, ok := known[t]
		if !ok {
			var err error
			d, err = repo.Resolve(ctx, t)
			switch {
			case errors.Is(err, errdef.ErrNotFound):
				*unreadable = append(*unreadable, "tag "+t+" was listed and is gone")
				continue
			case err != nil:
				return nil, nil, err
			}
		}
		now[t] = d
		if suffix := t[strings.LastIndex(t, "-")+1:]; len(suffix) != 12 || !strings.HasPrefix(d.Digest.Encoded(), suffix) {
			*unreadable = append(*unreadable, "tag "+t+" does not name the manifest it points at")
			continue
		}
		out = append(out, d)
	}
	return out, now, nil
}

// readOne fetches and verifies the referrer d of subject. An error is a
// registry failure, which is never remembered.
func (r *Reader) readOne(ctx context.Context, repo *remote.Repository, subject, d ocispec.Descriptor) (referrer, error) {
	bundle, attached, bad, err := fetchRecord(ctx, repo, d, delivery.PredicateOperation)
	switch {
	case err != nil:
		return referrer{}, err
	case (bundle != nil || bad != "") && attached != subject.Digest.String():
		return referrer{bad: fmt.Sprintf("%s: tagged as a record of this stream but attached to %q", d.Digest, attached)}, nil
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
	bundle, _, bad, err = fetchRecord(ctx, repo, d, predicate)
	return bundle, bad, err
}

// fetchRecord is fetchBundle that also returns the digest the manifest is
// attached to (its subject), empty when it names none.
func fetchRecord(ctx context.Context, repo *remote.Repository, d ocispec.Descriptor, predicate string) (bundle []byte, subject, bad string, err error) {
	if d.Size > MaxBytes {
		return nil, "", "", nil // too large to be a record manifest
	}
	raw, err := content.FetchAll(ctx, repo, d)
	if err != nil {
		return nil, "", "", err
	}
	var m ocispec.Manifest
	if json.Unmarshal(raw, &m) != nil || m.Annotations[AnnPredicateType] != predicate {
		return nil, "", "", nil
	}
	if m.Subject != nil {
		subject = m.Subject.Digest.String()
	}
	if len(m.Layers) != 1 || m.Layers[0].Size > MaxBytes {
		return nil, subject, d.Digest.String() + ": not one bundle layer", nil
	}
	bundle, err = content.FetchAll(ctx, repo, m.Layers[0])
	return bundle, subject, "", err
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
// following Stage already read (delivery.UpstreamTruncated). With a view, why
// says how many referrers on the anchor it could not verify: a record hidden
// there, or signed by a key the trust config does not list after a rotation,
// is not part of the view, so the follower says so rather than look whole.
// acked are the stand-ins the following Stage acknowledged.
func Upstream(ctx context.Context, repo *remote.Repository, stream string, verify delivery.Verifier, seen delivery.RecordMark, acked []string) (*delivery.StageStatus, string) {
	return (&Reader{Verify: verify}).Upstream(ctx, repo, stream, seen, acked)
}

// Upstream is the package Upstream through r's cache.
func (r *Reader) Upstream(ctx context.Context, repo *remote.Repository, stream string, seen delivery.RecordMark, acked []string) (*delivery.StageStatus, string) {
	l, err := r.ListStream(ctx, repo, stream)
	if err != nil {
		return nil, "reading the records: " + err.Error()
	}
	var unverified string
	if len(l.Unreadable) > 0 {
		unverified = fmt.Sprintf("%d referrers on the upstream anchor could not be verified, so a record signed by a key the trust config "+
			"does not list is not seen: %s", len(l.Unreadable), strings.Join(l.Unreadable, "; "))
	}
	if len(l.Records) == 0 {
		// A reference alone cannot tell a new stream from a wrong reference,
		// deleted records or records it cannot verify: unknown, never an
		// empty and whole stream.
		return nil, cmp.Or(unverified, noRecordFound)
	}
	s, why := delivery.UpstreamFromRecords(l.Records, seen, acked)
	if s != nil && unverified != "" {
		why = unverified
	}
	return s, why
}

// noRecordFound is why a reference-only read that finds no record of a stream
// says nothing about it.
const noRecordFound = "no record was found for this stream; the reference may be wrong or the records deleted"
