package fleetsrc

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// EvidenceSource reads operational targets from a fixture file (YAML or JSON —
// JSON is a YAML subset). It models a source that ingests already-produced
// evaluation results rather than observing a live environment: a remote or
// disconnected environment produces an evidence document, a platform ingests it,
// and the fleet exposes the targets with explicit freshness and completeness.
type EvidenceSource struct {
	id   string
	path string
}

// NewEvidenceSource returns an evidence source reading path.
func NewEvidenceSource(id, path string) *EvidenceSource {
	if id == "" {
		id = "evidence"
	}
	return &EvidenceSource{id: id, path: path}
}

// ID implements [fleet.Source].
func (s *EvidenceSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *EvidenceSource) Kind() string { return "evidence" }

// Collect reads and parses the evidence file into raw targets. A missing or
// unparseable file is a source error (surfaced as unavailable), not empty data.
func (s *EvidenceSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var doc evidenceDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse evidence %s: %w", s.path, err)
	}
	col := &fleet.Collection{}
	for _, t := range doc.Targets {
		col.Targets = append(col.Targets, t.toRaw())
	}
	if doc.State != nil {
		col.State = doc.State.toState()
	}
	return col, nil
}

// evidenceDoc is the on-disk schema of an evidence file.
type evidenceDoc struct {
	Targets []targetFixture `yaml:"targets" json:"targets"`
	State   *stateFixture   `yaml:"state,omitempty" json:"state,omitempty"`
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

// toState maps a fixture state to a source state. An explicit status lets a
// demo model a stale or partial environment without a live source.
func (s stateFixture) toState() *fleet.SourceState {
	status := fleet.SourceStatus(s.Status)
	switch status {
	case fleet.SourceStale, fleet.SourcePartial, fleet.SourceUnavailable, fleet.SourceAvailable:
	default:
		status = fleet.SourceAvailable
	}
	return &fleet.SourceState{Status: status}
}
