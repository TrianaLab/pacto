// Package fleet is the framework-independent operational-graph layer of Pacto.
//
// Externally this capability is the "Pacto Operational Graph": it composes many
// independent contracts, contract revisions, operational targets, and their
// relationships into a single versioned, navigable, verifiable read model that
// humans, CLIs, platforms, and agents can reason over.
//
// It deliberately models three identities separately and never flattens them:
//
//   - a [ServiceRecord] is a stable logical service ("payments-api", owner
//     payments) — who owns it, which revisions exist, where it is deployed;
//   - a [ContractRevision] is an immutable resolved revision
//     ("payments-api@sha256:…") — what it declares and how it differs from
//     another revision;
//   - a [TargetRecord] is a concrete operational target ("production-eu/
//     customer-a → kubernetes-workload payments/payments-api") — which revision
//     runs there and whether it is compliant.
//
// The package is a READ MODEL over many evaluations. It does not replace the
// pure engine (Contract × EvidenceSet → Findings); it references the engine's
// public domain types ([contract], [finding], [readiness], [graph], [lock])
// rather than re-modelling them. A [FleetSnapshot] is immutable once built; a
// [Query] is a pure, network-free view over it. The package imports no
// Kubernetes, no MCP, and no dashboard code — the boundary test in
// tests/architecture enforces this so external collectors and third-party Go
// consumers can use it without pulling those in.
//
// Incompleteness is always explicit. Sources report their [SourceState]; a
// snapshot and every query answer carry an as-of time, a [Completeness], and a
// list of [Limitation]s. An unavailable source is never rendered as an empty
// result: absence under partial coverage is not evidence of absence.
package fleet

