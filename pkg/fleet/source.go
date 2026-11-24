package fleet

import (
	"context"
	"encoding/json"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/readiness"
)

// Source is a fleet-owned ingestion seam. A source produces raw revision and
// target records for [Build] to compose. The interface is framework-neutral: a
// Kubernetes-backed source, an OCI-backed source, a local source, or a
// dashboard adapter each implement it, and none of them leak their transport
// into pkg/fleet. This is the inversion the architecture requires — the fleet
// layer defines the contract; adapters (including the dashboard) depend inward
// on it.
// SourceKindKubernetes is the Kind of a source reading the Pacto CRDs of a
// cluster. The dashboard's /api/sources pills use the same name.
const SourceKindKubernetes = "kubernetes"

// SourceKindKubernetesWorkloads is the Kind of a source reading the
// Deployments, StatefulSets, DaemonSets and CronJobs of a cluster. It reports
// inferred services and never a target.
const SourceKindKubernetesWorkloads = "kubernetes-workloads"

// SourceKindRegistryListing is the Kind of a source listing the golden paths
// and offerings published under a registry prefix (--discover). Templates
// and offerings never consume a service, so its health never makes an
// impact or gate answer partial.
const SourceKindRegistryListing = "registry"

// nonConsumerSourceKinds are the kinds whose sources never name a consumer.
var nonConsumerSourceKinds = []string{SourceKindKubernetesWorkloads, SourceKindRegistryListing}

// SourceKindEvidenceHTTP and SourceKindTargetState are the Kinds of the
// sources that report operational targets outside Kubernetes: the Evidence
// Server and a target state file.
const (
	SourceKindEvidenceHTTP = "evidence-http"
	SourceKindTargetState  = "target-state"
)

// SourceKindTargetSnapshot is the Kind of a source reading a signed snapshot
// of a cluster's Pactos from a registry (<registry>/pacto-targets). It reports
// targets.
const SourceKindTargetSnapshot = "target-snapshot"

// SourceKindMira is the Kind of a source reading the service map of a Mira
// trace store. It labels the source only; pkg/fleet never branches on it.
const SourceKindMira = "mira"

// SourceKindDetection is the Kind of a source reading what the Pacto operator
// detected in a cluster. It labels the source only; pkg/fleet never branches
// on it (an advisory source is [Collection.Advisory]).
const SourceKindDetection = "detection"

// SourceKindMesh is the Kind of a source reading the calls a service mesh or
// Hubble records. It labels the source only; pkg/fleet never branches on it.
const SourceKindMesh = "mesh"

// SourceKindTraces is the Kind of a source reading the calls a trace backend
// the operator detected already records, such as Jaeger's dependencies.
const SourceKindTraces = "traces"

// SourceKindOTLPReceiver is the Kind of the dashboard's OTLP/HTTP receiver,
// which maps the spans Collectors send it to calls.
const SourceKindOTLPReceiver = "otlp-receiver"

// SourceKindLinked is the Kind of a source reading the workloads the Pacto
// operator linked to a published contract with no Pacto: targets, like a
// Kubernetes source.
const SourceKindLinked = "kubernetes-linked"

// SourceKindMemberLinks is the Kind of the hub's carrier of member link
// facts ([Collection.ClusterLinks]). Build copies its links onto the cluster
// records and lists it nowhere: it is no source a reader configured.
const SourceKindMemberLinks = "member-links"

type Source interface {
	// ID is the stable identifier of this source instance (e.g. "oci", "k8s",
	// "local", or a named environment like "production-eu"). Used as provenance.
	ID() string
	// Kind classifies the source (e.g. "oci", "kubernetes", "local", "cache").
	Kind() string
	// Collect gathers everything this source can observe right now. It must
	// honor ctx cancellation. A non-nil error means the source could not be
	// observed at all; Build records it as unavailable (sanitized) rather than
	// as an empty result.
	Collect(ctx context.Context) (*Collection, error)
}

// ClusterSource is a Source that knows the cluster it reads before reading,
// so the unavailable state Build records when it fails still names it.
type ClusterSource interface {
	Source
	Cluster() *Cluster
}

// StaleState is the state of a source that failed now and serves the
// collection it read at lastSync: stale, with err sanitized so no raw
// transport text (server URLs, tokens, file paths) reaches a reader.
func StaleState(lastSync time.Time, err error) *SourceState {
	return &SourceState{Status: SourceStale, LastSuccessfulSync: &lastSync, Error: sanitizeError(err)}
}

