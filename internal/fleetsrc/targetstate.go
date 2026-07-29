package fleetsrc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// TargetStateFileSource reads operational targets and their evaluation results
// from a strict, versioned fixture file (YAML or JSON — JSON is a YAML subset).
//
// This is an OFFLINE, demo/test adapter. It is deliberately NOT the external
// EvidenceSet ingestion protocol: it does not carry signed, versioned Pacto
// EvidenceSet values and it does not re-evaluate evidence against contracts. It
// ingests precomputed target state (compliance, findings, coverage) behind a
// documented trust boundary. A production external-evidence path is a future
// source built on the same fleet.Source seam.
type TargetStateFileSource struct {
	id   string
	path string
}

// TargetStateSchemaV1 is the required schemaVersion of a v1 target-state file.
const TargetStateSchemaV1 = "pacto.dev/fleet-targets/v1"

// maxTargetFixtures bounds how many targets a single fixture file may declare.
const maxTargetFixtures = 5000

// NewTargetStateFileSource returns a target-state fixture source reading path.
func NewTargetStateFileSource(id, path string) *TargetStateFileSource {
	if id == "" {
		id = "target-state"
	}
	return &TargetStateFileSource{id: id, path: path}
}

// ID implements [fleet.Source].
func (s *TargetStateFileSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *TargetStateFileSource) Kind() string { return "target-state" }

// Collect reads, strictly decodes, and validates the fixture. A missing or
// structurally invalid file (unknown fields, wrong schema version, unparseable)
// is a source error. Individual invalid target entries are skipped with a
// structured limitation, keeping valid entries and marking the source partial.
func (s *TargetStateFileSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	doc, err := decodeTargetStateStrict(data)
	if err != nil {
		return nil, fmt.Errorf("target-state %s: %w", s.path, err)
	}
	if doc.SchemaVersion != TargetStateSchemaV1 {
		return nil, fmt.Errorf("target-state %s: unsupported schemaVersion %q (want %q)", s.path, doc.SchemaVersion, TargetStateSchemaV1)
	}
	if len(doc.Targets) > maxTargetFixtures {
		return nil, fmt.Errorf("target-state %s: %d targets exceeds the limit of %d", s.path, len(doc.Targets), maxTargetFixtures)
	}

	col := &fleet.Collection{}
	for i, t := range doc.Targets {
		if reason := t.validate(); reason != "" {
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationSourceRecordInvalid, Source: s.id,
				Message: fmt.Sprintf("target entry %d is invalid and was skipped: %s", i, reason),
			})
			continue
		}
		col.Targets = append(col.Targets, t.toRaw())
	}
	if doc.State != nil {
		col.State = doc.State.toState()
	}
	return col, nil
}

