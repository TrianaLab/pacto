package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/readiness"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// This file builds the strongly typed, discriminated entity-detail model. There
// is no map[string]any: the common envelope makes the entity kind explicit and
// carries EXACTLY ONE kind-specific payload (service / revision / target / owner
// / source). Every nested collection is a bounded preview (see preview.go), and
// every finding or limitation aggregated across more than one entity carries the
// canonical reference of the entity it affects, so a consumer never sees an
// orphan finding. The whole answer is deep-cloned before return, so a caller can
// mutate it without touching the immutable snapshot or any other answer.

// OwnershipInfo is an entity's ownership summary with a BOUNDED preview of
// per-revision ownership conflicts. Conflicts is a preview (not a raw slice) so a
// service with a pathological number of differently-owned revisions can never
// produce an unbounded ownership block.
type OwnershipInfo struct {
	// Owner is the human LABEL and presentation only; Ref carries the identity. Two
	// entities showing the same Owner may belong to two different owners.
	Owner string `json:"owner,omitempty"`
	// Ref is the canonical owner page. It is absent when the declaration carries no
	// canonical identity — the contract permits an owner block of contacts alone,
	// and there is no owner page for an email address.
	Ref *EntityRef `json:"ref,omitempty"`
	// Declared separates "ownership was declared, but not under a name the product
	// can route to" from "nobody declared an owner". Without it a contacts-only
	// owner renders as Unowned, which tells an operator to go and find a team that
	// already wrote down how to reach them.
	Declared bool `json:"declared"`
	// Unknown is set only on a service: no readable contract revision and no
	// source that reads the contract its targets run, so Pacto cannot tell
	// whether an owner is declared. The fleet still counts the service as
	// unowned.
	Unknown bool `json:"unknown,omitempty"`
	// Contacts is the declared contact block, and it is present ONLY where the
	// ownership shown IS the entity's own declaration — that is, on a contract
	// revision. A service or a target shows a DERIVED owner (one revision's
	// declaration standing in for the service, or the service's standing in for the
	// target), and printing that revision's contact points beside them would present
	// one arbitrary revision's escalation route as the entity's own. Absent therefore
	// means "not carried here", never "no contacts declared"; the revision page is
	// where the declaration is inspected.
	//
	// It is contact metadata and never identity: Ref stays absent for a contacts-only
	// owner however many contact points this carries.
	Contacts  *ContactsPreview `json:"contacts,omitempty"`
	Conflicts StringsPreview   `json:"conflicts"`
}

// RevisionIdentity describes how a revision (or a target's contract reference)
// resolved to content along the CONTENT-RETRIEVABILITY dimension only. Retrievable
// is the single content-retrievability judgement ([ContentIdentity.Retrievable])
// that Product Impact eligibility also enforces; IdentityClass carries the
// fine-grained classification (exact / missing-digest / mutable / no-ref / local /
// malformed / digest-mismatch) so a consumer can explain why content is or is not
// retrievable. This is INDEPENDENT of a target's revision-match certainty
// (TargetDetailData.LinkState): a target may be LinkState=exact with
// Retrievable=false when a runtime source knows the exact revision but Pacto cannot
// retrieve that content through a canonical ref.
type RevisionIdentity struct {
	Digest        string        `json:"digest,omitempty"`
	RequestedRef  string        `json:"requestedRef,omitempty"`
	ResolvedRef   string        `json:"resolvedRef,omitempty"`
	Retrievable   bool          `json:"retrievable"`
	IdentityClass IdentityClass `json:"identityClass" enum:"exact,missing-digest,mutable,no-ref,local,malformed,digest-mismatch"`
}

// revisionIdentity builds the identity block for a resolved reference, using the
// content-retrievability classifier so RevisionDetail and TargetDetail report the
// same Retrievable judgement Product Impact enforces.
func revisionIdentity(recordedDigest, requestedRef, resolvedRef string) RevisionIdentity {
	ei := ClassifyContentIdentity(resolvedRef, recordedDigest)
	return RevisionIdentity{
		Digest: recordedDigest, RequestedRef: requestedRef, ResolvedRef: resolvedRef,
		Retrievable: ei.Retrievable(), IdentityClass: ei.Class,
	}
}

// Observed-runtime bounds. A pathological source could put an arbitrarily large,
// arbitrarily nested map in a target's observed runtime; the product detail must
// bound the OUTPUT, the emitted key/value lengths, the recursion depth AND the
// work (no allocation or stringification proportional to the arbitrary input).
const (
	// maxRuntimeDepth caps how deep the runtime flatten descends; a composite at
	// this depth is summarized as a single short type-and-size marker (never
	// stringified whole).
	maxRuntimeDepth = 6
	// maxRuntimeValueLen caps each flattened scalar value's string length.
	maxRuntimeValueLen = 256
	// maxRuntimeKeyLen caps each flattened key/path length so long keys or deep
	// paths cannot bloat the preview.
	maxRuntimeKeyLen = 256
	// maxRuntimeScan bounds the number of facts the walk inspects, so a huge input
	// cannot make the flatten do unbounded work. Beyond it the walk stops and the
	// true total is reported as unknown.
	maxRuntimeScan = 2 * MaxDetailPreview
)

// ProductReadiness is the product-shaped readiness assessment: every useful scalar
// fact from readiness.Result plus a BOUNDED preview of the per-check results.
// readiness.Result embeds an unbounded Checks slice, so the product detail exposes
// this shape instead of the raw result.
type ProductReadiness struct {
	Score         int                    `json:"score"`
	TotalWeight   int                    `json:"totalWeight"`
	EarnedWeight  int                    `json:"earnedWeight"`
	MinScore      int                    `json:"minScore"`
	PartialCredit float64                `json:"partialCredit"`
	Expires       string                 `json:"expires,omitempty"`
	Expired       bool                   `json:"expired"`
	DaysRemaining *int                   `json:"daysRemaining,omitempty"`
	DoneCount     int                    `json:"doneCount"`
	PartialCount  int                    `json:"partialCount"`
	NotDoneCount  int                    `json:"notDoneCount"`
	DeferredCount int                    `json:"deferredCount"`
	Passing       bool                   `json:"passing"`
	Checks        ReadinessChecksPreview `json:"checks"`
}

// productReadiness maps a raw readiness.Result to the bounded product shape,
// preserving every scalar fact and bounding the per-check list. A nil result
// (no readiness declared) yields nil.
func productReadiness(r *readiness.Result) *ProductReadiness {
	if r == nil {
		return nil
	}
	var days *int
	if r.DaysRemaining != nil {
		d := *r.DaysRemaining
		days = &d
	}
	return &ProductReadiness{
		Score: r.Score, TotalWeight: r.TotalWeight, EarnedWeight: r.EarnedWeight,
		MinScore: r.MinScore, PartialCredit: r.PartialCredit, Expires: r.Expires,
		Expired: r.Expired, DaysRemaining: days, DoneCount: r.DoneCount,
		PartialCount: r.PartialCount, NotDoneCount: r.NotDoneCount,
		DeferredCount: r.DeferredCount, Passing: r.Passing,
		Checks: readinessChecksPreview(r.Checks),
	}
}

// runtimeWalker flattens a target's observed-runtime map into a computationally
// BOUNDED RuntimePreview. Nested maps and slices are
// flattened with dotted / indexed keys; a composite at the depth limit is
// summarized with a short "{map: N keys}" / "[array: N items]" marker (never
// stringified whole); each key and value is length-capped; the walk stops after
// maxRuntimeScan inspected facts; and map keys are selected through a bounded
// buffer (keysBounded), so the flatten never allocates a slice proportional to a
// pathologically wide map. `complete` stays true only if the walk visited the whole
// structure, so the exact total is reported only when it is truthfully known.
type runtimeWalker struct {
	items    []RuntimeFact
	scanned  int
	complete bool
}

// emit records one flattened fact, length-capping its key. Exceeding
// MaxDetailPreview does not make the total unknown: the fact was still inspected.
func (w *runtimeWalker) emit(key, val string) {
	w.scanned++
	if len(w.items) < MaxDetailPreview {
		w.items = append(w.items, RuntimeFact{Key: capLen(key, maxRuntimeKeyLen), Value: val})
	}
}

// budgetExhausted reports (and records) whether the scan budget is spent.
func (w *runtimeWalker) budgetExhausted() bool {
	if w.scanned >= maxRuntimeScan {
		w.complete = false
		return true
	}
	return false
}

func (w *runtimeWalker) walk(prefix string, v any, depth int) {
	switch t := v.(type) {
	case map[string]any:
		w.walkMap(prefix, t, depth)
	case []any:
		w.walkSlice(prefix, t, depth)
	default:
		w.emit(prefix, capRuntimeValue(v))
	}
}