// Collection is one source's contribution to a snapshot. A source that
// partially succeeded may return records AND set State so its degraded status
// reaches the snapshot instead of masquerading as complete data.
type Collection struct {
	Revisions []RawRevision
	Targets   []RawTarget
	// Cluster is the cluster of every target this source reports, nil when the
	// source does not know it. A [RawTarget.Cluster] overrides it.
	Cluster *Cluster
	// State, when non-nil, overrides Build's derived source state. Use it to
	// report a source that succeeded but is stale or partial. Its ID/Kind are
	// filled from the source if left blank.
	State *SourceState
	// Limitations report record-level problems a source hit while collecting
	// (an unreadable bundle, an invalid fixture entry). A non-empty Limitations
	// implies the source is partial: usable records are kept AND the problem is
	// surfaced, so "an invalid record exists" is never confused with "no record
	// exists". Messages must already be sanitized.
	Limitations []Limitation
	// Notes are limitations that leave the source's state as it is: what a
	// source did not read that never makes its answer partial.
	Notes []Limitation
	// Observed carries runtime-observed dependency edges (e.g. from OpenTelemetry
	// traces or a service mesh) this source witnessed. Endpoint names are raw
	// observed identities; [Build] resolves each to a unique domain-qualified
	// service and folds resolved edges into the snapshot as observed relationships.
	// An endpoint that resolves to zero or multiple services is never coerced to a
	// domain — it becomes an explicit limitation instead.
	Observed []ObservedEdge
	// ObservedUndeclaredOnly says the source cannot show a declared edge
	// unobserved: it reports only calls no contract declares (Kubernetes) or sees
	// only part of the calls (Mira's sampled, instrumented-only service map).
	// [Build] folds its observations and lets them corroborate a declared edge,
	// but its silence never counts against one.
	ObservedUndeclaredOnly bool
	// ObservedCautionOnly says the source's observed edges come from senders
	// nobody authenticated (the OTLP receiver): [Build] folds them as callers
	// and undeclared edges but never lets them corroborate a declared edge.
	ObservedCautionOnly bool
	// Provisioning is provider-reported state of what a consumer's
	// configuration reference or dependency asked for. It is never a target.
	Provisioning []RawProvisioning
	// Extensions are verified reports on a revision's extension capabilities;
	// never a target.
	Extensions []RawExtension
	// Usage is provider-reported usage and cost of what a consumer's
	// configuration reference or dependency asked for. It is never a target
	// and never changes compliance.
	Usage []RawUsage
	// ExternalUses are the external systems a source infers a service uses
	// from its configuration (the operator reads them from the workload's pod
	// spec). [Build] turns them into inferred relationships to the external
	// system, kept apart from declared and observed ones and out of every
	// index a gate reads: an inferred use never clears a gate.
	ExternalUses []RawExternalUse
	// InferredServices are workloads no contract describes. [Build] makes each
	// an inferred service and never a target, so it never counts in
	// compliance, link or target tallies. Their cluster is [Collection.Cluster].
	InferredServices []RawInferredService
	// ServiceFacts are values a source outside every contract reports for a
	// service it names: an owner, a business domain and the services it depends
	// on. [Build] keeps them as inferred values beside the declared fields and as
	// inferred edges: they never fill a declared field, never become an owner
	// claim and never enter an index a gate reads.
	ServiceFacts []RawServiceFact
	// InferredTemplates are golden paths a catalog outside every registry
	// holds. [Build] lists them as inferred, never as services, and never
	// offers them for creation.
	InferredTemplates []RawInferredTemplate
	// ListsTemplates says the source lists inferred templates. It is set even
	// when the source read none or failed, so an unavailable lister reads as
	// partial, never as "no templates".
	ListsTemplates bool
	// TemplatesComplete says a partial source still read every template: its
	// limitations are about other entities and never make the templates
	// partial.
	TemplatesComplete bool
	// Providers are rows describing what this source detected and read;
	// Build copies them, fills Source and Cluster and never lets a row claim
	// better health than the source.
	Providers []Provider
	// Advisory: an advisory source never makes a snapshot partial: its status
	// is reported and its rows are hints.
	Advisory bool
	// ClusterLinks are member link facts by cluster id, read only from a
	// [SourceKindMemberLinks] source.
	ClusterLinks map[string]ClusterLink
	// Offerings are the inferred-offering observations installations
	// advertised; never a service.
	Offerings []OfferingObservation
}

