package evidenceoci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// The subject ledger: one repository per subject prefix,
// <prefix>pacto-evidence, holding one write-once anchor tag per subject that
// ever received evidence. Referrers are listed per digest only, so the ledger
// is what lets a store with an open subject set find every subject again after
// a restart, with the registry still the only store. On a registry without
// Referrers the ledger also holds each record as a write-once tag under its
// subject's anchor. On a registry that serves them, a native marker tag says
// the subject holds referrers, so a later process whose probe finds no
// Referrers fails closed on it instead of reading it as empty.

// LedgerArtifactType is the artifact and layer type of a ledger anchor.
const LedgerArtifactType = "application/vnd.pacto.evidence.subject.v1+json"

const (
	// ledgerTagPrefix starts every tag the Evidence Server writes in a ledger.
	ledgerTagPrefix = "evidence-"
	// recordSuffixLen is how much of the record manifest digest a record tag
	// carries.
	recordSuffixLen = 12
	// anchorTagLen is the length of an anchor tag: the prefix and 64 hex.
	anchorTagLen = len(ledgerTagPrefix) + sha256.Size*2
	// recordTagLen is the length of a record tag: its anchor tag, '-' and 12
	// hex of the record manifest digest.
	recordTagLen = anchorTagLen + 1 + recordSuffixLen
	// nativeSuffix ends the native marker tag of a subject.
	nativeSuffix = "-native"
	// maxLedgerTags is the tag listing bound of ocireferrer.Tags (1000 pages
	// of 100), named in the error a ledger past it returns.
	maxLedgerTags = 100000
	// maxAnchorBytes bounds an anchor payload: one subject reference.
	maxAnchorBytes = 4096
)

// anchorDoc is the payload of a ledger anchor.
type anchorDoc struct {
	Subject string `json:"subject"`
}

// anchorTag is the ledger tag of the subject ref: evidence-<hex sha256(ref)>.
// It is keyed on the whole ref, so one digest in two repositories is two
// subjects.
func anchorTag(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return ledgerTagPrefix + hex.EncodeToString(sum[:])
}

// recordTag is the ledger tag of one record on the subject ref:
// evidence-<hex sha256(ref)>-<first 12 hex of the record manifest digest>. It
// starts with the anchor tag, so one listing of the evidence- prefix holds
// both. Never seek from an anchor tag: CNCF distribution lists an anchor's
// record tags before the anchor itself.
func recordTag(ref string, manifest digest.Digest) string {
	return anchorTag(ref) + "-" + manifest.Encoded()[:recordSuffixLen]
}

// nativeTag is the marker tag of the subject ref, written before its first
// native referrer: evidence-<hex sha256(ref)>-native.
func nativeTag(ref string) string {
	return anchorTag(ref) + nativeSuffix
}

// buildAnchor is the deterministic anchor artifact of s.
func buildAnchor(s Subject) ocireferrer.Artifact {
	payload, _ := json.Marshal(anchorDoc{Subject: s.Ref()}) // one string; marshalling cannot fail
	return ocireferrer.Build(LedgerArtifactType, LedgerArtifactType, payload, nil, nil)
}

// writeLedger anchors subj in p's ledger, then stores art as its record tag
// on a registry without Referrers or, ahead of the native push, marks subj as
// holding referrers: a later process whose probe finds no Referrers must fail
// closed on it, never read it as empty.
func (s *Store) writeLedger(ctx context.Context, p Prefix, subj Subject, mode capabilities.ReferrersMode, art ocireferrer.Artifact) error {
	if err := s.writeAnchor(ctx, p, subj); err != nil {
		return err
	}
	if mode == capabilities.ReferrersWriteOnceTags {
		return s.writeRecord(ctx, p, subj, art)
	}
	return s.writeNativeMarker(ctx, p, subj)
}

// writeAnchor records subj in p's ledger, once: rewriting an anchor that
// exists is a no-op, and the tag is confirmed after the push.
func (s *Store) writeAnchor(ctx context.Context, p Prefix, subj Subject) error {
	return ocireferrer.PushWriteOnce(ctx, s.ledgers[p], buildAnchor(subj), anchorTag(subj.Ref()))
}

// writeNativeMarker records, once, that subj holds native referrers. It
// carries the anchor artifact under the marker tag.
func (s *Store) writeNativeMarker(ctx context.Context, p Prefix, subj Subject) error {
	return ocireferrer.PushWriteOnce(ctx, s.ledgers[p], buildAnchor(subj), nativeTag(subj.Ref()))
}

// writeRecord stores art, a record on subj, as a write-once tag in p's
// ledger, for a registry that serves no Referrers. The tag is confirmed after
// the push and never moved.
func (s *Store) writeRecord(ctx context.Context, p Prefix, subj Subject, art ocireferrer.Artifact) error {
	return ocireferrer.PushWriteOnce(ctx, s.ledgers[p], art, recordTag(subj.Ref(), art.ManifestDesc.Digest))
}

