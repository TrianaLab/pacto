package fleetsrc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

// TargetStateSchemaV1 is the schemaVersion of a v1 target-state file.
const TargetStateSchemaV1 = "pacto.dev/fleet-targets/v1"

// TargetStateSchemaV2 is v1 plus provisioning reports. The file is unsigned,
// so every surface shows its reports as unverified.
const TargetStateSchemaV2 = "pacto.dev/fleet-targets/v2"

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
	if doc.SchemaVersion != TargetStateSchemaV1 && doc.SchemaVersion != TargetStateSchemaV2 {
		return nil, fmt.Errorf("target-state %s: unsupported schemaVersion %q (want %q or %q)", s.path, doc.SchemaVersion, TargetStateSchemaV1, TargetStateSchemaV2)
	}
	if doc.SchemaVersion == TargetStateSchemaV1 && doc.Provisioning != nil {
		return nil, fmt.Errorf("target-state %s: provisioning needs schemaVersion %q", s.path, TargetStateSchemaV2)
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
	// fleet.Build validates each report; this file only carries them.
	for _, p := range doc.Provisioning {
		col.Provisioning = append(col.Provisioning, p.toRaw())
	}
	if doc.State != nil {
		col.State = doc.State.toState()
	}
	return col, nil
}

// decodeTargetStateStrict decodes with KnownFields so unknown fields are
// rejected, and requires the input to be EXACTLY ONE document: a trailing second
// YAML document ("---\n…") is rejected rather than silently ignored, mirroring the
// exactly-one-value rule at the JSON boundaries.
func decodeTargetStateStrict(data []byte) (*targetStateDoc, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var doc targetStateDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(targetStateDoc)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("target-state file must contain exactly one document")
	}
	return &doc, nil
}

// targetStateDoc is the on-disk schema of a target-state file.
type targetStateDoc struct {
	SchemaVersion string                `yaml:"schemaVersion" json:"schemaVersion"`
	Targets       []targetFixture       `yaml:"targets" json:"targets"`
	Provisioning  []provisioningFixture `yaml:"provisioning,omitempty" json:"provisioning,omitempty"`
	State         *stateFixture         `yaml:"state,omitempty" json:"state,omitempty"`
}

// provisioningFixture is one provisioning report (v2): the state of what the
// consumer revision (service, domain, digest) asked for through its
// configuration reference or dependency (kind, name). An empty outcome is
// Observed.
type provisioningFixture struct {
	Service     string    `yaml:"service" json:"service"`
	Domain      string    `yaml:"domain,omitempty" json:"domain,omitempty"`
	Digest      string    `yaml:"digest" json:"digest"`
	Kind        string    `yaml:"kind" json:"kind"`
	Name        string    `yaml:"name" json:"name"`
	Environment string    `yaml:"environment,omitempty" json:"environment,omitempty"`
	Provider    string    `yaml:"provider,omitempty" json:"provider,omitempty"`
	ExternalRef string    `yaml:"externalRef,omitempty" json:"externalRef,omitempty"`
	Phase       string    `yaml:"phase,omitempty" json:"phase,omitempty"`
	Outcome     string    `yaml:"outcome,omitempty" json:"outcome,omitempty"`
	ObservedAt  time.Time `yaml:"observedAt" json:"observedAt"`
	Producer    string    `yaml:"producer,omitempty" json:"producer,omitempty"`
}

func (p provisioningFixture) toRaw() fleet.RawProvisioning {
	outcome := p.Outcome
	if outcome == "" {
		outcome = string(evidence.Observed)
	}
	return fleet.RawProvisioning{
		Service: p.Service, Domain: p.Domain, Digest: p.Digest, Kind: p.Kind, Name: p.Name, Environment: p.Environment,
		Provider: p.Provider, ExternalRef: p.ExternalRef, Phase: p.Phase, Outcome: outcome, Producer: p.Producer, ObservedAt: p.ObservedAt,
	}
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
	// EvidenceRefs records which collector saw the finding, so a demo can show provenance such as
	// "seen by: Kubernetes + kernel" (collector k8s-observer+ebpf).
	EvidenceRefs []evidenceRefFixture `yaml:"evidenceRefs,omitempty" json:"evidenceRefs,omitempty"`
}

type evidenceRefFixture struct {
	Source     string `yaml:"source" json:"source"`
	ObservedAt string `yaml:"observedAt,omitempty" json:"observedAt,omitempty"`
	Collector  string `yaml:"collector,omitempty" json:"collector,omitempty"`
	DetectedAt string `yaml:"detectedAt,omitempty" json:"detectedAt,omitempty"`
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
		for _, r := range f.EvidenceRefs {
			if r.Source == "" {
				return "finding evidence source is required"
			}
		}
	}
	return ""
}

func (t targetFixture) toRaw() fleet.RawTarget {
	var findings []finding.Finding
	for _, f := range t.Findings {
		var refs []finding.EvidenceRef
		for _, r := range f.EvidenceRefs {
			refs = append(refs, finding.EvidenceRef{Source: r.Source, ObservedAt: r.ObservedAt, Collector: r.Collector, DetectedAt: r.DetectedAt})
		}
		findings = append(findings, finding.Finding{
			Code:         finding.Code(f.Code),
			Severity:     finding.Severity(f.Severity),
			Category:     finding.Category(f.Category),
			Subject:      finding.SubjectRef{Kind: f.SubjectKind, Name: f.SubjectName},
			Message:      f.Message,
			EvidenceRefs: refs,
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
// model a stale, partial or unavailable environment without a live source. No
// timestamps are stamped here; fleet.Build stamps them using BuildOptions.Now so
// tests can pin the clock.
func (s stateFixture) toState() *fleet.SourceState {
	status := fleet.SourceStatus(s.Status)
	switch status {
	case fleet.SourceStale, fleet.SourcePartial, fleet.SourceUnavailable, fleet.SourceAvailable:
	default:
		status = fleet.SourceAvailable
	}
	return &fleet.SourceState{Status: status}
}
