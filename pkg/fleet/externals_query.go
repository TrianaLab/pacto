package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sort"
)

// Reason codes an external system is explained with.
const (
	ReasonExternalDeclared     = "EXTERNAL_DECLARED"
	ReasonExternalCatalogMatch = "EXTERNAL_CATALOG_MATCH"
	ReasonExternalNameOnly     = "EXTERNAL_NAME_ONLY"
	ReasonExternalObserved     = "EXTERNAL_OBSERVED"
	ReasonExternalInferred     = "EXTERNAL_INFERRED"
)

// ExternalView is the answer for one external system. External lists at most
// MaxExternalSources consumers and their sources; its totals count them all.
type ExternalView struct {
	Meta     Meta                `json:"meta"`
	External ExternalRecord      `json:"external"`
	Catalog  *ExternalCatalogRef `json:"catalog,omitempty" doc:"The catalog that classified it, with its digest and signature"`
	// FoldedInto is the node the answer folds it into, when it does.
	FoldedInto ExternalKey `json:"foldedInto,omitempty" doc:"The other-egress node this answer folds it into"`
	ExternalEvidence
}

// ExternalEvidence is where each source recognized or saw an external
// system, for the consumers an answer lists: each list holds at most
// MaxExternalSources entries and its total counts them all.
type ExternalEvidence struct {
	InferredSources      []ExternalInferredSource `json:"inferredSources,omitempty" doc:"Where each source recognized it in a listed consumer's configuration"`
	InferredSourcesTotal int                      `json:"inferredSourcesTotal,omitempty" doc:"Every inferred source of the listed consumers"`
	ObservedSources      []ExternalObservedSource `json:"observedSources,omitempty" doc:"Each trace source that saw a listed consumer call it"`
	ObservedSourcesTotal int                      `json:"observedSourcesTotal,omitempty" doc:"Every observed source of the listed consumers"`
}

// ExternalInferredSource is one inferred source and the consumer it names.
type ExternalInferredSource struct {
	Service ServiceKey `json:"service" doc:"The consumer"`
	InferredSource
}

// ExternalObservedSource is one trace source's calls from a consumer.
type ExternalObservedSource struct {
	Service ServiceKey `json:"service" doc:"The consumer"`
	ObservedSourceStat
}

// externalEvidence is the inferred and observed evidence of the edges from
// consumers to the external system key, bounded. Nil lists when none.
func (q *Query) externalEvidence(key ExternalKey, consumers []ServiceKey) ExternalEvidence {
	var inf []ExternalInferredSource
	var obs []ExternalObservedSource
	for _, rel := range q.snap.Relationships {
		if !externalEdge(rel, key) || !slices.Contains(consumers, rel.FromService) {
			continue
		}
		for _, s := range rel.InferredSources {
			inf = append(inf, ExternalInferredSource{Service: rel.FromService, InferredSource: s})
		}
		for _, s := range rel.ObservedSources {
			obs = append(obs, ExternalObservedSource{Service: rel.FromService, ObservedSourceStat: s})
		}
	}
	var ev ExternalEvidence
	if len(inf) > 0 {
		ev.InferredSources, ev.InferredSourcesTotal, _ = boundSlice(inf, MaxExternalSources)
	}
	if len(obs) > 0 {
		ev.ObservedSources, ev.ObservedSourcesTotal, _ = boundSlice(obs, MaxExternalSources)
	}
	return ev
}

// GetExternal returns one external system by identity, with or without the
// external: prefix. It returns a [NotFoundError] of kind external when no
// visible service names it.
func (q *Query) GetExternal(key string) (*ExternalView, error) {
	rec, into, err := q.lookupExternal(key)
	if err != nil {
		return nil, err
	}
	b := boundExternal(rec)
	return jsonClone(&ExternalView{Meta: q.externalMeta(), External: b, Catalog: q.externalCatalogRef(rec), FoldedInto: into,
		ExternalEvidence: q.externalEvidence(rec.Key, b.Consumers)}), nil
}

