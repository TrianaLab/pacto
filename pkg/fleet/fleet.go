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
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/readiness"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// ServiceKey is the stable identity of a logical service (its name).
type ServiceKey string

// RevisionKey is the stable identity of an immutable contract revision:
// ServiceKey@<contentID>, where contentID is the source's manifest digest or,
// when none was pinned, a digest derived from the revision's content. A mutable
// OCI tag or a bare version string is never a revision identity, so two revisions
// that merely share a tag or version can never collide on one key.
type RevisionKey string

// TargetKey is the stable identity of an operational target: scope/kind/name.
type TargetKey string

// NewServiceKey returns the canonical key for a service in the default (empty)
// domain — the ergonomic single-domain case. It is exactly
// NewServiceKeyDomain("", name).
func NewServiceKey(name string) ServiceKey {
	return NewServiceKeyDomain("", name)
}

// NewServiceKeyDomain returns the canonical logical-service key for a
// (domain, name) pair. A logical service is identified by BOTH a domain (the
// registry, org or platform scope it belongs to) and its name, so the same name
// in two different domains is never silently merged. The default (empty) domain
// encodes to just the escaped name (ergonomic); a non-empty domain encodes as
// "domain/name" with each component escaped, so the single unescaped "/" is
// always the domain separator and the key round-trips through [ParseServiceKey].
func NewServiceKeyDomain(domain, name string) ServiceKey {
	if domain == "" {
		return ServiceKey(escapeKeyPart(name))
	}
	return ServiceKey(escapeKeyPart(domain) + "/" + escapeKeyPart(name))
}

// ParseServiceKey decodes a service key back into its domain and name. A key with
// no unescaped "/" is a default-domain (empty domain) key.
func ParseServiceKey(k ServiceKey) (domain, name string) {
	s := string(k)
	if i := indexUnescapedSlash(s); i >= 0 {
		return unescapeKeyPart(s[:i]), unescapeKeyPart(s[i+1:])
	}
	return "", unescapeKeyPart(s)
}

// Replacement records that a declared service took over a service Pacto
// inferred from a workload, as the cluster showed it: the workload existed
// before the declaration that links them.
type Replacement struct {
	From ServiceKey `json:"from" doc:"The key of the inferred service that was replaced."`
	To   ServiceKey `json:"to" doc:"The key of the declared service that replaced it."`
	Link string     `json:"link" enum:"pacto-target" doc:"How the two are linked: pacto-target when a Pacto target runs as the workload."`
	// Target is the target that links them.
	Target TargetKey `json:"target,omitempty" doc:"The Pacto target that runs as the workload."`
	// At is never nil on a recorded replacement.
	At *time.Time `json:"at" doc:"When the Pacto that links them was created."`
	// workload is the replaced workload, which Restrict checks like an
	// inferred service.
	workload workloadKey
}

// ObservedResolution classifies how a raw observed (e.g. OpenTelemetry
// service.name) name mapped onto the fleet's domain-qualified identities.
type ObservedResolution int

const (
	// ObservedResolved: the name matched exactly one fleet service.
	ObservedResolved ObservedResolution = iota
	// ObservedUnknown: the name matched no fleet service.
	ObservedUnknown
	// ObservedAmbiguous: the name matched more than one service (same name in
	// different domains) — it cannot be attributed to a single domain.
	ObservedAmbiguous
	// ObservedInferred: the name matched exactly one service, and Pacto only
	// infers it from a workload no contract describes. The key is valid, but the
	// service is never a contracted endpoint: a caller stays uncontracted.
	ObservedInferred
)

// ObservedNameResolver returns a resolver that maps a raw observed service name
// to its UNIQUE domain-qualified [ServiceKey]. A name matching zero or multiple
// services is never coerced to the default domain — the resolution status says so
// and the returned key is only valid when [ObservedResolved] or
// [ObservedInferred]. The name index is
// built once, so repeated resolution is O(1). The name of a workload a Pacto
// target runs as resolves to that target's declared service. This is the single place observed
// telemetry crosses into Pacto identity, so observed evidence can never
// corroborate or affect a same-named service in another domain.
func (s *FleetSnapshot) ObservedNameResolver() func(string) (ServiceKey, ObservedResolution) {
	byName := map[string][]ServiceKey{}
	for k, svc := range s.Services {
		byName[svc.Name] = append(byName[svc.Name], k)
	}
	return func(name string) (ServiceKey, ObservedResolution) {
		if r, ok := s.replacedNames[name]; ok {
			return r.to, ObservedResolved
		}
		switch ks := byName[name]; len(ks) {
		case 1:
			if s.Services[ks[0]].Inferred() {
				return ks[0], ObservedInferred
			}
			return ks[0], ObservedResolved
		case 0:
			return "", ObservedUnknown
		default:
			return "", ObservedAmbiguous
		}
	}
}

// replacedName is the declared service a replaced workload name resolves to,
// and that workload, so a restricted view drops what it may not see.
type replacedName struct {
	to       ServiceKey
	workload workloadKey
}

// indexUnescapedSlash returns the index of the first "/" (component escaping
// guarantees any real "/" in a component was encoded as %2F, so the first raw
// "/" is the domain separator), or -1.
func indexUnescapedSlash(s string) int {
	return strings.IndexByte(s, '/')
}

// NewRevisionKey builds the immutable revision key: the domain-qualified
// [ServiceKey] joined with a content identity — a manifest digest when the source
// pinned one, otherwise a content digest derived by [Build]. A mutable OCI tag or
// a bare version string is never a revision identity.
func NewRevisionKey(serviceKey ServiceKey, contentID string) RevisionKey {
	return RevisionKey(string(serviceKey) + "@" + contentID)
}

// NewTargetKey builds a collision-safe target key from its scope, kind, and
// name. Each component is percent-escaped (so an embedded "/" or "%" can never
// forge a component boundary) and the three components are joined with "/". The
// encoding is canonical (see the round-trip test); a separate human display string
// is available via [TargetRecord.DisplayName]. Two distinct (scope,kind,name)
// triples can never map to the same key. [NewClusterTargetKey] adds a leading
// cluster component, and [ParseTargetKey] decodes either form.
func NewTargetKey(scope, kind, name string) TargetKey {
	return TargetKey(escapeKeyPart(scope) + "/" + escapeKeyPart(kind) + "/" + escapeKeyPart(name))
}

// UnknownClusterKeyPart is the cluster component of a target with no known
// cluster in a snapshot that holds more than one cluster. [ValidClusterID]
// never accepts it, so it can never name a real cluster, and the target keeps a
// key of its own: its 3-part key gathers every cluster's namesake.
const UnknownClusterKeyPart = "_"

// NewClusterTargetKey builds the key of a target in a snapshot that holds more
// than one cluster: the escaped cluster id, "/", then [NewTargetKey]. An empty
// cluster yields the 3-part [NewTargetKey], so a snapshot with zero or one
// cluster keeps today's keys.
func NewClusterTargetKey(cluster, scope, kind, name string) TargetKey {
	if cluster == "" {
		return NewTargetKey(scope, kind, name)
	}
	return TargetKey(escapeKeyPart(cluster) + "/" + string(NewTargetKey(scope, kind, name)))
}

