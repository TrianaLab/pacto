package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// sourcedUse pairs a raw external use with the id of the source that
// reported it.
type sourcedUse struct {
	use    RawExternalUse
	source string
}

// inferredKey is one inferred (consumer, external system) edge.
type inferredKey struct {
	from ServiceKey
	ext  ExternalKey
}

// foldExternalUses turns the external uses sources inferred from
// configuration into one inferred relationship per (consumer, external
// system), with every source's evidence, and records each use on the
// system's [ExternalRecord], so the answer cap applies. The relationships
// never enter a service index: an inferred use never clears a gate. A use
// whose consumer the snapshot does not hold, or whose identity is not one,
// is dropped and counted in one EXTERNAL_USE_UNATTRIBUTED per source; a use
// classified by a catalog other than the snapshot's adds one
// EXTERNAL_CATALOG_MISMATCH per (source, digest).
func foldExternalUses(snap *FleetSnapshot, uses []sourcedUse) {
	if len(uses) == 0 {
		return
	}
	cat := snap.externalCatalog()
	targets := targetsByScope(snap)
	unattributed := map[string]int{}
	mismatch := map[[2]string]bool{}
	edges := map[inferredKey]*Relationship{}
	for _, su := range uses {
		u := su.use
		svc := NewServiceKeyDomain(u.Domain, u.Service)
		id, err := externalid.Parse(u.Identity)
		if snap.namespaceShortForm(externalcatalog.Match{Kind: inferredMatch(u), Host: u.Host, Entry: u.Entry}) {
			continue // an in-cluster name of a namespace the snapshot knows
		}
		if snap.Services[svc] == nil || err != nil {
			unattributed[su.source]++
			continue
		}
		if u.Catalog != "" && !snapshotCatalog(snap, u.Catalog) {
			mismatch[[2]string{su.source, u.Catalog}] = true
		}
		key := ExternalKey(id)
		src := InferredSource{Source: su.source, Target: targetOfUse(targets, su.source, svc, u.Namespace), Kind: u.Kind, Where: u.Where,
			Host: u.Host, Confidence: inferredConfidence(u.Confidence), Entry: u.Entry, Catalog: u.Catalog, ObservedAt: copyTime(u.ObservedAt)}
		k := inferredKey{svc, key}
		rel := edges[k]
		if rel == nil {
			rel = &Relationship{FromService: svc, ToExternal: key, Type: RelationshipDependency, Provenance: ProvenanceInferred}
			edges[k] = rel
		}
		rel.InferredSources = append(rel.InferredSources, src)
		rec := externalRecord(snap, cat, key)
		rec.declarations[svc]++
		rec.Sources = append(rec.Sources, ExternalSource{Kind: ExternalSourcePodSpec, Service: svc, Target: src.Target,
			Provenance: ProvenanceInferred, Match: inferredMatch(u)})
	}
	for _, k := range slices.SortedFunc(maps.Keys(edges), func(a, b inferredKey) int {
		return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.ext, b.ext))
	}) {
		rel := edges[k]
		sortInferredSources(rel.InferredSources)
		rel.Confidence = strongestConfidence(rel.InferredSources)
		snap.Relationships = append(snap.Relationships, *rel)
	}
	externalUseLimitations(snap, unattributed, mismatch)
}

// externalUseLimitations adds the limitations of the uses foldExternalUses
// dropped or could not classify alike. No message echoes a use's Where.
func externalUseLimitations(snap *FleetSnapshot, unattributed map[string]int, mismatch map[[2]string]bool) {
	for _, src := range slices.Sorted(maps.Keys(unattributed)) {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationExternalUseUnattributed, Source: src,
			Message: fmt.Sprintf("%d external uses source %s reported name no service in this snapshot or no valid external identity; they were dropped", unattributed[src], src)})
	}
	var digests []string
	for _, c := range snap.ExternalCatalogs {
		digests = append(digests, c.Digest)
	}
	for _, m := range slices.SortedFunc(maps.Keys(mismatch), func(a, b [2]string) int {
		return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1]))
	}) {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationExternalCatalogMismatch, Source: m[0],
			Message: fmt.Sprintf("source %s classified external uses with catalog %s; this snapshot classifies with %s", m[0], m[1], strings.Join(digests, ", "))})
	}
}

