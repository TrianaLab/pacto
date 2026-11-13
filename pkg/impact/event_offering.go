package impact

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Limitation codes of an offering event.
const (
	// LimitationHiddenProviders: providers of the offering the viewer may not
	// see; their consumers are not listed.
	LimitationHiddenProviders = "HIDDEN_PROVIDERS"
	// LimitationOfferingNotInFleet: no visible service provides the offering.
	LimitationOfferingNotInFleet = "OFFERING_NOT_IN_FLEET"
	// LimitationEventNeedsService: a regression belongs to one revision of
	// one service, so it has no offering subject.
	LimitationEventNeedsService = "EVENT_NEEDS_SERVICE"
)

// analyzeOfferingEvent answers what happens to the consumers of every visible
// provider of opts.Event.Offering: one row per consumer, the worst effect over
// the providers speaking. A provider is never its own consumer.
func analyzeOfferingEvent(snap *fleet.FleetSnapshot, q *fleet.Query, opts Options) *Result {
	ev := *opts.Event
	res := newEventResult(snap, q, &ev, ev.Offering)
	if ev.Kind == EventRegressed {
		return res.partial(LimitationEventNeedsService, "a regression belongs to one revision of one service; name the service instead of the offering")
	}
	providers := offeringProviders(snap, ev.Offering)
	if len(providers) == 0 {
		return res.partial(LimitationOfferingNotInFleet, "no service in the operational graph provides the offering "+ev.Offering+"; consumers cannot be determined")
	}
	for _, p := range providers {
		res.Providers = append(res.Providers, string(p))
	}
	if n := q.HiddenProviders(ev.Offering); n > 0 {
		res.partial(LimitationHiddenProviders, strconv.Itoa(n)+" "+plural(n, "provider", "providers")+" of "+ev.Offering+
			" you may not see "+plural(n, "is", "are")+" not analyzed, so "+plural(n, "its", "their")+" consumers are not listed")
	}
	observed := res.eventObserved(snap, opts)
	rows := []AffectedConsumer{}
	for _, p := range providers {
		part := &Result{Event: res.Event, Limitations: []fleet.Limitation{}}
		graph := part.dependents(snap, q, p, ev.Kind == EventRemoved)
		part.eventFor(snap, q, p, graph, observed)
		res.Limitations = append(res.Limitations, part.Limitations...)
		if part.Completeness == fleet.CompletenessPartial {
			res.Completeness = fleet.CompletenessPartial
		}
		for _, c := range part.Consumers {
			if !slices.Contains(providers, fleet.NewServiceKeyDomain(c.Domain, c.Service)) {
				rows = mergeEventConsumer(rows, c)
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Domain+"/"+rows[i].Service < rows[j].Domain+"/"+rows[j].Service })
	res.Consumers = rows
	res.collectOwnersAndTargets(snap, providers...)
	res.ChangedOwnerKey = ""
	res.noteUnlistedOffering(snap, q, providers)
	res.noteStaleConsumers()
	res.Limitations = dedupeLimitations(res.Limitations)
	return res
}

// partial makes the answer partial with one limitation.
func (r *Result) partial(code, message string) *Result {
	r.Completeness = fleet.CompletenessPartial
	r.Limitations = append(r.Limitations, fleet.Limitation{Code: code, Source: limitationSource, Message: message})
	return r
}

// offeringProviders are the keys of the services in snap that provide
// offering, sorted.
func offeringProviders(snap *fleet.FleetSnapshot, offering string) []fleet.ServiceKey {
	var out []fleet.ServiceKey
	for k, s := range snap.Services {
		if s.Offering == offering {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// mergeEventConsumer adds c to rows, or folds it into the row for the same
// service: the worst effect wins and carries its own path, depth, provenance,
// confidence and note; edge types, resources and evidence are unioned. Policy
// verdicts are left as they are (see [Result.mergeConsumer] for referrers).
func mergeEventConsumer(rows []AffectedConsumer, c AffectedConsumer) []AffectedConsumer {
	i := slices.IndexFunc(rows, func(e AffectedConsumer) bool { return e.Service == c.Service && e.Domain == c.Domain })
	if i < 0 {
		return append(rows, c)
	}
	e := &rows[i]
	if effectRank(c.Effect) > effectRank(e.Effect) {
		e.Effect, e.Path, e.Depth, e.Direct, e.Required = c.Effect, c.Path, c.Depth, c.Direct, c.Required
		e.Provenance, e.Confidence, e.Note, e.Compatibility = c.Provenance, c.Confidence, c.Note, c.Compatibility
	}
	for _, v := range c.Via {
		if !slices.Contains(e.Via, v) {
			e.Via = append(e.Via, v)
		}
	}
	mergeResources(e, c)
	for _, ev := range c.Evidence {
		if !slices.ContainsFunc(e.Evidence, func(x Evidence) bool {
			return x.Kind == ev.Kind && x.Source == ev.Source && x.Message == ev.Message
		}) {
			e.Evidence = append(e.Evidence, ev)
		}
	}
	e.Evidence = boundEvidence(e.Evidence)
	return rows
}

// mergeResources adds the resources of c to e, a status both name once, as
// [resourcesOf] does for one provider.
func mergeResources(e *AffectedConsumer, c AffectedConsumer) {
	e.ResourcesTotal += c.ResourcesTotal
	for _, p := range c.Resources {
		if slices.ContainsFunc(e.Resources, func(x fleet.ProvisioningStatus) bool {
			return x.Environment == p.Environment && x.Entry == p.Entry && x.Phase == p.Phase && x.Source == p.Source
		}) {
			e.ResourcesTotal--
			continue
		}
		e.Resources = append(e.Resources, p)
	}
	e.Resources = e.Resources[:min(len(e.Resources), MaxConsumerResources)]
}

// noteUnlistedOffering is [Result.noteUnlisted] over every provider: hidden
// dependents counted once however many providers they call.
func (r *Result) noteUnlistedOffering(snap *fleet.FleetSnapshot, q *fleet.Query, providers []fleet.ServiceKey) {
	r.Hidden = q.HiddenDependentsOf(providers)
	if r.Event.Kind != EventRemoved {
		return
	}
	seen := map[string]bool{}
	for _, p := range providers {
		r.Hidden += snap.Services[p].HiddenPolicyReferrers
		for _, k := range q.TemplatesReferencing(p) {
			if !seen[string(k)] {
				seen[string(k)] = true
				r.Templates = append(r.Templates, string(k))
			}
		}
	}
}

// dedupeLimitations keeps the first of limitations with the same code and
// message: snapshot-wide facts read once per provider are said once.
func dedupeLimitations(ls []fleet.Limitation) []fleet.Limitation {
	seen := map[[2]string]bool{}
	out := ls[:0]
	for _, l := range ls {
		if k := [2]string{l.Code, l.Message}; !seen[k] {
			seen[k] = true
			out = append(out, l)
		}
	}
	return out
}

// providersNote names the providers of an offering answer in its gate reason.
func (r *Result) providersNote() string {
	if len(r.Providers) == 0 {
		return ""
	}
	names := make([]string, len(r.Providers))
	for i, k := range r.Providers {
		_, names[i] = fleet.ParseServiceKey(fleet.ServiceKey(k))
	}
	return " (" + plural(len(names), "provider ", "providers ") + strings.Join(names, ", ") + ")"
}
