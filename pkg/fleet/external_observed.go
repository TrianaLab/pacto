package fleet

import (
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/externalid"
)

// ObservedExternal names the external system a raw observed callee names, by
// the catalog the snapshot was built with: an OpenTelemetry system value the
// catalog claims, else a host it classifies (see [externalcatalog.Catalog.MatchHost]).
// A single label, a two-label name with no catalog rule and a name outside the
// ICANN suffixes name nothing: calling them external would assert a fact
// Pacto lacks. A caller checks first that the name is no fleet service.
func (s *FleetSnapshot) ObservedExternal(name string) (externalid.ID, bool) {
	m, ok := s.observedExternalMatch(name, ObservedUnknown)
	return m.Identity, ok
}

// observedExternalMatch is [FleetSnapshot.ObservedExternal] for a callee that
// resolved as res: only a name no fleet service takes can name an external.
func (s *FleetSnapshot) observedExternalMatch(name string, res ObservedResolution) (externalcatalog.Match, bool) {
	if res != ObservedUnknown {
		return externalcatalog.Match{}, false
	}
	cat := s.externalCatalog()
	if m, ok := cat.MatchOTel(name); ok {
		return m, true
	}
	for _, h := range externalcatalog.NormalizeHost(name) {
		if m, ok := cat.MatchHost(h); ok && !s.namespaceShortForm(m) {
			return m, true
		}
	}
	return externalcatalog.Match{}, false
}

// namespaceShortForm reports whether m is an unclassified host whose last
// label is the namespace of a Kubernetes target in the snapshot: the
// in-cluster short form <pod>.<service>.<namespace>, never an external.
// ponytail: only namespaces a target runs in are known; a namespace with no
// target still reads as a domain when it is also a top-level domain.
func (s *FleetSnapshot) namespaceShortForm(m externalcatalog.Match) bool {
	return externalcatalog.NamespaceShortForm(m, func(ns string) bool {
		for _, t := range s.Targets {
			if kubeTarget(t) && t.Scope == ns {
				return true
			}
		}
		return false
	})
}

// recordObservedExternal records, on the record of key, that caller was seen
// calling it, matched by match (host or system). One source per caller: the
// relationship carries the per-source counts.
func recordObservedExternal(snap *FleetSnapshot, caller ServiceKey, key ExternalKey, match string) {
	rec := externalRecord(snap, snap.externalCatalog(), key, string(key))
	rec.declarations[caller]++
	rec.Sources = append(rec.Sources, ExternalSource{Kind: ExternalSourceObserved, Service: caller, Provenance: ProvenanceObserved, Match: match})
}

// externalCatalog is the catalog the snapshot was built with, else the
// embedded default (a snapshot read back from JSON carries none).
func (s *FleetSnapshot) externalCatalog() *externalcatalog.Catalog {
	if s.catalog == nil {
		return externalcatalog.Default()
	}
	return s.catalog
}