func (w *runtimeWalker) walkMap(prefix string, t map[string]any, depth int) {
	if len(t) == 0 {
		w.emit(prefix, "{}")
		return
	}
	if depth >= maxRuntimeDepth {
		w.emit(prefix, fmt.Sprintf("{map: %d keys}", len(t)))
		return
	}
	keys, all := keysBounded(t, maxRuntimeScan-w.scanned)
	if !all {
		w.complete = false
	}
	for _, k := range keys {
		if w.budgetExhausted() {
			return
		}
		w.walk(prefix+"."+k, t[k], depth+1)
	}
}

func (w *runtimeWalker) walkSlice(prefix string, t []any, depth int) {
	if len(t) == 0 {
		w.emit(prefix, "[]")
		return
	}
	if depth >= maxRuntimeDepth {
		w.emit(prefix, fmt.Sprintf("[array: %d items]", len(t)))
		return
	}
	for i, e := range t {
		if w.budgetExhausted() {
			return
		}
		w.walk(fmt.Sprintf("%s[%d]", prefix, i), e, depth+1)
	}
}

// runtimePreview flattens m into a bounded, deterministic RuntimePreview. See
// [runtimeWalker]. The emitted list is capped at MaxDetailPreview, and Total is set
// only when the whole structure was visited within bounds.
func runtimePreview(m map[string]any) RuntimePreview {
	// The top level is driven directly (not via walkMap) so root keys are not
	// prefixed with a leading ".".
	w := &runtimeWalker{complete: true}
	keys, all := keysBounded(m, maxRuntimeScan)
	if !all {
		w.complete = false
	}
	for _, k := range keys {
		if w.budgetExhausted() {
			break
		}
		w.walk(k, m[k], 1)
	}
	rp := RuntimePreview{Count: len(w.items), Scanned: w.scanned, Items: append([]RuntimeFact{}, w.items...)}
	rp.Truncated = len(w.items) < w.scanned || !w.complete
	if w.complete {
		total := w.scanned
		rp.Total = &total
	}
	return rp
}

// capRuntimeValue stringifies a SCALAR runtime value and caps its length so one
// huge scalar cannot bloat the preview. Composites are summarized by the walk and
// never reach this function, so it never stringifies a whole nested structure.
func capRuntimeValue(v any) string { return capLen(fmt.Sprint(v), maxRuntimeValueLen) }

