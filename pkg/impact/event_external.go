package impact

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Limitation codes of an external event.
const (
	// LimitationExternalNotInFleet: no visible service declares, calls or
	// uses the external system.
	LimitationExternalNotInFleet = "EXTERNAL_NOT_IN_FLEET"
	// LimitationEventNeedsExternal: a change of something no contract in the
	// fleet describes belongs to an external system.
	LimitationEventNeedsExternal = "EVENT_NEEDS_EXTERNAL"
	// LimitationExternalEgressUnread: no observation source fed the
	// snapshot, so a service that calls the external system without
	// declaring it is not listed.
	LimitationExternalEgressUnread = "EXTERNAL_EGRESS_UNREAD"
)

// GateReasonExternal is the gate reason of an external answer with no
// inferred contract.
const GateReasonExternal = "an external system is never verified: Pacto observes no contract it controls"

// externalPinVersion is the version placeholder of a ref-less pin, the one
// discover's Declare text writes.
const externalPinVersion = "<the API version you call>"

// The consumer sources of an external answer (see [AffectedConsumer.Sources]).
const (
	sourceDeclared = "declared"
	// sourceInferredCall is a call a source saw whose caller is an inferred
	// service: inferred, never observed.
	sourceInferredCall = fleet.ProvenanceInferred
)

// ExternalSubject is the external system an external event is about.
type ExternalSubject struct {
	Identity string `json:"identity" doc:"The external system's canonical identity"`
	Class    string `json:"class,omitempty" doc:"The catalog class: vendor-api, managed-cloud, legacy or unclassified; absent when the fleet holds no record of it"`
	Provider string `json:"provider,omitempty" doc:"The pacto-inferred repository of its inferred contract; absent when none was read"`
	// Change is what changed between the two inferred revisions shown first.
	Change *fleet.ExternalProviderChange `json:"change,omitempty" doc:"What changed between the two revisions of its inferred contract shown first; checked says whether this installation's operator signed both"`
}

// analyzeExternalEvent answers what a change or an outage of the external
// system opts.Event.External does to every service that uses it: one row per
// consumer service of its record, whatever source names it. The record is
// the snapshot's own, never folded, so an external beyond any answer's node
// bound still answers.
func analyzeExternalEvent(snap *fleet.FleetSnapshot, q *fleet.Query, opts Options) *Result {
	ev := *opts.Event
	id := ev.External
	res := newEventResult(snap, q, &ev, id)
	res.External = &ExternalSubject{Identity: id}
	if ev.Kind != EventChanged && ev.Kind != EventUnavailable {
		return res.partial(LimitationEventNeedsService, "an external system is never removed or regressed through Pacto; ask what happens when "+id+" changes or becomes unavailable")
	}
	key := fleet.ExternalKey(id)
	rec := snap.Externals[key]
	if rec == nil {
		return res.partial(LimitationExternalNotInFleet, "no service in the operational graph declares, calls or uses the external system "+id+
			"; consumers cannot be determined. Find its identity with pacto fleet search")
	}
	res.External.Class, res.Hidden = rec.Class, rec.HiddenConsumers
	for _, s := range sortedExternalSources(rec.Sources) {
		row := externalConsumerRow(snap, key, s)
		res.Consumers = mergeEventConsumer(res.Consumers, row)
		if row.Confidence == ConfidenceUnknown { // a stale or caution-only call lowers the merged row too
			i := slices.IndexFunc(res.Consumers, func(e AffectedConsumer) bool { return e.Service == row.Service && e.Domain == row.Domain })
			res.Consumers[i].Confidence = ConfidenceUnknown
		}
	}
	required := externalRequired(rec.Sources)
	for i := range res.Consumers {
		finishExternalConsumer(snap, key, &res.Consumers[i], ev.Kind, required)
	}
	sort.SliceStable(res.Consumers, func(i, j int) bool {
		return res.Consumers[i].Domain+"/"+res.Consumers[i].Service < res.Consumers[j].Domain+"/"+res.Consumers[j].Service
	})
	if p := rec.Provider; p != nil {
		res.External.Provider, res.External.Change = p.Repository, p.Change
		res.inferredExternal, res.inferredRecord = id, rec
		if len(p.Revisions) > 0 {
			res.inferredVersion = p.Revisions[0].Version
		}
		res.Limitations = append(res.Limitations, fleet.Limitation{Code: LimitationInferredProvider, Source: limitationSource,
			Message: id + " has an inferred contract under " + p.Repository + "; Pacto never checks a range against it"})
		res.forceUnknownVerdicts()
	}
	if res.observationSources = observationSources(snap, false); len(res.observationSources) == 0 {
		res.Limitations = append(res.Limitations, fleet.Limitation{Code: LimitationExternalEgressUnread, Source: limitationSource,
			Message: "no observation source fed the snapshot, so a service that calls " + id + " without declaring it is not listed"})
	}
	if q.ExternalsUnreported() {
		res.partial(fleet.LimitationExternalsUnreported, "some targets do not report the external systems they use, so the consumers listed may be incomplete")
	}
	if note := claimNote(id, rec.Claim); note != "" {
		res.Notes = append(res.Notes, note)
	}
	res.Pin = pinFor(snap, key, rec, res.Consumers)
	res.collectOwnersAndTargets(snap)
	res.noteStaleConsumers()
	return res
}

