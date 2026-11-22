package evidenceoci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// The subject ledger: one repository per subject prefix,
// <prefix>pacto-evidence, holding one write-once anchor tag per subject that
// ever received evidence. Referrers are listed per digest only, so the ledger
// is what lets a store with an open subject set find every subject again after
// a restart, with the registry still the only store.

// LedgerArtifactType is the artifact and layer type of a ledger anchor.
const LedgerArtifactType = "application/vnd.pacto.evidence.subject.v1+json"

const (
	// ledgerTagPrefix starts every tag the Evidence Server writes in a ledger.
	ledgerTagPrefix = "evidence-"
	// anchorTagLen is the length of an anchor tag: the prefix and 64 hex.
	anchorTagLen = len(ledgerTagPrefix) + sha256.Size*2
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

// buildAnchor is the deterministic anchor artifact of s.
func buildAnchor(s Subject) ocireferrer.Artifact {
	payload, _ := json.Marshal(anchorDoc{Subject: s.Ref()}) // one string; marshalling cannot fail
	return ocireferrer.Build(LedgerArtifactType, LedgerArtifactType, payload, nil, nil)
}

// writeAnchor records subj in p's ledger, once: rewriting an anchor that
// exists is a no-op, and the tag is confirmed after the push.
func (s *Store) writeAnchor(ctx context.Context, p Prefix, subj Subject) error {
	return ocireferrer.PushWriteOnce(ctx, s.ledgers[p], buildAnchor(subj), anchorTag(subj.Ref()))
}

// ledgerTags lists p's ledger tags. A ledger repository that does not exist
// yet is an empty ledger.
func (s *Store) ledgerTags(ctx context.Context, p Prefix) ([]string, error) {
	tags, err := ocireferrer.Tags(ctx, s.ledgers[p], ledgerTagPrefix)
	if err != nil && !ocireferrer.RepositoryNotFound(err) {
		return nil, fmt.Errorf("evidence oci: ledger %s: %w", p.Ledger(), err)
	}
	return tags, nil
}

// ledgerSubjects reads every subject in p's ledger and counts the anchors
// that could not be read as one. An unreadable listing is an error.
func (s *Store) ledgerSubjects(ctx context.Context, p Prefix) ([]Subject, int, error) {
	tags, err := s.ledgerTags(ctx, p)
	if err != nil {
		return nil, 0, err
	}
	var out []Subject
	invalid := 0
	for _, tag := range tags {
		if len(tag) != anchorTagLen {
			continue
		}
		subj, err := s.anchorSubject(ctx, p, tag)
		if err != nil {
			invalid++
			continue
		}
		out = append(out, subj)
	}
	return out, invalid, nil
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