// decodeTargetStateStrict decodes with KnownFields so unknown fields are
// rejected rather than silently ignored.
func decodeTargetStateStrict(data []byte) (*targetStateDoc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var doc targetStateDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// targetStateDoc is the on-disk schema of a v1 target-state file.
type targetStateDoc struct {
	SchemaVersion string          `yaml:"schemaVersion" json:"schemaVersion"`
	Targets       []targetFixture `yaml:"targets" json:"targets"`
	State         *stateFixture   `yaml:"state,omitempty" json:"state,omitempty"`
}

type targetFixture struct {
	Scope           string            `yaml:"scope" json:"scope"`
	Kind            string            `yaml:"kind" json:"kind"`
	Name            string            `yaml:"name" json:"name"`
	Service         string            `yaml:"service" json:"service"`
	Labels          map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	RequestedRef    string            `yaml:"requestedRef,omitempty" json:"requestedRef,omitempty"`
	ResolvedRef     string            `yaml:"resolvedRef,omitempty" json:"resolvedRef,omitempty"`
	Digest          string            `yaml:"digest,omitempty" json:"digest,omitempty"`
	Compliance      string            `yaml:"compliance,omitempty" json:"compliance,omitempty"`
	Coverage        *fleet.Coverage   `yaml:"coverage,omitempty" json:"coverage,omitempty"`
	Findings        []findingFixture  `yaml:"findings,omitempty" json:"findings,omitempty"`
	ObservedRuntime map[string]any    `yaml:"observedRuntime,omitempty" json:"observedRuntime,omitempty"`
	EvidenceAt      *time.Time        `yaml:"evidenceAt,omitempty" json:"evidenceAt,omitempty"`
	ReconciledAt    *time.Time        `yaml:"reconciledAt,omitempty" json:"reconciledAt,omitempty"`
}

type findingFixture struct {
	Code        string `yaml:"code" json:"code"`
	Severity    string `yaml:"severity,omitempty" json:"severity,omitempty"`
	Category    string `yaml:"category,omitempty" json:"category,omitempty"`
	SubjectKind string `yaml:"subjectKind,omitempty" json:"subjectKind,omitempty"`
	SubjectName string `yaml:"subjectName,omitempty" json:"subjectName,omitempty"`
	Message     string `yaml:"message,omitempty" json:"message,omitempty"`
}

type stateFixture struct {
	Status  string `yaml:"status" json:"status"`
	Message string `yaml:"message,omitempty" json:"message,omitempty"`
}

// validComplianceStates and validSeverities are the accepted enum values.
var (
	validComplianceStates = map[string]bool{
		fleet.StatusCompliant: true, fleet.StatusNonCompliant: true, fleet.StatusUnknown: true,
		fleet.StatusInvalid: true, fleet.StatusReference: true, fleet.StatusNotEvaluated: true,
		"Warning": true, "": true,
	}
	validSeverities = map[string]bool{
		string(finding.SeverityError): true, string(finding.SeverityWarning): true,
		string(finding.SeverityInfo): true, string(finding.SeverityUnknown): true, "": true,
	}
)

// validate returns a human reason when the fixture entry is invalid, else "".
func (t targetFixture) validate() string {
	if t.Service == "" {
		return "service is required"
	}
	if t.Name == "" {
		return "name is required"
	}
	if !validComplianceStates[t.Compliance] {
		return fmt.Sprintf("unknown compliance %q", t.Compliance)
	}
	if t.Coverage != nil && (t.Coverage.Evaluated < 0 || t.Coverage.Required < 0) {
		return "coverage counts must be non-negative"
	}
	for _, f := range t.Findings {
		if f.Code == "" {
			return "finding code is required"
		}
		if !validSeverities[f.Severity] {
			return fmt.Sprintf("unknown finding severity %q", f.Severity)
		}
	}
	return ""
}

func (t targetFixture) toRaw() fleet.RawTarget {
	var findings []finding.Finding
	for _, f := range t.Findings {
		findings = append(findings, finding.Finding{
			Code:     finding.Code(f.Code),
			Severity: finding.Severity(f.Severity),
			Category: finding.Category(f.Category),
			Subject:  finding.SubjectRef{Kind: f.SubjectKind, Name: f.SubjectName},
			Message:  f.Message,
		})
	}
	return fleet.RawTarget{
		Scope:           t.Scope,
		Kind:            t.Kind,
		Name:            t.Name,
		Labels:          t.Labels,
		Service:         t.Service,
		RequestedRef:    t.RequestedRef,
		ResolvedRef:     t.ResolvedRef,
		Digest:          t.Digest,
		Compliance:      t.Compliance,
		Coverage:        t.Coverage,
		Findings:        findings,
		ObservedRuntime: t.ObservedRuntime,
		EvidenceAt:      t.EvidenceAt,
		ReconciledAt:    t.ReconciledAt,
	}
}

// toState maps a fixture state to a source state. An explicit status lets a demo
// model a stale, partial or unavailable environment without a live source.
func (s stateFixture) toState() *fleet.SourceState {
	status := fleet.SourceStatus(s.Status)
	switch status {
	case fleet.SourceStale, fleet.SourcePartial, fleet.SourceUnavailable, fleet.SourceAvailable:
	default:
		status = fleet.SourceAvailable
	}
	return &fleet.SourceState{Status: status}
}