// RawServiceFact is what a source outside every contract (an IDP's catalog)
// reports for one service it names. [Build] matches it to a service by
// ServiceKey when set, else by Name, and never guesses: a name that matches
// more than one service gives no value.
type RawServiceFact struct {
	// Name is the source's name for the service, matched case-insensitively
	// against the name of exactly one fleet service.
	Name string
	// ServiceKey, when set, is the only service the fact may match.
	ServiceKey ServiceKey
	// Entity is the source's own reference for the record it read, such as
	// component:default/ledger. It says where a value came from.
	Entity string
	// Owner is the owner the source reports; only its canonical key is kept.
	Owner contract.Owner
	// BusinessDomain is the business domain the source reports; a value that
	// is no valid service.domain slug is dropped.
	BusinessDomain string
	// DependsOn are the services the source says this one depends on.
	DependsOn []RawFactRef
	// ObservedAt is when the source read the record.
	ObservedAt *time.Time
	// Exported marks a record Pacto itself wrote to the source: reading it
	// back would echo Pacto's own values, so [Build] skips it.
	Exported bool
}

// RawInferredTemplate is one golden path a catalog outside every registry
// holds. It is never a service, never signed and never declared until a
// person accepts it.
type RawInferredTemplate struct {
	// Name is the catalog's name for the template, kept only when a contract
	// can carry it as service.name.
	Name string
	// Entity is the source's own reference, such as
	// template:default/springboot-service.
	Entity string
	// Title is the catalog's display title.
	Title string
	// Summary is the catalog's description, cut to the service.summary bound.
	Summary string
	// Type is the catalog's template type, such as service or website.
	Type string
	// Owner is the owner the catalog names; it is shown, never declared.
	Owner contract.Owner
	// From is the display origin the source writes, such as
	// Catalog (catalog.acme.io).
	From string
	// URL is where a person opens the template, empty when the source cannot
	// give one a browser opens.
	URL string
	// Acceptable says the source is one an operator configured, so a person
	// may accept the template as a golden path.
	Acceptable bool
	// ObservedAt is when the source read the template.
	ObservedAt *time.Time
}

// RawFactRef names a service a [RawServiceFact] depends on, matched like the
// fact itself.
type RawFactRef struct {
	// Name is the source's name for the service.
	Name string
	// ServiceKey, when set, is the only service the reference may match.
	ServiceKey ServiceKey
	// Entity is the source's own reference for the service.
	Entity string
}

// RawInferredService is one workload a source read (Kind is Deployment,
// StatefulSet, DaemonSet or CronJob): only its image, ports and probes, each
// with the object path it was read from. It never carries environment values,
// Secrets or ConfigMaps.
type RawInferredService struct {
	Namespace  string          `json:"namespace"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Image      *InferredValue  `json:"image,omitempty"`
	Ports      []InferredPort  `json:"ports,omitempty"`
	Probes     []InferredProbe `json:"probes,omitempty"`
	ObservedAt *time.Time      `json:"observedAt,omitempty"`
	// CreatedAt is when the workload was created (its
	// metadata.creationTimestamp), nil when the source does not know it.
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// Group, Repositories and RepositoryUnknown are set by the source from
	// the workload's own metadata and the GitOps objects it read; Build
	// validates them and fills Key and Label. They are not part of the wire
	// record a target snapshot carries.
	Group             *InferredGroup       `json:"-"`
	Repositories      []InferredRepository `json:"-"`
	RepositoryUnknown string               `json:"-"`
	// RepositoryUnknownFrom is the namespace and kind of each GitOps object
	// RepositoryUnknown was read from: a restricted view shows the reason only
	// to a viewer allowed to list every one of them, and an empty namespace to
	// none. Empty when the reason names no object.
	RepositoryUnknownFrom []WorkloadScope `json:"-"`
}

// WorkloadRef names the workload a Kubernetes target runs as.
type WorkloadRef struct {
	Kind, Name string
}

// RawExternalUse is one external system a source recognized in the
// configuration of the service (Service, Domain) running in Namespace.
// Identity is the external identity it was classified as, by the catalog
// whose digest is Catalog; Kind says where it was found (env, configMap,
// secretKeyRef, secretRef, externalNameService, selectorlessService,
// serviceEntry or ciliumFQDN, a Cilium egress allow-list rule) and Where names the object and key, never a value. Host is
// the normalized host and port, empty when only a key name matched.
type RawExternalUse struct {
	Service, Domain, Namespace string
	Identity, Class            string
	Confidence, Kind, Where    string
	Host, Entry, Catalog       string
	ObservedAt                 *time.Time
}

// RawProvisioning is one provider report about the resource a consumer
// revision (Service, Domain, Digest) asked for through its configuration
// reference or dependency (Kind "configuration" or "dependency", Name). Phase,
// Provider, ExternalRef and Environment are set only for an Observed outcome.
// Verified is true only for records accepted through signed envelopes.
type RawProvisioning struct {
	Service, Domain, Digest      string
	Kind, Name, Environment      string
	Provider, ExternalRef, Phase string
	Outcome                      string
	Producer, ProducerKeyID      string
	ObservedAt                   time.Time
	AcceptedAt                   *time.Time
	Verified                     bool
}

// RawExtension is one producer's report on the extension capability Ref that
// the consumer revision (Service, Domain, Digest) declares, under the report
// Subject. Present and
// Details are set only for an Observed outcome. Cluster is the cluster of the
// Evidence Server that accepted it. Verified is true only for records accepted
// through signed envelopes; [Build] drops every other one.
type RawExtension struct {
	Subject       string          `json:"subject,omitempty"`
	Service       string          `json:"service"`
	Domain        string          `json:"domain,omitempty"`
	Digest        string          `json:"digest"`
	Ref           string          `json:"ref"`
	Present       *bool           `json:"present,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
	Outcome       string          `json:"outcome"`
	Producer      string          `json:"producer"`
	ProducerKeyID string          `json:"producerKeyId,omitempty"`
	ObservedAt    time.Time       `json:"observedAt"`
	AcceptedAt    *time.Time      `json:"acceptedAt,omitempty"`
	Cluster       *Cluster        `json:"cluster,omitempty"`
	Verified      bool            `json:"verified,omitempty"`
}