// capLen truncates s to at most max runes, appending an ellipsis when truncated.
func capLen(s string, max int) string {
	if len(s) <= max { // fast path: byte length <= max implies rune count <= max
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// keysBounded returns up to limit lexicographically-smallest keys of m in sorted
// order and whether ALL of m's keys fit (len(m) <= limit). It allocates O(limit),
// never O(len(m)), so flattening a pathologically wide runtime map does not build a
// key slice proportional to that width. It visits every key once to select the
// smallest deterministically.
//
// This runs ONLY at Build time (the single documented unbounded-source pass over an
// untrusted source map), never at query time: the product query reads the
// precomputed bounded RuntimePreview, so no request ever pays this cost. Selecting
// the globally-smallest keys of an unordered map inherently inspects every key; the
// bound belongs at the source boundary, not in a clever query-time selection.
//
// ponytail: O(len(m)*limit) time worst case (keys arriving in descending order) and
// O(limit) space, both paid once at Build. Switch to a heap if a real Build-time
// workload ever makes the time cost show up.
func keysBounded[V any](m map[string]V, limit int) (keys []string, all bool) {
	if limit <= 0 {
		return nil, len(m) == 0
	}
	if len(m) <= limit {
		keys = make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, true
	}
	buf := make([]string, 0, limit)
	for k := range m {
		i := sort.SearchStrings(buf, k)
		switch {
		case len(buf) < limit:
			buf = append(buf, "")
			copy(buf[i+1:], buf[i:])
			buf[i] = k
		case i < limit: // k is smaller than the current largest kept key
			copy(buf[i+1:], buf[i:limit-1])
			buf[i] = k
		}
	}
	return buf, false
}

// ServiceDetailData is the service-kind payload: aggregate ownership, bounded
// previews of revisions / deployments / dependencies / dependents /
// relationships, and findings, evidence and limitations aggregated across the
// service's targets (each attributed to the affected target).
type ServiceDetailData struct {
	Domain    string         `json:"domain,omitempty"`
	Ownership *OwnershipInfo `json:"ownership,omitempty"`
	// OwnerRevision is the newest revision that declares an owner, the one the
	// service owner is taken from and where its contacts are shown. Nil when no
	// revision declares one (the owner, if any, comes from a target).
	OwnerRevision *EntityRef `json:"ownerRevision,omitempty"`
	// Capabilities are the distinct capability types the service's readable
	// revisions declare.
	Capabilities StringsPreview `json:"capabilities"`
	// Sources are the sources that contributed a revision or a target of the
	// service, each with its health.
	Sources RefPreview `json:"sources"`
	// UnresolvedDependencies are the declared dependencies no service in the
	// fleet resolves, external ones included, one row per ref. Together with
	// Dependencies and HiddenDependencies they add up to
	// Summary.DeclaredDependencies.
	UnresolvedDependencies UnresolvedDependenciesPreview `json:"unresolvedDependencies"`
	// ConsumedOfferings are the platform offerings the service consumes: its
	// declared dependencies and references that resolve to a provider.
	ConsumedOfferings OfferingUsesPreview `json:"consumedOfferings"`
	// InferredOfferings are suggestions, never declared consumption: an external or
	// unresolved dependency whose name equals an offering or a provider's name.
	InferredOfferings []InferredOffering `json:"inferredOfferings"`
	// CurrentRevision is the newest revision in use (see [lessRevisionChrono]),
	// else the newest known one; nil when the service has no revision.
	// Policies and Configurations are what it declares, with local schema
	// content read when the detail is requested.
	CurrentRevision *EntityRef            `json:"currentRevision,omitempty"`
	Policies        PoliciesPreview       `json:"policies"`
	Configurations  ConfigurationsPreview `json:"configurations"`
	// Summary aggregates the COMPLETE target/revision/edge populations. The
	// previews below are bounded, so a consumer that needs a proportion or a total
	// must read it here and never count preview Items.
	Summary   ServiceSummary `json:"summary"`
	Revisions RefPreview     `json:"revisions"`
	// ActiveRevisions is the subset of Revisions at least one target is linked to
	// (exact or inferred): the revisions actually in use, as opposed to every
	// revision the fleet has ever seen.
	ActiveRevisions RefPreview `json:"activeRevisions"`
	// Signatures is the bundle signature check of each revision in use, newest
	// first.
	Signatures   RevisionSignaturesPreview `json:"signatures"`
	Deployments  RefPreview                `json:"deployments"`
	Dependencies RefPreview                `json:"dependencies"`
	Dependents   RefPreview                `json:"dependents"`
	// HiddenDependencies and HiddenDependents count the neighbours the caller's
	// Kubernetes permissions hide (see ServiceRecord), never named.
	HiddenDependencies int `json:"hiddenDependencies,omitempty"`
	HiddenDependents   int `json:"hiddenDependents,omitempty"`
	// ReferencedBy names the services whose revisions declare a configuration or
	// policy REFERENCE to this service. A reference is not a dependency, so these
	// services are absent from Dependents and from graph traversal; a shared
	// configuration or policy service would otherwise never list its consumers.
	ReferencedBy  RefPreview                   `json:"referencedBy"`
	Relationships RelationshipsPreview         `json:"relationships"`
	Findings      AttributedFindingsPreview    `json:"findings"`
	Evidence      EvidencePreview              `json:"evidence"`
	Limitations   AttributedLimitationsPreview `json:"limitations"`
}

// RevisionDetailData is the revision-kind payload: parent service, immutable
// identity/content and provenance, validation, readiness, the DECLARED
// interfaces/configurations/policies/capabilities/state/workload themselves
// (bounded, not merely counted), bounded tools/skills/docs, exact/inferred
// targets, and the previous/next known revisions of the same logical service.
type RevisionDetailData struct {
	Service      EntityRef          `json:"service"`
	Version      string             `json:"version,omitempty"`
	PactoVersion string             `json:"pactoVersion,omitempty"`
	Identity     RevisionIdentity   `json:"identity"`
	Provenance   RevisionProvenance `json:"provenance"`
	Valid        bool               `json:"valid"`
	Readiness    *ProductReadiness  `json:"readiness,omitempty"`
	Validation   FindingsPreview    `json:"validation"`
	// Stages is how each stage of the validation run ended, in run order. Empty
	// when the contract document was never validated.
	Stages []ValidationStage `json:"stages"`
	// Environments is each declared environment and the check of its values.
	Environments   EnvironmentsPreview   `json:"environments"`
	Interfaces     InterfacesPreview     `json:"interfaces"`
	Configurations ConfigurationsPreview `json:"configurations"`
	Policies       PoliciesPreview       `json:"policies"`
	Capabilities   CapabilitiesPreview   `json:"capabilities"`
	Workload       string                `json:"workload,omitempty"`
	State          *StateSummary         `json:"state,omitempty"`
	Dependencies   RelationshipsPreview  `json:"dependencies"`
	Tools          ToolsPreview          `json:"tools"`
	Skills         StringsPreview        `json:"skills"`
	Docs           DocsPreview           `json:"docs"`
	// SBOM is the bounded summary of the revision's software inventory, absent when
	// the bundle ships none (or ships one that could not be parsed — see the
	// snapshot limitations, which distinguish the two).
	SBOM *SBOMSummary `json:"sbom,omitempty"`
	// Metadata is the contract's declared free-form metadata, bounded at Build.
	Metadata        RuntimePreview     `json:"metadata"`
	ExactTargets    RefPreview         `json:"exactTargets"`
	InferredTargets RefPreview         `json:"inferredTargets"`
	Previous        *EntityRef         `json:"previous,omitempty"`
	Next            *EntityRef         `json:"next,omitempty"`
	Ownership       *OwnershipInfo     `json:"ownership,omitempty"`
	Limitations     LimitationsPreview `json:"limitations"`
}

// TargetDetailData is the target-kind payload: the logical service and linked
// revision, the exact/inferred/ambiguous/unresolved link state, compliance,
// coverage, findings, observed runtime, contributing sources, the target's own
// contract identity, evidence/reconciliation timestamps, and stale/quarantined
// state.
type TargetDetailData struct {
	Service         EntityRef       `json:"service"`
	Revision        *EntityRef      `json:"revision,omitempty"`
	LinkState       string          `json:"linkState" enum:"exact,inferred,ambiguous,unresolved"`
	Scope           string          `json:"scope,omitempty"`
	Kind            string          `json:"kind,omitempty"`
	Compliance      string          `json:"compliance" enum:"Compliant,NonCompliant,Unknown,Warning,Invalid,Reference,NotEvaluated"`
	Coverage        *Coverage       `json:"coverage,omitempty"`
	Findings        FindingsPreview `json:"findings"`
	ObservedRuntime RuntimePreview  `json:"observedRuntime"`
	// Labels is the observed workload metadata the runtime source reported for THIS
	// target (namespace labels, selectors, annotations a source chose to surface).
	// It is target-scoped observation, unlike ServiceRelationships below.
	Labels RuntimePreview `json:"labels"`
	// Readiness is the readiness result a runtime source reported for this target,
	// present only when a source actually supplies one. A declared readiness gate
	// lives on the Revision; this is the observed counterpart and is absent — not
	// zero — when unobserved.
	Readiness *ProductReadiness `json:"readiness,omitempty"`
	// ServiceRelationships is the dependency neighbourhood of this target's LOGICAL
	// SERVICE. The snapshot attributes declared and observed edges to a service,
	// never to an individual target, so this is service-scoped context shown in the
	// target's frame: it is NOT evidence that this particular target made those
	// calls. It is named for its real scope so a consumer cannot present it as
	// target-scoped observation.
	ServiceRelationships RelationshipsPreview `json:"serviceRelationships"`
	Sources              StringsPreview       `json:"sources"`
	Source               string               `json:"source,omitempty"`
	Identity             RevisionIdentity     `json:"identity"`
	EvidenceAt           *time.Time           `json:"evidenceAt,omitempty"`
	ReconciledAt         *time.Time           `json:"reconciledAt,omitempty"`
	Stale                bool                 `json:"stale"`
	Quarantined          bool                 `json:"quarantined,omitempty"`
	Ownership            *OwnershipInfo       `json:"ownership,omitempty"`
	Limitations          LimitationsPreview   `json:"limitations"`
	Environment          TargetEnvironment    `json:"environment"`
}

// TargetEnvironment is the environment a target runs in, read from the
// TargetEnvironmentLabel, and whether the revision it runs declares it. An empty
// Name means unknown. Declared is unknown whenever Pacto cannot tell, with Reason
// saying why; Check is the declared environment's values check.
type TargetEnvironment struct {
	Name     string             `json:"name,omitempty"`
	Declared string             `json:"declared" enum:"declared,not-declared,unknown"`
	Reason   string             `json:"reason,omitempty"`
	Check    *EnvironmentResult `json:"check,omitempty"`
}

// targetEnvironmentName is the environment a target runs in, and whether its
// sources disagree on the label (then the name is empty).
func targetEnvironmentName(t *TargetRecord) (name string, conflict bool) {
	return t.Environment, t.environmentConflict
}

// targetEnvironment says where a target runs and whether its linked revision
// declares that environment.
func (q *Query) targetEnvironment(t *TargetRecord) TargetEnvironment {
	name, conflict := targetEnvironmentName(t)
	rev := q.snap.Revisions[t.ContractRevision]
	switch {
	case conflict:
		return TargetEnvironment{Declared: "unknown", Reason: "Sources disagree on this target's environment label."}
	case name == "":
		return TargetEnvironment{Declared: "unknown", Reason: "No source reports this target's environment. Pacto reads it from the " + TargetEnvironmentLabel + " label."}
	case rev == nil:
		return TargetEnvironment{Name: name, Declared: "unknown", Reason: "Pacto cannot tell which revision runs here, so it cannot check the environment against a contract."}
	}
	for _, e := range rev.Environments {
		if e.Name == name {
			return TargetEnvironment{Name: name, Declared: "declared", Check: &e}
		}
	}
	return TargetEnvironment{Name: name, Declared: "not-declared", Reason: "Revision " + revisionEntityRef(rev).Label + " does not declare environment " + name + "."}
}

// OwnerDetailData is the owner-kind payload: the complete owner-scoped aggregate,
// bounded previews of the owner's services, revisions and deployments, and a
// bounded attention preview.
type OwnerDetailData struct {
	// Summary covers the owner's COMPLETE populations. The previews beside it are
	// capped, so an owner-scoped proportion must be drawn from here: counting the
	// previews would turn "the first 200 targets" into "this owner's estate".
	Summary     OwnerSummary     `json:"summary"`
	Services    RefPreview       `json:"services"`
	Revisions   RefPreview       `json:"revisions"`
	Deployments RefPreview       `json:"deployments"`
	Attention   AttentionPreview `json:"attention"`
}

// DomainDetailData is the domain-kind payload: the estate of the services whose
// business domain this is (see ServiceRecord.BusinessDomain). Summary.Revisions
// counts those services' revisions, whatever domain each one declared.
type DomainDetailData struct {
	Summary     OwnerSummary     `json:"summary"`
	Ownership   OwnershipTally   `json:"ownership"`
	Services    RefPreview       `json:"services"`
	Owners      RefPreview       `json:"owners"`
	Deployments RefPreview       `json:"deployments"`
	Attention   AttentionPreview `json:"attention"`
}

// OfferingUse is one service consuming a provider of a platform offering, by a
// declared dependency or a configuration or policy reference. Compatibility and
// RangeStatus are the relationship's own, merged over the consumer's revisions:
// Compatibility is empty and RangeStatus unknown when they disagree.
type OfferingUse struct {
	Offering      string    `json:"offering"`
	Consumer      EntityRef `json:"consumer"`
	Provider      EntityRef `json:"provider"`
	Via           string    `json:"via" enum:"dependency,configuration_reference,policy_reference"`
	Compatibility string    `json:"compatibility,omitempty"`
	RangeStatus   string    `json:"rangeStatus,omitempty" enum:"satisfied,unsatisfied,unknown"`
}

// OfferingUsesPreview is a bounded preview of offering uses.
type OfferingUsesPreview struct {
	Total     int           `json:"total"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated"`
	Items     []OfferingUse `json:"items"`
}

func offeringUsesPreview(uses []OfferingUse) OfferingUsesPreview {
	it, total, trunc := boundSlice(uses, MaxDetailPreview)
	return OfferingUsesPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// ConsumeSnippet is the contract YAML a service adds to consume a provider of a
// platform offering, rendered once so every surface shows the same text.
// Dependency, Configuration and Policy say which blocks YAML holds.
type ConsumeSnippet struct {
	Ref           string `json:"ref"`
	Compatibility string `json:"compatibility,omitempty"`
	Dependency    bool   `json:"dependency"`
	Configuration bool   `json:"configuration"`
	Policy        bool   `json:"policy"`
	YAML          string `json:"yaml"`
}

// InferredOffering suggests the offering an external or unresolved dependency
// may be: its name equals the offering (offering-name) or the name of a service
// that provides it (provider-name, with Provider set).
type InferredOffering struct {
	// Ref is the UnresolvedDependency.Ref it was matched from.
	Ref      string     `json:"ref"`
	Offering EntityRef  `json:"offering"`
	Provider *EntityRef `json:"provider,omitempty"`
	Reason   string     `json:"reason" enum:"offering-name,provider-name"`
}

// OfferingProvider is one service providing a platform offering: who owns it,
// its versions, what its current revision declares and how to consume it.
// Consume is nil when the provider has no OCI reference to consume.
type OfferingProvider struct {
	Service         EntityRef             `json:"service"`
	Ownership       *OwnershipInfo        `json:"ownership,omitempty"`
	Summary         string                `json:"summary,omitempty"`
	CurrentRevision *EntityRef            `json:"currentRevision,omitempty"`
	Revisions       RefPreview            `json:"revisions"`
	ActiveRevisions RefPreview            `json:"activeRevisions"`
	Interfaces      InterfacesPreview     `json:"interfaces"`
	Configurations  ConfigurationsPreview `json:"configurations"`
	Policies        PoliciesPreview       `json:"policies"`
	Consume         *ConsumeSnippet       `json:"consume,omitempty"`
}

// OfferingProvidersPreview is a bounded preview of offering providers.
type OfferingProvidersPreview struct {
	Total     int                `json:"total"`
	Count     int                `json:"count"`
	Truncated bool               `json:"truncated"`
	Items     []OfferingProvider `json:"items"`
}

// MaxOfferingProviders bounds OfferingDetailData.Providers.
const MaxOfferingProviders = 25

// OfferingDetailData is the offering-kind payload: the services that provide a
// platform offering (see ServiceRecord.Offering) and the services that consume
// them.
type OfferingDetailData struct {
	Providers OfferingProvidersPreview `json:"providers"`
	Consumers OfferingUsesPreview      `json:"consumers"`
}

// SourceContribution counts the DISTINCT product entities attributable to one
// source, per kind, over the COMPLETE attributable population — never over the
// bounded [SourceDetailData.Entities] preview.
//
// It is a different measurement from the raw record counts beside it
// ([SourceDetailData.RevisionCount] / [SourceDetailData.TargetCount]) and the two
// legitimately disagree, so they are reported separately rather than reconciled:
//
//   - Records are per source, per RAW RECORD: a revision two sources both reported
//     is one record in each of their counts, and a source that reported the same
//     revision twice counted it twice.
//   - Contribution is per DISTINCT ENTITY: those two sources contribute one shared
//     revision entity each, and the repeat contributes one.
//   - Services are DERIVED, never reported: no source sends a service record, and a
//     service is attributable to every source that reported one of its revisions or
//     one of its targets. So Services is nonzero for a source whose record counts
//     are entirely revisions, and the same service is attributable to several
//     sources at once.
//
// Sum(Services, Revisions, Targets) is exactly [SourceDetailData.Entities].Total.
// The source entity ITSELF is not attributable to itself and is not counted here;
// [Query.Entities] with a source filter does include it, because that filter answers
// "what does this source scope contain", not "what did it contribute".
type SourceContribution struct {
	Services  int `json:"services"`
	Revisions int `json:"revisions"`
	Targets   int `json:"targets"`
}

// SourceDetailData is the source-kind payload: kind, health, sync/observation
// timestamps, raw record counts, the per-kind contributed-entity breakdown, a
// bounded preview of those entities, the sanitized source error, and the source's
// own limitations.
type SourceDetailData struct {
	Kind               string     `json:"kind,omitempty"`
	Health             string     `json:"health" enum:"available,partial,stale,unavailable"`
	LastSuccessfulSync *time.Time `json:"lastSuccessfulSync,omitempty"`
	ObservedAt         *time.Time `json:"observedAt,omitempty"`
	// RevisionCount and TargetCount are RAW RECORDS this source supplied, counted as
	// ingested — see [SourceContribution] for why they are not the contributed-entity
	// totals and must not be presented as them.
	RevisionCount int                `json:"revisionCount"`
	TargetCount   int                `json:"targetCount"`
	Contributed   SourceContribution `json:"contributed"`
	Entities      RefPreview         `json:"entities"`
	Error         *SourceError       `json:"error,omitempty"`
	Limitations   LimitationsPreview `json:"limitations"`
}

// EntityDetail is the common, versioned, discriminated envelope for any entity's
// full detail. Entity.Kind is the discriminator; EXACTLY ONE
// of Service/Revision/Target/Owner/Source is populated. Actions lists the
// available semantic actions route-neutrally (the transport maps them to hrefs).
type EntityDetail struct {
	Meta     ProductMeta         `json:"meta"`
	Entity   EntityRef           `json:"entity"`
	Status   string              `json:"status,omitempty"`
	Service  *ServiceDetailData  `json:"service,omitempty"`
	Revision *RevisionDetailData `json:"revision,omitempty"`
	Target   *TargetDetailData   `json:"target,omitempty"`
	Owner    *OwnerDetailData    `json:"owner,omitempty"`
	Source   *SourceDetailData   `json:"source,omitempty"`
	Domain   *DomainDetailData   `json:"domain,omitempty"`
	Offering *OfferingDetailData `json:"offering,omitempty"`
	Actions  []string            `json:"actions,omitempty"`
}

// EntityDetail returns the unified detail envelope for one entity, deep-cloned so
// the caller can mutate it freely. Supported kinds: service, revision, target,
// owner, source, domain and offering.
func (q *Query) EntityDetail(kind EntityKind, key string) (*EntityDetail, error) {
	d, err := q.entityDetail(kind, key)
	if err != nil {
		return nil, err
	}
	// One terminal deep copy severs every alias into the snapshot: a caller may
	// mutate any nested map, slice or pointer without touching the snapshot or a
	// later answer (requirement: product-query immutability).
	return jsonClone(d), nil
}

func (q *Query) entityDetail(kind EntityKind, key string) (*EntityDetail, error) {
	switch kind {
	case KindService:
		return q.serviceDetail(key)
	case KindRevision:
		return q.revisionDetail(key)
	case KindTarget:
		return q.targetDetail(key)
	case KindOwner:
		return q.ownerDetail(key)
	case KindSource:
		return q.sourceDetail(key)
	case KindDomain:
		return q.domainDetail(key)
	case KindOffering:
		return q.offeringDetail(key)
	default:
		return nil, &InvalidQueryError{Field: "kind", Value: string(kind), Reason: "not a known entity kind"}
	}
}

func allViews() []KnowledgeView { return []KnowledgeView{ViewExpected, ViewObserved, ViewDifferences} }

func (q *Query) serviceDetail(key string) (*EntityDetail, error) {
	view, err := q.GetService(key)
	if err != nil {
		return nil, err
	}
	s := view.Service
	summary, inUse := q.serviceSummary(view)
	var active []*ContractRevision
	for _, r := range view.Revisions {
		if inUse[r.Key] {
			active = append(active, r)
		}
	}
	self := serviceEntityRef(s)
	ownership := serviceOwnership(s, view.Revisions)
	ownership.Unknown = !ownership.Declared && q.ownershipUnknown(s)
	nb, nbErr := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(s.Key), Direction: DirectionBoth, Views: allViews()})
	rels, nbLims := serviceRelationships(nb, nbErr, self)
	current, cfgs, pols := q.currentRevisionContent(active, view.Revisions)
	unresolved := unresolvedDependencyItems(self, view.Dependencies)
	data := &ServiceDetailData{
		Domain:                 s.Domain,
		Ownership:              ownership,
		OwnerRevision:          q.ownerRevision(s),
		Capabilities:           stringsPreview(slices.DeleteFunc(q.serviceCapabilities(s), func(c string) bool { return c == "" })),
		Sources:                refPreview(q.serviceSources(view)),
		UnresolvedDependencies: unresolved,
		ConsumedOfferings:      offeringUsesPreview(q.offeringUses(func(*ServiceRecord) bool { return true }, s.Key)),
		InferredOfferings:      q.inferredOfferings(unresolved.Items),
		CurrentRevision:        current,
		Policies:               pols,
		Configurations:         cfgs,
		Summary:                summary,
		// Newest first, in revision chronology: a version list ordered by content
		// digest is unreadable as history.
		Revisions:          refPreview(chronologicalRevisionRefs(view.Revisions)),
		ActiveRevisions:    refPreview(chronologicalRevisionRefs(active)),
		Signatures:         revisionSignatures(active),
		Deployments:        refPreview(targetRefs(view.Targets)),
		Dependencies:       refPreview(q.dependencyRefs(view.Dependencies)),
		Dependents:         refPreview(q.serviceKeyRefs(view.Dependents)),
		HiddenDependencies: s.HiddenDependencies,
		HiddenDependents:   s.HiddenDependents,
		ReferencedBy:       refPreview(q.referencingServices(s.Key)),
		Findings:           attributedTargetFindingsPreview(view.Targets),
		Evidence:           evidencePreview(evidenceForTargets(view.Targets)),
		Relationships:      rels,
		Limitations:        serviceLimitations(nbLims, view.Targets),
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: self, Status: s.Status,
		Service: data, Actions: []string{"open-graph", "compare", "impact"},
	}, nil
}

func (q *Query) revisionDetail(key string) (*EntityDetail, error) {
	rev := q.snap.Revisions[RevisionKey(key)]
	if rev == nil {
		return nil, &NotFoundError{Kind: "revision", ID: key}
	}
	exact, inferred := q.targetsForRevision(rev.Key)
	prev, next := q.siblingRevisions(rev)
	cfgRefs, polRefs := q.revisionRefResolutions(rev.Key)
	cfgs, pols := withSchemaContent(rev, withConfigResolutions(rev.configurations, cfgRefs), policiesPreview(rev.Contract.Policies, polRefs))
	data := &RevisionDetailData{
		Configurations:  cfgs,
		Policies:        pols,
		Service:         q.serviceRef(rev.ServiceKey),
		Version:         rev.Version,
		PactoVersion:    rev.PactoVersion,
		Identity:        revisionIdentity(rev.Digest, rev.RequestedRef, rev.ResolvedRef),
		Provenance:      revisionProvenance(rev),
		Valid:           rev.Valid,
		Readiness:       productReadiness(rev.Readiness),
		Validation:      findingsPreview(rev.Validation),
		Stages:          append([]ValidationStage{}, rev.Stages...),
		Environments:    environmentsPreview(rev.Environments),
		Capabilities:    capabilitiesPreview(rev.Contract.Capabilities),
		Interfaces:      interfacesPreview(rev.Contract.Interfaces, rev.Tools, rev.SpecsRead, q.snap.GeneratedAt),
		Workload:        rev.Contract.Workload,
		State:           stateSummary(rev.Contract.State),
		Dependencies:    relationshipsPreview(q.revisionEdges(rev.Key)),
		Tools:           toolsPreview(rev.Tools),
		Skills:          stringsPreview(rev.Skills),
		Docs:            docsPreview(rev.Docs),
		SBOM:            rev.SBOM,
		Metadata:        rev.Metadata,
		ExactTargets:    refPreview(exact),
		InferredTargets: refPreview(inferred),
		Previous:        prev,
		Next:            next,
		Ownership:       revisionOwnership(rev),
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: revisionEntityRef(rev), Status: revisionStatus(rev),
		Revision: data, Actions: []string{"open-graph", "compare", "impact"},
	}, nil
}

func (q *Query) targetDetail(key string) (*EntityDetail, error) {
	tv, err := q.GetTarget(key)
	if err != nil {
		return nil, err
	}
	t := tv.Target
	data := &TargetDetailData{
		Service:    q.serviceRef(t.ServiceKey),
		LinkState:  targetLinkState(t),
		Scope:      t.Scope,
		Kind:       t.Kind,
		Compliance: t.Compliance,
		Coverage:   t.Coverage,
		Findings:   findingsPreview(t.Findings),
		// Read off the snapshot record, not off t: tv.Target is a JSON clone and the
		// bounded label projection is unexported, so it does not survive the clone.
		Labels:       q.snap.Targets[t.Key].labels,
		Readiness:    productReadiness(t.Readiness),
		Sources:      stringsPreview(t.Sources),
		Source:       t.Source,
		Identity:     revisionIdentity(t.Digest, t.RequestedRef, t.ResolvedRef),
		EvidenceAt:   t.EvidenceAt,
		ReconciledAt: t.ReconciledAt,
		Stale:        t.Stale,
		Quarantined:  t.Quarantined,
		Ownership:    targetOwnership(q.snap.Services[t.ServiceKey]),
		Limitations:  limitationsPreview(t.Limitations),
		Environment:  q.targetEnvironment(q.snap.Targets[t.Key]), // the record: the clone drops unexported state
		// The observed-runtime preview was bounded ONCE at Build time (see
		// TargetRecord.ObservedRuntime); the product query reads that precomputed
		// projection in O(bound), never re-flattening an arbitrarily wide raw map.
		ObservedRuntime: t.ObservedRuntime,
	}
	if t.ContractRevision != "" {
		if rev := q.snap.Revisions[t.ContractRevision]; rev != nil {
			ref := revisionEntityRef(rev)
			data.Revision = &ref
		}
	}
	if nb, e := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(t.ServiceKey), Direction: DirectionBoth, Views: allViews()}); e == nil {
		data.ServiceRelationships = relationshipsPreviewFromNeighborhood(nb)
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: targetEntityRef(t), Status: t.Compliance,
		Target: data, Actions: []string{"open-graph", "service"},
	}, nil
}