// externalsUnreported is the limitation an external answer carries when a
// visible target cannot say which external systems it uses: any of them may
// be a consumer it does not list. Nil when every target reports.
func (q *Query) externalsUnreported() []Limitation {
	n := 0
	for _, t := range q.snap.Targets {
		if slices.ContainsFunc(t.Limitations, func(l Limitation) bool { return l.Code == LimitationExternalsUnreported }) {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return []Limitation{{Code: LimitationExternalsUnreported, Source: "fleet",
		Message: fmt.Sprintf("%d targets do not report the external systems they use, so the consumers listed may be incomplete", n)}}
}

// externalMeta is meta for an external answer: partial, citing
// externalsUnreported, when a visible target does not report its externals.
func (q *Query) externalMeta() Meta {
	m := q.meta()
	if derived := q.externalsUnreported(); derived != nil {
		m.Limitations, m.LimitationsTruncated = BoundLimitations(append(derived, cloneLimitations(q.snap.Limitations)...))
		m.Completeness = partialOf(m.Completeness)
	}
	return m
}

// partialOf is c lowered to partial when it claimed complete.
func partialOf(c Completeness) Completeness {
	if c == CompletenessComplete {
		return CompletenessPartial
	}
	return c
}

// explainExternal lists one reason per source, the catalog match or the
// name-only fallback, and the fold. Its status is unknown: Pacto never
// observes an external's health.
func (q *Query) explainExternal(key string) (*ExplainResult, error) {
	rec, into, err := q.lookupExternal(key)
	if err != nil {
		return nil, err
	}
	res := &ExplainResult{Meta: q.externalMeta(), Subject: ExternalSubjectPrefix + string(rec.Key), Kind: string(KindExternal), Status: StatusUnknown, Reasons: []Reason{}}
	b := boundExternal(rec)
	for _, src := range b.Sources {
		if src.Kind != ExternalSourcePodSpec {
			res.Reasons = append(res.Reasons, externalSourceReason(src))
		}
	}
	for _, src := range q.externalEvidence(rec.Key, b.Consumers).InferredSources {
		res.Reasons = append(res.Reasons, inferredSourceReason(src))
	}
	switch {
	case rec.Catalog != nil:
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalCatalogMatch,
			Message: fmt.Sprintf("classified %s by entry %s of the %s catalog %s", rec.Class, rec.Catalog.Entry, rec.Catalog.Origin, rec.Catalog.Digest)})
	case rec.Folded == 0:
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalNameOnly, Message: "no catalog entry names it; consumers that spell it differently do not join"})
	}
	switch {
	case into != "":
		res.Reasons = append(res.Reasons, Reason{Code: LimitationExternalCap, Message: fmt.Sprintf("this answer folds it into %s with the least-consumed external systems beyond %d", into, MaxExternalNodes)})
	case rec.Folded > 0:
		res.Reasons = append(res.Reasons, Reason{Code: LimitationExternalCap, Message: fmt.Sprintf("folds %d external systems beyond %d", rec.Folded, MaxExternalNodes)})
	}
	sortReasons(res.Reasons)
	return res, nil
}

// externalSourceReason explains one declared or observed piece of evidence.
func externalSourceReason(src ExternalSource) Reason {
	if src.Kind == ExternalSourceObserved {
		return Reason{Code: ReasonExternalObserved, Message: fmt.Sprintf("observed %s calling it (matched by %s)", src.Service, src.Match)}
	}
	msg := fmt.Sprintf("declared by %s dependencies[%d] (name: %s", src.Service, src.Index, src.Dependency)
	if src.Version != "" {
		msg += ", version: " + src.Version
	}
	return Reason{Code: ReasonExternalDeclared, Message: msg + ")", Assertion: &AssertionRef{Kind: "dependency", Name: src.Dependency}}
}