// RawUsage is one provider report on how much of the resource a consumer
// revision asked for is used and what it costs: [RawProvisioning] with Metrics
// and Cost in place of Phase. Provider, ExternalRef, Metrics and Cost are set
// only for an Observed outcome. Verified is true only for records accepted
// through signed envelopes.
type RawUsage struct {
	Service, Domain, Digest string
	Kind, Name, Environment string
	Provider, ExternalRef   string
	Metrics                 []evidence.UsageMetric
	Cost                    *evidence.UsageCost
	Outcome                 string
	Producer, ProducerKeyID string
	ObservedAt              time.Time
	AcceptedAt              *time.Time
	Verified                bool
}

// ObservedEdge is one runtime-observed caller→provider dependency. From and To are
// raw observed (e.g. OTel service.name) identities, not yet Pacto service keys.
type ObservedEdge struct {
	From  string
	To    string
	Count int
	// FirstSeen/LastSeen bound the observation window this edge was witnessed in
	// (zero when the source carries no timestamps). They flow into the observed
	// relationship so a stale offline sample is never presented as current truth.
	FirstSeen time.Time
	LastSeen  time.Time
}

// RawRevision is what a definition or baseline source knows about a resolved
// contract revision. Bundle carries the parsed contract and its FS; the
// resolved identity (requestedRef → resolvedRef + digest) is preserved.
type RawRevision struct {
	Bundle *contract.Bundle
	// Domain is the logical-service domain (registry/org/platform scope) this
	// revision belongs to. Empty means the default domain. It comes from the
	// source, not the contract, so the same service name in two registries or
	// organizations stays distinct.
	Domain       string
	RequestedRef string
	ResolvedRef  string
	Digest       string
	Lock         *lock.Lock
	FetchedAt    *time.Time
	// Document is optional: the bundle's document as the loader already decoded
	// it with contract.ParseDocument. Build validates it instead of decoding the
	// YAML again. It must be the decode of the same bytes as the Bundle.
	Document *contract.Document
	// GuaranteesUnseen is why the source could not read this revision's
	// interface guarantees and dependency requirements, or "" when it could.
	// A revision rebuilt from a status an older operator wrote records
	// neither, so a requirement on it is unknown rather than unmet.
	GuaranteesUnseen string
	// Provenance is what a registry source found attached to the bundle digest
	// (signature and referrers), or nil when the source does not check.
	Provenance *BundleProvenance
	// Baseline is the digest of the published revision this unpublished one
	// was planned from (pacto ci), or "". An inferred service.name that
	// baseline declares is the same service, not a competing claim.
	Baseline string
	// Contributor is set only by a source that read the revision from a
	// producer's contribution repository and verified its signature. Build
	// reads such a revision as a contributed, inferred service.
	Contributor *Contributor
}

// Contributor is the producer whose contribution repository a revision was
// read from. KeyID is the key that verified its signature; ReadAt is when the
// source read it and AsOf the signed manifest's created time, zero when
// unknown. NewerRefused is the newest version of the service the source
// refused before reading this older one, empty when it read the newest.
type Contributor struct {
	Producer, KeyID string
	ReadAt, AsOf    time.Time
	NewerRefused    string
	// Renews is the digest of the record this one repeats, re-pushed only to
	// renew its age (contract.AnnotationRenews), or "".
	Renews string
	// Cluster is the cluster whose installation advertised the repository
	// the record was read from; empty for a producer the trust config grants.
	Cluster string
}