func (q *Query) ownerDetail(key string) (*EntityDetail, error) {
	var services, deployments, revisions []EntityRef
	// The aggregate is accumulated in the SAME walk that builds the previews, over the
	// same records, so the summary and the lists can never describe two populations.
	var sum OwnerSummary
	// The estate is read off the DECLARATIONS, in one walk over the revisions: a
	// service belongs here when any of its revisions names this owner, which is the
	// same rule [Query.ownerClaims] applies to every owner filter. Reading it off
	// ServiceRecord.Owner instead would show an owner one revision of a disputed
	// service and none of the service that revision belongs to.
	//
	// The comparison is IDENTITY ([contract.Owner.IsKey]), never the free-text owner
	// search: this page IS one canonical owner, and a substring rule would hand
	// `team-a` the estate of `team-a-platform`.
	claimed := map[ServiceKey]bool{}
	for _, r := range q.snap.Revisions {
		if r.Owner.IsKey(key) {
			claimed[r.ServiceKey] = true
			revisions = append(revisions, revisionEntityRef(r))
			// Read validity from the authoritative snapshot record: revisionStatus knows
			// the difference between "validated and invalid" and "never validated".
			if revisionStatus(r) == StatusInvalid {
				sum.InvalidRevisions++
			}
		}
	}
	for sk, s := range q.snap.Services {
		if !claimed[sk] {
			continue
		}
		services = append(services, serviceEntityRef(s))
		for _, tk := range s.Targets {
			if t := q.snap.Targets[tk]; t != nil {
				deployments = append(deployments, targetEntityRef(t))
				sum.addTarget(t)
			}
		}
	}
	if len(services) == 0 && len(revisions) == 0 {
		return nil, &NotFoundError{Kind: "owner", ID: key}
	}
	sum.Services, sum.Revisions, sum.Targets = len(services), len(revisions), len(deployments)
	sortEntityRefs(services)
	sortEntityRefs(deployments)
	sortEntityRefs(revisions)
	// A constant, valid filter (owner key only) never errors; ignore it deliberately.
	// Attention is ALREADY offset-paged; build the preview from the list (not just
	// its Items) so the owner detail reports the TRUE matched total and truncation,
	// never a double-truncated page count. The key is the same identity the estate
	// above was walked with, so the backlog and the estate describe one owner.
	ownerAttention, _ := q.Attention(AttentionFilter{OwnerKey: key})
	return &EntityDetail{
		Meta: q.productMeta(), Entity: ownerEntityRef(key),
		Owner: &OwnerDetailData{
			Summary:     sum,
			Services:    refPreview(services),
			Revisions:   refPreview(revisions),
			Deployments: refPreview(deployments),
			Attention:   attentionPreviewFromList(ownerAttention),
		},
	}, nil
}

