// Package runrecord defines the v1 run record. Run records are unsigned
// statistics about Pacto runs. Nothing that decides ever reads them.
//
// Experimental: not covered by semantic versioning.
package runrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

const (
	// Version is the schema identifier every v1 record carries.
	Version = "pacto.trianalab.io/run-record/v1"
	// ArtifactType is the artifact type of a record referrer.
	ArtifactType = "application/vnd.pacto.run.v1"
	// LayerType is the media type of the layer holding the encoded record.
	LayerType = ArtifactType + "+json"
	// AnchorType is the artifact type of a service's stats anchor.
	AnchorType = "application/vnd.pacto.stats.v1"
	// AnchorLayerType is the media type of the anchor's layer.
	AnchorLayerType = AnchorType + "+json"
	// AnchorTagPrefix starts every anchor tag, keeping Pacto's anchors apart
	// from other tools' tags in a shared repository.
	AnchorTagPrefix = "pacto-stats-"
	// AnnRecordedAt is an unsigned ordering hint on a record referrer.
	AnnRecordedAt = "dev.pacto.recorded-at"
	// MaxRecordBytes bounds an encoded record.
	MaxRecordBytes = 64 << 10
)

// Kinds of run.
const (
	KindDiff    = "diff"
	KindImpact  = "impact"
	KindPublish = "publish"
)

// Outcomes of a run.
const (
	OutcomePassed     = "passed"
	OutcomeFailed     = "failed"
	OutcomeUnverified = "unverified"
	OutcomePublished  = "published"
	OutcomeUnchanged  = "unchanged"
	OutcomeRefused    = "refused"
)

// Gate verdicts.
const (
	VerdictClear      = "clear"
	VerdictBlocked    = "blocked"
	VerdictUnverified = "unverified"
	VerdictRefused    = "refused"
)

// The legal values validate accepts, each listed once so a later kind is
// added in one place.
var (
	kinds         = []string{KindDiff, KindImpact, KindPublish}
	outcomes      = []string{OutcomePassed, OutcomeFailed, OutcomeUnverified, OutcomePublished, OutcomeUnchanged, OutcomeRefused}
	verdicts      = []string{VerdictClear, VerdictBlocked, VerdictUnverified, VerdictRefused}
	completeness  = []string{"complete", "partial", "empty"}
	idPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	errIncomplete = errors.New("consumers, completeness and asOf must be set together")
)

// Gate is the gate a run reached.
type Gate struct {
	// Verdict is clear, blocked, unverified or refused.
	Verdict string `json:"verdict"`
	// Reason is a stable code for why, when the run has one.
	Reason string `json:"reason,omitempty"`
}

// Consumers counts an impact run's consumers. The first four are a disjoint
// partition: every consumer is in exactly one of them.
type Consumers struct {
	// Incompatible consumers break on the change.
	Incompatible int `json:"incompatible"`
	// Unknown consumers could not be classified from the evidence.
	Unknown int `json:"unknown"`
	// Compatible consumers keep working.
	Compatible int `json:"compatible"`
	// Unclassified consumers carry no recognised verdict.
	Unclassified int `json:"unclassified"`
	// Blocking counts the consumers that hold the gate.
	Blocking int `json:"blocking"`
}

