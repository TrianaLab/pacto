/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
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
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// RecordStore writes operation records and reads a stream back from its
// anchor: as OCI referrers where the streams registry serves the Referrers
// API, and as one write-once tag per record in the streams repository where
// it does not. It holds no state: the registry is the store.
type RecordStore struct {
	// Streams is the repository ("host/path") holding one anchor per stream.
	Streams string
	// Approvals is the repository a person's client pushes signed approvals
	// to, anchored per stream like Streams; only the recorder pushes to
	// Streams.
	Approvals string
	Options   ocireferrer.Options
	Verify    delivery.Verifier
	// Prober probes each registry host once; nil is ocireferrer.Probes.
	Prober *ocireferrer.Prober
}

// place is one subject a record is attached to.
type place struct {
	repo *remote.Repository
	desc ocispec.Descriptor
	// stream is set on an anchor: the stream whose records it holds.
	stream string
	// mode is how records are attached here: native referrers, or (on an
	// anchor only) write-once tags.
	mode capabilities.ReferrersMode
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

// anchor makes sure the stream's anchor exists and returns it with the mode
// its registry takes records in. The anchor is a deterministic manifest, so
// writing it again changes nothing.
func (s *RecordStore) anchor(ctx context.Context, stream string) (*place, error) {
	return s.anchorIn(ctx, s.Streams, stream)
}

// AnchorApprovals anchors stream in the approvals repository and returns
// where a client pushes a signed approval of it: "<repository>@<digest>".
func (s *RecordStore) AnchorApprovals(ctx context.Context, stream string) (string, error) {
	if _, err := s.anchorIn(ctx, s.Approvals, stream); err != nil {
		return "", err
	}
	return deliveryrecord.AnchorRef(s.Approvals, stream), nil
}

// Push writes bundle, a signed approval or intent record of predicate for
// the operation uid, on the stream anchor a, and returns where it is.
func (s *RecordStore) Push(ctx context.Context, a *place, predicate string, bundle []byte, uid string) (string, error) {
	return deliveryrecord.Push(ctx, a.repo, a.stream, a.mode, predicate, bundle, map[string]string{deliveryrecord.AnnUID: uid})
}

// anchorIn makes sure stream's anchor exists in the repository path.
func (s *RecordStore) anchorIn(ctx context.Context, path, stream string) (*place, error) {
	repo, err := s.open(path)
	if err != nil {
		return nil, err
	}
	art := deliveryrecord.Anchor(stream)
	tag := deliveryrecord.AnchorTag(stream)
	if got, err := repo.Resolve(ctx, tag); err != nil || got.Digest != art.ManifestDesc.Digest {
		if err := ocireferrer.Push(ctx, repo, art); err != nil {
			return nil, err
		}
		if err := repo.Tag(ctx, art.ManifestDesc, tag); err != nil {
			return nil, err
		}
	}
	caps, err := s.prober().Probe(ctx, repo, art.ManifestDesc.Digest.String(), true)
	if err != nil {
		return nil, err
	}
	// The cheapest proof that the stream lists in its mode.
	if caps.Referrers == capabilities.ReferrersNative {
		err = ocireferrer.Enumerate(ctx, repo, art.ManifestDesc)
	} else {
		_, err = ocireferrer.Tags(ctx, repo, tag+"-")
	}
	if err != nil {
		return nil, err
	}
	return &place{repo: repo, desc: art.ManifestDesc, stream: stream, mode: caps.Referrers}, nil
}

func (s *RecordStore) prober() *ocireferrer.Prober {
	if s.Prober != nil {
		return s.Prober
	}
	return ocireferrer.Probes
}

// Probe proves the streams repository can be written and listed, and
// reports what its registry supports.
func (s *RecordStore) Probe(ctx context.Context) (capabilities.Capabilities, error) {
	a, err := s.anchor(ctx, "pacto-delivery-recorder-probe")
	if err != nil {
		return capabilities.Capabilities{}, err
	}
	// Answered from the prober's cache: anchor just probed this host.
	return s.prober().Probe(ctx, a.repo, a.desc.Digest.String(), true)
}

// Mode is the registry mode Probe reports, such as
// "referrers=native listing=catalog", or empty when the probe fails. The target
// snapshot records it.
func (s *RecordStore) Mode(ctx context.Context) string {
	caps, err := s.Probe(ctx)
	if err != nil {
		return ""
	}
	return caps.Mode()
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
		return nil, nil, err
	}
	return l.Records, l.Unreadable, nil
}

// locate finds where a record of a move from→to goes: to and from in the
// apply repository, the contract to names in its own repository and the
// anchor. A digest the registry does not hold, or one that is not a digest
// (a refusal can name anything), is skipped; any other failure is returned.
// A placement outside the streams repository is kept only where its host
// serves the Referrers API: the recorder never tags a user's apply or
// contract repository, so a host without Referrers gets no placement. A
// failed probe is returned, so the write is retried instead of losing the
// placement for good.
func (s *RecordStore) locate(ctx context.Context, anchor *place, apply *source, from, to string) (locations, error) {
	l, err := s.candidates(ctx, anchor, apply, from, to)
	if err != nil {
		return l, err
	}
	for _, p := range []**place{&l.to, &l.from, &l.contract} {
		if *p, err = s.native(ctx, *p); err != nil {
			return l, err
		}
	}
	return l, nil
}

// native returns p when its host serves the Referrers API, nil when it
// does not, and the probe error when the probe could not tell.
func (s *RecordStore) native(ctx context.Context, p *place) (*place, error) {
	if p == nil {
		return nil, nil
	}
	caps, err := s.prober().Probe(ctx, p.repo, p.desc.Digest.String(), false)
	switch {
	case err != nil:
		return nil, err
	case caps.Referrers != capabilities.ReferrersNative:
		return nil, nil
	}
	p.mode = capabilities.ReferrersNative
	return p, nil
}

// candidates finds every subject a record of from→to could attach to.
func (s *RecordStore) candidates(ctx context.Context, anchor *place, apply *source, from, to string) (locations, error) {
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
// placement exists. The anchor placement goes in the stream's mode; the
// others are native referrers (locate kept only those). Each placement's
// AnnRegistryMode says how that copy was written.
func (s *RecordStore) write(ctx context.Context, bundle []byte, st delivery.Statement, l locations) error {
	chain := st.Predicate.Chain
	ann := map[string]string{
		deliveryrecord.AnnBundleContent: "dsse-envelope",
		deliveryrecord.AnnPredicateType: delivery.PredicateOperation,
		deliveryrecord.AnnUID:           st.Predicate.Intent.UID,
		deliveryrecord.AnnStream:        chain.Stream,
		deliveryrecord.AnnSeq:           strconv.FormatInt(chain.Seq, 10),
	}
	build := func(p *place) ocireferrer.Artifact {
		subject, a := p.desc, maps.Clone(ann)
		a[deliveryrecord.AnnRegistryMode] = string(p.mode)
		return ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, bundle, &subject, a)
	}
	for _, p := range []*place{l.to, l.from, l.contract} {
		if p == nil {
			continue
		}
		art := build(p)
		if err := ocireferrer.Push(ctx, p.repo, art); err != nil {
			return err
		}
		if err := ocireferrer.Confirm(ctx, p.repo, p.desc, art.ManifestDesc); err != nil {
			return err
		}
	}
	a := l.anchor
	return deliveryrecord.PutRecord(ctx, a.repo, a.desc, a.stream, chain.Seq, build(a), a.mode)
}
