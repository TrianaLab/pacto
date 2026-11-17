package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// Reason codes an external system is explained with.
const (
	ReasonExternalDeclared     = "EXTERNAL_DECLARED"
	ReasonExternalCatalogMatch = "EXTERNAL_CATALOG_MATCH"
	ReasonExternalNameOnly     = "EXTERNAL_NAME_ONLY"
	// ReasonExternalUnclassified is a record no catalog entry classifies whose
	// consumers still join through a declared identity or a host's domain.
	ReasonExternalUnclassified = "EXTERNAL_UNCLASSIFIED"
	// ReasonExternalSourcesTruncated says the declared reasons list only some
	// consumers, or that permissions hide some.
	ReasonExternalSourcesTruncated = "EXTERNAL_SOURCES_TRUNCATED"
)

// ExternalView is the answer for one external system. External lists at most
// MaxExternalSources consumers and their sources; its totals count them all.
type ExternalView struct {
	Meta     Meta                `json:"meta"`
	External ExternalRecord      `json:"external"`
	Catalog  *ExternalCatalogRef `json:"catalog,omitempty" doc:"The catalog that classified it, with its digest and signature"`
	// FoldedInto is the node the answer folds it into, when it does.
	FoldedInto ExternalKey `json:"foldedInto,omitempty" doc:"The other-egress node this answer folds it into"`
}

// GetExternal returns one external system by identity, with or without the
// external: prefix. It returns a [NotFoundError] of kind external when no
// visible service names it.
func (q *Query) GetExternal(key string) (*ExternalView, error) {
	rec, into, err := q.lookupExternal(key)
	if err != nil {
		return nil, err
	}
	return jsonClone(&ExternalView{Meta: q.meta(), External: boundExternal(rec), Catalog: q.externalCatalogRef(rec), FoldedInto: into}), nil
}

// explainExternal lists one reason per source, the catalog match or the
// name-only fallback, and the fold. Its status is unknown: Pacto never
// observes an external's health.
func (q *Query) explainExternal(key string) (*ExplainResult, error) {
	rec, into, err := q.lookupExternal(key)
	if err != nil {
		return nil, err
	}
	res := &ExplainResult{Meta: q.meta(), Subject: ExternalSubjectPrefix + string(rec.Key), Kind: string(KindExternal), Status: StatusUnknown, Reasons: []Reason{}}
	b := boundExternal(rec)
	for _, src := range b.Sources {
		msg := fmt.Sprintf("declared by %s dependencies[%d] (name: %s", src.Service, src.Index, src.Dependency)
		if src.Version != "" {
			msg += ", version: " + src.Version
		}
		if src.Superseded {
			msg += ", not in the newest revision"
		}
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalDeclared, Message: msg + ")", Assertion: &AssertionRef{Kind: "dependency", Name: src.Dependency}})
	}
	if more := rec.ConsumersTotal - len(b.Consumers); more > 0 || rec.HiddenConsumers > 0 {
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalSourcesTruncated, Message: fmt.Sprintf(
			"%d of %d consumers (%d declarations) are listed; %d more are not, and %d consumers are hidden by your permissions; pacto fleet get counts them",
			len(b.Consumers), rec.ConsumersTotal, rec.SourcesTotal, more, rec.HiddenConsumers)})
	}
	switch {
	case rec.Catalog != nil:
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalCatalogMatch,
			Message: fmt.Sprintf("classified %s by entry %s of the %s catalog %s", rec.Class, rec.Catalog.Entry, rec.Catalog.Origin, rec.Catalog.Digest)})
	case rec.Folded > 0:
	case rec.JoinedBy == externalcatalog.MatchIdentity:
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalUnclassified, Message: "no catalog entry classifies it; its consumers join through the identity they declare"})
	case rec.JoinedBy == externalcatalog.MatchHost:
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalUnclassified, Message: "no catalog entry classifies it; its consumers join through its registrable domain"})
	default:
		res.Reasons = append(res.Reasons, Reason{Code: ReasonExternalNameOnly, Message: "no catalog alias equals this name; consumers that spell it differently do not join"})
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
	nodeSet, cycles := q.walkGraph(res, root, gq, maxNodes, func(k ServiceKey) []graphStep {
		if k != root {
			return q.graphNeighbours(k, root, "", DirectionDependents, gq.IncludeObserved)
		}
		steps := make([]graphStep, len(rec.Consumers))
		for i, c := range rec.Consumers {
			steps[i] = graphStep{Key: c, Provenance: ProvenanceDeclared}
		}
		return steps
	})
	// The root is not a service: a cycle is the path from its first consumer.
	for _, c := range cycles {
		res.Cycles = append(res.Cycles, c[1:])
	}
	res.Edges, res.Unresolved = q.graphEdges(nodeSet, root, DirectionDependents, "")
	for _, rel := range q.snap.Relationships {
		if externalEdge(rel, key) && nodeSet[rel.FromService] {
			res.Edges = append(res.Edges, rel)
		}
	}
	return res, nil
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
}

// externalDetail is one external system's page.
func (q *Query) externalDetail(key string) (*EntityDetail, error) {
	rec, into, err := q.lookupExternal(key)
	if err != nil {
		return nil, err
	}
	b := boundExternal(rec)
	data := &ExternalDetailData{External: b, Consumers: []ExternalConsumer{}, Catalog: q.externalCatalogRef(rec), FoldedInto: into}
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
	return &EntityDetail{Meta: q.productMeta(), Entity: ref, Status: ref.Status, External: data}, nil
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
	if declared, _ := knowledgeFromViews(bp.views); !declared || bp.dir == DirectionDependencies {
		return res, nil
	}
	res.TotalNodes += len(rec.Consumers)
	// Only drawn consumers get an edge, so no edge names a node the answer
	// leaves out.
	drawn := map[ServiceKey]bool{}
	for _, c := range rec.Consumers {
		if len(res.Nodes) >= bp.maxNodes {
			res.Truncated = true
			break
		}
		drawn[c] = true
		ref := q.serviceRef(c)
		res.Nodes = append(res.Nodes, NeighborhoodNode{Ref: ref, Depth: 1, Status: ref.Status, Owner: q.snap.Services[c].Owner.DisplayString()})
	}
	edges := q.externalEdges(func(k ServiceKey) bool { return drawn[k] }, rec.Key)
	res.Edges, res.TruncatedEdges = boundEdges(edges, bp.maxEdges)
	res.Truncated = res.Truncated || res.TruncatedEdges
	return res, nil
}

// externalEdges is one declared edge per in-scope consumer and external
// system it names (only that one, when only is set), keyed as the answer
// names the system, ordered by consumer then identity.
func (q *Query) externalEdges(inScope func(ServiceKey) bool, only ExternalKey) []NeighborhoodEdge {
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
	if declared, _ := knowledgeFromViews(bp.views); !declared || bp.dir == DirectionDependents {
		return
	}
	edges := q.externalEdges(func(k ServiceKey) bool { _, ok := depthOf[k]; return ok }, "")
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