import (
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// ServiceKey is the stable identity of a logical service (its name).
type ServiceKey string

// RevisionKey is the stable identity of an immutable contract revision.
// It prefers the manifest digest, then the resolved ref, then the version — a
// mutable OCI tag alone is never a revision identity.
type RevisionKey string

// TargetKey is the stable identity of an operational target: scope/kind/name.
type TargetKey string

// NewServiceKey returns the logical service key for a service name.
func NewServiceKey(name string) ServiceKey { return ServiceKey(name) }

// NewRevisionKey builds a revision key from a service and the most immutable
// identity available (digest > resolvedRef > version).
func NewRevisionKey(service, digest, resolvedRef, version string) RevisionKey {
	id := digest
	if id == "" {
		id = resolvedRef
	}
	if id == "" {
		id = version
	}
	if id == "" {
		id = "unknown"
	}
	return RevisionKey(service + "@" + id)
}

// NewTargetKey builds a collision-safe target key from its scope, kind, and
// name. Each component is percent-escaped (so an embedded "/" or "%" can never
// forge a component boundary) and the three components are joined with "/". The
// encoding round-trips through [ParseTargetKey]; a separate human display string
// is available via [TargetRecord.DisplayName]. Two distinct (scope,kind,name)
// triples can never map to the same key.
func NewTargetKey(scope, kind, name string) TargetKey {
	return TargetKey(escapeKeyPart(scope) + "/" + escapeKeyPart(kind) + "/" + escapeKeyPart(name))
}

// ParseTargetKey losslessly decodes a target key back into its components. ok is
// false when the key is not a well-formed three-component key.
func ParseTargetKey(k TargetKey) (scope, kind, name string, ok bool) {
	parts := strings.Split(string(k), "/")
	if len(parts) != 3 {
		return "", "", "", false
	}
	return unescapeKeyPart(parts[0]), unescapeKeyPart(parts[1]), unescapeKeyPart(parts[2]), true
}

// escapeKeyPart percent-encodes "%" first (so encoding is unambiguous) then "/".
func escapeKeyPart(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	return strings.ReplaceAll(s, "/", "%2F")
}

// unescapeKeyPart reverses escapeKeyPart. "/" is decoded before "%" so that an
// escaped literal "%2F" (encoded as "%252F") round-trips correctly.
func unescapeKeyPart(s string) string {
	s = strings.ReplaceAll(s, "%2F", "/")
	return strings.ReplaceAll(s, "%25", "%")
}

// Completeness reports whether a snapshot or query answer covers everything it
// intended to.
type Completeness string

const (
	// CompletenessComplete means every source was available and current.
	CompletenessComplete Completeness = "complete"
	// CompletenessPartial means at least one source was unavailable, stale, or
	// itself partial — the answer must be treated as incomplete knowledge.
	CompletenessPartial Completeness = "partial"
	// CompletenessEmpty means no source produced any record.
	CompletenessEmpty Completeness = "empty"
)

// SourceStatus is the health of a single source participating in a snapshot.
type SourceStatus string

const (
	SourceAvailable   SourceStatus = "available"
	SourcePartial     SourceStatus = "partial"
	SourceStale       SourceStatus = "stale"
	SourceUnavailable SourceStatus = "unavailable"
)

// Provenance distinguishes how a relationship became known. Only "declared" is
// produced today; the discriminator leaves a clean path for a future observed
// (OTel) or inferred graph without conflating them with declared intent.
const (
	ProvenanceDeclared = "declared"
	ProvenanceObserved = "observed"
	ProvenanceInferred = "inferred"
)

// Contract/compliance state strings, aligned with the operator and dashboard
// vocabulary. Declared here so the fleet layer does not import the dashboard.
const (
	StatusCompliant    = "Compliant"
	StatusNonCompliant = "NonCompliant"
	StatusUnknown      = "Unknown"
	StatusWarning      = "Warning"
	StatusInvalid      = "Invalid"
	StatusReference    = "Reference"
	StatusNotEvaluated = "NotEvaluated"
)

// severityRank orders statuses from most to least severe for aggregation and
// validation. Lower rank is more severe. Invalid is strictly worse than
// NonCompliant and is never collapsed into it.
var severityRank = map[string]int{
	StatusInvalid: 0, StatusNonCompliant: 1, StatusUnknown: 2, StatusWarning: 3,
	StatusCompliant: 4, StatusReference: 5, StatusNotEvaluated: 6,
}

// ValidStatus reports whether s is a canonical status value.
func ValidStatus(s string) bool {
	_, ok := severityRank[s]
	return ok
}

// SchemaVersion is the version of the fleet snapshot/query wire model. It is
// carried on every snapshot and query answer so consumers can detect model
// changes.
const SchemaVersion = "pacto.dev/fleet/v1"

// Limitation codes explain, in a structured way, why an answer is incomplete or
// why a record could not be fully established. Codes are stable and typed so
// agents branch on them; the human message is advisory.
const (
	LimitationNoSourcesConfigured = "NO_SOURCES_CONFIGURED"
	LimitationSourceUnavailable   = "SOURCE_UNAVAILABLE"
	LimitationSourceStale         = "SOURCE_STALE"
	LimitationSourcePartial       = "SOURCE_PARTIAL"
	LimitationUnresolvedDep       = "UNRESOLVED_DEPENDENCY"
	LimitationEvidenceMissing     = "EVIDENCE_MISSING"
	LimitationSourceRecordInvalid = "SOURCE_RECORD_INVALID"
	LimitationDuplicateSourceID   = "DUPLICATE_SOURCE_ID"
	LimitationRevisionUnresolved  = "REVISION_IDENTITY_UNRESOLVED"
	LimitationRevisionConflict    = "REVISION_CONTENT_CONFLICT"
	LimitationTargetRefConflict   = "TARGET_REFERENCE_CONFLICT"
	LimitationTargetFieldConflict = "TARGET_FIELD_CONFLICT"
	LimitationOwnerConflict       = "OWNER_CONFLICT"
)

// Limitation is a structured, machine-readable reason an answer is incomplete.
// Prose lives in Message; agents branch on Code.
type Limitation struct {
	Code    string `json:"code"`
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}

// SourceError is a sanitized source failure. It never carries secrets, tokens,
// or raw authentication/transport error text — only a category code and a
// generic message safe to surface to any consumer.
type SourceError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SourceState reports the health and provenance of one source in a snapshot.
type SourceState struct {
	ID                 string       `json:"id"`
	Kind               string       `json:"kind"`
	Status             SourceStatus `json:"status"`
	LastSuccessfulSync *time.Time   `json:"lastSuccessfulSync,omitempty"`
	ObservedAt         *time.Time   `json:"observedAt,omitempty"`
	Error              *SourceError `json:"error,omitempty"`
	RevisionCount      int          `json:"revisionCount"`
	TargetCount        int          `json:"targetCount"`
}

// ToolSummary is a bounded projection of an agent-invocable tool derived from a
// revision's OpenAPI interface. It omits the full input schema and operation
// body so search/get answers stay small; callers fetch bodies lazily.
type ToolSummary struct {
	Name     string `json:"name"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Summary  string `json:"summary,omitempty"`
	Mutating bool   `json:"mutating"`
}

// DocRef is a pointer to an in-bundle document. It carries no body — bodies are
// fetched lazily, never eagerly duplicated into the index.
type DocRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// Coverage records how much of a target's required assertion set was actually
// evaluated against evidence.
type Coverage struct {
	Evaluated int `json:"evaluated"`
	Required  int `json:"required"`
}

// ServiceRecord is a stable logical service. It references its revisions and
// targets by key rather than copying them, and only carries an aggregate Status
// when the derivation is unambiguous.
type ServiceRecord struct {
	Key       ServiceKey        `json:"key"`
	Name      string            `json:"name"`
	Owner     contract.Owner    `json:"owner,omitempty"`
	Revisions []RevisionKey     `json:"revisions,omitempty"`
	Targets   []TargetKey       `json:"targets,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Status    string            `json:"status,omitempty"`
	Sources   []string          `json:"sources,omitempty"`
}

// ContractRevision is an immutable resolved revision. It embeds a reference to
// the declared contract (the interfaces/configs/deps/policies/state/readiness it
// declares) and adds computed projections (readiness result, validation
// findings, derived tools/skills/doc refs) plus its resolved identity.
type ContractRevision struct {
	Key          RevisionKey        `json:"key"`
	Service      string             `json:"service"`
	ServiceKey   ServiceKey         `json:"serviceKey"`
	PactoVersion string             `json:"pactoVersion,omitempty"`
	Version      string             `json:"version,omitempty"`
	RequestedRef string             `json:"requestedRef,omitempty"`
	ResolvedRef  string             `json:"resolvedRef,omitempty"`
	Digest       string             `json:"digest,omitempty"`
	Owner        contract.Owner     `json:"owner,omitempty"`
	Contract     *contract.Contract `json:"contract,omitempty"`
	Readiness    *readiness.Result  `json:"readiness,omitempty"`
	Validation   []finding.Finding  `json:"validation,omitempty"`
	Valid        bool               `json:"valid"`
	Tools        []ToolSummary      `json:"tools,omitempty"`
	Skills       []string           `json:"skills,omitempty"`
	Docs         []DocRef           `json:"docs,omitempty"`
	Lock         *lock.Lock         `json:"lock,omitempty"`
	Source       string             `json:"source"`
	Sources      []string           `json:"sources,omitempty"`
	FetchedAt    *time.Time         `json:"fetchedAt,omitempty"`

	// bundle carries the parsed bundle used only DURING Build (to derive tools,
	// skills, docs and validation). It is never serialized and is never exposed
	// through a query result, so callers cannot mutate snapshot-owned bundle data.
	bundle *contract.Bundle
	// validated records that this revision had raw YAML and was run through the
	// validator at build time. Stored so status queries never dereference the
	// build-only bundle after Build.
	validated bool
}

// TargetRecord is a concrete operational target associated with a revision.
// Its identity is generic (scope/kind/name) and does not assume Kubernetes,
// though the first implementations are Kubernetes-derived.
type TargetRecord struct {
	Key              TargetKey         `json:"key"`
	Scope            string            `json:"scope,omitempty"`
	Kind             string            `json:"kind,omitempty"`
	Name             string            `json:"name"`
	Labels           map[string]string `json:"labels,omitempty"`
	Service          string            `json:"service"`
	ServiceKey       ServiceKey        `json:"serviceKey"`
	ContractRevision RevisionKey       `json:"contractRevision,omitempty"`
	RequestedRef     string            `json:"requestedRef,omitempty"`
	ResolvedRef      string            `json:"resolvedRef,omitempty"`
	Digest           string            `json:"digest,omitempty"`
	Compliance       string            `json:"compliance"`
	Findings         []finding.Finding `json:"findings,omitempty"`
	Coverage         *Coverage         `json:"coverage,omitempty"`
	Readiness        *readiness.Result `json:"readiness,omitempty"`
	ObservedRuntime  map[string]any    `json:"observedRuntime,omitempty"`
	EvidenceAt       *time.Time        `json:"evidenceAt,omitempty"`
	ReconciledAt     *time.Time        `json:"reconciledAt,omitempty"`
	Source           string            `json:"source"`
	Sources          []string          `json:"sources,omitempty"`
	Stale            bool              `json:"stale"`
	Limitations      []Limitation      `json:"limitations,omitempty"`
}

// DisplayName returns a human-readable "scope/kind/name" for a target, with
// components unescaped. Unlike the canonical Key it is lossy (a name containing
// "/" is shown verbatim) and must not be used as an identity.
func (t *TargetRecord) DisplayName() string {
	return strings.Join([]string{t.Scope, t.Kind, t.Name}, "/")
}

// Relationship types. Configuration and policy references are kept distinct from
// dependencies and from each other — collapsing them into one generic "reference"
// edge loses meaning that consumers and future impact analysis need.
const (
	RelationshipDependency = "dependency"
	RelationshipConfigRef  = "configuration_reference"
	RelationshipPolicyRef  = "policy_reference"
)

// Relationship is a directed, revision-scoped graph edge. A declared edge
// originates from a specific [ContractRevision] (FromRevision) of a logical
// service (FromService) — never from "the service's latest revision". Only
// declared edges are produced today; Provenance leaves room for observed/inferred
// edges later without conflating them with declared intent.
type Relationship struct {
	FromService      string      `json:"fromService"`
	FromRevision     RevisionKey `json:"fromRevision,omitempty"`
	To               string      `json:"to"`
	ToService        string      `json:"toService,omitempty"`
	ResolvedRevision RevisionKey `json:"resolvedRevision,omitempty"`
	Type             string      `json:"type"`
	Provenance       string      `json:"provenance"`
	Required         bool        `json:"required,omitempty"`
	Compatibility    string      `json:"compatibility,omitempty"`
	Resolved         bool        `json:"resolved"`
	RequestedRef     string      `json:"requestedRef,omitempty"`
	LockedDigest     string      `json:"lockedDigest,omitempty"`
	LockedVersion    string      `json:"lockedVersion,omitempty"`
	Reason           string      `json:"reason,omitempty"`
}

// FleetSnapshot is the immutable read model produced by [Build]. Maps serialize
// deterministically (encoding/json sorts string keys); slices are sorted at
// build time. It is safe for concurrent read-only queries.
type FleetSnapshot struct {
	// SchemaVersion identifies the wire model; SnapshotID is a deterministic
	// content identity (a hash over the whole snapshot) so consumers can prove
	// two answers came from the same system view and can compare snapshots.
	SchemaVersion string                            `json:"schemaVersion"`
	SnapshotID    string                            `json:"snapshotId"`
	GeneratedAt   time.Time                         `json:"generatedAt"`
	Services      map[ServiceKey]*ServiceRecord     `json:"services"`
	Revisions     map[RevisionKey]*ContractRevision `json:"revisions"`
	Targets       map[TargetKey]*TargetRecord       `json:"targets"`
	Relationships []Relationship                    `json:"relationships"`
	Sources       []SourceState                     `json:"sources"`
	Completeness  Completeness                      `json:"completeness"`
	Limitations   []Limitation                      `json:"limitations,omitempty"`

	// reverseDeps maps a service name to the names of services that declare a
	// dependency on it, across all their revisions (the dependents index). Built
	// once at Build time; never mutated afterwards.
	reverseDeps map[string][]string
	// forwardDeps maps a service name to the union of resolved dependency service
	// names across all its revisions, for aggregated service-level traversal.
	forwardDeps map[string][]string
	// forwardDepsByRevision maps a specific revision to the resolved dependency
	// service names IT declares — the revision-accurate adjacency used when a
	// graph query names a revision or a target (never "the latest revision").
	forwardDepsByRevision map[RevisionKey][]string
}

// Service returns the logical service record by name, or nil.
func (s *FleetSnapshot) Service(name string) *ServiceRecord {
	return s.Services[NewServiceKey(name)]
}