// domainDetail is the estate of one business domain, walked the way ownerDetail
// walks an owner's: the summary and the previews come from one pass over the same
// services. No domain has no page; it is the NoBusinessDomain filter.
func (q *Query) domainDetail(key string) (*EntityDetail, error) {
	var services, deployments []EntityRef
	var sum OwnerSummary
	var ownership OwnershipTally
	owners := map[string]bool{}
	for _, s := range q.snap.Services {
		if key == "" || s.BusinessDomain != key {
			continue
		}
		services = append(services, serviceEntityRef(s))
		q.addOwnership(&ownership, s)
		for _, k := range q.ownerClaimsOf(s).keys {
			owners[k] = true
		}
		for _, rk := range s.Revisions {
			sum.Revisions++
			if revisionStatus(q.snap.Revisions[rk]) == StatusInvalid {
				sum.InvalidRevisions++
			}
		}
		for _, tk := range s.Targets {
			t := q.snap.Targets[tk]
			deployments = append(deployments, targetEntityRef(t))
			sum.addTarget(t)
		}
	}
	if len(services) == 0 {
		return nil, &NotFoundError{Kind: "domain", ID: key}
	}
	ownerRefs := make([]EntityRef, 0, len(owners))
	for _, k := range slices.Sorted(maps.Keys(owners)) {
		ownerRefs = append(ownerRefs, ownerEntityRef(k))
	}
	sum.Services, sum.Targets = len(services), len(deployments)
	sortEntityRefs(services)
	sortEntityRefs(deployments)
	// A constant, valid filter never errors.
	attention, _ := q.Attention(AttentionFilter{BusinessDomain: key})
	return &EntityDetail{
		Meta: q.productMeta(), Entity: domainEntityRef(key),
		Domain: &DomainDetailData{
			Summary:     sum,
			Ownership:   ownership,
			Services:    refPreview(services),
			Owners:      refPreview(ownerRefs),
			Deployments: refPreview(deployments),
			Attention:   attentionPreviewFromList(attention),
		},
	}, nil
}

