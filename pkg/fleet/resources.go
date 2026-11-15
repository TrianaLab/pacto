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
	return []ResourceConsumption{{State: consumptionNotReported, Provenance: ProvenanceObserved, Reason: consumptionNotReportedReason}}
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
// values its consumer declares for that provider under an entry the use's
// edges name. The values come from the consumer revision the use's
// provisioning is about, the newest one with such an edge: cfgs when that is
// cur, the consumer's current revision, else that revision's own preview. A
// dependency row requested nothing.
// ponytail: the join reads the bounded preview; a revision with more
// configurations than it holds loses the tail, flagged by RequestedTruncated.
func (q *Query) attachRequested(uses []OfferingUse, cur *ContractRevision, cfgs ConfigurationsPreview) {
	for i := range uses {
		u := &uses[i]
		if u.Via != RelationshipConfigRef {
			continue
		}
		rev, entries := q.configRefRequest(ServiceKey(u.Consumer.Key), ServiceKey(u.Provider.Key))
		revCfgs := cfgs
		if rev != nil && (cur == nil || rev.Key != cur.Key) {
			_, revCfgs, _ = q.currentRevisionContent(rev)
		}
		var consumer *EntityRef
		if rev != nil {
			ref := revisionEntityRef(rev)
			consumer = &ref
		}
		for _, c := range revCfgs.Items {
			if res := c.Resolution; res != nil && res.Service != nil && res.Service.Key == u.Provider.Key && entries[c.Name] {
				u.Requested = append(u.Requested, RequestedConfiguration{Entry: c.Name, Configuration: c.Configuration, Revision: res.Revision,
					ConsumerRevision: consumer, Values: c.Values, Provenance: ProvenanceDeclared})
			}
		}
		u.RequestedTruncated = revCfgs.Truncated
	}
}

// configRefRequest is the newest consumer revision (see [lessRevisionChrono])
// with a configuration reference to provider in the (possibly restricted)
// view, the revision offeringUsesIn reports provisioning about, and the entry
// names of that revision's references to provider.
func (q *Query) configRefRequest(consumer, provider ServiceKey) (*ContractRevision, map[string]bool) {
	var rels []*Relationship
	var newest *ContractRevision
	var newestKey RevisionKey
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		if rel.Type == RelationshipConfigRef && rel.ToService == provider && offeringRelationship(rel, consumer) {
			rels = append(rels, rel)
			if r := q.snap.Revisions[rel.FromRevision]; r != nil && (newest == nil || lessRevisionChrono(newest, r)) {
				newest, newestKey = r, r.Key
			}
		}
	}
	entries := map[string]bool{}
	for _, rel := range rels {
		if rel.FromRevision == newestKey {
			entries[rel.To] = true
		}
	}
	return newest, entries
}
