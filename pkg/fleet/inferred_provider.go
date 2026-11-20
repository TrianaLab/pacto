package fleet

import (
	"maps"
	"slices"
	"strconv"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// inferredLocation is the external system whose inferred contract ref names:
// the identity below its pacto-inferred segment, canonical. Only the location
// marks a contract inferred: content never does.
func inferredLocation(ref string) (ExternalKey, bool) {
	id, ok := contract.InferredRef(ref)
	if !ok {
		return "", false
	}
	parsed, err := externalid.Parse(id)
	if err != nil {
		return "", false
	}
	return ExternalKey(parsed), true
}

// divertInferredProvider keeps rev out of the services when a source read it
// under pacto-inferred: it waits in snap.inferredProviders, under the
// identity of its repository path, for attachInferredProviders. A revision
// whose service.external is missing or names another identity is attached to
// nothing and raises INFERRED_IDENTITY_MISMATCH. It reports whether rev was
// diverted.
func divertInferredProvider(snap *FleetSnapshot, rev *ContractRevision, source string) bool {
	key, ok := inferredLocation(rev.ResolvedRef)
	if !ok {
		return false
	}
	declared := rev.Contract.Service.External
	if id, err := externalid.Parse(declared); err != nil || ExternalKey(id) != key {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredIdentityMismatch, Source: source,
			Message: "the contract at " + rev.ResolvedRef + " says it describes external " + quoteOrNone(declared) + " but its repository names " + string(key) + "; it is attached to no external system"})
		return true
	}
	if snap.inferredProviders == nil {
		snap.inferredProviders = map[ExternalKey][]*ContractRevision{}
	}
	if !slices.ContainsFunc(snap.inferredProviders[key], func(r *ContractRevision) bool { return r.Key == rev.Key }) {
		snap.inferredProviders[key] = append(snap.inferredProviders[key], rev)
	}
	return true
}

// quoteOrNone is s quoted, or "none" when it is empty.
func quoteOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return `"` + s + `"`
}

// attachInferredProviders makes each diverted inferred contract the Provider
// of its external system, creating the record when no consumer names it.
func attachInferredProviders(snap *FleetSnapshot, cat *externalcatalog.Catalog) {
	for key, revs := range snap.inferredProviders {
		rec := externalRecord(snap, cat, key)
		rec.Provider = externalProvider(revs)
		if s := rec.Provider.Revisions[0].Surface; rec.Class == externalcatalog.ClassUnclassified && s != nil && validExternalClass(s.Class) {
			rec.Class = s.Class
		}
	}
	snap.inferredProviders = nil
}

// externalProvider is the provider block of revs: the revisions this
// installation's operator signed first, then the rest, each newest first, so
// a hand-pushed unsigned tag is never the one shown. Bounded at
// MaxExternalSources.
func externalProvider(revs []*ContractRevision) *ExternalProvider {
	revs = slices.Clone(revs)
	slices.SortFunc(revs, func(a, b *ContractRevision) int {
		if lessRevisionChrono(a, b) {
			return 1
		}
		return -1 // keys are unique, so never equal
	})
	unsigned := slices.DeleteFunc(slices.Clone(revs), operatorSigned)
	revs = append(slices.DeleteFunc(revs, func(r *ContractRevision) bool { return !operatorSigned(r) }), unsigned...)
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(revs[0].ResolvedRef))
	p := &ExternalProvider{Repository: repo, RevisionsTotal: len(revs), RangeStatus: RangeUnknown, RangeReason: RangeReasonInferredProvider}
	kept, _, _ := boundSlice(revs, MaxExternalSources)
	for _, r := range kept {
		pr := ExternalProviderRevision{Version: r.Version, Digest: r.Digest, Ref: r.ResolvedRef}
		if r.Record != nil {
			pr.Surface = r.Record.Surface
		}
		if r.Provenance != nil {
			sig := r.Provenance.Signature
			pr.Signature = &sig
		}
		p.Revisions = append(p.Revisions, pr)
	}
	return p
}

// operatorSigned reports whether only this installation's operator signed
// rev (see [SignatureSourceOperator]): a range is never checked against it.
func operatorSigned(rev *ContractRevision) bool {
	return rev.Provenance != nil && rev.Provenance.Signature.Source == SignatureSourceOperator
}

// restrictedProvider is rec's provider as r, its restricted copy, shows it.
// The surface aggregates every consumer's sources, so a view that lost any of
// them keeps the block without it.
func restrictedProvider(r, rec *ExternalRecord) *ExternalProvider {
	if len(r.hidden) > 0 || len(r.Sources) < len(rec.Sources) {
		return withoutSurface(rec.Provider)
	}
	return rec.Provider
}

// withoutSurface is p as a restricted view sees it when a source or consumer
// its surface was computed from is hidden: every revision keeps its version,
// digest, ref and signature, and no surface.
func withoutSurface(p *ExternalProvider) *ExternalProvider {
	if p == nil {
		return nil
	}
	out := *p
	out.Revisions = slices.Clone(p.Revisions)
	for i := range out.Revisions {
		out.Revisions[i].Surface = nil
	}
	return &out
}