// offeringDetail is one platform offering: its providers, bounded, and the
// services that consume any of them. No offering has no page.
func (q *Query) offeringDetail(key string) (*EntityDetail, error) {
	var providers []EntityRef
	for _, s := range q.snap.Services {
		if key != "" && s.Offering == key {
			providers = append(providers, serviceEntityRef(s))
		}
	}
	if len(providers) == 0 {
		return nil, &NotFoundError{Kind: "offering", ID: key}
	}
	sortEntityRefs(providers)
	shown, total, trunc := boundSlice(providers, MaxOfferingProviders)
	items := make([]OfferingProvider, 0, len(shown))
	for _, ref := range shown {
		items = append(items, q.offeringProvider(q.snap.Services[ServiceKey(ref.Key)]))
	}
	uses := q.offeringUses(func(p *ServiceRecord) bool { return p.Offering == key }, "")
	return &EntityDetail{
		Meta: q.productMeta(), Entity: offeringEntityRef(key),
		Offering: &OfferingDetailData{
			Providers: OfferingProvidersPreview{Total: total, Count: len(items), Truncated: trunc, Items: items},
			Consumers: offeringUsesPreview(uses),
		},
	}, nil
}

// offeringProvider projects one provider of an offering from the snapshot.
func (q *Query) offeringProvider(s *ServiceRecord) OfferingProvider {
	inUse := map[RevisionKey]bool{}
	for _, tk := range s.Targets {
		inUse[q.snap.Targets[tk].ContractRevision] = true
	}
	var all, active []*ContractRevision
	var newest *ContractRevision
	for _, rk := range s.Revisions {
		r := q.snap.Revisions[rk]
		all = append(all, r)
		if inUse[rk] {
			active = append(active, r)
		}
		if readableRevision(r) && (newest == nil || lessRevisionChrono(newest, r)) {
			newest = r
		}
	}
	current, cfgs, pols := q.currentRevisionContent(active, all)
	p := OfferingProvider{
		Service: serviceEntityRef(s), Ownership: serviceOwnership(s, all), Summary: s.Summary, CurrentRevision: current,
		Revisions: refPreview(chronologicalRevisionRefs(all)), ActiveRevisions: refPreview(chronologicalRevisionRefs(active)),
		Interfaces:     interfacesPreview(nil, nil, nil, q.snap.GeneratedAt),
		Configurations: cfgs, Policies: pols, Consume: consumeSnippet(s, newest),
	}
	if current != nil {
		if rec := q.snap.Revisions[RevisionKey(current.Key)]; rec.Contract != nil {
			p.Interfaces = interfacesPreview(rec.Contract.Interfaces, rec.Tools, rec.SpecsRead, q.snap.GeneratedAt)
		}
	}
	return p
}

// offeringUses is every service consuming a provider match accepts, from one
// consumer when from is set: one row per consumer, provider and relationship
// type, over every revision. Revisions that disagree on the range status make
// it unknown.
func (q *Query) offeringUses(match func(provider *ServiceRecord) bool, from ServiceKey) []OfferingUse {
	type useKey struct {
		consumer, provider ServiceKey
		via                string
	}
	seen := map[useKey]int{}
	var uses []OfferingUse
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		if !offeringRelationship(rel, from) {
			continue
		}
		p := q.snap.Services[rel.ToService]
		if p == nil || p.Offering == "" || !match(p) {
			continue
		}
		k := useKey{rel.FromService, rel.ToService, rel.Type}
		if j, ok := seen[k]; ok {
			u := &uses[j]
			if u.RangeStatus == "" {
				u.RangeStatus = rel.RangeStatus
			} else if rel.RangeStatus != "" && rel.RangeStatus != u.RangeStatus {
				u.RangeStatus = RangeUnknown
			}
			if rel.Compatibility != u.Compatibility {
				u.Compatibility = ""
			}
			continue
		}
		seen[k] = len(uses)
		uses = append(uses, OfferingUse{
			Offering: p.Offering, Consumer: q.serviceRef(rel.FromService), Provider: serviceEntityRef(p),
			Via: rel.Type, Compatibility: rel.Compatibility, RangeStatus: rel.RangeStatus,
		})
	}
	slices.SortStableFunc(uses, func(a, b OfferingUse) int {
		return cmp.Or(cmp.Compare(a.Consumer.Key, b.Consumer.Key), cmp.Compare(a.Provider.Key, b.Provider.Key), cmp.Compare(a.Via, b.Via))
	})
	return uses
}

// inferredOfferings suggests an offering for each unresolved dependency whose
// name, the last segment of its ref, equals an offering or, failing that, the
// name of a provider, compared case-insensitively. A partial match is no match.
func (q *Query) inferredOfferings(unresolved []UnresolvedDependency) []InferredOffering {
	out := []InferredOffering{}
	if len(unresolved) == 0 {
		return out
	}
	offerings, providers := map[string]bool{}, map[string]*ServiceRecord{}
	for _, k := range slices.Sorted(maps.Keys(q.snap.Services)) {
		if s := q.snap.Services[k]; s.Offering != "" {
			offerings[s.Offering] = true
			if lower := strings.ToLower(s.Name); providers[lower] == nil {
				providers[lower] = s
			}
		}
	}
	// ponytail: bounded by the unresolved preview.
	for _, u := range unresolved {
		repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(cmp.Or(u.RequestedRef, u.Ref)))
		name := strings.ToLower(path.Base(repo))
		switch p := providers[name]; {
		case offerings[name]:
			out = append(out, InferredOffering{Ref: u.Ref, Offering: offeringEntityRef(name), Reason: "offering-name"})
		case p != nil:
			out = append(out, InferredOffering{Ref: u.Ref, Offering: offeringEntityRef(p.Offering), Provider: new(serviceEntityRef(p)), Reason: "provider-name"})
		}
	}
	return out
}

// offeringRelationship reports whether rel can consume an offering: a declared,
// resolved and visible dependency or configuration or policy reference to
// another service, from from when it is set. An observed call is no declared use.
func offeringRelationship(rel *Relationship, from ServiceKey) bool {
	return rel.Provenance == ProvenanceDeclared && rel.Resolved && !rel.Hidden && rel.ToService != rel.FromService && (from == "" || rel.FromService == from)
}

func (q *Query) sourceDetail(key string) (*EntityDetail, error) {
	var st *SourceState
	for i := range q.snap.Sources {
		if q.snap.Sources[i].ID == key {
			st = &q.snap.Sources[i]
			break
		}
	}
	if st == nil {
		return nil, &NotFoundError{Kind: "source", ID: key}
	}
	var lims []Limitation
	for _, l := range q.snap.Limitations {
		if l.Source == key {
			lims = append(lims, l)
		}
	}
	// Counted over the COMPLETE attributable population, before the preview bounds it:
	// a source past MaxDetailPreview must still report its true breakdown.
	entities := q.entitiesFromSource(key)
	var contributed SourceContribution
	for _, e := range entities {
		switch e.Kind {
		case KindService:
			contributed.Services++
		case KindRevision:
			contributed.Revisions++
		case KindTarget:
			contributed.Targets++
		}
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: sourceEntityRef(*st), Status: string(st.Status),
		Source: &SourceDetailData{
			Kind:               st.Kind,
			Health:             string(st.Status),
			LastSuccessfulSync: st.LastSuccessfulSync,
			ObservedAt:         st.ObservedAt,
			RevisionCount:      st.RevisionCount,
			TargetCount:        st.TargetCount,
			Contributed:        contributed,
			Entities:           refPreview(entities),
			Error:              st.Error,
			Limitations:        limitationsPreview(lims),
		},
	}, nil
}

// targetLinkState classifies a target's REVISION-MATCH CERTAINTY as exact,
// inferred, ambiguous or unresolved -- how confidently the fleet knows which
// revision this target is running. This is a different question from whether the
// linked content is resolver-retrievable (RevisionIdentity.Retrievable): an exact
// match can point at content Pacto cannot fetch (a trusted digest with no canonical
// ref). Only exact and inferred are ever recorded on the target; ambiguous is
// derived from the ambiguity limitation, and everything else is unresolved.
func targetLinkState(t *TargetRecord) string {
	switch t.RevisionMatch {
	case revisionMatchExact:
		return "exact"
	case revisionMatchInferred:
		return "inferred"
	default:
		if hasLimitation(t.Limitations, LimitationRevisionAmbiguous) {
			return "ambiguous"
		}
		return "unresolved"
	}
}

// dependencyRefs turns a service's declared dependency relationships into DISTINCT
// provider-service references (skipping unresolved edges that name no provider
// service, and collapsing multiple relationships to the same provider).
func (q *Query) dependencyRefs(deps []Relationship) []EntityRef {
	seen := map[ServiceKey]bool{}
	var out []EntityRef
	for _, rel := range deps {
		if rel.ToService == "" || seen[rel.ToService] {
			continue
		}
		if s := q.snap.Services[rel.ToService]; s != nil {
			seen[rel.ToService] = true
			out = append(out, serviceEntityRef(s))
		}
	}
	sortEntityRefs(out)
	return out
}

