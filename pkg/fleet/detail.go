package fleet

import (
	"fmt"
	"sort"
	"time"

	"github.com/trianalab/pacto/v3/pkg/readiness"
	"github.com/trianalab/pacto/v3/pkg/semver"
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
	Owner     string         `json:"owner,omitempty"`
	Ref       *EntityRef     `json:"ref,omitempty"`
	Conflicts StringsPreview `json:"conflicts"`
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
// BOUNDED RuntimePreview (requirement, item 13). Nested maps and slices are
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
	// Summary aggregates the COMPLETE target/revision/edge populations. The
	// previews below are bounded, so a consumer that needs a proportion or a total
	// must read it here and never count preview Items.
	Summary   ServiceSummary `json:"summary"`
	Revisions RefPreview     `json:"revisions"`
	// ActiveRevisions is the subset of Revisions at least one target is linked to
	// (exact or inferred): the revisions actually in use, as opposed to every
	// revision the fleet has ever seen.
	ActiveRevisions RefPreview                   `json:"activeRevisions"`
	Deployments     RefPreview                   `json:"deployments"`
	Dependencies    RefPreview                   `json:"dependencies"`
	Dependents      RefPreview                   `json:"dependents"`
	Relationships   RelationshipsPreview         `json:"relationships"`
	Findings        AttributedFindingsPreview    `json:"findings"`
	Evidence        EvidencePreview              `json:"evidence"`
	Limitations     AttributedLimitationsPreview `json:"limitations"`
}

// RevisionDetailData is the revision-kind payload: parent service, immutable
// identity/content and provenance, validation, readiness, the DECLARED
// interfaces/configurations/policies/capabilities/state/workload themselves
// (bounded, not merely counted), bounded tools/skills/docs, exact/inferred
// targets, and the previous/next known revisions of the same logical service.
type RevisionDetailData struct {
	Service        EntityRef             `json:"service"`
	Version        string                `json:"version,omitempty"`
	PactoVersion   string                `json:"pactoVersion,omitempty"`
	Identity       RevisionIdentity      `json:"identity"`
	Provenance     RevisionProvenance    `json:"provenance"`
	Valid          bool                  `json:"valid"`
	Readiness      *ProductReadiness     `json:"readiness,omitempty"`
	Validation     FindingsPreview       `json:"validation"`
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
}

// OwnerDetailData is the owner-kind payload: bounded previews of the owner's
// services, revisions and deployments, plus a bounded attention preview.
type OwnerDetailData struct {
	Services    RefPreview       `json:"services"`
	Revisions   RefPreview       `json:"revisions"`
	Deployments RefPreview       `json:"deployments"`
	Attention   AttentionPreview `json:"attention"`
}

// SourceDetailData is the source-kind payload: kind, health, sync/observation
// timestamps, record counts, a bounded preview of contributed entities, the
// sanitized source error, and the source's own limitations.
type SourceDetailData struct {
	Kind               string             `json:"kind,omitempty"`
	Health             string             `json:"health" enum:"available,partial,stale,unavailable"`
	LastSuccessfulSync *time.Time         `json:"lastSuccessfulSync,omitempty"`
	ObservedAt         *time.Time         `json:"observedAt,omitempty"`
	RevisionCount      int                `json:"revisionCount"`
	TargetCount        int                `json:"targetCount"`
	Entities           RefPreview         `json:"entities"`
	Error              *SourceError       `json:"error,omitempty"`
	Limitations        LimitationsPreview `json:"limitations"`
}

// EntityDetail is the common, versioned, discriminated envelope for any entity's
// full detail (requirement 2.4). Entity.Kind is the discriminator; EXACTLY ONE
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
	Actions  []string            `json:"actions,omitempty"`
}