// inferredRefReason is the reason of a declared dependency joined to an
// external system through its inferred contract.
func inferredRefReason(key ExternalKey) string {
	return "joined external " + string(key) + " through its inferred contract; this Pacto system does not deliver it"
}

// matchInferredContract is the ExternalSource match of a dependency that pins
// an external system's inferred contract.
const matchInferredContract = "its inferred contract"

// joinInferredRef joins dependency idx of rev, whose ref names key's inferred
// contract, to that external system: the consumer is counted on its record,
// and a declared range is unknown with reason INFERRED_PROVIDER.
func joinInferredRef(snap *FleetSnapshot, cat *externalcatalog.Catalog, rk RevisionKey, rev *ContractRevision, idx int, dep contract.Dependency, key ExternalKey, rel *Relationship) {
	recordExternalDeclaration(snap, cat, rk, rev, idx, dep, key, matchInferredContract)
	rel.ToExternal, rel.Reason = key, inferredRefReason(key)
	if dep.Compatibility != "" {
		rel.RangeStatus, rel.RangeReason = RangeUnknown, RangeReasonInferredProvider
	}
}

// inferredProviderGroup is the declared ranges checked against one inferred
// provider: the attention item it raises.
type inferredProviderGroup struct {
	entity    EntityRef
	service   string
	name      string
	consumers []ServiceKey
}

// inferredProviderAttention reports one knowledge item per inferred provider a
// visible declared range is checked against: an external system's inferred
// contract, or a service whose revisions only this installation's operator
// signed. The fix is a declared contract, so its action is declare.
func (q *Query) inferredProviderAttention() []AttentionItem {
	groups := map[string]*inferredProviderGroup{}
	for _, rel := range q.snap.Relationships {
		if rel.Provenance != ProvenanceDeclared || rel.RangeReason != RangeReasonInferredProvider {
			continue
		}
		k := ExternalSubjectPrefix + string(rel.ToExternal)
		if rel.ToExternal == "" {
			k = string(rel.ToService)
		}
		g := groups[k]
		if g == nil {
			g = q.inferredProviderGroup(rel)
			groups[k] = g
		}
		if !slices.Contains(g.consumers, rel.FromService) {
			g.consumers = append(g.consumers, rel.FromService)
		}
	}
	var out []AttentionItem
	for _, k := range slices.Sorted(maps.Keys(groups)) {
		out = append(out, groups[k].item())
	}
	return out
}

// inferredProviderGroup is the empty group of rel's provider.
func (q *Query) inferredProviderGroup(rel Relationship) *inferredProviderGroup {
	if rel.ToExternal != "" {
		return &inferredProviderGroup{entity: externalEntityRef(q.externalRecord(rel.ToExternal)), name: string(rel.ToExternal)}
	}
	svc := q.snap.Services[rel.ToService]
	return &inferredProviderGroup{entity: serviceEntityRef(svc), service: string(svc.Key), name: svc.Name}
}

// item is the attention item of g.
func (g *inferredProviderGroup) item() AttentionItem {
	slices.Sort(g.consumers)
	names := make([]string, len(g.consumers))
	for i, c := range g.consumers {
		_, names[i] = ParseServiceKey(c)
	}
	label, verb := strconv.Itoa(len(names))+" services check a range against "+g.name+"'s inferred contract", " pin"
	if len(names) == 1 {
		label, verb = "1 service checks a range against "+g.name+"'s inferred contract", " pins"
	}
	return AttentionItem{
		Entity: g.entity, Service: g.service, Label: label,
		Severity: severityInfo, Code: RangeReasonInferredProvider, Category: categoryKnowledge,
		Summary:    "Range checked against an inferred contract",
		Reason:     joinNames(names) + verb + " a compatibility range on " + g.name + "'s inferred contract, which Pacto published from what it sees; Pacto never checks a range against it",
		NextStep:   "Declare a contract for " + g.name + " with pacto declare, then point the ranges at it",
		NextAction: declare(),
	}
}

// Lines is how the CLI and the TUI describe p: the shown revision, who signed
// it (worded from its actual signature), that a range is never checked
// against it and how much of the surface it holds.
func (p *ExternalProvider) Lines() []string {
	rangeLine := "range checks: unverified (" + p.RangeReason + ")"
	if len(p.Revisions) == 0 {
		return []string{p.Repository, rangeLine}
	}
	rev := p.Revisions[0]
	head := p.Repository + ":" + rev.Version + " (" + rev.Digest + ")"
	if p.RevisionsTotal > 1 {
		head += ", " + strconv.Itoa(p.RevisionsTotal) + " revisions read"
	}
	signer := "unsigned, publisher unknown"
	if rev.Signature != nil && rev.Signature.Source == SignatureSourceOperator {
		signer = "signed by this installation's operator from what Pacto sees"
	}
	surface := "surface not shown: the record holds none, or some of its sources are outside your view"
	if s := rev.Surface; s != nil {
		surface = "surface: hosts " + strconv.Itoa(len(s.Hosts)) + ", ports " + strconv.Itoa(len(s.Ports)) + ", clients " + strconv.Itoa(len(s.Clients))
	}
	return []string{head, signer, rangeLine, surface}
}