// ledgerTags lists p's ledger tags. A ledger repository that does not exist
// yet is an empty ledger; one past the listing bound is unreadable.
func (s *Store) ledgerTags(ctx context.Context, p Prefix) ([]string, error) {
	tags, err := ocireferrer.Tags(ctx, s.ledgers[p], ledgerTagPrefix)
	switch {
	case errors.Is(err, ocireferrer.ErrTagPagesExceeded):
		return nil, fmt.Errorf("evidence oci: ledger %s holds more than %d tags, the most one listing reads: let registry retention delete old record tags: %w", p.Ledger(), maxLedgerTags, err)
	case err != nil && !ocireferrer.RepositoryNotFound(err):
		return nil, fmt.Errorf("evidence oci: ledger %s: %w", p.Ledger(), err)
	}
	return tags, nil
}

// ledgerRead is one read of a prefix's ledger.
type ledgerRead struct {
	subjects []Subject
	// records maps an anchor tag to the record tags under it.
	records map[string][]string
	// native holds the anchor tags whose subject has native referrers.
	native map[string]bool
	// invalid counts anchors that could not be read and record tags with no
	// anchor.
	invalid int
}

// ledgerSubjects reads every subject in p's ledger and groups the record
// tags by anchor. An unreadable listing is an error.
func (s *Store) ledgerSubjects(ctx context.Context, p Prefix) (ledgerRead, error) {
	tags, err := s.ledgerTags(ctx, p)
	if err != nil {
		return ledgerRead{}, err
	}
	out := ledgerRead{records: map[string][]string{}, native: map[string]bool{}}
	anchors := map[string]bool{}
	for _, tag := range tags {
		if len(tag) != anchorTagLen {
			continue
		}
		anchors[tag] = true
		subj, err := s.anchorSubject(ctx, p, tag)
		if err != nil {
			out.invalid++
			continue
		}
		out.subjects = append(out.subjects, subj)
	}
	for _, tag := range tags {
		if len(tag) == anchorTagLen+len(nativeSuffix) && strings.HasSuffix(tag, nativeSuffix) && anchors[tag[:anchorTagLen]] {
			out.native[tag[:anchorTagLen]] = true
			continue
		}
		if len(tag) != recordTagLen || tag[anchorTagLen] != '-' {
			continue
		}
		// The anchor is written first, so a record without one was planted
		// or its anchor deleted: a record nobody can attribute is unread
		// evidence.
		if anchor := tag[:anchorTagLen]; anchors[anchor] {
			out.records[anchor] = append(out.records[anchor], tag)
		} else {
			out.invalid++
		}
	}
	return out, nil
}

// anchorSubject reads one anchor. A decoded anchor is memoized: its tag fixes
// the hash of its content.
func (s *Store) anchorSubject(ctx context.Context, p Prefix, tag string) (Subject, error) {
	key := p.Ledger() + ":" + tag
	s.mu.Lock()
	subj, ok := s.anchors[key]
	s.mu.Unlock()
	if ok {
		return subj, nil
	}
	payload, err := fetchAnchor(ctx, s.ledgers[p], tag)
	if err != nil {
		return Subject{}, err
	}
	if subj, err = validAnchor(tag, payload, p); err != nil {
		return Subject{}, err
	}
	s.mu.Lock()
	s.anchors[key] = subj
	s.mu.Unlock()
	return subj, nil
}

// fetchAnchor reads the payload of the anchor tagged tag, checking the
// manifest is an anchor before fetching its one layer.
func fetchAnchor(ctx context.Context, repo *remote.Repository, tag string) ([]byte, error) {
	_, data, err := oras.FetchBytes(ctx, repo, tag, oras.FetchBytesOptions{MaxBytes: maxManifestBytes})
	if err != nil {
		return nil, err
	}
	var m ocispec.Manifest
	if err := strictjson.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: anchor %s: %w", ErrInvalidArtifact, tag, err)
	}
	if m.ArtifactType != LedgerArtifactType || len(m.Layers) != 1 ||
		m.Layers[0].MediaType != LedgerArtifactType || m.Layers[0].Size > maxAnchorBytes {
		return nil, fmt.Errorf("%w: anchor %s is not a %s artifact with one layer", ErrInvalidArtifact, tag, LedgerArtifactType)
	}
	return content.FetchAll(ctx, repo, m.Layers[0])
}

// validAnchor decodes an anchor payload read under tag in p's ledger. It is a
// ledger subject only when it parses, sits under p and hashes to tag.
func validAnchor(tag string, payload []byte, p Prefix) (Subject, error) {
	var doc anchorDoc
	if err := strictjson.Unmarshal(payload, &doc); err != nil {
		return Subject{}, fmt.Errorf("%w: anchor %s: %w", ErrInvalidArtifact, tag, err)
	}
	subj, err := ParseSubject(doc.Subject)
	if err != nil {
		return Subject{}, fmt.Errorf("%w: anchor %s: %w", ErrInvalidArtifact, tag, err)
	}
	if !p.Covers(subj) {
		return Subject{}, fmt.Errorf("%w: anchor %s names %s, outside %s", ErrInvalidArtifact, tag, subj.Ref(), p)
	}
	if anchorTag(subj.Ref()) != tag {
		return Subject{}, fmt.Errorf("%w: anchor %s names %s, whose tag is %s", ErrInvalidArtifact, tag, subj.Ref(), anchorTag(subj.Ref()))
	}
	return subj, nil
}