// snapshotCatalog reports whether digest is one of the catalogs the
// snapshot classifies with.
func snapshotCatalog(snap *FleetSnapshot, digest string) bool {
	return slices.ContainsFunc(snap.ExternalCatalogs, func(c ExternalCatalogRef) bool { return c.Digest == digest })
}

// targetsByScope maps each (source, service, namespace) to the lowest key of
// the Kubernetes targets of that service there that the source reported, and
// ("", service, namespace) to the lowest key from any source: they share one
// visibility. Keying by source keeps a use on its own cluster's target when
// two clusters run the service in a namespace of the same name.
func targetsByScope(snap *FleetSnapshot) map[[3]string]TargetKey {
	out := map[[3]string]TargetKey{}
	put := func(at [3]string, k TargetKey) {
		if cur, ok := out[at]; !ok || k < cur {
			out[at] = k
		}
	}
	for k, t := range snap.Targets {
		if !kubeTarget(t) {
			continue
		}
		put([3]string{"", string(t.ServiceKey), t.Scope}, k)
		for _, src := range append([]string{t.Source}, t.Sources...) {
			put([3]string{src, string(t.ServiceKey), t.Scope}, k)
		}
	}
	return out
}

// targetOfUse is the target a use's source reported for its consumer in its
// namespace, else any source's.
func targetOfUse(targets map[[3]string]TargetKey, source string, svc ServiceKey, ns string) TargetKey {
	if k, ok := targets[[3]string{source, string(svc), ns}]; ok {
		return k
	}
	return targets[[3]string{"", string(svc), ns}]
}

// inferredConfidence is c when it is likely, else possible: a confidence a
// source does not name never reads stronger than the weakest.
func inferredConfidence(c string) string {
	if c == externalcatalog.ConfidenceLikely {
		return c
	}
	return externalcatalog.ConfidencePossible
}

// inferredMatch is how a use was joined to its identity: by its host, else
// by a key name only.
func inferredMatch(u RawExternalUse) string {
	if u.Host != "" {
		return externalcatalog.KindHost
	}
	return externalcatalog.KindKeyName
}

// strongestConfidence is likely when any source is, else possible.
func strongestConfidence(srcs []InferredSource) string {
	if slices.ContainsFunc(srcs, func(s InferredSource) bool { return s.Confidence == externalcatalog.ConfidenceLikely }) {
		return externalcatalog.ConfidenceLikely
	}
	return externalcatalog.ConfidencePossible
}

// sortInferredSources orders an inferred edge's evidence deterministically.
func sortInferredSources(srcs []InferredSource) {
	slices.SortFunc(srcs, func(a, b InferredSource) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Target, b.Target), cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Where, b.Where), cmp.Compare(a.Host, b.Host))
	})
}

// restrictInferredEvidence drops, from out's inferred edges, the evidence
// read on a target out hides, and an edge left with none: restrictExternals
// counts its consumer. out's targets and relationships must be restricted.
func restrictInferredEvidence(out *FleetSnapshot) {
	for i := range out.Relationships {
		rel := &out.Relationships[i]
		if rel.Provenance != ProvenanceInferred {
			continue
		}
		rel.InferredSources = slices.DeleteFunc(slices.Clone(rel.InferredSources), func(s InferredSource) bool {
			return s.Target != "" && out.Targets[s.Target] == nil
		})
		rel.Confidence = strongestConfidence(rel.InferredSources)
	}
	out.Relationships = slices.DeleteFunc(out.Relationships, func(rel Relationship) bool {
		return rel.Provenance == ProvenanceInferred && len(rel.InferredSources) == 0
	})
}