// inferredSourceReason explains where one source recognized it: the kind
// and the object and key, never a value, then the confidence, the
// normalized host, the catalog entry and the target.
func inferredSourceReason(src ExternalInferredSource) Reason {
	msg := fmt.Sprintf("inferred from %s %s of %s (%s", src.Kind, src.Where, src.Service, src.Confidence)
	for _, kv := range [][2]string{{"host", src.Host}, {"entry", src.Entry}, {"target", string(src.Target)}} {
		if kv[1] != "" {
			msg += ", " + kv[0] + " " + kv[1]
		}
	}
	return Reason{Code: ReasonExternalInferred, Message: msg + ")"}
}

// externalEdge reports whether rel is a declared dependency on the external
// system key as an answer names it.
func externalEdge(rel Relationship, key ExternalKey) bool {
	return rel.ToExternal != "" && !rel.Hidden && (rel.ToExternal == key || rel.FoldedInto == key)
}

// externalRecord is the record an answer names key by: a kept one, the
// other-egress node, or a folded identity asked for by name.
func (q *Query) externalRecord(key ExternalKey) *ExternalRecord {
	if rec := q.externals[key]; rec != nil {
		return rec
	}
	return q.snap.Externals[key]
}

// graphExternal walks the consumers of an external system, then with
// Transitive their dependents. An external has no dependencies Pacto knows.
func (q *Query) graphExternal(gq GraphQuery, key ExternalKey, maxNodes int) (*GraphResult, error) {
	if gq.Direction == DirectionDependencies {
		return nil, &InvalidQueryError{Field: "direction", Value: string(gq.Direction), Reason: "an external system has no dependencies Pacto knows"}
	}
	rec, _, err := q.lookupExternal(string(key))
	if err != nil {
		return nil, err
	}
	key = rec.Key
	root := ServiceKey(ExternalSubjectPrefix + string(key))
	res := &GraphResult{Meta: q.meta(), External: key, Direction: DirectionDependents, Nodes: []GraphNode{}}
	steps := q.externalConsumerSteps(key, gq.IncludeObserved)
	nodeSet, cycles := q.walkGraph(res, root, gq, maxNodes, func(k ServiceKey) []graphStep {
		if k != root {
			return q.graphNeighbours(k, root, "", DirectionDependents, gq.IncludeObserved)
		}
		return steps
	})
	// The root is not a service: a cycle is the path from its first consumer.
	for _, c := range cycles {
		res.Cycles = append(res.Cycles, c[1:])
	}
	res.Edges, res.Unresolved = q.graphEdges(nodeSet, root, DirectionDependents, "")
	for _, rel := range q.snap.Relationships {
		if externalEdge(rel, key) && nodeSet[rel.FromService] && graphProvenance(rel.Provenance, gq.IncludeObserved) {
			res.Edges = append(res.Edges, rel)
		}
	}
	return res, nil
}

// graphProvenance reports whether a graph answer draws an edge of
// provenance p: declared always, observed when asked for, and inferred
// never, since the graph's provenance names only declared and observed.
func graphProvenance(p string, includeObserved bool) bool {
	return p == ProvenanceDeclared || (includeObserved && p == ProvenanceObserved)
}

// externalConsumerSteps are the first steps of a graph walk from external
// system key: each consumer an edge the graph draws reaches, in key order,
// labelled declared when any declares it, else observed.
func (q *Query) externalConsumerSteps(key ExternalKey, includeObserved bool) []graphStep {
	prov := map[ServiceKey]string{}
	for _, rel := range q.snap.Relationships {
		if !externalEdge(rel, key) || !graphProvenance(rel.Provenance, includeObserved) {
			continue
		}
		if prov[rel.FromService] != ProvenanceDeclared {
			prov[rel.FromService] = rel.Provenance
		}
	}
	steps := make([]graphStep, 0, len(prov))
	for _, c := range slices.Sorted(maps.Keys(prov)) {
		steps = append(steps, graphStep{Key: c, Provenance: prov[c]})
	}
	return steps
}

