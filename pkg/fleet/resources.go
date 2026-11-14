package fleet

// ResourceActionsNote is the one sentence every surface prints under a
// service's resources: Pacto changes no resource, and the provider's schema
// checks a request where Pacto resolves it.
const ResourceActionsNote = "Resize or delete runs in the provider's tools. pacto_edit refuses a request the provider's schema rejects, and pacto validate and push fail on it."

// hiddenResourceProviders counts the distinct providers the viewer may not
// see that consumer uses and that a visible report of consumer names: its
// declared, hidden dependency or configuration reference edges joined to its
// own provisioning reports. Both inputs are the consumer's own facts; nothing
// the hidden provider declares is read, so the count is a lower bound: a
// hidden provider nothing reported on stays in HiddenDependencies only.
func (q *Query) hiddenResourceProviders(consumer ServiceKey) int {
	reported := map[provisioningKey]bool{}
	for _, rec := range q.snap.Provisioning {
		if rec.ServiceKey == consumer {
			reported[provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}] = true
		}
	}
	hidden := map[ServiceKey]bool{}
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		r := q.snap.Revisions[rel.FromRevision]
		if hiddenProviderEdge(rel, consumer) && r != nil && reported[provisioningKey{consumer, r.Digest, provisioningKind(rel.Type), rel.To}] {
			hidden[rel.hiddenTo] = true
		}
	}
	return len(hidden)
}

// hasHiddenProvider reports whether key declares a dependency or
// configuration reference on a provider the viewer may not see.
func (q *Query) hasHiddenProvider(key ServiceKey) bool {
	for i := range q.snap.Relationships {
		if hiddenProviderEdge(&q.snap.Relationships[i], key) {
			return true
		}
	}
	return false
}

// hiddenProviderEdge reports whether rel is from's declared, resolved
// dependency or configuration reference to a provider Restrict hid.
func hiddenProviderEdge(rel *Relationship, from ServiceKey) bool {
	return rel.Hidden && rel.FromService == from && ConsumesEdge(*rel)
}

// consumptionNotReportedReason is why a resource's consumption is not-reported.
const consumptionNotReportedReason = "no provider reports usage or cost for this resource to Pacto; ask Mira for live signals"

// notReportedConsumption is the consumption of a resource no provider reports
// usage or cost for: an explicit state, never zero.
func notReportedConsumption() []ResourceConsumption {
	return []ResourceConsumption{{State: "not-reported", Provenance: ProvenanceObserved, Reason: consumptionNotReportedReason}}
}

// revisionsOf is every revision of s.
func (q *Query) revisionsOf(s *ServiceRecord) []*ContractRevision {
	revs := make([]*ContractRevision, 0, len(s.Revisions))
	for _, rk := range s.Revisions {
		revs = append(revs, q.snap.Revisions[rk])
	}
	return revs
}

// attachProviderFacts sets each use's provider owner, as the offering page
// derives it, and the provider's availability.
func (q *Query) attachProviderFacts(uses []OfferingUse) {
	for i := range uses {
		p := q.snap.Services[ServiceKey(uses[i].Provider.Key)]
		av := q.providerAvailability(p)
		uses[i].ProviderOwner, uses[i].ProviderAvailability = serviceOwnership(p, q.revisionsOf(p)), &av
	}
}

// attachRequested joins each configuration reference use with the request
// values cfgs, the consumer's current revision, declares for that provider
// under an entry the use's edges name. A dependency row requested nothing.
// ponytail: cfgs is the bounded preview; a revision with more configurations
// than it holds loses the tail, flagged by RequestedTruncated.
func (q *Query) attachRequested(uses []OfferingUse, cfgs ConfigurationsPreview) {
	for i := range uses {
		u := &uses[i]
		if u.Via != RelationshipConfigRef {
			continue
		}
		entries := q.configRefEntries(ServiceKey(u.Consumer.Key), ServiceKey(u.Provider.Key))
		for _, c := range cfgs.Items {
			if res := c.Resolution; res != nil && res.Service != nil && res.Service.Key == u.Provider.Key && entries[c.Name] {
				u.Requested = append(u.Requested, RequestedConfiguration{Entry: c.Name, Configuration: c.Configuration, Revision: res.Revision, Values: c.Values, Provenance: ProvenanceDeclared})
			}
		}
		u.RequestedTruncated = cfgs.Truncated
	}
}

// configRefEntries is the entry names of consumer's configuration references
// to provider in the (possibly restricted) view.
func (q *Query) configRefEntries(consumer, provider ServiceKey) map[string]bool {
	entries := map[string]bool{}
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		if rel.Type == RelationshipConfigRef && rel.ToService == provider && offeringRelationship(rel, consumer) {
			entries[rel.To] = true
		}
	}
	return entries
}