// ParseTargetKey decodes a target key built by [NewTargetKey] (cluster "") or
// [NewClusterTargetKey]. It accepts exactly 3 or 4 canonically escaped
// components and rejects a 4-part key with an empty cluster component, which
// [NewClusterTargetKey] never emits.
func ParseTargetKey(k TargetKey) (cluster, scope, kind, name string, ok bool) {
	parts := strings.Split(string(k), "/")
	if len(parts) != 3 && len(parts) != 4 {
		return "", "", "", "", false
	}
	for _, p := range parts {
		if escapeKeyPart(unescapeKeyPart(p)) != p {
			return "", "", "", "", false
		}
	}
	if len(parts) == 4 {
		if parts[0] == "" {
			return "", "", "", "", false
		}
		cluster, parts = unescapeKeyPart(parts[0]), parts[1:]
	}
	return cluster, unescapeKeyPart(parts[0]), unescapeKeyPart(parts[1]), unescapeKeyPart(parts[2]), true
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

// Provenance distinguishes how a relationship became known: "declared" (from a
// contract), "observed" (folded from runtime telemetry, see
// foldObservedRelationships) or "inferred" (an external system a source
// recognized in a service's configuration, see foldExternalUses, a service
// dependency a revision's inferred record names, see revisionInferredEdges,
// or a runtime call one of whose ends no contract describes: an inferred
// service, see foldObservedRelationships; inferred service edges live in
// InferredRelationships). Inferred also marks a provider row Pacto detected
// in a cluster (see pkg/detect), a proposal that never turns anything on.
// They are
// kept separate so neither an observed nor an inferred edge is conflated with
// declared intent, and an inferred edge never enters an index a gate reads.
const (
	ProvenanceDeclared = "declared"
	ProvenanceObserved = "observed"
	ProvenanceInferred = "inferred"
	// ProvenanceDeclaredObserved is the combined provenance a MERGED edge carries when
	// it is backed by BOTH a declaration and an observation (see edgeProvenance). It is a
	// distinct finite value the neighborhood projection emits on the wire, so every
	// provenance enum must declare it or the generated SDK declares a value impossible.
	ProvenanceDeclaredObserved = ProvenanceDeclared + "+" + ProvenanceObserved
)

// OwnerProvenance says where ServiceRecord.Owner comes from. The precedence is
// declared > attested > declared-via-target > inferred: a published revision's
// owner wins, then an owner read from the repository at a commit a verified CI
// identity signed, then the owner a target's observing source read from the
// contract it runs. Owner never holds an inferred value, because approvals and
// every ownership answer read it: inferred means no declared or attested owner
// exists while an inferred one is held in a separate field, so Owner stays
// empty. The inferred tier is set only together with the field that holds that
// value.
const (
	OwnerProvenanceDeclared          = "declared"
	OwnerProvenanceDeclaredViaTarget = "declared-via-target"
	OwnerProvenanceAttested          = "attested"
	OwnerProvenanceInferred          = "inferred"
)

// Reconciliation classifies a DECLARED dependency edge against the snapshot's
// observation data. It is an explicit backend fact — never inferred from name
// resolution or whether the provider happens to be deployed. Empty for
// observed/inferred edges and non-dependency edges.
const (
	// ReconciliationMatched: declared AND corroborated by an observed edge.
	ReconciliationMatched = "matched"
	// ReconciliationDeclaredNotObserved: observation data exists but did not
	// witness this edge (dormant, or the window was too short — not proof it is dead).
	ReconciliationDeclaredNotObserved = "declared-not-observed"
	// ReconciliationInsufficient: no observation data at all, so the edge cannot be
	// reconciled. A declared edge is NEVER called reconciled without observed data.
	ReconciliationInsufficient = "insufficient"
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

// CanonicalStatuses returns the complete canonical status vocabulary, sorted. It
// enumerates the same table ValidStatus accepts from, so a consumer comparing its
// own copy of the vocabulary against this one is comparing against exactly what
// the fleet validates, and a status added here without a severity would not be
// canonical at all.
func CanonicalStatuses() []string { return slices.Sorted(maps.Keys(severityRank)) }

// SchemaVersion is the version of the fleet snapshot/query wire model. It is
// carried on every snapshot and query answer so consumers can detect model
// changes.
const SchemaVersion = "pacto.dev/fleet/v1"

// MaxProvisioningClockSkew is how far a provisioning report's observedAt may
// run ahead of the time it was accepted. A report dated further in the future
// is rejected, so a producer cannot outrank later reports by lying about time.
const MaxProvisioningClockSkew = 5 * time.Minute

// MaxProvisioningRecords bounds the provisioning reports a snapshot keeps; the
// newest win.
const MaxProvisioningRecords = 10000

// MaxUsageRecords bounds the usage reports a snapshot keeps; the newest win.
const MaxUsageRecords = 10000

// Limitation codes explain, in a structured way, why an answer is incomplete or
// why a record could not be fully established. Codes are stable and typed so
// agents branch on them; the human message is advisory.
const (
	LimitationNoSourcesConfigured = "NO_SOURCES_CONFIGURED"
	LimitationSourceUnavailable   = "SOURCE_UNAVAILABLE"
	LimitationSourceStale         = "SOURCE_STALE"
	LimitationSourcePartial       = "SOURCE_PARTIAL"
	// LimitationK8sNamespaceScoped: a Kubernetes source reads a list of
	// namespaces, so a service in any other namespace is unknown.
	LimitationK8sNamespaceScoped = "K8S_NAMESPACE_SCOPED"
	// LimitationK8sNamespaceUnreadable: a namespace a Kubernetes source was told
	// to read could not be read; what runs there is unknown.
	LimitationK8sNamespaceUnreadable = "K8S_NAMESPACE_UNREADABLE"
	LimitationUnresolvedDep          = "UNRESOLVED_DEPENDENCY"
	// LimitationRangeUnsatisfied: a declared dependency resolves to a provider
	// whose version in the fleet is outside the consumer's compatibility range.
	LimitationRangeUnsatisfied    = "DEPENDENCY_RANGE_UNSATISFIED"
	LimitationEvidenceMissing     = "EVIDENCE_MISSING"
	LimitationSourceRecordInvalid = "SOURCE_RECORD_INVALID"
	// LimitationPactoCRDNotInstalled: the cluster serves no Pacto CRD, so a
	// Kubernetes source reads no declared target there; its workloads show as
	// inferred services.
	LimitationPactoCRDNotInstalled = "PACTO_CRD_NOT_INSTALLED"
	// LimitationInferredServicesCapped: a source reported more than
	// MaxInferredServices workloads; the rest were not read.
	LimitationInferredServicesCapped = "INFERRED_SERVICES_CAPPED"
	// LimitationDeclarationUnverified: Pacto could not read the Pacto targets
	// of an inferred service's namespace, so a contract may describe it.
	LimitationDeclarationUnverified = "DECLARATION_UNVERIFIED"
	// LimitationServiceInferred: the answer is about an inferred service, which
	// no contract describes, so who depends on it is unknown and only its
	// observed calls are known.
	LimitationServiceInferred = "SERVICE_INFERRED"
	// LimitationServiceGroupAmbiguous: two groups of one rule claim an
	// inferred service's workload, so it stays ungrouped; Pacto never merges.
	LimitationServiceGroupAmbiguous = "SERVICE_GROUP_AMBIGUOUS"
	// LimitationServiceNameCollision: two or more folders or references claim
	// one service name and at least one only inferred it. Build drops every
	// inferred claimant, so a guessed name never joins another service.
	LimitationServiceNameCollision = "SERVICE_NAME_COLLISION"
	// LimitationInferredReplacementAmbiguous: a Pacto target names a workload
	// Pacto infers a service from, but more than one declared service targets
	// it or the target's cluster is unknown, so the inferred service stays.
	LimitationInferredReplacementAmbiguous = "INFERRED_REPLACEMENT_AMBIGUOUS"
	LimitationDuplicateSourceID            = "DUPLICATE_SOURCE_ID"
	LimitationRevisionUnresolved           = "REVISION_IDENTITY_UNRESOLVED"
	LimitationRevisionAmbiguous            = "REVISION_LINK_AMBIGUOUS"
	// LimitationRevisionDocConflict: two sources contributed the same immutable
	// revision but disagree about its documents (a different set of paths, or the
	// same path with different content). There is no honest way to pick a winner —
	// whichever one served would make the revision's documents depend on source
	// ordering — so the revision's documents become unreadable and say why.
	LimitationRevisionDocConflict = "REVISION_DOCUMENT_CONFLICT"
	// LimitationRevisionLockConflict: two sources contributed the same immutable
	// revision but disagree about its pacto.lock. The lock decides which bundle a
	// declared dependency or reference resolves to, so it is dropped rather than
	// picked by arrival order.
	LimitationRevisionLockConflict = "REVISION_LOCK_CONFLICT"
	LimitationTargetRefConflict    = "TARGET_REFERENCE_CONFLICT"
	LimitationTargetFieldConflict  = "TARGET_FIELD_CONFLICT"
	LimitationOwnerConflict        = "OWNER_CONFLICT"
	// LimitationDomainConflict: no contract revision of a service declares a
	// business domain and the sources observing its targets report different ones,
	// so its domain is unknown rather than picked.
	LimitationDomainConflict = "DOMAIN_CONFLICT"
	// LimitationOfferingConflict: no contract revision of a service declares a
	// platform offering and the sources observing its targets report different
	// ones, so it has none rather than one picked.
	LimitationOfferingConflict = "OFFERING_CONFLICT"
	// LimitationObservedIdentityUnresolved: a runtime-observed endpoint name could
	// not be mapped to exactly one domain-qualified fleet service.
	LimitationObservedIdentityUnresolved = "OBSERVED_IDENTITY_UNRESOLVED"
	// LimitationSBOMUnreadable: the bundle ships an SBOM file that could not be
	// parsed. The revision therefore carries no SBOM summary, and that absence must
	// NOT be read as "this revision declares no software inventory".
	LimitationSBOMUnreadable = "SBOM_UNREADABLE"
	// The four projection-scope codes below state what a graph perspective cannot
	// know. They belong here rather than at their emit sites because an agent is
	// told to branch on Code, and a code it can only learn by reading Go source is
	// not part of the vocabulary this const block promises.
	//
	// LimitationTargetRevisionUnresolved: the revision a deployment runs is not
	// authoritatively known, so that revision's declared dependencies are not
	// attributed to the target.
	LimitationTargetRevisionUnresolved = "TARGET_REVISION_UNRESOLVED"
	// LimitationDependentsServiceScoped: inbound dependencies are known only at
	// logical-service scope; Pacto does not observe which consumers routed to one
	// concrete deployment.
	LimitationDependentsServiceScoped = "DEPENDENTS_LOGICAL_SERVICE_SCOPED"
	// LimitationObservedNotRevisionScoped: runtime observation is recorded per
	// service, so it is never attributed to a specific revision edge.
	LimitationObservedNotRevisionScoped = "OBSERVED_NOT_REVISION_SCOPED"
	// LimitationObservedNotTargetScoped: runtime observation establishes
	// service-to-service traffic, not which concrete provider target served it.
	LimitationObservedNotTargetScoped = "OBSERVED_NOT_TARGET_SCOPED"
	// LimitationNeighborhoodUnavailable: the relationships of a service could not
	// be computed, so its relationship list is empty for that reason alone.
	LimitationNeighborhoodUnavailable = "NEIGHBORHOOD_UNAVAILABLE"
	// LimitationProvisioningConflict: two reporters disagree about the
	// provisioning phase of one use in one environment, so its status is Unknown.
	LimitationProvisioningConflict = "PROVISIONING_CONFLICT"
	// LimitationProvisioningTruncated: the sources reported more than
	// MaxProvisioningRecords provisioning reports; the oldest were dropped.
	LimitationProvisioningTruncated = "PROVISIONING_TRUNCATED"
	// LimitationProvisioningUnmatched: provisioning reports name no contract
	// revision in the snapshot, or a use that revision does not resolve.
	LimitationProvisioningUnmatched = "PROVISIONING_UNMATCHED"
	// LimitationProvisioningEnvironmentScoped: a phase list narrowed to one
	// environment leaves out the consumers that have no provisioning status
	// there, because their current revision does not declare it.
	LimitationProvisioningEnvironmentScoped = "PROVISIONING_ENVIRONMENT_SCOPED"
	// LimitationUsageConflict: two reporters disagree about the usage or
	// cost of one resource; its consumption reads unknown.
	LimitationUsageConflict = "USAGE_CONFLICT"
	// LimitationUsageTruncated: the sources reported more than
	// MaxUsageRecords usage reports; the oldest were dropped.
	LimitationUsageTruncated = "USAGE_TRUNCATED"
	// LimitationUsageUnmatched: usage reports name no contract revision in
	// the snapshot or a use it resolves; they were dropped.
	LimitationUsageUnmatched = "USAGE_UNMATCHED"
	// LimitationFreshnessNotEvaluated: the snapshot was built with a zero
	// freshness window (--freshness 0), so no target's evidence age was checked
	// and no target is marked stale. Stale counts read zero because they were not
	// computed. It does not change completeness, which states source health.
	LimitationFreshnessNotEvaluated = "FRESHNESS_NOT_EVALUATED"
	// LimitationExternalCatalogUnavailable: an external catalog the caller
	// configured could not be read, so external systems it would classify are
	// joined by name only. It makes the answer partial.
	LimitationExternalCatalogUnavailable = "EXTERNAL_CATALOG_UNAVAILABLE"
	// LimitationInferredRecordUnreadable: a revision at pactoVersion 2.5 or
	// later ships a pacto.inferred.yaml that cannot be parsed or whose cited
	// files do not match the bundle, so its inferred dependencies are unknown,
	// not absent. It makes the answer partial.
	LimitationInferredRecordUnreadable = "INFERRED_RECORD_UNREADABLE"
	// LimitationDeclarationDraftIgnored: a bundle whose inferred record is a
	// declaration draft (state: draft) is not a version, so the revision is
	// left out. Completeness is unchanged.
	LimitationDeclarationDraftIgnored = "DECLARATION_DRAFT_IGNORED"
	// LimitationDeclarationSignerUnverified: a declared revision carries
	// cluster facts but no signature this reader verified names its asker as
	// a person, so the cluster facts, coverage and hidden counts are dropped.
	// It makes the answer partial.
	LimitationDeclarationSignerUnverified = "DECLARATION_SIGNER_UNVERIFIED"
	// LimitationDeclarationSignerNotAllowed: a declared revision is signed
	// by its asker, but the reader's installation signer policy does not
	// allow that person or could not be read, so its cluster facts, coverage
	// and hidden counts are dropped. It makes the answer partial.
	LimitationDeclarationSignerNotAllowed = "DECLARATION_SIGNER_NOT_ALLOWED"
	// LimitationExternalCap: the answer names MaxExternalNodes external
	// systems and folds the least-consumed rest into one other-egress node.
	// It makes the answer partial.
	LimitationExternalCap = "EXTERNAL_CAP"
	// LimitationClusterUnknown: targets carry no cluster id while another source
	// names a cluster, so they cannot be told apart from another cluster's
	// targets. In a snapshot with several clusters, or when such a target merged
	// into a clustered one, the source that delivered them is partial; otherwise
	// the limitation is informational.
	LimitationClusterUnknown = "CLUSTER_UNKNOWN"
	// LimitationClusterConflict: two evidence sources report one record (same
	// producer, subject and times) as accepted in two different clusters. Both
	// targets are kept and the later source is partial.
	LimitationClusterConflict = "CLUSTER_CONFLICT"
	// LimitationExternalUseUnattributed: a source reported external uses of
	// a service this snapshot does not hold, or with an identity that is not
	// one; they were dropped.
	LimitationExternalUseUnattributed = "EXTERNAL_USE_UNATTRIBUTED"
	// LimitationExternalCatalogMismatch: a source classified external uses
	// with a catalog other than the one this snapshot classifies with, so
	// one external system may join evidence from two catalog versions.
	LimitationExternalCatalogMismatch = "EXTERNAL_CATALOG_MISMATCH"
	// LimitationExternalsUnreported: a target's source cannot say which
	// external systems it uses (an older operator, partial coverage or a
	// stale status), so no external answer can rule it out as a consumer.
	LimitationExternalsUnreported = "EXTERNALS_UNREPORTED"
	// LimitationTargetSnapshotUnverified: a target snapshot was read with no
	// delivery trust config, so nothing vouches for what it says.
	LimitationTargetSnapshotUnverified = "TARGET_SNAPSHOT_UNVERIFIED"
	// LimitationTargetSnapshotUnscoped: a target snapshot verified only under
	// a key not scoped to its cluster, which another cluster's recorder could
	// hold.
	LimitationTargetSnapshotUnscoped = "TARGET_SNAPSHOT_UNSCOPED"
	// LimitationTargetSnapshotStale: a target snapshot is older than the
	// freshness bound, so its cluster stopped publishing or the tag was
	// re-pointed to an older one.
	LimitationTargetSnapshotStale = "TARGET_SNAPSHOT_STALE"
	// LimitationTargetSnapshotInvalid: a target snapshot is missing, does not
	// verify, names another cluster than its tag or claims a future asOf; it
	// was not used.
	LimitationTargetSnapshotInvalid = "TARGET_SNAPSHOT_INVALID"
	// LimitationTargetSnapshotBound: a target snapshot read hit a bound (too
	// many cluster tags, a snapshot too large or the read timeout).
	LimitationTargetSnapshotBound = "TARGET_SNAPSHOT_BOUND"
	// LimitationTargetSnapshotScoped: the publishing operator watches only
	// some namespaces, so a namespace it did not read is unknown, never empty.
	LimitationTargetSnapshotScoped = "TARGET_SNAPSHOT_SCOPED"
	// LimitationClusterHidden: a restricted view hides every service of one
	// reviewed cluster, because the reader may read no namespace there, the
	// review failed or nobody can be reviewed there. Source is the cluster id.
	// It makes the answer partial.
	LimitationClusterHidden = "CLUSTER_HIDDEN"
	// LimitationClusterFilterUnknown: a list filtered by a cluster id that no
	// cluster of the answer has, such as a display name or a retired cluster,
	// so its empty list says nothing about that cluster. Completeness is
	// unchanged.
	LimitationClusterFilterUnknown = "CLUSTER_FILTER_UNKNOWN"
)

// Cluster name sources: how a [Cluster] name was found. They mirror the values
// of pkg/kubecluster, which pkg/fleet cannot import. The live Kubernetes source
// sets node-label, context or uid; a target state file sets file.
const (
	ClusterNameFromNodeLabel = "node-label"
	ClusterNameFromContext   = "context"
	ClusterNameFromFile      = "file"
	ClusterNameFromUID       = "uid"
)

// Cluster relations of a target (see [TargetRecord.ClusterRelation]).
const (
	// ClusterRelationRunsIn: a Kubernetes source read the target in the cluster.
	ClusterRelationRunsIn = "runs-in"
	// ClusterRelationReportedThrough: the cluster only reported the target, such
	// as the cluster whose Evidence Server accepted its evidence.
	ClusterRelationReportedThrough = "reported-through"
)

// Cluster identifies one Kubernetes cluster a source reads or reports from.
type Cluster struct {
	// ID is the kube-system namespace UID: the cluster identity.
	ID string `json:"id" doc:"The kube-system namespace UID: the identity of the cluster."`
	// Name is a display name only, never an identity.
	Name string `json:"name,omitempty" doc:"A display name for the cluster; never an identity."`
	// NameSource says how Name was found.
	NameSource string `json:"nameSource,omitempty" enum:"node-label,context,file,uid" doc:"How the name was found: a node label, the kubeconfig context name, a target state file or the first characters of the UID."`
	// ReportedBy is the id of the source that reported Name, empty when this
	// process read the name itself.
	ReportedBy string `json:"reportedBy,omitempty" doc:"The source that reported the name; empty when this process read it itself."`
}

// ClusterRecord is one cluster a snapshot knows, with every source that
// reported its id.
type ClusterRecord struct {
	Cluster
	Sources []string `json:"sources" doc:"Every source that reported this cluster id."`
	// Access says why a restricted view shows nothing of this cluster; empty
	// when it shows what the reader may read.
	Access string `json:"access,omitempty" enum:"no-access,review-failed,not-reviewable" doc:"Why a restricted view shows nothing of this cluster: you may read no namespace there, the review failed or took too long, or nobody can be reviewed there. Empty when the view shows what you may read."`
}

// Cluster access marks of a restricted view (see [ClusterRecord.Access]).
const (
	ClusterNoAccess      = "no-access"
	ClusterReviewFailed  = "review-failed"
	ClusterNotReviewable = "not-reviewable"
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
	Status             SourceStatus `json:"status" enum:"available,partial,stale,unavailable"`
	LastSuccessfulSync *time.Time   `json:"lastSuccessfulSync,omitempty"`
	ObservedAt         *time.Time   `json:"observedAt,omitempty"`
	Error              *SourceError `json:"error,omitempty"`
	RevisionCount      int          `json:"revisionCount"`
	TargetCount        int          `json:"targetCount"`
	Namespaces         []string     `json:"namespaces,omitempty" doc:"The namespaces a Kubernetes source read that you may see. Empty with no namespacesTotal means every namespace; empty with namespacesTotal above 0 means every namespace it read is hidden from you. Anything in another namespace is unknown."`
	NamespacesTotal    int          `json:"namespacesTotal,omitempty" doc:"How many namespaces the source read, when Namespaces is capped or restricted. Above 0, the source reads a list of namespaces, never every namespace."`
	InferredCount      int          `json:"inferredCount,omitempty" doc:"The inferred services this source reported, after the bound; a restricted view counts only the inferred services you may see"`
	Advisory           bool         `json:"advisory,omitempty" doc:"An advisory source never makes the answer partial; its rows are hints."`
	Ref                string       `json:"ref,omitempty" doc:"The pinned reference the source read, such as a target snapshot's repository@digest"`
	Signer             string       `json:"signer,omitempty" doc:"The trust entry that verified what the source read (its name, or key:<fingerprint>), never a name the signed payload claims; empty means unverified"`
	Publisher          string       `json:"publisher,omitempty" doc:"The publisher the signed payload names, such as the ServiceAccount that pushed a target snapshot: a claim of the signer, never verified on its own"`
	SignerUnscoped     bool         `json:"signerUnscoped,omitempty" doc:"The trust entry in signer is not scoped to the cluster the source describes, so the cluster it names is the signer's claim alone"`
	Cluster            *Cluster     `json:"cluster,omitempty" doc:"The cluster the source describes"`
}

// degraded reports whether st is a source that is not available and counts
// against an answer: an advisory source never does.
func (st SourceState) degraded() bool { return st.Status != SourceAvailable && !st.Advisory }

// ToolSummary is a bounded projection of an agent-invocable tool derived from a
// revision's OpenAPI interface. It omits the full input schema and operation
// body so search/get answers stay small; callers fetch bodies lazily.
type ToolSummary struct {
	Name string `json:"name"`
	// Interface is the declared interface the operation was derived from. It is
	// recorded at derivation time so a consumer can group operations under their
	// interface without re-deriving the (conditional) tool-name prefix.
	Interface string `json:"interface,omitempty"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Summary   string `json:"summary,omitempty"`
	Mutating  bool   `json:"mutating"`
}

// DocRef is a pointer to an in-bundle document. It carries no body — bodies are
// fetched lazily, never eagerly duplicated into the index.
type DocRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	// digest is the SHA-256 of the body as it stood at Build. It is the whole of
	// the revision-content immutability guarantee for lazily-read documents: the
	// snapshot keeps the fingerprint (32 bytes) instead of the prose, and
	// [Query.RevisionDocument] refuses to serve bytes that no longer match it. It
	// is unexported for the same reason [ContractRevision.bundle] is — it is a
	// build-time integrity fact, not product data — so it never reaches the wire.
	// Empty means the body could not be fingerprinted at Build (unreadable or over
	// [MaxDocumentBytes]); such a document can never be served, because there is
	// nothing to prove the bytes on hand are the revision's.
	digest string
}

// Coverage records how much of a target's required assertion set was actually
// evaluated against evidence.
type Coverage struct {
	Evaluated int `json:"evaluated"`
	Required  int `json:"required"`
}

// Declaration values of a [ServiceRecord].
const (
	DeclarationDeclared = "declared"
	DeclarationInferred = "inferred"
)

// ServiceRecord is a stable logical service. It references its revisions and
// targets by key rather than copying them, and only carries an aggregate Status
// when the derivation is unambiguous.
type ServiceRecord struct {
	Key ServiceKey `json:"key"`
	// Domain is the registry path in the service key (oci://ghcr.io/acme/pay ->
	// ghcr.io/acme), shown as Registry. BusinessDomain is the business domain.
	Domain string `json:"domain,omitempty"`
	// BusinessDomain is the business domain of the service (see
	// deriveBusinessDomain). Empty means No domain, unless BusinessDomainUnknown
	// says Pacto cannot tell: no readable revision and no source that reports the
	// domain, or sources that disagree on it.
	BusinessDomain        string `json:"businessDomain,omitempty"`
	BusinessDomainUnknown bool   `json:"businessDomainUnknown,omitempty"`
	// Offering is the platform offering (service.offering) the service provides
	// and Summary its one-line purpose (service.summary), derived like
	// BusinessDomain (see deriveOffering). Both are exported so a detail answer
	// cloned through JSON keeps them.
	Offering string `json:"offering,omitempty"`
	Summary  string `json:"summary,omitempty"`
	// SummaryUnknown says an empty Summary is not a known absence: the sources
	// report different summaries, or no revision was read and no source reports
	// one. Like BusinessDomainUnknown, unknown is never "declares none".
	SummaryUnknown bool   `json:"summaryUnknown,omitempty"`
	Name           string `json:"name"`
	// NameInferred is where Name comes from when no revision declares it:
	// every revision of the service inferred it from its folder.
	NameInferred *contract.Inferred `json:"nameInferred,omitempty"`
	Owner        contract.Owner     `json:"owner,omitzero"`
	Revisions    []RevisionKey      `json:"revisions,omitempty"`
	Targets      []TargetKey        `json:"targets,omitempty"`
	Labels       map[string]string  `json:"labels,omitempty"`
	Status       string             `json:"status,omitempty"`
	Sources      []string           `json:"sources,omitempty"`
	// OwnerProvenance says where Owner comes from (see [OwnerProvenanceDeclared]
	// for the classes and their precedence); empty when Owner is empty and no
	// inferred owner exists. Owner never holds an inferred value.
	OwnerProvenance string `json:"ownerProvenance,omitempty" enum:"declared,declared-via-target,attested,inferred"`
	// OfferingProvenance says where Offering comes from: declared by a contract
	// revision, or observed, reported only by the sources observing its targets.
	OfferingProvenance string `json:"offeringProvenance,omitempty" enum:"declared,observed"`
	// HiddenDependencies and HiddenDependents count the services this one
	// depends on or calls, and the services, or callers with no contract, that
	// depend on or call it, which the caller's permissions hide. They are never
	// named. Zero outside a restricted answer.
	HiddenDependencies int `json:"hiddenDependencies,omitempty"`
	HiddenDependents   int `json:"hiddenDependents,omitempty"`
	// HiddenPolicyReferrers counts the hidden services that reference this
	// one through a policy reference and are not already counted in
	// HiddenDependents. They are never named. Zero outside a restricted answer.
	HiddenPolicyReferrers int `json:"hiddenPolicyReferrers,omitempty"`
	// Template marks a golden path: its newest readable revision declares
	// service.template and the fleet knows it runs nowhere (see deriveTemplate).
	// A golden path consumes nothing, provides no offering and stays out of the
	// population summaries.
	Template bool `json:"template,omitempty"`
	// Origin is the golden path the newest readable revision says the service
	// was created from (service.origin), as DECLARED provenance, with the golden
	// path revision it resolves to or the reason it resolves to none.
	Origin *ServiceOrigin `json:"origin,omitempty"`
	// HiddenCreated counts, on a golden path, the services created from it that
	// the caller's permissions hide. They are never named. Zero outside a
	// restricted answer.
	HiddenCreated int `json:"hiddenCreated,omitempty"`
	// Declaration says whether a contract describes the service.
	Declaration string `json:"declaration" enum:"declared,inferred" doc:"declared when a contract revision or a Pacto target describes the service; inferred when Pacto only sees a workload no contract Pacto read describes; workload.declarationUnverified says Pacto could not read the Pacto targets there"`
	// Workload is what Pacto read from the workload of an inferred service.
	Workload *InferredWorkload `json:"workload,omitempty"`
}

// Inferred reports whether Pacto only sees a workload of the service and no
// contract describes it. A record with no Declaration reads as declared.
func (s *ServiceRecord) Inferred() bool { return s.Declaration == DeclarationInferred }

// InferredWorkload is the workload an inferred service is: where it runs and
// the image, ports and probes Pacto read, each with the object path it read.
type InferredWorkload struct {
	Source     string          `json:"source" doc:"The source that read the workload"`
	Cluster    string          `json:"cluster,omitempty" doc:"The cluster id, whenever Pacto knows the source's cluster; the service name carries it only in a snapshot with several clusters"`
	Namespace  string          `json:"namespace"`
	Kind       string          `json:"kind" doc:"Deployment, StatefulSet, DaemonSet or CronJob"`
	Name       string          `json:"name"`
	Image      *InferredValue  `json:"image,omitempty"`
	Ports      []InferredPort  `json:"ports,omitempty"`
	Probes     []InferredProbe `json:"probes,omitempty"`
	ObservedAt *time.Time      `json:"observedAt,omitempty"`
	// DeclarationUnverified and Dropped live on the record, not only in a
	// limitation's prose, so a restricted view can never lose them.
	DeclarationUnverified bool     `json:"declarationUnverified,omitempty" doc:"Pacto could not read the Pacto targets of this namespace, so a contract may describe this workload (DECLARATION_UNVERIFIED)"`
	Dropped               []string `json:"dropped,omitempty" doc:"The fields Pacto read but left out because they held a control character or ran past their bound, such as image or ports[0]"`
	// Group is how the cluster already groups the workload; every inferred
	// record carries one. It is the workload's own words and never verified.
	Group *InferredGroup `json:"group,omitempty"`
	// Repositories are the source repositories a GitOps object names for the
	// group, at most MaxInferredRepositories.
	Repositories      []InferredRepository `json:"repositories,omitempty"`
	RepositoryUnknown string               `json:"repositoryUnknown,omitempty" doc:"Why Pacto could not read the repository of a group that names one, for example Applications unreadable (forbidden)"`
	// repositoryUnknownFrom is RawInferredService.RepositoryUnknownFrom.
	repositoryUnknownFrom []WorkloadScope
}

// InferredGroup is how the cluster already groups an inferred service's workload.
type InferredGroup struct {
	Rule      string `json:"rule" enum:"argocd,flux-kustomization,flux-helmrelease,helm,part-of,instance,workload" doc:"The rule that matched, in precedence order: Argo CD Application, Flux Kustomization or HelmRelease, Helm release, app.kubernetes.io/part-of, app.kubernetes.io/instance, else the workload itself"`
	Namespace string `json:"namespace,omitempty" doc:"The namespace of the owning object, as the workload's own metadata names it; empty for an Argo CD Application in the Argo CD namespace"`
	Name      string `json:"name"`
	From      string `json:"from" doc:"The labels, annotations or field manager Pacto read the group from"`
	Key       string `json:"key" doc:"rule/namespace/name, prefixed with the cluster in a snapshot with several clusters; the entities group filter takes it"`
	Label     string `json:"label" doc:"The group in words, for example Argo CD Application shop/cart"`
	Ambiguous string `json:"ambiguous,omitempty" doc:"Why the workload stays ungrouped: two groups of one rule claim it (SERVICE_GROUP_AMBIGUOUS)"`
}

// InferredRepository is a source repository a GitOps object names for the group.
type InferredRepository struct {
	URL       string `json:"url" doc:"Without credentials, query or fragment"`
	Path      string `json:"path,omitempty" doc:"The folder the object deploys from, when it names one"`
	From      string `json:"from" doc:"The object and field Pacto read it from, for example Application argocd/cart spec.source.repoURL"`
	Namespace string `json:"namespace" doc:"The namespace of the GitOps object Pacto read it from"`
	Kind      string `json:"kind" enum:"Application,GitRepository,Kustomization,HelmRelease" doc:"The kind of that object; a restricted view keeps the repository only for a viewer whom Kubernetes lets list this kind in this namespace"`
}

// InferredValue is one value read from a workload and the object path it was
// read from.
type InferredValue struct {
	Value string `json:"value"`
	From  string `json:"from" doc:"The object path Pacto read the value from"`
}

// InferredPort is one container port read from a workload.
type InferredPort struct {
	Port     int    `json:"port"`
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	From     string `json:"from" doc:"The object path Pacto read the port from"`
}

// InferredProbe is one HTTP probe read from a workload: its path (without a
// query or fragment) and port.
type InferredProbe struct {
	Kind string `json:"kind" enum:"readiness,liveness,startup"`
	Path string `json:"path,omitempty"`
	Port string `json:"port,omitempty"`
	From string `json:"from" doc:"The object path Pacto read the probe from"`
}

// ServiceOrigin is a service's declared service.origin and what it resolves to:
// the golden path service and revision whose content digest matches Digest, or
// empty GoldenPath and Revision with the Reason none does.
type ServiceOrigin struct {
	Ref        string      `json:"ref"`
	Digest     string      `json:"digest"`
	GoldenPath ServiceKey  `json:"goldenPath,omitempty"`
	Revision   RevisionKey `json:"revision,omitempty"`
	Reason     string      `json:"reason,omitempty"`
}

// ContractRevision is an immutable resolved revision. It embeds a reference to
// the declared contract (the interfaces/configs/deps/policies/state/readiness it
// declares) and adds computed projections (readiness result, validation
// findings, derived tools/skills/doc refs) plus its resolved identity.
type ContractRevision struct {
	Key          RevisionKey        `json:"key"`
	Service      string             `json:"service"`
	Domain       string             `json:"domain,omitempty"`
	ServiceKey   ServiceKey         `json:"serviceKey"`
	PactoVersion string             `json:"pactoVersion,omitempty"`
	Version      string             `json:"version,omitempty"`
	RequestedRef string             `json:"requestedRef,omitempty"`
	ResolvedRef  string             `json:"resolvedRef,omitempty"`
	Digest       string             `json:"digest,omitempty"`
	Owner        contract.Owner     `json:"owner,omitempty"`
	Contract     *contract.Contract `json:"contract,omitempty"`
	// Inferred lists what a local read implied rather than read from the
	// contract (an absent pactoVersion, an inferred service.name), with sources.
	Inferred []contract.Inferred `json:"inferred,omitempty"`
	// Record is the bundle's pacto.inferred.yaml, read only for a revision at
	// pactoVersion 2.5 or later whose cited files match the bundle. Its
	// dependencies project as inferred relationships; detail exposes its fields.
	Record     *contract.InferredRecord `json:"-"`
	Readiness  *readiness.Result        `json:"readiness,omitempty"`
	Validation []finding.Finding        `json:"validation,omitempty"`
	Valid      bool                     `json:"valid"`
	// Stages is how each stage of the validation run ended. Empty when the
	// contract document could not be read, so nothing was validated.
	Stages []ValidationStage `json:"stages,omitempty"`
	// Environments is each declared environment with the result of checking its
	// values (see [EnvironmentResult]), bounded per environment at Build.
	Environments []EnvironmentResult `json:"environments,omitempty"`
	Tools        []ToolSummary       `json:"tools,omitempty"`
	// SpecsRead names the declared interfaces whose referenced document was
	// successfully read at Build time. It exists so a consumer can distinguish an
	// interface that declares no operations from one whose document was never
	// available (absence of evidence is not evidence of absence).
	SpecsRead []string   `json:"specsRead,omitempty"`
	Skills    []string   `json:"skills,omitempty"`
	Docs      []DocRef   `json:"docs,omitempty"`
	Lock      *lock.Lock `json:"lock,omitempty"`
	// Metadata is the BOUNDED, deterministic projection of the contract's free-form
	// metadata map, flattened ONCE at Build. The map is author-controlled and
	// arbitrarily wide, so it is bounded at the source boundary exactly like
	// TargetRecord.ObservedRuntime rather than re-flattened per request.
	Metadata RuntimePreview `json:"metadata"`
	// SBOM summarizes the bundle's software inventory when it ships a readable one.
	// Nil means no SBOM was read; a LimitationSBOMUnreadable distinguishes "the
	// bundle has none" from "the bundle has one we could not parse".
	SBOM *SBOMSummary `json:"sbom,omitempty"`
	// Sources lists every source that contributed this revision, sorted. Source is
	// the first of them, so it is stable rather than a record of which source
	// happened to finish first: a revision is immutable content keyed by its
	// digest, so co-contributors have contributed the same thing and none of them
	// is the authoritative one. Read Sources when the question is provenance.
	Source    string     `json:"source"`
	Sources   []string   `json:"sources,omitempty"`
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	// Provenance is the signature and referrers a registry source found attached
	// to the bundle digest. Nil means no source that read it checks signatures.
	Provenance *BundleProvenance `json:"provenance,omitempty"`

	// bundle carries the parsed bundle used during Build (to derive tools, skills,
	// docs and validation) and afterwards as the read-only backing store for lazy
	// document reads (see Query.RevisionDocument, which reads a bounded body for a
	// path this revision already listed in Docs). It is never serialized and the
	// pointer is never handed to a caller, so nobody can mutate snapshot-owned
	// bundle data.
	//
	// The bundle filesystem is only a byte SOURCE, never the authority on what this
	// revision's documents are: it may be a live os.DirFS over a working directory
	// that changes after Build. The authority is the per-document digest recorded
	// in Docs, which every read verifies.
	bundle *contract.Bundle
	// guaranteesUnseen is RawRevision.GuaranteesUnseen; see GuaranteesUnseen.
	guaranteesUnseen string
	// docConflict, when non-empty, is why this revision's documents cannot be
	// served: two sources contributed the same immutable revision with different
	// document content. It is set by mergeRevision, reported as a
	// LimitationRevisionDocConflict, and turns every document read into an explicit
	// unavailable rather than an arbitrary, order-dependent winner.
	docConflict string
	// recordUnreadable says Build dropped this revision's pacto.inferred.yaml
	// (LimitationInferredRecordUnreadable), so detail reads "unknown", never
	// "nothing inferred".
	recordUnreadable bool
	// declarationChecked says a signature the revision's provenance verified
	// names its record's declarer as a person (acceptDeclaration).
	declarationChecked bool
	// declarationRefused is why the reader does not accept the declarer:
	// an unverified signature or a signer policy that refuses it; "" when
	// it is accepted (acceptDeclaration).
	declarationRefused string
	// declarationHidden and hiddenClusterFields are set on a restricted
	// view's clone when the viewer may not read the namespace the
	// declaration came from: its cluster fields are gone and counted.
	declarationHidden   bool
	hiddenClusterFields int
	// validated records that this revision's contract document was readable and
	// was run through the validator at build time. Stored so status queries never
	// dereference the build-only bundle after Build.
	validated bool
	// localStages is the validation run with no configuration ref resolved,
	// kept when revalidateConfigRefs replaces it so a restricted view can
	// derive the verdict again without a provider it hides.
	localStages []validation.Stage
	// differences are the declared configuration values the revision's
	// environments disagree on (see [validation.EnvironmentDifferences]), at
	// most MaxDetailPreview, with differencesTotal counting them all; or
	// differencesErr, why they could not be read. Computed at Build because a
	// query's revision clone carries no bundle files.
	differences      []validation.EnvironmentDifference
	differencesTotal int
	differencesErr   string
	// lockConflict, when non-empty, is why this revision has no lock: two sources
	// contributed it with disagreeing pacto.lock resolutions, so Lock was dropped
	// (see mergeRevisionLock). Like docConflict it carries a reason rather than a
	// flag, because lockReference reaches a nil lock either way and an operator
	// told only "no pins recorded" would re-run `pacto lock`, regenerate the same
	// pins and see nothing change. It also makes the drop sticky: a third
	// contributor that happens to agree with one side must not reinstate pins the
	// fleet has already reported as contested.
	lockConflict string
	// contentHash is lock.HashFS of the bundle of a golden path revision that
	// declares service.template, the digest a service.origin records whatever
	// source read the path (see resolveOrigins). Empty on every other revision.
	contentHash string
	// fixedPolicy records that the contract declares no policy but its bundle
	// serves policy/schema.json, which a policy reference to it resolves to.
	fixedPolicy bool
	// configurations is the bounded projection of Contract.Configurations, built
	// ONCE here at the source boundary. Each scope's values map is author-controlled
	// and arbitrarily wide, and selecting its bounded key set is O(width * bound);
	// paying that per revision-detail request would let one wide contract multiply
	// the cost of every unauthenticated GET by its scope count. Reference
	// resolutions are joined onto a copy at query time because they are a property
	// of the snapshot's relationships, not of the contract.
	configurations ConfigurationsPreview
}

// SBOMSummary is the bounded projection of a bundle's SBOM: enough to answer
// "does this revision ship a software inventory, how large is it, and what
// licenses does it pull in" WITHOUT retaining the packages themselves. A snapshot
// holds every revision of every service and one SBOM can list thousands of
// packages, so the inventory stays in the bundle and only its shape is projected.
type SBOMSummary struct {
	// Format is the source document format the parser recognized ("spdx" or
	// "cyclonedx").
	Format string `json:"format,omitempty"`
	// Packages is the COMPLETE package count of the document, never a bounded one.
	Packages int `json:"packages"`
	// Licenses buckets packages by declared license, most common first, with
	// packages declaring none in "unspecified". It is bounded to the most common
	// [maxSBOMLicenses]; everything past the bound is folded into OtherLicensed, so
	// the buckets plus OtherLicensed always equal Packages and a distribution drawn
	// from them has a true denominator.
	Licenses []LicenseCount `json:"licenses,omitempty"`
	// OtherLicensed counts the packages whose license fell outside the buckets
	// above — the long tail beyond the bound, kept so nothing is silently dropped.
	OtherLicensed int `json:"otherLicensed,omitempty"`
}

// LicenseCount is one license bucket of an [SBOMSummary].
type LicenseCount struct {
	License string `json:"license"`
	Count   int    `json:"count"`
}

// TargetEnvironmentLabel on a target names the environment it runs in. It is the
// label the delivery promoter places a Pacto by (delivery.EnvironmentLabel).
const TargetEnvironmentLabel = "delivery.pacto.trianalab.io/environment"

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
	Domain           string            `json:"domain,omitempty"`
	ServiceKey       ServiceKey        `json:"serviceKey"`
	ContractRevision RevisionKey       `json:"contractRevision,omitempty"`
	// RevisionMatch records HOW ContractRevision was linked: "exact" (immutable
	// digest — the revision known to be running) or "inferred" (a unique but
	// MUTABLE tag/version correlation, not proof). Empty when there is no link;
	// an ambiguous mutable match yields no link and a REVISION_LINK_AMBIGUOUS
	// limitation instead. Only an exact link may be presented as authoritative.
	RevisionMatch string `json:"revisionMatch,omitempty"`
	RequestedRef  string `json:"requestedRef,omitempty"`
	ResolvedRef   string `json:"resolvedRef,omitempty"`
	Digest        string `json:"digest,omitempty"`
	Compliance    string `json:"compliance"`
	// Owner is the owner the observing source read from the contract it
	// evaluates (the operator reports it in status.contract.owner). It lets a
	// fleet with no contract revisions still say who owns the service.
	Owner contract.Owner `json:"owner,omitzero"`
	// BusinessDomain and BusinessDomainUnknown are the business domain of the
	// service this target belongs to (ServiceRecord.BusinessDomain), copied here so
	// a reference carries it.
	BusinessDomain        string `json:"businessDomain,omitempty"`
	BusinessDomainUnknown bool   `json:"businessDomainUnknown,omitempty"`
	// Environment is the TargetEnvironmentLabel value, empty when no source
	// reports it or when sources disagree on it (see targetEnvironmentName).
	Environment string            `json:"environment,omitempty"`
	Findings    []finding.Finding `json:"findings,omitempty"`
	Coverage    *Coverage         `json:"coverage,omitempty"`
	Readiness   *readiness.Result `json:"readiness,omitempty"`
	// ObservedRuntime is the BOUNDED, deterministic projection of the source's raw
	// observed-runtime map, computed ONCE at Build time (the single unbounded-source
	// pass) from the untrusted, arbitrarily wide/deep source data. The raw map is not
	// retained on the snapshot: an untrusted source cannot make a query, a clone or
	// the snapshot export do work proportional to its width, because every consumer
	// (product detail, snapshot export, SnapshotID) sees only this bounded projection.
	ObservedRuntime RuntimePreview `json:"observedRuntime"`
	EvidenceAt      *time.Time     `json:"evidenceAt,omitempty"`
	ReconciledAt    *time.Time     `json:"reconciledAt,omitempty"`
	Source          string         `json:"source"`
	Sources         []string       `json:"sources,omitempty"`
	Stale           bool           `json:"stale"`
	// Quarantined is set when two sources contributed this target key with
	// conflicting identity-bearing fields (service, domain, revision, digest, ref).
	// The record is kept visible with its conflict limitations but is NOT treated
	// as authoritative (it does not drive service aggregate status), so source
	// order can never silently pick an identity.
	Quarantined bool `json:"quarantined,omitempty"`
	// Cluster is the kube-system UID of the cluster that reported this target;
	// empty when no source names one. ClusterName is display only,
	// ClusterNameSource says how it was found and ClusterNameReportedBy which
	// source reported it (empty when this process read it). ClusterRelation says
	// whether the target runs in the cluster or was only reported through it.
	Cluster               string       `json:"cluster,omitempty" doc:"The kube-system UID of the cluster that reported this target."`
	ClusterName           string       `json:"clusterName,omitempty" doc:"A display name for the cluster; never an identity."`
	ClusterNameSource     string       `json:"clusterNameSource,omitempty" enum:"node-label,context,file,uid" doc:"How the cluster name was found."`
	ClusterNameReportedBy string       `json:"clusterNameReportedBy,omitempty" doc:"The source that reported the cluster name; empty when this process read it itself."`
	ClusterRelation       string       `json:"clusterRelation,omitempty" enum:"runs-in,reported-through" doc:"runs-in when a Kubernetes source read the target in the cluster; reported-through when the cluster only reported it."`
	Limitations           []Limitation `json:"limitations,omitempty"`
	// labels is the bounded projection of Labels, computed once by boundTargetLabels
	// after ingestion. Labels itself has to stay a raw map because TargetQuery filters
	// on it, so this is the only way the detail path can honor the same
	// bound-once-at-Build rule ObservedRuntime already follows.
	labels RuntimePreview
	// reportedDomains are the distinct service.domain values the observing sources
	// read from the contract this target runs (the operator reports it in
	// status.contract.domain); more than one is a disagreement. domainKnown says a
	// source read the domain at all, so an empty set means the contract declares
	// none. deriveBusinessDomain falls back to them when no revision declares a
	// domain.
	reportedDomains []string
	domainKnown     bool
	// reportedOfferings and reportedSummaries are the service.offering and
	// service.summary values the observing sources read from that contract.
	reportedOfferings, reportedSummaries []string
	// environmentConflict: sources disagree on the TargetEnvironmentLabel, so the
	// label is dropped from Labels and Environment stays empty.
	environmentConflict bool
	// clusterUnknownFrom are the sources that delivered this target with no
	// known cluster while the snapshot knew one; Restrict rebuilds
	// CLUSTER_UNKNOWN from them.
	clusterUnknownFrom []string
	// clusterInvalidFrom are the sources that named an invalid cluster id for
	// this target, and clusterConflictFrom those that reported it in a cluster
	// another evidence source names for the same record; Restrict rebuilds
	// their limitations from them.
	clusterInvalidFrom, clusterConflictFrom []string
	// workload is the workload the target runs as, when its source knows it;
	// Build never infers a service from it.
	workload *WorkloadRef
	// createdAt is when the declaration behind the target was created.
	createdAt *time.Time
}

// DisplayName returns a human-readable "scope/kind/name" for a target, with
// components unescaped. A target whose key carries a cluster and whose cluster
// has a name reads "cluster / scope / name". Unlike the canonical Key it is
// lossy (a name containing "/" is shown verbatim) and must not be used as an
// identity.
func (t *TargetRecord) DisplayName() string {
	if cluster, _, _, _, ok := ParseTargetKey(t.Key); ok && cluster != "" && t.ClusterName != "" {
		return t.ClusterName + " / " + t.Scope + " / " + t.Name
	}
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

// ConsumesEdge reports whether rel is a declared, resolved edge through which
// its source consumes its destination: a dependency or a configuration
// reference. These are the edges the dependency indexes hold, so graph
// traversal, dependents, hidden counts and impact all count the same consumers.
func ConsumesEdge(rel Relationship) bool {
	return rel.Provenance == ProvenanceDeclared && rel.Resolved &&
		(rel.Type == RelationshipDependency || rel.Type == RelationshipConfigRef)
}

// Relationship is a directed, revision-scoped graph edge. A declared edge
// originates from a specific [ContractRevision] (FromRevision) of a logical
// service (FromService) — never from "the service's latest revision". Declared,
// observed and inferred edges are kept apart (see Provenance); an observed edge
// is folded from runtime telemetry and carries ObservedCount/Source, an
// inferred edge (to an external system read from configuration, or to a
// service a revision's inferred record names) carries
// InferredSources/Confidence, and an inferred runtime call with an end no
// contract describes carries ObservedCount/ObservedSources and lives in
// [FleetSnapshot.InferredRelationships].
type Relationship struct {
	FromService      ServiceKey  `json:"fromService"`
	FromRevision     RevisionKey `json:"fromRevision,omitempty"`
	To               string      `json:"to"`
	ToService        ServiceKey  `json:"toService,omitempty"`
	ResolvedRevision RevisionKey `json:"resolvedRevision,omitempty"`
	Type             string      `json:"type"`
	Provenance       string      `json:"provenance"`
	// Reconciliation, for a declared dependency edge, is the explicit
	// declared-vs-observed outcome computed against the snapshot's observed edges
	// (matched / declared-not-observed / insufficient). Empty for observed and
	// inferred edges. Consumers must render "reconciled" from THIS, never from name
	// resolution or provider deployment.
	Reconciliation string `json:"reconciliation,omitempty"`
	Required       bool   `json:"required,omitempty"`
	Compatibility  string `json:"compatibility,omitempty"`
	Resolved       bool   `json:"resolved"`
	RequestedRef   string `json:"requestedRef,omitempty"`
	LockedDigest   string `json:"lockedDigest,omitempty"`
	LockedVersion  string `json:"lockedVersion,omitempty"`
	Reason         string `json:"reason,omitempty"`
	// NameFromRef marks a declared dependency whose name the contract does not
	// declare: it was derived from the ref, so it resolves only to a service
	// published at that repository.
	NameFromRef bool `json:"nameFromRef,omitempty"`
	// RangeStatus, for a resolved declared dependency with a compatibility range,
	// says whether the provider version the fleet holds satisfies that range:
	// satisfied, unsatisfied or unknown (a range or version semver cannot read, or
	// provider revisions that disagree). Empty when no range is declared or the
	// dependency is unresolved. Resolved only means the provider exists; read
	// this field before saying a dependency is met.
	RangeStatus string `json:"rangeStatus,omitempty" enum:"satisfied,unsatisfied,unknown"`
	// External marks a declared dependency this Pacto system does not deliver:
	// it is not expected in the fleet, so not resolving is not a gap. Version
	// is the free-text version an external dependency with no ref declares.
	External bool   `json:"external,omitempty"`
	Version  string `json:"version,omitempty"`
	// ToExternal is the external system a ref-less external dependency or an
	// observed call joins (see [ExternalRecord]). Resolved stays false: no
	// fleet service delivers it.
	ToExternal ExternalKey `json:"toExternal,omitempty" doc:"The canonical identity of the external system this ref-less external dependency or observed call joins; resolved stays false because no fleet service delivers it"`
	// FoldedInto is set only on an answer: the other-egress node ToExternal is
	// folded into when the answer names more than MaxExternalNodes external
	// systems. ToExternal keeps the real identity.
	FoldedInto ExternalKey `json:"foldedInto,omitempty" doc:"On an answer that folds external systems, the other-egress node toExternal is folded into; toExternal keeps the real identity"`
	// DependencyBehavior is what a declared dependency requires of its
	// provider's behavior and whether the provider meets it (pactoVersion 2.2).
	DependencyBehavior
	// DeprecatedInterfaces lists the deprecated interfaces of the provider
	// revision a resolved dependency reaches (see [providerDeprecations]).
	DeprecatedInterfaces []InterfaceDeprecation `json:"deprecatedInterfaces,omitempty"`
	// ProviderLifecycle is the service lifecycle (pactoVersion 2.4) of the
	// provider revision a resolved dependency or configuration reference reaches
	// (see [providerServiceLifecycle]): the provider's own declaration.
	ProviderLifecycle *contract.InterfaceLifecycle `json:"providerLifecycle,omitempty"`
	// Hidden marks a declared dependency that resolves to a service the
	// caller's Kubernetes permissions hide: ToService and ResolvedRevision are
	// blank, and To is only what the declaring contract itself says.
	Hidden bool `json:"hidden,omitempty"`
	// hiddenTo is the hidden provider's key, kept off the wire so the declared
	// count collapses a hidden provider exactly like a visible one.
	hiddenTo ServiceKey
	// ObservedCount is the TOTAL runtime calls witnessed across all sources for an
	// observed (Provenance == observed) edge. Zero for declared edges.
	ObservedCount int `json:"observedCount,omitempty"`
	// Source is the id of the sole source that witnessed an observed edge; it is
	// EMPTY when more than one source contributed (see ObservedSources), so a
	// multi-source count is never attributed to just the first source.
	Source string `json:"source,omitempty"`
	// ObservedSources is the per-source breakdown of an observed edge — each
	// source's own count and observation window — so no source's contribution is
	// collapsed onto another. Empty for declared edges.
	ObservedSources []ObservedSourceStat `json:"observedSources,omitempty"`
	// FirstSeen/LastSeen bound the observation window (min/max across sources) of an
	// observed edge. This is the trace window, NOT "current" runtime truth; an old
	// offline sample keeps its old window.
	FirstSeen *time.Time `json:"firstSeen,omitempty"`
	LastSeen  *time.Time `json:"lastSeen,omitempty"`
	// InferredSources is the per-source evidence of an inferred edge, sorted.
	// Empty for declared and observed edges.
	InferredSources []InferredSource `json:"inferredSources,omitempty" doc:"Where each source recognized this external system or service in the caller's configuration or inferred record; set only on an inferred edge"`
	// Confidence is the strongest confidence of an inferred edge's sources.
	Confidence string `json:"confidence,omitempty" enum:"likely,possible" doc:"The strongest confidence of the inferred sources: likely when a host or system value matched, possible when only a key name did"`
}

// InferredSource is one source's evidence for an inferred edge: the kind of
// object it read, where (an object and a key, never a value), the normalized
// host, the catalog entry and digest it was classified by and when.
type InferredSource struct {
	Source string `json:"source" doc:"The id of the source that reported it"`
	// Target is the target the evidence was read on, when the source names
	// one this snapshot holds; a restricted view drops evidence of a hidden one.
	Target     TargetKey  `json:"target,omitempty" doc:"The target the evidence was read on"`
	Kind       string     `json:"kind" doc:"What was read: env, configMap, secretKeyRef, secretRef, externalNameService, selectorlessService or serviceEntry on a workload; inferredRecord (a dependency the revision's pacto.inferred.yaml names); or, for an external system the inferred record names, helmValues, compose, kustomize, sdk, terraform or crossplane"`
	Where      string     `json:"where,omitempty" doc:"The object and key it was found in, never a value"`
	Host       string     `json:"host,omitempty" doc:"The normalized host and port; empty when only a key name matched"`
	Confidence string     `json:"confidence" enum:"likely,possible"`
	Entry      string     `json:"entry,omitempty" doc:"The catalog entry that classified it"`
	Catalog    string     `json:"catalog,omitempty" doc:"The digest of the catalog the source classified it with"`
	ObservedAt *time.Time `json:"observedAt,omitempty" doc:"When the source last read it"`
	// Revision is set on a repository source: the consumer revision whose
	// inferred record names the external system.
	Revision RevisionKey `json:"revision,omitempty" doc:"The consumer revision whose inferred record names it, for a repository source"`
}

// ExternalKey is the canonical identity of an external system, an
// externalid.ID: "stripe", "aws/rds/<account>/<region>/<id>" or
// "host/<registrable-domain>".
type ExternalKey string

// OtherEgress is the node an answer folds the external systems beyond
// MaxExternalNodes into. No identity can take it (externalid.Parse refuses it).
const OtherEgress ExternalKey = externalid.Reserved

// ClassOtherEgress is the class of the OtherEgress node.
const ClassOtherEgress = "other-egress"

// The kinds of an [ExternalSource].
const (
	// ExternalSourceContract is a dependency a contract declares.
	ExternalSourceContract = "contract"
	// ExternalSourceObserved is a runtime call a trace source saw.
	ExternalSourceObserved = "observed"
	// ExternalSourcePodSpec is a use a source inferred from a workload's pod
	// spec (see [RawExternalUse]).
	ExternalSourcePodSpec = "pod-spec"
	// ExternalSourceRepository is a use the inferred record of the consumer's
	// newest revision names: read from its repository folder, never declared.
	ExternalSourceRepository = "repository"
)

// ExternalRecord is one external system: a vendor API, a managed cloud
// resource or a legacy host that no contract in the fleet describes. Pacto
// never delivers, observes the health of or checks a version range against an
// external system: it never clears a gate.
type ExternalRecord struct {
	Key   ExternalKey `json:"key" doc:"The canonical identity"`
	Label string      `json:"label" doc:"The catalog label, else the name the first visible consumer, by service key, declared"`
	Class string      `json:"class" enum:"vendor-api,managed-cloud,legacy,unclassified,other-egress" doc:"The catalog class, unclassified when no catalog entry names it"`
	// Aliases are the catalog entry's aliases, so a search finds it by any.
	Aliases []string `json:"aliases,omitempty" doc:"The aliases of the catalog entry that classified it"`
	// Sources is the evidence, one per consumer and declaring position, each
	// with its own provenance. SourcesTotal counts every declaration and every
	// observed or inferred source.
	Sources      []ExternalSource      `json:"sources" doc:"The evidence that names this external system, one per consumer and dependency position, each with its own provenance"`
	SourcesTotal int                   `json:"sourcesTotal" doc:"Every declaration across every revision of every consumer, plus each observed or inferred source"`
	Catalog      *ExternalCatalogMatch `json:"catalog,omitempty" doc:"The catalog entry that classified it; absent when no catalog entry classifies it"`
	Consumers    []ServiceKey          `json:"consumers" doc:"The services that declare, call or use it"`
	// ConsumersTotal counts Consumers before an answer bounds them.
	ConsumersTotal  int `json:"consumersTotal" doc:"How many services declare, call or use it"`
	HiddenConsumers int `json:"hiddenConsumers,omitempty" doc:"How many consumers the caller's permissions hide"`
	Folded          int `json:"folded,omitempty" doc:"On the other-egress node, how many external systems it folds"`
	// JoinedBy is the strongest match among the visible sources: identity,
	// catalog-alias, host, then normalized-name.
	JoinedBy string `json:"joinedBy,omitempty" enum:"identity,catalog-alias,host,normalized-name" doc:"How its consumers join it, the strongest match among its sources: identity, catalog-alias, host or normalized-name (joined by name only)"`
	// declarations counts, per consumer, the revisions that declare it plus
	// each observed or inferred source.
	declarations map[ServiceKey]int
	// catalogLabel is the label its catalog entry (or the fold) gives it;
	// empty, the label comes from the visible sources, so a restricted view
	// never shows a name only a hidden consumer declared.
	catalogLabel string
	// hidden is the consumers a restriction hid, so a fold counts each once.
	hidden map[ServiceKey]bool
}

// ExternalSource is one piece of evidence that names an external system.
type ExternalSource struct {
	Kind     string      `json:"kind" enum:"contract,observed,pod-spec,repository" doc:"Where the evidence comes from: a contract's dependency, a call a trace source saw, a use inferred from a workload's pod spec or a use the inferred record of the consumer's repository names"`
	Service  ServiceKey  `json:"service" doc:"The consumer"`
	Revision RevisionKey `json:"revision,omitempty" doc:"The newest consumer revision that declares it, or whose inferred record names it for a repository source"`
	// Target is empty for a contract source.
	Target     TargetKey `json:"target,omitempty" doc:"The target the evidence was observed on; empty for a contract source"`
	Dependency string    `json:"dependency,omitempty" doc:"The declared dependency name"`
	Index      int       `json:"index" doc:"The position of the dependency in the contract's dependencies"`
	Version    string    `json:"version,omitempty" doc:"The free-text version the dependency declares"`
	Required   bool      `json:"required,omitempty"`
	Provenance string    `json:"provenance" enum:"declared,observed,inferred"`
	Match      string    `json:"match" doc:"How the evidence was joined to the identity: identity, catalog-alias, host, normalized-name, system (an OpenTelemetry system value), keyName (a configuration key name only), sdk (a client library in a manifest) or resource (a Terraform resource or Crossplane object)"`
	// Superseded says the consumer has a newer revision that no longer
	// declares the dependency at this position, so the declaration may be
	// history rather than what the service runs today.
	Superseded bool `json:"superseded,omitempty" doc:"A newer revision of the consumer no longer declares it at this position"`
}

// ExternalCatalogMatch names the catalog entry that classified an external system.
type ExternalCatalogMatch struct {
	Origin string `json:"origin" enum:"default,organization"`
	Digest string `json:"digest" doc:"The digest of the catalog artifact"`
	Entry  string `json:"entry" doc:"The identity of the catalog entry"`
}

// ExternalCatalogRef records one catalog a snapshot classified external
// systems with (see [externalcatalog.Ref]).
type ExternalCatalogRef struct {
	Origin     string `json:"origin" enum:"default,organization"`
	Repository string `json:"repository,omitempty"`
	Digest     string `json:"digest" doc:"The digest of the catalog artifact; the recorded input whichever copy was read"`
	Copy       string `json:"copy" enum:"registry,embedded" doc:"registry when the bytes in use are the registry artifact at this digest (pulled, or the embedded copy matched against the signed artifact), embedded when the copy compiled into this binary is used unchecked"`
	Signature  string `json:"signature" enum:"verified,unverified,unsigned,unknown"`
	Signer     string `json:"signer,omitempty"`
	Reason     string `json:"reason,omitempty" doc:"Why the signature reads as it does"`
}

// missing reports whether a declared dependency failed to resolve in a way
// worth reporting: an external one is not expected in the fleet, and an
// observed call to an external system resolves to that system.
func (r Relationship) missing() bool { return !r.Resolved && !r.External && r.ToExternal == "" }

// ObservedSourceStat is one observation source's contribution to an observed
// relationship: the calls it witnessed and the window it saw them in.
type ObservedSourceStat struct {
	Source    string     `json:"source"`
	Count     int        `json:"count"`
	FirstSeen *time.Time `json:"firstSeen,omitempty"`
	LastSeen  *time.Time `json:"lastSeen,omitempty"`
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
	// InferredRelationships are runtime calls with at least one end no
	// contract describes (an inferred service). They are kept out of
	// Relationships and every index a gate, impact or reconciliation reads:
	// an inferred edge never satisfies a dependency. Sorted like Relationships.
	InferredRelationships []Relationship `json:"inferredRelationships,omitempty"`
	// Replacements are the inferred services a declared service took over,
	// sorted by From.
	Replacements []Replacement `json:"replacements,omitempty"`
	Sources      []SourceState `json:"sources"`
	Completeness Completeness  `json:"completeness"`
	Limitations  []Limitation  `json:"limitations,omitempty"`
	// Clusters are the clusters any source reported, sorted by id. Target keys
	// carry a cluster component only when there are two or more.
	Clusters []ClusterRecord `json:"clusters,omitempty"`
	// clusterKeyed: Build keyed targets by cluster (two or more clusters). It
	// survives Restrict, which may leave one cluster visible under 4-part keys.
	clusterKeyed bool
	// declarationSigners is BuildOptions.DeclarationSigners.
	declarationSigners *DeclarationSignerPolicy
	// UncontractedCallers are observed callers with no contract of a service
	// that has one. Sorted by (ToService, Caller).
	UncontractedCallers []UncontractedCaller `json:"uncontractedCallers,omitempty"`
	// Provisioning are the validated provider reports on what consumers'
	// configuration references and dependencies asked for, each matched to a
	// revision in this snapshot and a use it resolves. Sorted deterministically.
	Provisioning []ProvisioningRecord `json:"provisioning,omitempty"`
	// Usage are the validated provider reports on the usage and cost of what
	// consumers asked for, matched like Provisioning and kept apart from it.
	// Each carries whether it was signed and whether it is stale; a reader
	// shows its figures as trusted only through [ResourceConsumption].
	Usage []UsageRecord `json:"usage,omitempty"`
	// Externals are the external systems the fleet's contracts name, one
	// record per canonical identity.
	Externals map[ExternalKey]*ExternalRecord `json:"externals,omitempty"`
	// ExternalCatalogs are the catalogs that classified Externals, the
	// default first.
	ExternalCatalogs []ExternalCatalogRef `json:"externalCatalogs,omitempty"`
	// Providers are what each source detected and read evidence from, sorted
	// by source then id. They never change completeness or any source state.
	Providers []Provider `json:"providers,omitempty"`

	// templateEdges are the declared dependency and reference edges of the
	// golden paths (see deriveTemplate). They are kept out of Relationships and
	// the dependency indexes, because a golden path consumes nothing: impact,
	// dependents, offering consumers and attention never count it. Readers of a
	// revision's own references read both lists through declaredEdges.
	templateEdges []Relationship

	// catalog is the external catalog the snapshot was built with (see
	// [FleetSnapshot.ObservedExternal]); nil means the embedded default.
	catalog *externalcatalog.Catalog

	// provisioningWindow is BuildOptions.FreshnessWindow: a provisioning report
	// older than it (RecentEvidenceWindow when zero) is stale.
	provisioningWindow time.Duration
	// provisioningDropped counts, per consumer service, the reports dropped past
	// MaxProvisioningRecords, so a restricted view reports only its own losses.
	provisioningDropped map[ServiceKey]int
	// usageDropped counts, per consumer service, the usage reports dropped
	// past MaxUsageRecords.
	usageDropped map[ServiceKey]int

	// targetsHidden holds, in a restricted snapshot, the visible services that
	// lost a target to the view: those visible only because they declare a
	// platform offering (see [Query.Restrict]) and those with a target in a
	// hidden namespace. What their targets run is not fully known.
	targetsHidden map[ServiceKey]bool
	// revisionsHidden holds, in a restricted snapshot, the visible services
	// that lost a revision to the view (a golden path visible only as one).
	revisionsHidden map[ServiceKey]bool
	// hiddenServices holds, in a restricted snapshot, the services the view
	// hides, so a name that resolves to one is counted and never shown.
	hiddenServices map[ServiceKey]bool
	// hiddenConsumers maps, in a restricted snapshot, a provider to the hidden
	// services with a declared, resolved [ConsumesEdge] to it. Only counted.
	hiddenConsumers map[ServiceKey]map[ServiceKey]bool
	// hiddenDependents maps, in a restricted snapshot, a service to the hidden
	// services that depend on or call it and its hidden uncontracted callers
	// (by their inferred service key, else as caller:<name> keys). Only counted.
	hiddenDependents map[ServiceKey]map[ServiceKey]bool
	// hiddenReferrers maps, in a restricted snapshot, a service to the hidden
	// services whose policy references resolve to it. Only counted.
	hiddenReferrers map[ServiceKey]map[ServiceKey]bool
	// hiddenProviders counts, in a restricted snapshot, the hidden providers
	// of each offering that keeps a visible provider. Only counted.
	hiddenProviders map[string]int
	// hiddenExternals counts, in a restricted snapshot, the external systems
	// only hidden services name. They are never named.
	hiddenExternals int
	// hiddenUnresolvedRefs holds, in a restricted snapshot, the declared,
	// unresolved references of the services the view hides, so counts of
	// unresolved uses stay honest. Never serialized and never named.
	hiddenUnresolvedRefs []hiddenUnresolvedRef
	// hiddenProviderMatch holds, in a restricted snapshot, the repositories
	// and names of each offering's hidden providers, so a visible consumer's
	// unresolved reference to one still counts. Never serialized and never named.
	hiddenProviderMatch map[string]providerMatch

	// reverseDeps maps a service KEY to the keys of services that declare a
	// dependency or a resolved configuration reference on it (see
	// [ConsumesEdge]), across all their revisions (the dependents index). Built
	// once at Build time; never mutated afterwards. Keyed by the domain-qualified
	// ServiceKey (not the bare name) so same-named services in different domains
	// never share a dependents set.
	reverseDeps map[ServiceKey][]ServiceKey
	// forwardDeps maps a service KEY to the union of resolved dependency service
	// keys across all its revisions, for aggregated service-level traversal.
	forwardDeps map[ServiceKey][]ServiceKey
	// forwardDepsByRevision maps a specific revision to the resolved dependency
	// service keys IT declares — the revision-accurate adjacency used when a graph
	// query names a revision or a target (never "the latest revision").
	forwardDepsByRevision map[RevisionKey][]ServiceKey
	// observedReverse/observedForward mirror reverseDeps/forwardDeps for OBSERVED
	// (runtime) edges only, kept SEPARATE so the declared graph stays declared and
	// callers can layer observed evidence on top rather than conflating the two.
	observedReverse map[ServiceKey][]ServiceKey
	observedForward map[ServiceKey][]ServiceKey
	// inferredReverse/inferredForward are the adjacency of InferredRelationships
	// between services, apart from the declared and observed indexes.
	inferredReverse map[ServiceKey][]ServiceKey
	inferredForward map[ServiceKey][]ServiceKey
	// replacedNames are the observed names of the workloads a Pacto target
	// runs as, each mapped to the declared service of that target (see
	// replaceInferred). ObservedNameResolver consults them first.
	replacedNames map[string]replacedName
	// observingSources are the ids of the sources that reported at least one
	// observed edge: an inferred service's calls are known only where one of
	// them is available.
	observingSources map[string]bool
	// workloadOnlyPartial: Build found the snapshot partial only because of a
	// workload source. Restrict and the external fold clear it.
	workloadOnlyPartial bool
}

// ProvisioningRecord is one validated provisioning report (see
// [RawProvisioning]) about the consumer revision Digest of ServiceKey. Source
// and SourceKind name the source that delivered it.
type ProvisioningRecord struct {
	ServiceKey    ServiceKey `json:"serviceKey"`
	Service       string     `json:"service"`
	Domain        string     `json:"domain,omitempty"`
	Digest        string     `json:"digest"`
	Kind          string     `json:"kind" enum:"configuration,dependency"`
	Name          string     `json:"name"`
	Environment   string     `json:"environment,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	ExternalRef   string     `json:"externalRef,omitempty"`
	Phase         string     `json:"phase,omitempty"`
	Outcome       string     `json:"outcome"`
	Producer      string     `json:"producer,omitempty"`
	ProducerKeyID string     `json:"producerKeyId,omitempty"`
	ObservedAt    time.Time  `json:"observedAt"`
	AcceptedAt    *time.Time `json:"acceptedAt,omitempty"`
	Verified      bool       `json:"verified"`
	Source        string     `json:"source"`
	SourceKind    string     `json:"sourceKind,omitempty"`
}

// UsageRecord is one validated usage report (see [RawUsage]) about the
// consumer revision Digest of ServiceKey. Stale says it is older than the
// provisioning window when the snapshot was built. Source and SourceKind name
// the source that delivered it.
type UsageRecord struct {
	ServiceKey    ServiceKey             `json:"serviceKey"`
	Service       string                 `json:"service"`
	Domain        string                 `json:"domain,omitempty"`
	Digest        string                 `json:"digest"`
	Kind          string                 `json:"kind" enum:"configuration,dependency"`
	Name          string                 `json:"name"`
	Environment   string                 `json:"environment,omitempty"`
	Provider      string                 `json:"provider,omitempty"`
	ExternalRef   string                 `json:"externalRef,omitempty"`
	Metrics       []evidence.UsageMetric `json:"metrics,omitempty"`
	Cost          *evidence.UsageCost    `json:"cost,omitempty"`
	Outcome       string                 `json:"outcome"`
	Producer      string                 `json:"producer,omitempty"`
	ProducerKeyID string                 `json:"producerKeyId,omitempty"`
	ObservedAt    time.Time              `json:"observedAt"`
	AcceptedAt    *time.Time             `json:"acceptedAt,omitempty"`
	Verified      bool                   `json:"verified"`
	Stale         bool                   `json:"stale"`
	Source        string                 `json:"source"`
	SourceKind    string                 `json:"sourceKind,omitempty"`
}

// UncontractedCaller is a runtime caller no contract describes, of a service
// that has one. Caller is the raw observed identity, for example
// "k8s:payments/Deployment/legacy-billing" (see [K8sCaller]); in a snapshot
// with several clusters a Kubernetes caller carries its cluster first:
// "k8s:<clusterUID>/payments/Deployment/legacy-billing". The k8s: prefix keeps
// it from ever resolving to a contracted service by accident.
type UncontractedCaller struct {
	Caller    string     `json:"caller"`
	ToService ServiceKey `json:"toService"`
	Sources   []string   `json:"sources"`
	// Ambiguous is set when the caller name matched more than one service.
	Ambiguous bool `json:"ambiguous,omitempty"`
	// Entity is the inferred service this caller is, when Pacto sees its
	// workload. The caller stays uncontracted.
	Entity ServiceKey `json:"entity,omitempty" doc:"The inferred service this caller is, when Pacto sees its workload"`
}

// K8sCaller is the identity of a Kubernetes workload with no contract:
// "k8s:" then the escaped cluster id and "/" when cluster is set, then
// namespace/kind/name. Namespace and object names are RFC 1123 and a kind is
// alphanumeric, so the part count tells the two forms apart.
func K8sCaller(cluster, namespace, kind, name string) string {
	c := namespace + "/" + kind + "/" + name
	if cluster != "" {
		c = escapeKeyPart(cluster) + "/" + c
	}
	return "k8s:" + c
}

// ParseK8sCaller decodes a [K8sCaller] string. ok is false for any other
// caller, and for a k8s: caller with neither 3 nor 4 parts.
func ParseK8sCaller(s string) (cluster, namespace, kind, name string, ok bool) {
	rest, k8s := strings.CutPrefix(s, "k8s:")
	parts := strings.Split(rest, "/")
	if !k8s || len(parts) < 3 || len(parts) > 4 {
		return "", "", "", "", false
	}
	if len(parts) == 4 {
		cluster, parts = unescapeKeyPart(parts[0]), parts[1:]
	}
	return cluster, parts[0], parts[1], parts[2], true
}

// Service returns the logical service record by name, or nil.
func (s *FleetSnapshot) Service(name string) *ServiceRecord {
	return s.Services[NewServiceKey(name)]
}