// Record is one run record.
type Record struct {
	// Version is always [Version].
	Version string `json:"version"`
	// ID is the distinct-change key, see [ID].
	ID string `json:"id"`
	// Kind is diff, impact or publish.
	Kind string `json:"kind"`
	// Service is the contract's service.name.
	Service string `json:"service"`
	// OldRevision is the old bundle digest (diff, impact) or the published
	// baseline version (publish), when there is one.
	OldRevision string `json:"oldRevision,omitempty"`
	// NewRevision is the new bundle digest (diff, impact) or the version
	// being published (publish).
	NewRevision string `json:"newRevision,omitempty"`
	// BundleDigest is the published bundle's digest (publish only).
	BundleDigest string `json:"bundleDigest,omitempty"`
	// DeployDigest is the published deploy artifact's digest (publish only).
	DeployDigest string `json:"deployDigest,omitempty"`
	// Classification is the change classification, when the run computed one.
	Classification string `json:"classification,omitempty"`
	// Gate is the gate the run reached (impact, refused publish).
	Gate *Gate `json:"gate,omitempty"`
	// Consumers counts the consumers of the change (impact only).
	Consumers *Consumers `json:"consumers,omitempty"`
	// Completeness is complete, partial or empty: how complete the snapshot
	// behind Consumers was (impact only).
	Completeness string `json:"completeness,omitempty"`
	// AsOf is the snapshot's time in UTC (impact only).
	AsOf *time.Time `json:"asOf,omitempty"`
	// Outcome is what the run ended with.
	Outcome string `json:"outcome"`
	// PactoVersion is the Pacto release that wrote the record, distinct from
	// a contract's pactoVersion.
	PactoVersion string `json:"pactoVersion"`
	// RecordedAt is when the record was written, UTC, second precision.
	RecordedAt time.Time `json:"recordedAt"`
	// Repository is the CI repository, only when asked for.
	Repository string `json:"repository,omitempty"`
	// CIRunURL is the CI run's URL, only when asked for.
	CIRunURL string `json:"ciRunUrl,omitempty"`
	// Environment is the CI environment name, only when asked for.
	Environment string `json:"environment,omitempty"`
}

// ID is the distinct-change key of a run:
// "sha256:" + hex(sha256(kind|service|oldRevision|newRevision)).
func ID(kind, service, oldRevision, newRevision string) string {
	return "sha256:" + hexSum(kind+"|"+service+"|"+oldRevision+"|"+newRevision)
}

// AnchorTag is the tag that keeps service's anchor from being collected.
func AnchorTag(service string) string {
	return AnchorTagPrefix + hexSum(service)
}

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Encode validates r and renders it as JSON.
func Encode(r Record) ([]byte, error) {
	if err := validate(r); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

// Parse strictly decodes one record of at most [MaxRecordBytes] and checks its
// structure. It never recomputes the id or judges cross-field meaning.
func Parse(data []byte) (Record, error) {
	if len(data) > MaxRecordBytes {
		return Record{}, fmt.Errorf("run record is larger than %d bytes", MaxRecordBytes)
	}
	var r Record
	if err := strictjson.Unmarshal(data, &r); err != nil {
		return Record{}, err
	}
	return r, validate(r)
}

func validate(r Record) error {
	for _, check := range []func(Record) error{validateIdentity, validateEnums, validateCounts} {
		if err := check(r); err != nil {
			return fmt.Errorf("invalid run record: %w", err)
		}
	}
	return nil
}

func validateIdentity(r Record) error {
	switch {
	case r.Version != Version:
		return fmt.Errorf("version %q is not %s", r.Version, Version)
	case !idPattern.MatchString(r.ID):
		return fmt.Errorf("id %q is not sha256:<64 hex>", r.ID)
	case r.Service == "":
		return errors.New("service is empty")
	case r.RecordedAt.IsZero():
		return errors.New("recordedAt is missing")
	}
	return nil
}

func validateEnums(r Record) error {
	switch {
	case !slices.Contains(kinds, r.Kind):
		return fmt.Errorf("unknown kind %q", r.Kind)
	case !slices.Contains(outcomes, r.Outcome):
		return fmt.Errorf("unknown outcome %q", r.Outcome)
	case r.Gate != nil && !slices.Contains(verdicts, r.Gate.Verdict):
		return fmt.Errorf("unknown gate verdict %q", r.Gate.Verdict)
	case r.Completeness != "" && !slices.Contains(completeness, r.Completeness):
		return fmt.Errorf("unknown completeness %q", r.Completeness)
	}
	return nil
}

func validateCounts(r Record) error {
	set := []bool{r.Consumers != nil, r.Completeness != "", r.AsOf != nil}
	if slices.Contains(set, !set[0]) {
		return errIncomplete
	}
	if c := r.Consumers; c != nil && min(c.Incompatible, c.Unknown, c.Compatible, c.Unclassified, c.Blocking) < 0 {
		return errors.New("a consumer count is negative")
	}
	return nil
}
