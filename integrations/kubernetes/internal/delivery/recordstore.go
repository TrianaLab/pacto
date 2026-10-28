/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// Errors the recorder tells apart.
var (
	// errUnsupported: the registry has no native Referrers API. Records are
	// unsupported there; there is no tag fallback.
	errUnsupported = errors.New("the registry has no Referrers API")
)

// RecordStore writes operation records as OCI referrers and reads a stream
// back from its anchor. It holds no state: the registry is the store.
type RecordStore struct {
	// Streams is the repository ("host/path") holding one anchor per stream.
	Streams string
	Options ocireferrer.Options
	Verify  delivery.Verifier
}

// place is one subject a record is attached to.
type place struct {
	repo *remote.Repository
	desc ocispec.Descriptor
	// stream is set on an anchor: the stream whose records it holds.
	stream string
}

// locations are where one record goes. Anchor is always set; the others are
// nil when there is nothing to attach to.
type locations struct {
	to, from, contract, anchor *place
}

func (l locations) subjects() delivery.RecordSubjects {
	d := func(p *place) string {
		if p == nil {
			return ""
		}
		return p.desc.Digest.String()
	}
	return delivery.RecordSubjects{To: d(l.to), From: d(l.from), Contract: d(l.contract), Anchor: d(l.anchor)}
}

// stored is one verified record read back from a stream.
type stored = delivery.StoredRecord

func (s *RecordStore) open(path string, insecureHosts ...string) (*remote.Repository, error) {
	opts := s.Options
	opts.Insecure = slices.Concat(opts.Insecure, insecureHosts)
	return ocireferrer.Open(path, opts)
}

// AnchorTag is the tag that keeps a stream's anchor from being collected by a
// registry that removes untagged manifests.
func AnchorTag(stream string) string {
	sum := sha256.Sum256([]byte(stream))
	return "stream-" + hex.EncodeToString(sum[:])
}

// anchor makes sure the stream's anchor exists and returns it. The anchor is
// a deterministic manifest, so writing it again changes nothing.
func (s *RecordStore) anchor(ctx context.Context, stream string) (*place, error) {
	repo, err := s.open(s.Streams)
	if err != nil {
		return nil, err
	}
	art := deliveryrecord.Anchor(stream)
	tag := AnchorTag(stream)
	if got, err := repo.Resolve(ctx, tag); err != nil || got.Digest != art.ManifestDesc.Digest {
		if err := ocireferrer.Push(ctx, repo, art); err != nil {
			return nil, err
		}
		if err := repo.Tag(ctx, art.ManifestDesc, tag); err != nil {
			return nil, err
		}
	}
	if err := ocireferrer.Enumerate(ctx, repo, art.ManifestDesc); err != nil {
		return nil, classify(err)
	}
	return &place{repo: repo, desc: art.ManifestDesc, stream: stream}, nil
}

func classify(err error) error {
	if errors.Is(err, errdef.ErrUnsupported) {
		return fmt.Errorf("%w: %w", errUnsupported, err)
	}
	return err
}

// Probe proves the streams repository answers native referrers discovery.
func (s *RecordStore) Probe(ctx context.Context) error {
	_, err := s.anchor(ctx, "pacto-delivery-recorder-probe")
	return err
}

// list reads every record on the anchor that a key trusted for the stream
// signed. Referrers that are not records are skipped, and so is a record that
// will not verify: it is not the stream's history, and anyone who can push to
// the streams repository could otherwise freeze any Stage by attaching one.
// ignored names those records, with why, for the condition to show. A
// trusted record that went missing still stops the recorder: the head the
// Stage names is then not among the records (conflict).
func (s *RecordStore) list(ctx context.Context, anchor *place) (recs []stored, ignored []string, err error) {
	l, err := deliveryrecord.ListStream(ctx, anchor.repo, anchor.stream, s.Verify)
	if err != nil {
		return nil, nil, classify(err)
	}
	return l.Records, l.Unreadable, nil
}

// locate finds where a record of a move from→to goes: to and from in the
// apply repository, the contract to names in its own repository and the
// anchor. A digest the registry does not hold, or one that is not a digest
// (a refusal can name anything), is skipped; any other failure is returned.
func (s *RecordStore) locate(ctx context.Context, anchor *place, apply *applyRef, from, to string) (locations, error) {
	l := locations{anchor: anchor}
	if apply == nil {
		return l, nil
	}
	var insecure []string
	if apply.insecure {
		insecure = append(insecure, apply.host())
	}
	repo, err := s.open(apply.repo, insecure...)
	if err != nil {
		return l, err
	}
	if l.to, err = resolve(ctx, repo, to); err != nil {
		return l, err
	}
	if l.from, err = resolve(ctx, repo, from); err != nil {
		return l, err
	}
	if l.to == nil {
		return l, nil
	}
	raw, err := content.FetchAll(ctx, repo, l.to.desc)
	if err != nil {
		return l, err
	}
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m) // an artifact that is not a manifest names no contract
	ref := contract.TrimOCIScheme(m.Annotations[delivery.ContractAnnotation])
	if delivery.ContractDigest(ref) == "" {
		return l, nil
	}
	path, digest := contract.SplitOCIDigest(ref)
	crepo, err := s.open(path, insecure...)
	if err != nil {
		return l, nil // a contract reference that names no repository has nowhere to attach
	}
	l.contract, err = resolve(ctx, crepo, digest)
	return l, err
}

func resolve(ctx context.Context, repo *remote.Repository, digest string) (*place, error) {
	if _, err := delivery.ParseDigest(digest); err != nil {
		return nil, nil
	}
	desc, err := repo.Resolve(ctx, digest)
	switch {
	case errors.Is(err, errdef.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return &place{repo: repo, desc: desc}, nil
}

// write attaches bundle to every location and confirms each is listed:
// subjects first and the anchor last, so a record on the anchor means every
// placement exists.
func (s *RecordStore) write(ctx context.Context, bundle []byte, st delivery.Statement, l locations) error {
	ann := map[string]string{
		deliveryrecord.AnnBundleContent: "dsse-envelope",
		deliveryrecord.AnnPredicateType: delivery.PredicateOperation,
		deliveryrecord.AnnUID:           st.Predicate.Intent.UID,
		deliveryrecord.AnnStream:        st.Predicate.Chain.Stream,
		deliveryrecord.AnnSeq:           strconv.FormatInt(st.Predicate.Chain.Seq, 10),
	}
	for _, p := range []*place{l.to, l.from, l.contract, l.anchor} {
		if p == nil {
			continue
		}
		subject := p.desc
		art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, bundle, &subject, ann)
		if err := ocireferrer.Push(ctx, p.repo, art); err != nil {
			return err
		}
		if err := ocireferrer.Confirm(ctx, p.repo, p.desc, art.ManifestDesc); err != nil {
			return err
		}
	}
	return nil
}
