package fleet

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// inferredLocation is the external system whose inferred contract ref names:
// the identity below its pacto-inferred segment, canonical, and that segment
// as written. inferred reports whether ref lies under pacto-inferred at all,
// the one predicate impact and delivery use too: such a ref is never a
// service. key is empty when the identity does not parse (the reserved
// other-egress, or a host identity of the wrong arity), so it names no
// external system either.
func inferredLocation(ref string) (key ExternalKey, id string, inferred bool) {
	id, inferred = contract.InferredRef(ref)
	if !inferred {
		return "", "", false
	}
	if parsed, err := externalid.Parse(id); err == nil {
		key = ExternalKey(parsed)
	}
	return key, id, true
}

// divertInferredProvider keeps rev out of the services when a source read it
// under pacto-inferred: it waits in snap.inferredProviders, under the
// identity of its repository path, for attachInferredProviders. A revision
// whose service.external is missing or names another identity is attached to
// nothing and raises INFERRED_IDENTITY_MISMATCH. It reports whether rev was
// diverted.
func divertInferredProvider(snap *FleetSnapshot, rev *ContractRevision, source string) bool {
	key, path, ok := inferredLocation(rev.ResolvedRef)
	if !ok {
		return false
	}
	declared := rev.Contract.Service.External
	if id, err := externalid.Parse(declared); key == "" || err != nil || ExternalKey(id) != key {
		names := "names " + string(key)
		if key == "" {
			names = "names " + path + ", which is no external identity"
		}
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredIdentityMismatch, Source: source,
			Message: "the contract at " + rev.ResolvedRef + " says it describes external " + quoteOrNone(declared) + " but its repository " + names + "; it is attached to no external system"})
		return true
	}
	if snap.inferredProviders == nil {
		snap.inferredProviders = map[ExternalKey][]*ContractRevision{}
	}
	if i := slices.IndexFunc(snap.inferredProviders[key], func(r *ContractRevision) bool { return r.Key == rev.Key }); i >= 0 {
		// Another source read the same digest: what it checked fills in what
		// the first copy lacks, as mergeRevision does for a service.
		existing := snap.inferredProviders[key][i]
		mergeProvenance(existing, rev)
		if existing.Record == nil {
			existing.Record = rev.Record
		}
		return true
	}
	snap.inferredProviders[key] = append(snap.inferredProviders[key], rev)
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
// of its external system, creating the record when no consumer names it. An
// external no catalog classifies takes the class of the shown revision only
// when this installation's operator signed it (see [inferredClassRef]).
func attachInferredProviders(snap *FleetSnapshot, cat *externalcatalog.Catalog) {
	for key, revs := range snap.inferredProviders {
		rec := externalRecord(snap, cat, key)
		rec.Provider = externalProvider(revs)
		shown := rec.Provider.Revisions[0]
		if s := shown.Surface; rec.Class == externalcatalog.ClassUnclassified && s != nil && validExternalClass(s.Class) && operatorSignature(shown.Signature) {
			rec.Class = s.Class
		}
	}
	snap.inferredProviders = nil
}

// operatorSignature reports whether sig is this installation's operator's.
func operatorSignature(sig *BundleSignature) bool {
	return sig != nil && sig.Source == SignatureSourceOperator
}

// inferredClassRef is the ref of the inferred contract rec's class came from,
// "" when a catalog classified it or its class is not the shown revision's.
func inferredClassRef(rec *ExternalRecord) string {
	if rec.Catalog != nil || rec.Provider == nil || len(rec.Provider.Revisions) == 0 {
		return ""
	}
	shown := rec.Provider.Revisions[0]
	if s := shown.Surface; s == nil || s.Class != rec.Class || rec.Class == externalcatalog.ClassUnclassified || !operatorSignature(shown.Signature) {
		return ""
	}
	return shown.Ref
}

