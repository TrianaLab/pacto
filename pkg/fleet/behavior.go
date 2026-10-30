package fleet

import (
	"slices"
	"sort"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Guarantee statuses of a declared dependency (see [DependencyBehavior]).
const (
	GuaranteeMet     = "met"
	GuaranteeUnmet   = "unmet"
	GuaranteeUnknown = "unknown"
)

// DependencyBehavior is what a declared dependency requires of its provider's
// interfaces (dependencies[].requires, pactoVersion 2.2) and what the fleet
// found when it checked the provider's contract with [contract.Unmet].
//
// GuaranteeStatus is empty when the dependency requires nothing. Otherwise it
// is met when every provider revision the dependency reaches meets every
// requirement, unmet when none of them does (Unmet lists what the newest one
// refuses) and unknown when the fleet cannot tell: the provider is not in the
// fleet, no revision is in range or the revisions disagree. GuaranteeReason
// says which. Unknown is a missing observation, never a refusal.
type DependencyBehavior struct {
	Requires        []contract.Requirement `json:"requires,omitempty"`
	GuaranteeStatus string                 `json:"guaranteeStatus,omitempty" enum:"met,unmet,unknown"`
	GuaranteeReason string                 `json:"guaranteeReason,omitempty"`
	Unmet           []contract.Mismatch    `json:"unmet,omitempty"`
}

// InterfaceDeprecation is one deprecated interface of a provider: its name and
// its retirement schedule (dates are YYYY-MM-DD).
type InterfaceDeprecation struct {
	Interface  string `json:"interface"`
	Deprecated string `json:"deprecated"`
	Sunset     string `json:"sunset,omitempty"`
	Successor  string `json:"successor,omitempty"`
}

// clone copies b so the copy shares no slice with b.
func (b DependencyBehavior) clone() DependencyBehavior {
	b.Requires = slices.Clone(b.Requires)
	b.Unmet = slices.Clone(b.Unmet)
	return b
}

// reachableRevisions lists the provider revisions a resolved dependency can
// reach, oldest first: the locked revision when the lock pins one, otherwise
// every revision of the provider whose version the declared range accepts
// (every revision when the range is absent).
func reachableRevisions(snap *FleetSnapshot, rel Relationship) []*ContractRevision {
	if rel.ResolvedRevision != "" {
		return []*ContractRevision{snap.Revisions[rel.ResolvedRevision]}
	}
	var out []*ContractRevision
	for _, rk := range snap.Services[rel.ToService].Revisions {
		rev := snap.Revisions[rk]
		if rel.Compatibility == "" || CheckRange(rel.Compatibility, rev.Version) == RangeSatisfied {
			out = append(out, rev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessRevisionChrono(out[i], out[j]) })
	return out
}

// dependencyBehavior checks what dep requires against every provider revision
// the dependency reaches. consumer names the declaring service in each
// mismatch.
func dependencyBehavior(snap *FleetSnapshot, consumer string, dep contract.Dependency, rel Relationship) DependencyBehavior {
	b := DependencyBehavior{Requires: slices.Clone(dep.Requires)}
	if len(dep.Requires) == 0 {
		return b
	}
	b.GuaranteeStatus = GuaranteeUnknown
	if !rel.Resolved {
		b.GuaranteeReason = "no contract of the provider in the fleet to check against"
		return b
	}
	revs := reachableRevisions(snap, rel)
	if len(revs) == 0 {
		b.GuaranteeReason = "no revision of the provider in the fleet is in the accepted range"
		return b
	}
	met := 0
	for _, rev := range revs {
		if m := contract.Unmet(consumer, dep, rev.Contract); len(m) > 0 {
			b.Unmet = m // oldest first, so the newest refusal is the one kept
		} else {
			met++
		}
	}
	switch met {
	case len(revs):
		b.GuaranteeStatus, b.Unmet = GuaranteeMet, nil
	case 0:
		b.GuaranteeStatus = GuaranteeUnmet
	default:
		b.GuaranteeReason, b.Unmet = "the provider revisions it can reach disagree: some meet what it requires and some do not", nil
	}
	return b
}

// providerDeprecations lists the deprecated interfaces of the newest provider
// revision a resolved dependency reaches. A contract does not say which of a
// provider's interfaces it calls, so every deprecated one is listed, as
// validation's DEPENDENCY_INTERFACE_DEPRECATED warning does.
func providerDeprecations(snap *FleetSnapshot, rel Relationship) []InterfaceDeprecation {
	revs := reachableRevisions(snap, rel)
	if len(revs) == 0 {
		return nil
	}
	var out []InterfaceDeprecation
	for _, iface := range revs[len(revs)-1].Contract.Interfaces {
		if l := iface.Lifecycle; l != nil {
			out = append(out, InterfaceDeprecation{Interface: iface.Name, Deprecated: l.Deprecated, Sunset: l.Sunset, Successor: l.Successor})
		}
	}
	return out
}

// guaranteeRank orders guarantee statuses by how much they matter to a reader.
var guaranteeRank = map[string]int{GuaranteeMet: 1, GuaranteeUnknown: 2, GuaranteeUnmet: 3}

// foldBehavior merges one declared relationship's behavior facts into an
// edge: the worst guarantee status of its claims and the union of the
// provider interfaces they found deprecated, by interface name.
func foldBehavior(e *NeighborhoodEdge, rel Relationship) {
	if guaranteeRank[rel.GuaranteeStatus] > guaranteeRank[e.GuaranteeStatus] {
		e.GuaranteeStatus = rel.GuaranteeStatus
	}
	for _, d := range rel.DeprecatedInterfaces {
		if !slices.ContainsFunc(e.DeprecatedInterfaces, func(x InterfaceDeprecation) bool { return x.Interface == d.Interface }) {
			e.DeprecatedInterfaces = append(e.DeprecatedInterfaces, d)
		}
	}
	sort.Slice(e.DeprecatedInterfaces, func(i, j int) bool { return e.DeprecatedInterfaces[i].Interface < e.DeprecatedInterfaces[j].Interface })
}

// declaredClaim is the claim a declared relationship contributes to an edge.
func declaredClaim(rel Relationship) DeclaredClaim {
	return DeclaredClaim{
		SourceRevision: rel.FromRevision, Required: rel.Required, Compatibility: rel.Compatibility,
		Reconciliation: rel.Reconciliation, RequestedRef: rel.RequestedRef,
		LockedVersion: rel.LockedVersion, LockedDigest: rel.LockedDigest,
		DependencyBehavior: rel.DependencyBehavior.clone(),
	}
}
