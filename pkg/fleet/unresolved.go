package fleet

import (
	"hash/fnv"
	"path"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// hiddenUnresolvedRef is a declared, unresolved reference of a service a
// restricted view hides. from is an FNV-64a hash of the service key: it
// dedupes the service's revisions without naming it.
type hiddenUnresolvedRef struct {
	from         uint64
	Type         string
	RequestedRef string
	NameFromRef  bool
}

// unresolvedDeclared reports whether rel is a declared dependency,
// configuration or policy reference that did not resolve and is expected to:
// an external dependency is not delivered here, so it never resolves.
func unresolvedDeclared(rel Relationship) bool {
	switch rel.Type {
	case RelationshipDependency, RelationshipConfigRef, RelationshipPolicyRef:
		return rel.Provenance == ProvenanceDeclared && !rel.Resolved && !rel.External
	}
	return false
}

// appendHiddenUnresolved appends rel, an edge from a hidden service, to hidden
// when it is an unresolved declared reference, keeping neither its service
// nor its revision.
func appendHiddenUnresolved(hidden []hiddenUnresolvedRef, rel Relationship) []hiddenUnresolvedRef {
	if !unresolvedDeclared(rel) {
		return hidden
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(rel.FromService))
	return append(hidden, hiddenUnresolvedRef{from: h.Sum64(), Type: rel.Type, RequestedRef: rel.RequestedRef, NameFromRef: rel.NameFromRef})
}

// UnresolvedRefCount counts the declared references of relTypes the snapshot
// could not resolve that may name one of providers: by the repository of one
// of its revisions, or, as dependencies resolve, by a last path segment that
// is its name (with or without -pacto), which also covers a fleet read from
// directories and a relative ref. A consumer counts once per type and
// requested repository whatever its revision count, and a reference that may
// name several providers counts once. The references of services a restricted
// view hides are counted, never named. A dependency counts only when its name
// was derived from its ref: one that declares its name resolves by that name.
func UnresolvedRefCount(snap *FleetSnapshot, providers []ServiceKey, relTypes ...string) int {
	return countUnresolved(snap, providerMatchOf(snap, providers), relTypes)
}

// providerMatch is what an unresolved reference may name a provider by: the
// repositories its revisions came from and its name.
type providerMatch struct{ repos, names map[string]bool }

// providerMatchOf is the providerMatch of providers in snap.
func providerMatchOf(snap *FleetSnapshot, providers []ServiceKey) providerMatch {
	m, want := providerMatch{repos: map[string]bool{}, names: map[string]bool{}}, map[ServiceKey]bool{}
	for _, p := range providers {
		_, name := ParseServiceKey(p)
		m.names[name], want[p] = true, true
	}
	for _, rev := range snap.Revisions {
		if !want[rev.ServiceKey] {
			continue
		}
		for _, ref := range []string{rev.RequestedRef, rev.ResolvedRef} {
			if r := repository(ref); r != "" {
				m.repos[r] = true
			}
		}
	}
	return m
}

// countUnresolved counts the unresolved declared references of relTypes that
// match m (see [UnresolvedRefCount]).
func countUnresolved(snap *FleetSnapshot, m providerMatch, relTypes []string) int {
	repos, names := m.repos, m.names
	type use struct {
		visible     ServiceKey
		hidden      uint64
		typ, toRepo string
	}
	seen := map[use]bool{}
	note := func(u use, ref string, nameFromRef bool) {
		if u.typ == RelationshipDependency && !nameFromRef {
			return
		}
		u.toRepo = repository(ref)
		last := strings.TrimSuffix(path.Base(strings.TrimRight(u.toRepo, "/")), "-pacto")
		if slices.Contains(relTypes, u.typ) && (repos[u.toRepo] || names[last]) {
			seen[u] = true
		}
	}
	for _, r := range snap.Relationships {
		if unresolvedDeclared(r) {
			note(use{visible: r.FromService, typ: r.Type}, r.RequestedRef, r.NameFromRef)
		}
	}
	for _, h := range snap.hiddenUnresolvedRefs {
		note(use{hidden: h.from, typ: h.Type}, h.RequestedRef, h.NameFromRef)
	}
	return len(seen)
}

// repository is the repository a ref names, without scheme, tag or digest.
func repository(ref string) string {
	repo, _, _ := contract.SplitOCIRef(contract.TrimOCIScheme(ref))
	return repo
}