// externalProvider is the provider block of revs: the revisions this
// installation's operator signed first, then the rest, each newest first, so
// a hand-pushed unsigned tag is never the one shown. Bounded at
// MaxExternalSources. Only the shown revision carries its surface: a surface
// is unbounded, and no reader shows an older one.
func externalProvider(revs []*ContractRevision) *ExternalProvider {
	revs = slices.Clone(revs)
	slices.SortFunc(revs, func(a, b *ContractRevision) int {
		if LessRevisionChrono(a, b) {
			return 1
		}
		return -1 // keys are unique, so never equal
	})
	unsigned := slices.DeleteFunc(slices.Clone(revs), operatorSigned)
	revs = append(slices.DeleteFunc(revs, func(r *ContractRevision) bool { return !operatorSigned(r) }), unsigned...)
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(revs[0].ResolvedRef))
	p := &ExternalProvider{Repository: repo, RevisionsTotal: len(revs), RangeStatus: RangeUnknown, RangeReason: RangeReasonInferredProvider}
	kept, _, _ := boundSlice(revs, MaxExternalSources)
	for i, r := range kept {
		pr := ExternalProviderRevision{Version: r.Version, Digest: r.Digest, Ref: r.ResolvedRef}
		if i == 0 && r.Record != nil {
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

// withoutSurface is p as a restricted view sees it: every revision keeps its
// version, digest, ref and signature, and no surface. The surface was
// computed elsewhere, from consumers and sources (fleet source ids, never
// namespaces) this view cannot check against what it hides, and values a
// partial run carried forward name consumers that may have left. So every
// restricted view drops it, and SurfaceHidden says so.
func withoutSurface(p *ExternalProvider) *ExternalProvider {
	if p == nil {
		return nil
	}
	out := *p
	out.Revisions = slices.Clone(p.Revisions)
	for i := range out.Revisions {
		if out.Revisions[i].Surface != nil {
			out.Revisions[i].Surface, out.SurfaceHidden = nil, true
		}
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
// and a declared range is unknown with reason INFERRED_PROVIDER. A ref whose
// identity (path, as written) does not parse joins nothing and stays
// unresolved with its reason.
func joinInferredRef(snap *FleetSnapshot, cat *externalcatalog.Catalog, rk RevisionKey, rev *ContractRevision, idx int, dep contract.Dependency, key ExternalKey, path string, rel *Relationship) {
	if key == "" {
		rel.Reason = "the ref names " + contract.InferredRepository + "/" + path + ", which is no external identity, so it joins no external system and is never a service"
		return
	}
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
		if rel.Provenance != ProvenanceDeclared || rel.RangeReason != RangeReasonInferredProvider || !q.inScope(rel.ToService) {
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
		Reason:     joinNames(names) + verb + " a compatibility range on " + g.name + "'s inferred contract, which lives under " + contract.InferredRepository + "; Pacto never checks a range against it",
		NextStep:   g.nextStep(),
		NextAction: g.nextAction(),
	}
}

// nextAction is the action of nextStep: pacto declare for a service, a
// hand-written contract for an external system (pacto declare reads only
// workloads) or a service whose name no command may carry.
func (g *inferredProviderGroup) nextAction() *NextAction {
	if a := declareAction(g.name, ""); a != nil && g.service != "" {
		return a
	}
	return editContract()
}

// nextStep is the step that fixes g today. A service is declared with pacto
// declare; pacto declare reads only workloads, so an external system's
// declared contract is written by hand until pacto claim exists (517-S5).
func (g *inferredProviderGroup) nextStep() string {
	if g.service != "" {
		return "Declare a contract for " + g.name + " with pacto declare, then point the ranges at it"
	}
	return InferredExternalNextStep(g.name)
}

// InferredExternalNextStep is how to replace external id's inferred contract
// with a declared one today.
func InferredExternalNextStep(id string) string {
	return "Write a contract with service.external: " + id + ", publish it outside " + contract.InferredRepository + ", then point the ranges at it"
}

// SignatureWords says what sig, the signature check of an inferred
// contract's revision, establishes about who published it. Only a check that
// ran says unsigned or names a signer; nil means no source checked.
func SignatureWords(sig *BundleSignature) string {
	switch {
	case sig == nil:
		return "signature not checked, publisher unknown"
	case sig.Source == SignatureSourceOperator:
		return "signed by this installation's operator from what Pacto sees"
	case sig.Status == "unsigned":
		return "unsigned, publisher unknown"
	case sig.Status == "unknown" || sig.Status == "":
		return "signature could not be checked, publisher unknown"
	case sig.Signer != "":
		return "signed by " + sig.Signer + ", not this installation's operator (" + sig.Status + ")"
	}
	return "signed, not by this installation's operator (" + sig.Status + ")"
}

// surfaceLine is how Lines describes s, the shown revision's surface: when
// it was last computed, whether that run saw everything and each list's
// size.
func surfaceLine(s *contract.InferredSurface) string {
	line := "surface as of " + s.AsOf
	if c := s.Completeness; c != nil && c.State == contract.CoveragePartial {
		line += ", partial (" + strings.Join(c.Limitations, ", ") + "): some sources did not answer, so values may be carried over from earlier runs"
	}
	return line + ": hosts " + strconv.Itoa(len(s.Hosts)) + ", ports " + strconv.Itoa(len(s.Ports)) + ", clients " + strconv.Itoa(len(s.Clients))
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
	surface := "surface: none recorded in this revision"
	switch {
	case rev.Surface != nil:
		surface = surfaceLine(rev.Surface)
	case p.SurfaceHidden:
		surface = "surface hidden: your view is restricted"
	}
	return []string{head, SignatureWords(rev.Signature), rangeLine, surface}
}