// OfferingObservation is what an installation's inferred-offering producer
// advertised it observed: the slugs of its last complete run and when that
// run finished, why the newest run stopped short and why Pacto could not
// read its records. Drift reads it so a head that stopped moving never
// reads as in sync.
type OfferingObservation struct {
	Producer, Cluster string
	Observed          []string
	AsOf              *time.Time
	// Partial is why the producer's newest run stopped short; Unread why
	// its records could not all be read.
	Partial, Unread string
}

// OwnerView is raw as the owner rule reads it, before Build: its service,
// version, digest, owner, inferred record (read as Build reads it) and
// provenance, shared with raw so a verdict stamped on it lands on raw. A
// revision with no digest or contract has no view (false): nothing attached
// to it can be verified.
func OwnerView(raw RawRevision) (ContractRevision, bool) {
	if raw.Digest == "" || raw.Bundle == nil || raw.Bundle.Contract == nil {
		return ContractRevision{}, false
	}
	c := raw.Bundle.Contract
	key := NewServiceKeyDomain(raw.Domain, c.Service.Name)
	rec, _ := inferredRecord(raw.Bundle.FS, c, "")
	return ContractRevision{Key: NewRevisionKey(key, raw.Digest), Service: c.Service.Name, Domain: raw.Domain, ServiceKey: key,
		Version: c.Service.Version, Digest: raw.Digest, ResolvedRef: raw.ResolvedRef, Owner: c.Service.Owner, Record: rec,
		Provenance: raw.Provenance}, true
}

// RawTarget is what an observer source knows about a concrete deployed instance.
type RawTarget struct {
	Scope        string
	Kind         string
	Name         string
	Labels       map[string]string
	Service      string
	Domain       string
	RequestedRef string
	ResolvedRef  string
	Digest       string
	Compliance   string
	Owner        contract.Owner
	// BusinessDomain is the service.domain the source read from the contract the
	// target runs. BusinessDomainKnown says the source reads the domain at all, so
	// an empty BusinessDomain means that contract declares none; without it, and
	// with no domain, the domain is unknown. A value that is not a domain slug is
	// dropped and reads as unknown.
	BusinessDomain      string
	BusinessDomainKnown bool
	// Offering is the service.offering the source read from the contract the
	// target runs; a value that is not a slug is dropped. Summary is that
	// contract's service.summary.
	Offering        string
	Summary         string
	Findings        []finding.Finding
	Coverage        *Coverage
	Readiness       *readiness.Result
	ObservedRuntime map[string]any
	EvidenceAt      *time.Time
	ReconciledAt    *time.Time
	Limitations     []Limitation
	// Cluster is the cluster of this one record, overriding
	// [Collection.Cluster]. Evidence records set it to the cluster that accepted
	// them, so two servers listing one record report one cluster.
	Cluster *Cluster
	// Workload is the workload this target runs as, nil when the source does
	// not know it. [Build] never infers a service from that workload.
	Workload *WorkloadRef
	// CreatedAt is when the declaration that produced this target was
	// created (a Pacto's metadata.creationTimestamp), nil when unknown.
	CreatedAt *time.Time
	// Link is set when the target is a workload the operator linked to a
	// published contract with no Pacto.
	Link *ObservedLink
}

// MemorySource is an in-memory [Source]. It is the substrate for tests, the
// demo fleet, and any caller that has already gathered records by other means.
type MemorySource struct {
	id      string
	kind    string
	col     *Collection
	err     error
	collect func(ctx context.Context) (*Collection, error)
}

// NewMemorySource returns a source that always yields the given collection.
func NewMemorySource(id, kind string, col *Collection) *MemorySource {
	return &MemorySource{id: id, kind: kind, col: col}
}

// NewFailingSource returns a source whose Collect always fails with err. It
// models an unavailable environment (unreachable registry, disconnected
// cluster) for testing partial-fleet semantics.
func NewFailingSource(id, kind string, err error) *MemorySource {
	return &MemorySource{id: id, kind: kind, err: err}
}

// ID implements [Source].
func (s *MemorySource) ID() string { return s.id }

// Kind implements [Source].
func (s *MemorySource) Kind() string { return s.kind }

// Collect implements [Source].
func (s *MemorySource) Collect(ctx context.Context) (*Collection, error) {
	if s.collect != nil {
		return s.collect(ctx)
	}
	if s.err != nil {
		return nil, s.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.col == nil {
		return &Collection{}, nil
	}
	return s.col, nil
}
