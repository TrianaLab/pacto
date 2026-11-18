package fleet

// inferredEnds reports whether an observed edge whose ends resolved as
// fromRes and toRes is an inferred relationship: both ends name a fleet
// service and at least one of them is an inferred service.
func inferredEnds(fromRes, toRes ObservedResolution) bool {
	known := func(r ObservedResolution) bool { return r == ObservedResolved || r == ObservedInferred }
	return known(fromRes) && known(toRes) && (fromRes == ObservedInferred || toRes == ObservedInferred)
}

// emitInferredRelationship appends the inferred relationship of one
// aggregated runtime edge with an inferred end to InferredRelationships,
// never to Relationships. A service callee extends the inferred adjacency; an
// external callee records the inferred caller on the system's record.
func emitInferredRelationship(snap *FleetSnapshot, k observedEdgeKey, a *observedEdgeAgg) {
	rel := aggregatedRelationship(k, a, ProvenanceInferred)
	snap.InferredRelationships = append(snap.InferredRelationships, rel)
	if k.ext != "" {
		recordObservedExternal(snap, k.from, k.ext, a.match, ProvenanceInferred)
		return
	}
	snap.inferredForward[k.from] = appendUnique(snap.inferredForward[k.from], k.to)
	snap.inferredReverse[k.to] = appendUnique(snap.inferredReverse[k.to], k.from)
}
