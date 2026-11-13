package fleet

import (
	"cmp"
	"slices"
	"time"
)

// ObservedConsumer is a service, or a caller no contract describes, seen
// calling a provider of an offering at runtime without declaring a use of any
// of its providers. Exactly one of Consumer and Caller is set.
type ObservedConsumer struct {
	Consumer *EntityRef `json:"consumer,omitempty"`
	Caller   string     `json:"caller,omitempty" doc:"A caller no contract describes"`
	// Uncontracted marks a Caller row: it carries no time, so LastSeen and
	// Stale stay unset and the last sighting is unknown.
	Uncontracted bool      `json:"uncontracted,omitempty"`
	Provider     EntityRef `json:"provider"`
	Provenance   string    `json:"provenance" enum:"observed"`
	// LastSeen is the end of the observation window of the call.
	LastSeen *time.Time `json:"lastSeen,omitempty"`
	// Stale is set when the call was last seen more than 24h before the
	// snapshot's as-of time, or the source gave no time.
	Stale   bool     `json:"stale,omitempty"`
	Sources []string `json:"sources,omitempty"`
}

// ObservedConsumersPreview is a bounded preview of observed consumers; Total
// counts every one.
type ObservedConsumersPreview struct {
	Total     int                `json:"total"`
	Count     int                `json:"count"`
	Truncated bool               `json:"truncated"`
	Items     []ObservedConsumer `json:"items"`
}

// observedConsumers lists the visible observed callers of providers that
// declare no use of any of them (declared holds those that do), then the
// providers' uncontracted callers, sorted by provider then consumer or caller.
func (q *Query) observedConsumers(providers []*ServiceRecord, declared map[ServiceKey]bool) ObservedConsumersPreview {
	isProvider := map[ServiceKey]*ServiceRecord{}
	for _, p := range providers {
		isProvider[p.Key] = p
	}
	var out []ObservedConsumer
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		p := isProvider[rel.ToService]
		if p == nil || rel.Provenance != ProvenanceObserved || rel.Hidden || declared[rel.FromService] || rel.FromService == rel.ToService {
			continue
		}
		out = append(out, ObservedConsumer{
			Consumer: new(serviceEntityRef(q.snap.Services[rel.FromService])), Provider: serviceEntityRef(p), Provenance: ProvenanceObserved,
			LastSeen: rel.LastSeen, Stale: rel.LastSeen == nil || q.observedStale(rel.LastSeen), Sources: observedSources(rel),
		})
	}
	for _, c := range q.snap.UncontractedCallers {
		if p := isProvider[c.ToService]; p != nil {
			out = append(out, ObservedConsumer{Caller: c.Caller, Uncontracted: true, Provider: serviceEntityRef(p), Provenance: ProvenanceObserved, Sources: c.Sources})
		}
	}
	slices.SortStableFunc(out, func(a, b ObservedConsumer) int {
		return cmp.Or(cmp.Compare(a.Provider.Key, b.Provider.Key), cmp.Compare(observedConsumerKey(a), observedConsumerKey(b)))
	})
	shown, total, trunc := boundSlice(out, MaxDetailPreview)
	return ObservedConsumersPreview{Total: total, Count: len(shown), Truncated: trunc, Items: shown}
}

// observedConsumerKey sorts service rows before caller rows, each by name.
func observedConsumerKey(c ObservedConsumer) string {
	if c.Consumer != nil {
		return "0" + c.Consumer.Key
	}
	return "1" + c.Caller
}

// observedSources is the ids of the sources that witnessed an observed edge.
func observedSources(rel *Relationship) []string {
	if rel.Source != "" {
		return []string{rel.Source}
	}
	out := make([]string, 0, len(rel.ObservedSources))
	for _, s := range rel.ObservedSources {
		out = append(out, s.Source)
	}
	return out
}

// hiddenObservedConsumers counts the distinct hidden callers of providers,
// related or uncontracted, that are not hidden declared consumers already
// counted in OfferingDetailData.HiddenConsumers.
func (q *Query) hiddenObservedConsumers(providers []*ServiceRecord) int {
	callers, consumers := map[ServiceKey]bool{}, map[ServiceKey]bool{}
	for _, p := range providers {
		for k := range q.snap.hiddenDependents[p.Key] {
			callers[k] = true
		}
		for k := range q.snap.hiddenConsumers[p.Key] {
			consumers[k] = true
		}
	}
	n := 0
	for k := range callers {
		if !consumers[k] {
			n++
		}
	}
	return n
}
