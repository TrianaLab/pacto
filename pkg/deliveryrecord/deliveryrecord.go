// Package deliveryrecord is where delivery records live in a registry: the
// stream anchor every record of a Stage is attached to, the annotations on a
// record's referrer manifest, and reading a subject's records back verified.
// The recorder writes through it and every reader (CLI, MCP, dashboard) reads
// through it, so both agree on the layout.
package deliveryrecord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	l, err := List(ctx, repo, Anchor(stream).ManifestDesc, verify)
	ours := l.Records[:0]
	for _, r := range l.Records {
		if got := r.Statement.Predicate.Chain.Stream; got != stream {
			l.Unreadable = append(l.Unreadable, fmt.Sprintf("%s: names stream %q, read from the anchor of %q", r.Manifest, got, stream))
			continue
		}
		ours = append(ours, r)
	}
	l.Records = ours
	return l, err
}

// List reads every record attached to subject in repo and verifies each.
// Referrers that are not records are skipped, and so is a second manifest
// carrying a record already listed (a re-signed retry: same payload, new
// signature). A listing or fetch failure is an error: the registry could not
// be read, which is never the same as no record.
func List(ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor, verify delivery.Verifier) (Listing, error) {
	var descs []ocispec.Descriptor
	if err := repo.Referrers(ctx, subject, "", func(page []ocispec.Descriptor) error {
		descs = append(descs, page...)
		return nil
	}); err != nil {
		return Listing{}, err
	}
	var out Listing
	seen := map[string]bool{}
	for _, d := range descs {
		bundle, bad, err := fetchBundle(ctx, repo, d, delivery.PredicateOperation)
		switch {
		case err != nil:
			return Listing{}, err
		case bad != "":
			out.Unreadable = append(out.Unreadable, bad)
			continue
		case bundle == nil:
			continue // another tool's referrer
		}
		st, id, err := delivery.OpenRecord(bundle, verify)
		if err != nil {
			out.Unreadable = append(out.Unreadable, d.Digest.String()+": "+err.Error())
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out.Records = append(out.Records, delivery.StoredRecord{ID: id, Manifest: d.Digest.String(), Statement: st})
	}
	return out, nil
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
func Unchecked(string, []byte) error { return nil }

// Upstream reads what a Stage in another cluster verified from its stream's
// records, verified with verify (a trust config naming the key that signs
// that stream). It is nil, with the reason, when the registry cannot be read,
// a record does not verify or the stream is not whole: a downstream Stage
// never promotes on history it cannot check.
func Upstream(ctx context.Context, repo *remote.Repository, stream string, verify delivery.Verifier) (*delivery.StageStatus, string) {
	l, err := ListStream(ctx, repo, stream, verify)
	if err != nil {
		return nil, "reading the records: " + err.Error()
	}
	return delivery.UpstreamFromRecords(l.Records, l.Unreadable)
}