// EntityDetail returns the unified detail envelope for one entity, deep-cloned so
// the caller can mutate it freely. Supported kinds: service, revision, target,
// owner, source.
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
	data := &ServiceDetailData{
		Domain:    s.Domain,
		Ownership: serviceOwnership(s, view.Revisions),
		Summary:   summary,
		// Newest first, in revision chronology: a version list ordered by content
		// digest is unreadable as history.
		Revisions:       refPreview(chronologicalRevisionRefs(view.Revisions)),
		ActiveRevisions: refPreview(chronologicalRevisionRefs(active)),
		Deployments:     refPreview(targetRefs(view.Targets)),
		Dependencies:    refPreview(q.dependencyRefs(view.Dependencies)),
		Dependents:      refPreview(q.serviceKeyRefs(view.Dependents)),
		Findings:        attributedTargetFindingsPreview(view.Targets),
		Evidence:        evidencePreview(evidenceForTargets(view.Targets)),
		Limitations:     attributedLimitationsPreview(attributedTargetLimitations(view.Targets)),
	}
	if nb, e := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(s.Key), Direction: DirectionBoth, Views: allViews()}); e == nil {
		// The neighborhood is ALREADY bounded (nodes and edges). Build the relationships
		// preview from it so a truncated neighborhood reports Truncated=true AND an
		// UNKNOWN total (never the pre-truncation scanned edge count as the total).
		data.Relationships = relationshipsPreviewFromNeighborhood(nb)
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: serviceEntityRef(s), Status: s.Status,
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
	data := &RevisionDetailData{
		Service:         q.serviceRef(rev.ServiceKey),
		Version:         rev.Version,
		PactoVersion:    rev.PactoVersion,
		Identity:        revisionIdentity(rev.Digest, rev.RequestedRef, rev.ResolvedRef),
		Provenance:      revisionProvenance(rev),
		Valid:           rev.Valid,
		Readiness:       productReadiness(rev.Readiness),
		Validation:      findingsPreview(rev.Validation),
		Capabilities:    capabilitiesPreview(rev.Contract.Capabilities),
		Interfaces:      interfacesPreview(rev.Contract.Interfaces, rev.Tools, rev.SpecsRead),
		Configurations:  configurationsPreview(rev.Contract.Configurations),
		Policies:        policiesPreview(rev.Contract.Policies),
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
		Service:      q.serviceRef(t.ServiceKey),
		LinkState:    targetLinkState(t),
		Scope:        t.Scope,
		Kind:         t.Kind,
		Compliance:   t.Compliance,
		Coverage:     t.Coverage,
		Findings:     findingsPreview(t.Findings),
		Labels:       labelsPreview(t.Labels),
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
	for _, s := range q.snap.Services {
		if s.Owner.DisplayString() != key {
			continue
		}
		services = append(services, serviceEntityRef(s))
		for _, tk := range s.Targets {
			if t := q.snap.Targets[tk]; t != nil {
				deployments = append(deployments, targetEntityRef(t))
			}
		}
	}
	for _, r := range q.snap.Revisions {
		if r.Owner.DisplayString() == key {
			revisions = append(revisions, revisionEntityRef(r))
		}
	}
	if len(services) == 0 && len(revisions) == 0 {
		return nil, &NotFoundError{Kind: "owner", ID: key}
	}
	sortEntityRefs(services)
	sortEntityRefs(deployments)
	sortEntityRefs(revisions)
	// A constant, valid filter (owner only) never errors; ignore it deliberately.
	// Attention is ALREADY offset-paged; build the preview from the list (not just
	// its Items) so the owner detail reports the TRUE matched total and truncation,
	// never a double-truncated page count.
	ownerAttention, _ := q.Attention(AttentionFilter{Owner: key})
	return &EntityDetail{
		Meta: q.productMeta(), Entity: ownerEntityRef(key),
		Owner: &OwnerDetailData{
			Services:    refPreview(services),
			Revisions:   refPreview(revisions),
			Deployments: refPreview(deployments),
			Attention:   attentionPreviewFromList(ownerAttention),
		},
	}, nil
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
	return &EntityDetail{
		Meta: q.productMeta(), Entity: sourceEntityRef(*st), Status: string(st.Status),
		Source: &SourceDetailData{
			Kind:               st.Kind,
			Health:             string(st.Status),
			LastSuccessfulSync: st.LastSuccessfulSync,
			ObservedAt:         st.ObservedAt,
			RevisionCount:      st.RevisionCount,
			TargetCount:        st.TargetCount,
			Entities:           refPreview(q.entitiesFromSource(key)),
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
func (q *Query) revisionEdges(revKey RevisionKey) []NeighborhoodEdge {
	var out []NeighborhoodEdge
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.FromRevision != revKey || rel.ToService == "" {
			continue
		}
		e := q.newEdge(rel.FromService, rel.ToService)
		q.foldRelationshipIntoEdge(e, rel)
		finalizeEdge(e)
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].To.Key < out[j].To.Key })
	return out
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
// to build a bounded answer (requirement, item 8).
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

// entitiesFromSource returns every entity contributed by the named source.
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
	owner := s.Owner.DisplayString()
	info := &OwnershipInfo{Owner: owner}
	if owner != "" {
		ref := ownerEntityRef(owner)
		info.Ref = &ref
	}
	var conflicts []string
	for _, r := range revs {
		ro := r.Owner.DisplayString()
		if ro != "" && ro != owner {
			conflicts = append(conflicts, string(r.Key)+": "+ro)
		}
	}
	info.Conflicts = stringsPreview(conflicts)
	return info
}

// revisionOwnership reports the owner declared by THIS revision, with the owner ref the
// service page already emits. Without the ref the same owner was a link on the service
// page and dead text on the revision page, so the trail out of a revision to "everything
// this team owns" simply stopped there. Per-revision conflicts are a SERVICE-level fact
// (one revision cannot disagree with itself), so this carries no Conflicts preview.
func revisionOwnership(rev *ContractRevision) *OwnershipInfo {
	return ownerInfo(rev.Owner.DisplayString())
}

// targetOwnership reports the owner of the target's LOGICAL SERVICE. A target has
// no owner of its own, and leaving the block off entirely made an operator who
// landed on a target from the graph unable to answer "who do I page" without
// navigating back up. Conflicts stay a service-level fact.
func targetOwnership(s *ServiceRecord) *OwnershipInfo {
	if s == nil {
		return nil
	}
	return ownerInfo(s.Owner.DisplayString())
}

// ownerInfo builds the owner block with the owner ref, so the trail out to
// "everything this team owns" is available from every entity that has an owner.
func ownerInfo(owner string) *OwnershipInfo {
	info := &OwnershipInfo{Owner: owner}
	if owner != "" {
		ref := ownerEntityRef(owner)
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