// sortedExternalSources are sources with the declared ones first, required
// before optional, so a consumer's merged row keeps its declared facts.
func sortedExternalSources(sources []fleet.ExternalSource) []fleet.ExternalSource {
	out := slices.Clone(sources)
	rank := func(s fleet.ExternalSource) int {
		switch {
		case s.Kind == fleet.ExternalSourceContract && s.Required:
			return 0
		case s.Kind == fleet.ExternalSourceContract:
			return 1
		}
		return 2
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// externalRequired are the consumers with a declared required use.
func externalRequired(sources []fleet.ExternalSource) map[fleet.ServiceKey]bool {
	out := map[fleet.ServiceKey]bool{}
	for _, s := range sources {
		if s.Kind == fleet.ExternalSourceContract && s.Required {
			out[s.Service] = true
		}
	}
	return out
}

// externalConsumerRow is the row one source of an external's record gives
// its consumer: where it was seen and the client the source's revision
// names. Effect, provenance and confidence are set once the rows of the
// consumer are merged (see [finishExternalConsumer]).
func externalConsumerRow(snap *fleet.FleetSnapshot, key fleet.ExternalKey, s fleet.ExternalSource) AffectedConsumer {
	subject := fleet.ServiceKey(fleet.ExternalSubjectPrefix + string(key))
	name := externalSourceName(s)
	var observed []resolvedObservedEdge
	if name == fleet.ExternalSourceObserved {
		observed = externalObservedEdges(snap, key, s.Service, subject)
	}
	c, stale := consumerBase(snap, s.Service, subject, observed)
	c.Depth, c.Direct, c.CompatibilityVerdict = 1, true, CompatibilityUnknown
	c.Sources, c.Via = []string{name}, []string{externalVia(name)}
	c.Client = fleet.ExternalClientOf(snap, s.Revision, key)
	caution := len(observed) > 0 && !slices.ContainsFunc(observed, func(e resolvedObservedEdge) bool { return !e.cautionOnly })
	if stale || caution {
		c.Confidence = ConfidenceUnknown
	}
	return c
}

// externalObservedEdges are the observed calls from service to the external
// key, as the edges the consumer's evidence reads: their last time, count,
// staleness and whether only caution-only sources saw them.
func externalObservedEdges(snap *fleet.FleetSnapshot, key fleet.ExternalKey, service, subject fleet.ServiceKey) []resolvedObservedEdge {
	var out []resolvedObservedEdge
	for _, r := range snap.Relationships {
		if r.Provenance == fleet.ProvenanceObserved && r.ToExternal == key && r.FromService == service {
			out = append(out, resolvedObservedEdge{consumer: service, provider: subject, source: observedSource(r),
				count: r.ObservedCount, lastSeen: copyTime(r.LastSeen), stale: r.LastSeen != nil && staleAt(snap, r.LastSeen),
				cautionOnly: snap.CautionOnly(r)})
		}
	}
	return out
}

// externalSourceName is the consumer source of a source: a contract's
// dependency is declared, a call seen from an inferred service is inferred,
// every other kind keeps its name.
func externalSourceName(s fleet.ExternalSource) string {
	switch {
	case s.Kind == fleet.ExternalSourceContract:
		return sourceDeclared
	case s.Kind == fleet.ExternalSourceObserved && s.Provenance == fleet.ProvenanceInferred:
		return sourceInferredCall
	}
	return s.Kind
}

// externalVia is the edge type a consumer source makes its service a
// consumer by.
func externalVia(name string) string {
	switch name {
	case sourceDeclared:
		return fleet.RelationshipDependency
	case fleet.ExternalSourceObserved:
		return fleet.ProvenanceObserved
	}
	return fleet.ProvenanceInferred
}

// finishExternalConsumer sets the facts of a merged row: required, the range
// it declares, provenance and confidence from its sources, the client of its
// newest revision when no source's revision names one, and its effect.
func finishExternalConsumer(snap *fleet.FleetSnapshot, key fleet.ExternalKey, c *AffectedConsumer, kind string, required map[fleet.ServiceKey]bool) {
	svc := fleet.NewServiceKeyDomain(c.Domain, c.Service)
	declared, observed := slices.Contains(c.Sources, sourceDeclared), slices.Contains(c.Sources, fleet.ExternalSourceObserved)
	c.Required, c.Compatibility = required[svc], externalRange(snap, key, svc)
	c.Provenance = provenance(declared, observed)
	if c.Confidence != ConfidenceUnknown {
		c.Confidence = confidence(1, declared, contract.IsRange(c.Compatibility), observed)
		if !declared && !observed {
			c.Confidence = ConfidenceInferred
		}
	}
	if c.Client == nil {
		if rev := newestRevision(snap, svc); rev != nil {
			c.Client = fleet.ExternalClientOf(snap, rev.Key, key)
		}
	}
	c.Effect = EffectAttention
	if kind == EventUnavailable {
		c.Effect = effectForUse(c.Required, declared)
	}
	c.Note = externalNote(c.Effect)
}

// externalRange is the compatibility range service declares on the external
// key, through a dependency joined to its inferred contract; "" for none.
func externalRange(snap *fleet.FleetSnapshot, key fleet.ExternalKey, service fleet.ServiceKey) string {
	for _, rel := range snap.Relationships {
		if rel.ToExternal == key && rel.FromService == service && rel.Compatibility != "" {
			return rel.Compatibility
		}
	}
	return ""
}

// effectForUse is what an outage of an external system does to a use of it:
// a declared required use breaks, a declared optional one may degrade, and
// an observed or inferred one is unknown, because nothing says what it needs.
func effectForUse(required, declared bool) string {
	switch {
	case declared && required:
		return EffectBreaks
	case declared:
		return EffectMayDegrade
	}
	return EffectUnknown
}

// externalNote says why an external answer's consumer has its effect.
func externalNote(effect string) string {
	switch effect {
	case EffectAttention:
		return "it uses the external system that changed; review how it calls it"
	case EffectBreaks:
		return "it declares the external system as required"
	case EffectMayDegrade:
		return "it declares the external system as optional"
	}
	return "no contract says what it needs from the external system"
}

// pinFor is the dependencies block that pins a range on the external key,
// set when a listed consumer declares none. With an inferred contract the
// range points at its repository and is the one the organization pins most
// (unknown when nobody pins one); without one no range can be pinned, so the
// block declares the external with the version placeholder.
func pinFor(snap *fleet.FleetSnapshot, key fleet.ExternalKey, rec *fleet.ExternalRecord, consumers []AffectedConsumer) string {
	if !slices.ContainsFunc(consumers, func(c AffectedConsumer) bool { return !contract.IsRange(c.Compatibility) }) {
		return ""
	}
	id := string(key)
	d := contract.Dependency{Name: contract.ExternalDependencyName(id), Required: true}
	if rec.Provider != nil {
		d.Ref, d.Compatibility = "oci://"+strings.TrimPrefix(rec.Provider.Repository, "oci://"), commonRange(snap, key)
		return contract.ExternalDependencyYAML(d)
	}
	d.External, d.Version = true, externalPinVersion
	if d.Name != id {
		d.Identity = id
	}
	return contract.ExternalDependencyYAML(d)
}

// commonRange is the range the declared dependencies on key pin most, ties
// by string order; unknown when none pins one.
func commonRange(snap *fleet.FleetSnapshot, key fleet.ExternalKey) string {
	counts := map[string]int{}
	for _, rel := range snap.Relationships {
		if rel.ToExternal == key && contract.IsRange(rel.Compatibility) {
			counts[rel.Compatibility]++
		}
	}
	best := contract.CompatibilityUnknown
	for r, n := range counts {
		if b := counts[best]; n > b || (n == b && r < best) {
			best = r
		}
	}
	return best
}

// externalGateReason is the gate reason of an external answer: always
// unverified, naming the inferred contract shown when one was read.
func (r *Result) externalGateReason() string {
	seen := ". No observation source was consulted, so an undeclared caller is not listed"
	if len(r.observationSources) > 0 {
		seen = ". Observation sources consulted: " + strings.Join(r.observationSources, ", ")
	}
	if r.inferredExternal == "" {
		return GateReasonExternal + seen
	}
	n, k := len(r.Consumers), 0
	for _, c := range r.Consumers {
		if contract.IsRange(c.Compatibility) {
			k++
		}
	}
	return r.inferredExternal + "'s inferred contract " + strings.TrimSpace(r.inferredVersion+" is") +
		" no contract this organization controls, so no range against it can be verified: " +
		strconv.Itoa(k) + " of " + strconv.Itoa(n) + " " + plural(n, "consumer pins", "consumers pin") + " a range on it. " +
		r.inferredRecord.InferredNextStep(r.inferredExternal) + seen
}

// claimNote says who claims external id, so the declared contract wins in
// impact too: the claimed contract and how to ask what a change to it does,
// or how to claim it. A claimer the viewer may not read says nothing, so a
// hidden claim never invites a second one. Consumer effects stay the
// vendor's: the event is about the external system, never the declared
// contract.
func claimNote(id string, c *fleet.ExternalClaim) string {
	switch {
	case c == nil:
		return "external " + id + " has no claim: name its owner with pacto claim " + id + " --owner <team>"
	case c.Hidden:
		return ""
	}
	_, name := fleet.ParseServiceKey(c.Service)
	repo, _, _ := strings.Cut(c.Ref, "@")
	return "external " + id + " is claimed by " + name + " " + c.Version +
		"; the impact of a change to its contract is pacto impact oci://" + repo + ":" + c.Version + " <new>"
}
