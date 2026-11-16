package fleet

import (
	"cmp"
	"slices"
	"time"
)

// Provisioning attention codes.
const (
	codeProvisioningFailed = "PROVISIONING_FAILED"
	codeProvisioningStale  = "PROVISIONING_STALE"
)

// provisioningUseKey is one use a provider may report on: a consumer, the
// provider (its hidden key when the viewer may not see it) and the kind of use.
type provisioningUseKey struct {
	from, to ServiceKey
	via      string
	hidden   bool
}

// provisioningAttention reports each resource whose effective phase is Failed
// and each whose last report is stale and about the use's current consumer
// revision. A never-reported resource raises nothing: Unknown is every use's
// default. It reads the (possibly restricted) view: a hidden consumer's
// reports are gone, and a visible consumer's report about a provider the
// viewer may not see still raises its item, with the provider, adapter and
// external reference left out. Resource phase never feeds compliance.
func (q *Query) provisioningAttention() []AttentionItem {
	rels := map[provisioningUseKey][]*Relationship{}
	current := map[provisioningUseKey]*ContractRevision{}
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		k, ok := provisioningAttentionKey(rel)
		if !ok {
			continue
		}
		rels[k] = append(rels[k], rel)
		if r := q.snap.Revisions[rel.FromRevision]; r != nil && (current[k] == nil || lessRevisionChrono(current[k], r)) {
			current[k] = r
		}
	}
	idx := q.provisioningIndex()
	var out []AttentionItem
	for k, rs := range rels {
		statuses, _ := q.provisioningFor(OfferingUse{Consumer: q.serviceRef(k.from)}, rs, current[k], idx)
		for _, st := range statuses {
			if it, ok := q.provisioningItem(k, st, current[k]); ok {
				out = append(out, it)
			}
		}
	}
	slices.SortFunc(out, func(a, b AttentionItem) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Label, b.Label), cmp.Compare(a.Reason, b.Reason))
	})
	return out
}

// provisioningAttentionKey is the use rel belongs to, when it is a declared
// dependency or configuration reference to a visible provider or to one
// Restrict hid.
func provisioningAttentionKey(rel *Relationship) (provisioningUseKey, bool) {
	if hiddenProviderEdge(rel, rel.FromService) {
		return provisioningUseKey{rel.FromService, rel.hiddenTo, rel.Type, true}, true
	}
	if offeringRelationship(rel, "") && ConsumesEdge(*rel) {
		return provisioningUseKey{rel.FromService, rel.ToService, rel.Type, false}, true
	}
	return provisioningUseKey{}, false
}

// provisioningItem is the attention item for one status, if it needs one.
func (q *Query) provisioningItem(k provisioningUseKey, st ProvisioningStatus, current *ContractRevision) (AttentionItem, bool) {
	consumer := q.snap.Services[k.from]
	it := AttentionItem{Entity: serviceEntityRef(consumer), Service: string(k.from), Category: categoryProvisioning, Source: st.Source}
	var state string
	switch {
	case st.Phase == "Failed":
		it.Severity, it.Code, it.Summary, state = severityError, codeProvisioningFailed, "Resource failed to provision", " is Failed"
	case st.Stale && current != nil && st.Revision != nil && st.Revision.Key == string(current.Key):
		it.Severity, it.Code, it.Summary, state = severityWarning, codeProvisioningStale, "Resource status is stale", " has a stale report"
	default:
		return AttentionItem{}, false
	}
	where := " in " + cmp.Or(st.Environment, "default")
	if st.Entry != "" {
		where += " for " + st.Entry
	}
	providerName := "a hidden provider"
	if !k.hidden {
		providerName = q.snap.Services[k.to].Name
	}
	it.Label = consumer.Name + ": " + providerName + where + state
	it.Reason, it.NextStep = provisioningAttentionReason(st, k.hidden, where), provisioningNextStep(st, k.hidden)
	return it, true
}

// provisioningAttentionReason names who reported what, signed or unsigned, and when.
// A hidden provider's adapter and external reference stay out.
func provisioningAttentionReason(st ProvisioningStatus, hidden bool, where string) string {
	who := "a provider"
	if !hidden {
		who = st.Provider
		if st.ExternalRef != "" {
			who += " (" + st.ExternalRef + ")"
		}
	}
	signed := "signed"
	if !st.Verified {
		signed = "unsigned"
	}
	reason := who + " reported " + st.ReportedPhase + where + ", " + signed + ", as of " + st.AsOf.UTC().Format(time.RFC3339)
	if st.Reason != "" {
		reason += "; " + st.Reason
	}
	if hidden {
		reason += "; " + hiddenMessage
	}
	return reason
}

// provisioningNextStep points at the provider's tools, where the resource is
// fixed, resized or deleted.
func provisioningNextStep(st ProvisioningStatus, hidden bool) string {
	const tools = "; resize or delete runs in the provider's tools"
	if hidden {
		return "Ask the provider's owner to check this resource" + tools
	}
	ref := "this resource"
	if st.ExternalRef != "" {
		ref = st.ExternalRef
	}
	return "Check " + st.Provider + "'s status for " + ref + tools
}