// siblingRevisions returns the PREVIOUS (older) and NEXT (newer) known revisions
// of the same logical service in canonical revision chronology (nil at the ends).
//
// "Previous"/"next" mean revision chronology, NOT RevisionKey lexicography. A
// RevisionKey is `ServiceKey@contentID` where contentID is usually a content
// digest, so lexical key order is digest order -- semantically arbitrary. The
// canonical order is instead: revisions with a valid semver version sort ascending
// by semver (so 1.9.0 < 1.10.0 < 2.0.0 and a prerelease sorts before its release),
// and revisions without a valid semver version sort AFTER all semver revisions.
// The immutable content digest embedded in the RevisionKey is used ONLY as a
// deterministic tie-breaker between two revisions that compare equal (the same
// version, or both non-semver) -- never as the primary version chronology -- so
// changing a revision's content digest never reorders two distinct versions and a
// map/source iteration permutation never changes the result.
func (q *Query) siblingRevisions(rev *ContractRevision) (prev, next *EntityRef) {
	var revs []*ContractRevision
	for _, r := range q.snap.Revisions {
		if r.ServiceKey == rev.ServiceKey {
			revs = append(revs, r)
		}
	}
	sort.Slice(revs, func(i, j int) bool { return lessRevisionChrono(revs[i], revs[j]) })
	for i, r := range revs {
		if r.Key != rev.Key {
			continue
		}
		if i > 0 {
			e := revisionEntityRef(revs[i-1])
			prev = &e
		}
		if i < len(revs)-1 {
			e := revisionEntityRef(revs[i+1])
			next = &e
		}
		break
	}
	return prev, next
}

// lessRevisionChrono is the canonical ascending revision order used by
// siblingRevisions: semver chronology first, the immutable RevisionKey as a
// deterministic tie-breaker only (see siblingRevisions for the full rationale).
func lessRevisionChrono(a, b *ContractRevision) bool {
	if c := semver.Compare(a.Version, b.Version); c != 0 {
		return c < 0
	}
	return a.Key < b.Key
}

// revisionEdges builds the declared, resolved dependency edges a specific revision
// declares. Observed edges carry no revision, so only this revision's declared
// dependencies appear; every edge shares this revision's service as its source, so
// ordering by destination is sufficient and deterministic.
//
// This is FINE-GRAINED scope, so it builds through the same [dependencyEdge] the
// revision projection uses. The merged service-scope pipeline is deliberately not
// reused here: it would compute an edge-scope declared-vs-observed Difference for an
// edge nobody observed at revision granularity, which both over-claims and
// self-contradicts -- "expected, not observed" printed beside a declared claim whose
// own service-scoped reconciliation says matched. The service-scoped fact travels as
// ObservationScope + ServiceCorroboration instead.
func (q *Query) revisionEdges(revKey RevisionKey) []NeighborhoodEdge {
	var out []NeighborhoodEdge
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.FromRevision != revKey || rel.ToService == "" {
			continue
		}
		out = append(out, dependencyEdge(q.serviceRef(rel.FromService), q.serviceRef(rel.ToService), rel))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].To.Key < out[j].To.Key })
	return out
}

// revisionRefResolutions indexes a revision's declared configuration- and
// policy-reference edges by the DECLARED NAME they belong to, so the revision
// detail can attach each resolution to the scope or policy that authored it.
//
// The resolution itself is not recomputed here: revisionReferenceEdges already
// resolved every reference against the referring revision's own domain at Build
// time, which is what makes two same-named services in different domains
// resolve to different destinations. This only reads that verdict back.
func (q *Query) revisionRefResolutions(revKey RevisionKey) (cfg, pol map[string]*RefResolution) {
	cfg, pol = map[string]*RefResolution{}, map[string]*RefResolution{}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.FromRevision != revKey {
			continue
		}
		var into map[string]*RefResolution
		switch rel.Type {
		case RelationshipConfigRef:
			into = cfg
		case RelationshipPolicyRef:
			into = pol
		default:
			continue
		}
		res := &RefResolution{Resolved: rel.Resolved, Reason: rel.Reason}
		if rel.Resolved {
			// A resolved edge whose service is gone is reported as unresolved rather
			// than as a link to nothing: the destination must be a real canonical
			// entity or the reference is honestly not navigable.
			if s := q.snap.Services[rel.ToService]; s != nil {
				ref := serviceEntityRef(s)
				res.Service = &ref
			} else {
				res.Resolved = false
				if !rel.Hidden { // a hidden one keeps the reason that says so
					res.Reason = "the referenced service is not present in this snapshot"
				}
			}
		}
		into[rel.To] = res
	}
	return cfg, pol
}

// referencingServices names the services whose revisions declare a configuration
// or policy reference to the given service. Reference edges are not dependency
// edges and so are absent from reverseDeps and from graph traversal; without
// this, a shared configuration or policy service could be reached from its
// consumers but never listed its consumers back.
func (q *Query) referencingServices(key ServiceKey) []EntityRef {
	var keys []ServiceKey
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipConfigRef && rel.Type != RelationshipPolicyRef {
			continue
		}
		if !rel.Resolved || rel.ToService != key || rel.FromService == key {
			continue
		}
		keys = appendUnique(keys, rel.FromService)
	}
	sortKeys(keys)
	return q.serviceKeyRefs(keys)
}

// targetsForRevision splits the targets linked to a revision into exact and
// inferred references.
func (q *Query) targetsForRevision(revKey RevisionKey) (exact, inferred []EntityRef) {
	for _, t := range q.snap.Targets {
		if t.ContractRevision != revKey {
			continue
		}
		ref := targetEntityRef(t)
		if t.RevisionMatch == revisionMatchInferred {
			inferred = append(inferred, ref)
		} else {
			exact = append(exact, ref)
		}
	}
	sortEntityRefs(exact)
	sortEntityRefs(inferred)
	return exact, inferred
}

func (q *Query) serviceKeyRefs(keys []ServiceKey) []EntityRef {
	out := make([]EntityRef, 0, len(keys))
	for _, k := range keys {
		out = append(out, q.serviceRef(k))
	}
	return out
}

func revisionRefs(revs []*ContractRevision) []EntityRef {
	out := make([]EntityRef, 0, len(revs))
	for _, r := range revs {
		out = append(out, revisionEntityRef(r))
	}
	return out
}

func targetRefs(targets []*TargetRecord) []EntityRef {
	out := make([]EntityRef, 0, len(targets))
	for _, t := range targets {
		out = append(out, targetEntityRef(t))
	}
	return out
}

// attributedTargetFindingsPreview aggregates every target's findings into a
// BOUNDED preview, attributing each to the target it affects so a service-level
// finding is never orphaned. Total is the truthful full count summed across
// targets, but only the emitted prefix (MaxDetailPreview) is converted, so a
// pathological target set can never force unbounded ProductFinding conversion just
// to build a bounded answer.
func attributedTargetFindingsPreview(targets []*TargetRecord) AttributedFindingsPreview {
	total := 0
	for _, t := range targets {
		total += len(t.Findings)
	}
	items := make([]AttributedFinding, 0, min(total, MaxDetailPreview))
	for _, t := range targets {
		if len(items) >= MaxDetailPreview {
			break
		}
		ref := targetEntityRef(t)
		for _, f := range t.Findings {
			if len(items) >= MaxDetailPreview {
				break
			}
			items = append(items, AttributedFinding{Finding: productFinding(f), Entity: ref})
		}
	}
	return AttributedFindingsPreview{Total: total, Count: len(items), Truncated: total > MaxDetailPreview, Items: items}
}

// attributedTargetLimitations aggregates every target's limitations, attributing
// each to the target it affects.
func attributedTargetLimitations(targets []*TargetRecord) []AttributedLimitation {
	var out []AttributedLimitation
	for _, t := range targets {
		ref := targetEntityRef(t)
		for _, l := range t.Limitations {
			out = append(out, AttributedLimitation{Limitation: l, Entity: ref})
		}
	}
	return out
}

func evidenceForTargets(targets []*TargetRecord) []EvidenceItem {
	var out []EvidenceItem
	for _, t := range targets {
		if t.EvidenceAt != nil {
			out = append(out, EvidenceItem{Target: targetEntityRef(t), At: copyTime(t.EvidenceAt)})
		}
	}
	sortEvidenceDesc(out)
	return out
}