// searchExternals is at most MaxSearchExternals external systems whose
// identity, label or alias holds text, and how many match.
func (q *Query) searchExternals(text string) ([]EntityRef, int) {
	if text == "" {
		return nil, 0
	}
	var out []EntityRef
	for _, rec := range q.sortedExternals() {
		if externalTextMatches(rec, text) {
			out = append(out, externalEntityRef(rec))
		}
	}
	shown, total, _ := boundSlice(out, MaxSearchExternals)
	return shown, total
}

// ExternalConsumer is one service that declares an external system.
type ExternalConsumer struct {
	Service  EntityRef `json:"service"`
	Required bool      `json:"required,omitempty" doc:"Whether any of its declarations is required"`
	Versions []string  `json:"versions,omitempty" doc:"The free-text versions its declarations name, sorted"`
}

// ExternalDetailData is the external-kind payload: the record, bounded, its
// listed consumers and the catalog that classified it.
type ExternalDetailData struct {
	External   ExternalRecord      `json:"external"`
	Consumers  []ExternalConsumer  `json:"consumers"`
	Catalog    *ExternalCatalogRef `json:"catalog,omitempty" doc:"The catalog that classified it, with its digest and signature"`
	FoldedInto ExternalKey         `json:"foldedInto,omitempty" doc:"The other-egress node this answer folds it into"`
	ExternalEvidence
}

// externalDetail is one external system's page.
func (q *Query) externalDetail(key string) (*EntityDetail, error) {
	rec, into, err := q.lookupExternal(key)
	if err != nil {
		return nil, err
	}
	b := boundExternal(rec)
	data := &ExternalDetailData{External: b, Consumers: []ExternalConsumer{}, Catalog: q.externalCatalogRef(rec), FoldedInto: into,
		ExternalEvidence: q.externalEvidence(rec.Key, b.Consumers)}
	for _, c := range b.Consumers {
		ec := ExternalConsumer{Service: q.serviceRef(c)}
		for _, src := range b.Sources {
			if src.Service == c {
				ec.Required = ec.Required || src.Required
				if src.Version != "" && !slices.Contains(ec.Versions, src.Version) {
					ec.Versions = append(ec.Versions, src.Version)
				}
			}
		}
		sort.Strings(ec.Versions)
		data.Consumers = append(data.Consumers, ec)
	}
	ref := externalEntityRef(rec)
	derived := q.externalsUnreported()
	meta := q.productMetaWith(derived)
	if derived != nil {
		meta.Completeness = partialOf(meta.Completeness)
	}
	return &EntityDetail{Meta: meta, Entity: ref, Status: ref.Status, External: data}, nil
}

// externalNeighborhood focuses an external system: its consumers at depth 1,
// unless only dependencies are asked for, which it has none of.
func (q *Query) externalNeighborhood(key string, bp boundedParams) (*Neighborhood, error) {
	rec, _, err := q.lookupExternal(key)
	if err != nil {
		return nil, err
	}
	focus := externalEntityRef(rec)
	res := &Neighborhood{
		Meta: q.productMeta(), Perspective: PerspectiveService, RequestedFocus: focus, FocusService: focus,
		Direction: bp.dir, Depth: bp.depth, EffectiveDepth: 1, Views: bp.views, MaxNodes: bp.maxNodes, MaxEdges: bp.maxEdges,
		Nodes: []NeighborhoodNode{{Ref: focus, Focus: true, Status: focus.Status}}, Edges: []NeighborhoodEdge{},
		Limitations: limitationsPreview(nil), TotalNodes: 1,
	}
	if bp.dir == DirectionDependencies {
		return res, nil
	}
	consumers := map[ServiceKey]bool{}
	for _, c := range rec.Consumers {
		consumers[c] = true
	}
	// The consumers drawn are those an edge in the requested views reaches.
	edges := q.externalEdges(func(k ServiceKey) bool { return consumers[k] }, rec.Key, bp.views)
	var shown []ServiceKey
	for _, e := range edges {
		if c := ServiceKey(e.From.Key); !slices.Contains(shown, c) {
			shown = append(shown, c)
		}
	}
	res.TotalNodes += len(shown)
	for _, c := range shown {
		if len(res.Nodes) >= bp.maxNodes {
			res.Truncated = true
			break
		}
		ref := q.serviceRef(c)
		res.Nodes = append(res.Nodes, NeighborhoodNode{Ref: ref, Depth: 1, Status: ref.Status, Owner: q.snap.Services[c].Owner.DisplayString()})
	}
	res.Edges, res.TruncatedEdges = boundEdges(edges, bp.maxEdges)
	res.Truncated = res.Truncated || res.TruncatedEdges
	return res, nil
}

