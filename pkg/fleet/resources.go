package fleet

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