// entitiesFromSource returns every DISTINCT product entity attributable to the
// named source: the revisions and targets it reported, plus the services derived
// from them. Attribution is not exclusive — an entity two sources both reported is
// attributable to both — and it is not the source's raw record count; see
// [SourceContribution]. The source entity itself is excluded: a source does not
// contribute itself.
func (q *Query) entitiesFromSource(key string) []EntityRef {
	var out []EntityRef
	for _, s := range q.snap.Services {
		if containsStr(s.Sources, key) {
			out = append(out, serviceEntityRef(s))
		}
	}
	for _, r := range q.snap.Revisions {
		if containsStr(r.Sources, key) || r.Source == key {
			out = append(out, revisionEntityRef(r))
		}
	}
	for _, t := range q.snap.Targets {
		if containsStr(t.Sources, key) || t.Source == key {
			out = append(out, targetEntityRef(t))
		}
	}
	sortEntityRefs(out)
	return out
}

// serviceOwnership reports the service owner and a BOUNDED preview of per-revision
// ownership conflicts (a revision whose owner differs from the service owner).
func serviceOwnership(s *ServiceRecord, revs []*ContractRevision) *OwnershipInfo {
	key := s.Owner.KeyString()
	info := ownerInfo(s.Owner)
	// A conflict is a disagreement of IDENTITY, and it is spelled out with the
	// namespace: a revision owned by DRI `alice` disagreeing with a service owned by
	// team `alice` would otherwise print as a revision disagreeing with itself.
	var conflicts []string
	for _, r := range revs {
		rk, ok := r.Owner.Key()
		if ok && rk.String() != key {
			conflicts = append(conflicts, string(r.Key)+": "+rk.Label())
		}
	}
	info.Conflicts = stringsPreview(conflicts)
	return info
}

// ownershipUnknown reports whether Pacto cannot tell if the service declares an
// owner: no revision has a readable contract that is not invalid, and no source
// read the contract a target runs. Domain validity is deliberately not part of
// it; an invalid domain slug says nothing about the owner block.
func (q *Query) ownershipUnknown(s *ServiceRecord) bool {
	for _, rk := range s.Revisions {
		if r := q.snap.Revisions[rk]; r.Contract != nil && (!r.validated || r.Valid) {
			return false
		}
	}
	for _, tk := range s.Targets {
		if q.snap.Targets[tk].domainKnown {
			return false
		}
	}
	return true
}

// currentRevisionContent picks the service's current revision (the newest in
// use, else the newest known) and projects its policies and configurations.
// It reads the SNAPSHOT record: active and all are clones without bundle files.
func (q *Query) currentRevisionContent(active, all []*ContractRevision) (*EntityRef, ConfigurationsPreview, PoliciesPreview) {
	cfgs, pols := ConfigurationsPreview{Items: []ConfigurationSummary{}}, PoliciesPreview{Items: []PolicySummary{}}
	pick := active
	if len(pick) == 0 {
		pick = all
	}
	var cur *ContractRevision
	for _, r := range pick {
		if cur == nil || lessRevisionChrono(cur, r) {
			cur = r
		}
	}
	if cur == nil {
		return nil, cfgs, pols
	}
	rec := q.snap.Revisions[cur.Key]
	ref := revisionEntityRef(rec)
	if rec.Contract == nil {
		return &ref, cfgs, pols
	}
	cfgRefs, polRefs := q.revisionRefResolutions(rec.Key)
	cfgs, pols = withSchemaContent(rec, withConfigResolutions(rec.configurations, cfgRefs), policiesPreview(rec.Contract.Policies, polRefs))
	return &ref, cfgs, pols
}

// ownerRevision is the newest revision (see [lessRevisionChrono]) that declares an
// owner, the one [deriveOwner] takes the service owner from; nil when none does.
func (q *Query) ownerRevision(s *ServiceRecord) *EntityRef {
	var newest *ContractRevision
	for _, rk := range s.Revisions {
		if r := q.snap.Revisions[rk]; !r.Owner.IsEmpty() && (newest == nil || lessRevisionChrono(newest, r)) {
			newest = r
		}
	}
	if newest == nil {
		return nil
	}
	ref := revisionEntityRef(newest)
	return &ref
}

// serviceSources is every source that contributed a revision or a target of the
// service, sorted by id, with the source's health when the snapshot holds it.
func (q *Query) serviceSources(view *ServiceView) []EntityRef {
	ids := map[string]bool{}
	add := func(primary string, all []string) {
		for _, id := range append([]string{primary}, all...) {
			if id != "" {
				ids[id] = true
			}
		}
	}
	for _, t := range view.Targets {
		add(t.Source, t.Sources)
	}
	for _, r := range view.Revisions {
		add(r.Source, r.Sources)
	}
	states := map[string]SourceState{}
	for _, st := range q.snap.Sources {
		states[st.ID] = st
	}
	out := make([]EntityRef, 0, len(ids))
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		st, ok := states[id]
		if !ok {
			st = SourceState{ID: id}
		}
		out = append(out, sourceEntityRef(st))
	}
	return out
}

// unresolvedDependencyItems lists the service's declared dependencies (deps is
// [ServiceView.Dependencies]) that no
// visible service resolves (external ones included), one row per ref: External
// is set when any declaring revision marks it external, Version is the first
// one declared. A dependency hidden by permissions is counted in
// HiddenDependencies instead, so the three lists add up to the declared count.
func unresolvedDependencyItems(self EntityRef, deps []Relationship) UnresolvedDependenciesPreview {
	byRef := map[string]int{}
	var out []UnresolvedDependency
	for _, rel := range deps {
		if rel.ToService != "" || rel.Hidden { // deps is ServiceView.Dependencies: declared dependencies only
			continue
		}
		i, ok := byRef[rel.To]
		if !ok {
			byRef[rel.To] = len(out)
			out = append(out, unresolvedDep(self, rel))
			continue
		}
		out[i].External = out[i].External || rel.External
		if out[i].Version == "" {
			out[i].Version = rel.Version
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	it, total, trunc := boundSlice(out, MaxUnresolvedDependencies)
	return UnresolvedDependenciesPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// serviceRelationships builds the relationships preview from the service's
// neighborhood, which is ALREADY bounded (nodes and edges), so a truncated
// neighborhood reports Truncated=true and an UNKNOWN total. When the neighborhood
// cannot be computed the preview is empty with an unknown total and a limitation
// says why, so an empty list never reads as "no relationships".
func serviceRelationships(nb *Neighborhood, err error, self EntityRef) (RelationshipsPreview, []AttributedLimitation) {
	if err != nil {
		return RelationshipsPreview{Items: []NeighborhoodEdge{}}, []AttributedLimitation{{
			Entity: self,
			Limitation: Limitation{
				Code:    LimitationNeighborhoodUnavailable,
				Message: "The relationships of this service could not be computed: " + err.Error(),
			},
		}}
	}
	return relationshipsPreviewFromNeighborhood(nb), nil
}

// serviceLimitations puts the service's own limitations first, so the bound
// never drops them behind its targets' limitations.
func serviceLimitations(own []AttributedLimitation, targets []*TargetRecord) AttributedLimitationsPreview {
	return attributedLimitationsPreview(append(own, attributedTargetLimitations(targets)...))
}

// revisionOwnership reports the owner declared by THIS revision, with the owner ref the
// service page already emits. Without the ref the same owner was a link on the service
// page and dead text on the revision page, so the trail out of a revision to "everything
// this team owns" simply stopped there. Per-revision conflicts are a SERVICE-level fact
// (one revision cannot disagree with itself), so this carries no Conflicts preview.
//
// The revision is the contract inspector, so it is the one entity that carries the
// declared contact points (see [OwnershipInfo.Contacts]) — including for an owner
// block of contacts alone, whose whole content would otherwise be unreachable
// anywhere in the product.
func revisionOwnership(rev *ContractRevision) *OwnershipInfo {
	info := ownerInfo(rev.Owner)
	c := contactsPreview(rev.Owner.ContactSet())
	info.Contacts = &c
	return info
}

// targetOwnership reports the owner of the target's LOGICAL SERVICE. A target has
// no owner of its own, and leaving the block off entirely made an operator who
// landed on a target from the graph unable to answer "who do I page" without
// navigating back up. Conflicts stay a service-level fact.
func targetOwnership(s *ServiceRecord) *OwnershipInfo {
	if s == nil {
		return nil
	}
	return ownerInfo(s.Owner)
}

// ownerInfo builds the owner block with the owner ref, so the trail out to
// "everything this team owns" is available from every entity that has an owner —
// and says that ownership was declared even when there is no canonical owner to
// follow the trail to.
func ownerInfo(o contract.Owner) *OwnershipInfo {
	info := &OwnershipInfo{Owner: o.DisplayString(), Declared: !o.IsEmpty()}
	if key := o.KeyString(); key != "" {
		ref := ownerEntityRef(key)
		info.Ref = &ref
	}
	return info
}

// revisionStatus reports Invalid for a validated-and-invalid revision, else empty.
func revisionStatus(r *ContractRevision) string {
	if r.validated && !r.Valid {
		return StatusInvalid
	}
	return ""
}