// externalEdges is one merged edge per in-scope consumer and external
// system it names (only that one, when only is set) that matches views,
// keyed as the answer names the system, ordered by consumer then identity.
func (q *Query) externalEdges(inScope func(ServiceKey) bool, only ExternalKey, views []KnowledgeView) []NeighborhoodEdge {
	merged := map[string]*NeighborhoodEdge{}
	for _, rel := range q.snap.Relationships {
		to := only
		if only == "" {
			to = cmp.Or(rel.FoldedInto, rel.ToExternal)
		}
		if !externalEdge(rel, to) || !inScope(rel.FromService) {
			continue
		}
		id := edgeID(string(rel.FromService), ExternalSubjectPrefix+string(to), RelationDependency)
		e := merged[id]
		if e == nil {
			e = &NeighborhoodEdge{ID: id, Relation: RelationDependency, From: q.serviceRef(rel.FromService), To: externalEntityRef(q.externalRecord(to))}
			merged[id] = e
		}
		q.foldRelationshipIntoEdge(e, rel)
	}
	out := make([]NeighborhoodEdge, 0, len(merged))
	for _, id := range slices.Sorted(maps.Keys(merged)) {
		e := merged[id]
		if !edgeMatchesViews(*e, views) {
			continue
		}
		finalizeEdge(e)
		// Pacto observes no external system here, so no verdict compares.
		e.Difference = ""
		out = append(out, *e)
	}
	return out
}

// boundEdges keeps at most max edges and reports whether it cut any.
func boundEdges(edges []NeighborhoodEdge, max int) ([]NeighborhoodEdge, bool) {
	if len(edges) > max {
		return edges[:max], true
	}
	return edges, false
}

// addExternalLeaves adds to a service neighborhood the external systems its
// walked services declare, as leaf nodes one hop past their consumer, and the
// edges to them. Both count against the answer's node and edge caps.
func (q *Query) addExternalLeaves(res *Neighborhood, depthOf map[ServiceKey]int, bp boundedParams) {
	if bp.dir == DirectionDependents {
		return
	}
	edges := q.externalEdges(func(k ServiceKey) bool { _, ok := depthOf[k]; return ok }, "", bp.views)
	leafDepth := map[string]int{}
	var leaves []string
	for _, e := range edges {
		d := depthOf[ServiceKey(e.From.Key)] + 1
		if prev, ok := leafDepth[e.To.Key]; !ok || d < prev {
			if !ok {
				leaves = append(leaves, e.To.Key)
			}
			leafDepth[e.To.Key] = d
		}
	}
	res.TotalNodes += len(leaves)
	slices.SortFunc(leaves, func(a, b string) int { return cmp.Or(cmp.Compare(leafDepth[a], leafDepth[b]), cmp.Compare(a, b)) })
	drawn := map[string]bool{}
	for _, k := range leaves {
		if len(res.Nodes) >= bp.maxNodes {
			res.Truncated = true
			break
		}
		drawn[k] = true
		ref := externalEntityRef(q.externalRecord(ExternalKey(k)))
		res.Nodes = append(res.Nodes, NeighborhoodNode{Ref: ref, Depth: leafDepth[k], Status: ref.Status})
	}
	for _, e := range edges {
		if !drawn[e.To.Key] {
			continue
		}
		if len(res.Edges) >= bp.maxEdges {
			res.Truncated, res.TruncatedEdges = true, true
			break
		}
		res.Edges = append(res.Edges, e)
	}
}
