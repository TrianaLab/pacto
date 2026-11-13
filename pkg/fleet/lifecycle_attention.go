package fleet

import (
	"cmp"
	"fmt"
	"slices"
)

// lifecycleAttention reports each service that consumes a deprecated or
// past-sunset provider, through a declared dependency or configuration
// reference (validation's DependencyServiceWarnings scope; a policy only
// constrains its contract). It is the one producer the attention list, Status,
// the Overview tile and servicesNeedingAttention read. Per consumer, provider
// and kind of use the newest consumer revision speaks, as in
// [Query.offeringUsesIn]; one item remains per consumer and provider, the worst.
// A provider the viewer may not see carries no lifecycle (hideProvider), so no
// item names it.
func (q *Query) lifecycleAttention() []AttentionItem {
	type useKey struct {
		from, to ServiceKey
		via      string
	}
	newest := map[useKey]Relationship{}
	for _, rel := range q.snap.Relationships {
		if !offeringRelationship(&rel, "") || (rel.Type != RelationshipDependency && rel.Type != RelationshipConfigRef) {
			continue
		}
		k := useKey{rel.FromService, rel.ToService, rel.Type}
		if kept, ok := newest[k]; !ok || lessRevisionChrono(q.snap.Revisions[kept.FromRevision], q.snap.Revisions[rel.FromRevision]) {
			newest[k] = rel
		}
	}
	type pairKey struct{ from, to ServiceKey }
	worst := map[pairKey]AttentionItem{}
	for k, rel := range newest {
		if rel.ProviderLifecycle == nil {
			continue
		}
		it := q.lifecycleItem(rel)
		p := pairKey{k.from, k.to}
		// Equal severities keep the dependency, so the answer never depends on map order.
		if prev, ok := worst[p]; !ok || severityOrder[it.Severity] < severityOrder[prev.Severity] ||
			(it.Severity == prev.Severity && k.via == RelationshipDependency) {
			worst[p] = it
		}
	}
	out := make([]AttentionItem, 0, len(worst))
	for _, it := range worst {
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, b AttentionItem) int {
		return cmp.Or(cmp.Compare(a.Label, b.Label), cmp.Compare(a.Service, b.Service))
	})
	return out
}

// lifecycleItem is the attention item for one consumer's use of a provider
// whose reached revision declares a lifecycle, worded as validation's
// DependencyServiceWarnings words it.
func (q *Query) lifecycleItem(rel Relationship) AttentionItem {
	consumer, provider := q.snap.Services[rel.FromService], q.snap.Services[rel.ToService]
	rev, l := providerLifecycleRevision(q.snap, rel), rel.ProviderLifecycle
	it := AttentionItem{
		Entity: serviceEntityRef(consumer), Service: string(rel.FromService), Label: consumer.Name + " uses " + provider.Name,
		Category: categoryLifecycle, Severity: severityWarning, Code: "DEPENDENCY_SERVICE_DEPRECATED",
		Summary: "Uses a deprecated provider",
		Reason:  fmt.Sprintf("%s@%s is deprecated since %s", provider.Name, rev.Version, l.Deprecated),
	}
	switch {
	case l.SunsetReached(q.snap.GeneratedAt):
		it.Severity, it.Code, it.Summary = severityError, "DEPENDENCY_SERVICE_SUNSET", "Uses a provider past its sunset"
		it.Reason += fmt.Sprintf(" and reached its sunset %s: it may be retired in any release", l.Sunset)
	case l.Sunset != "":
		it.Reason += ", sunset " + l.Sunset
	}
	target := "a supported version of " + provider.Name
	if l.Successor != "" {
		it.Reason += "; successor " + l.Successor
		target = l.Successor
	}
	it.NextStep = "Move " + consumer.Name + " to " + target
	return it
}

// lifecycleTally counts the distinct real services with a lifecycle item and
// grades them like the worst item, so the Overview tile and the list it opens
// agree.
func (q *Query) lifecycleTally() (int, string) {
	consumers, sev := map[string]bool{}, severityWarning
	for _, it := range q.lifecycleAttention() {
		if !q.populationService(ServiceKey(it.Service)) {
			continue
		}
		consumers[it.Service] = true
		if it.Severity == severityError {
			sev = severityError
		}
	}
	return len(consumers), sev
}
