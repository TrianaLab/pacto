package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
)

// Provider lifecycle values on an offering row and the Lifecycle filter (see
// [EntityRef.Lifecycle] and [EntityFilter.Lifecycle]).
const (
	LifecycleSupported  = "supported"
	LifecycleDeprecated = "deprecated"
	LifecycleSunset     = "sunset"
	LifecycleUnknown    = "unknown"
)

// lifecycleRank orders lifecycles from best to worst: an unknown provider
// may be deprecated, so it outranks a supported one.
var lifecycleRank = map[string]int{LifecycleSupported: 0, LifecycleUnknown: 1, LifecycleDeprecated: 2, LifecycleSunset: 3}

func validLifecycleFilter(s string) bool { return s == LifecycleDeprecated || s == LifecycleSunset }

// offeringUsesInCalls counts offeringUsesIn calls, so a test can pin that
// an offering list reads the uses once.
var offeringUsesInCalls atomic.Int64

// offeringFacts is what one offering row says beyond its providers and
// targets, gathered over every visible provider of the offering.
type offeringFacts struct {
	owners                    ownerClaimSet
	lifecycle                 string
	consumers, failed, hidden map[ServiceKey]bool
	targetsUnknown            bool
	providers                 []*ServiceRecord
	uses                      []OfferingUse
}

// offeringFactsByName gathers the facts of every offering row in one pass over
// the services and one pass over the uses, so a list stays linear in the
// relationships.
func (q *Query) offeringFactsByName() map[string]*offeringFacts {
	by := map[string]*offeringFacts{}
	placementKnown := targetPlacementKnown(q.snap)
	for _, s := range q.snap.Services {
		if s.Offering == "" {
			continue
		}
		f := by[s.Offering]
		if f == nil {
			f = &offeringFacts{lifecycle: LifecycleSupported, consumers: map[ServiceKey]bool{}, failed: map[ServiceKey]bool{}, hidden: map[ServiceKey]bool{}}
			by[s.Offering] = f
		}
		f.providers = append(f.providers, s)
		f.owners.merge(q.ownerClaimsOf(s))
		f.targetsUnknown = f.targetsUnknown || !placementKnown || q.snap.targetsHidden[s.Key]
		maps.Copy(f.hidden, q.snap.hiddenConsumers[s.Key])
	}
	uses, _ := q.offeringUses(func(p *ServiceRecord) bool { return p.Offering != "" }, "")
	for _, u := range uses {
		f := by[u.Offering]
		f.uses = append(f.uses, u)
		key := ServiceKey(u.Consumer.Key)
		f.consumers[key] = true
		for _, st := range u.Provisioning {
			if st.Phase == string(evidence.PhaseFailed) {
				f.failed[key] = true
			}
		}
	}
	for name, f := range by {
		for _, p := range f.providers {
			f.lifecycle = worstLifecycle(f.lifecycle, revisionLifecycle(q.providerCurrentRevision(p), q.snap.GeneratedAt))
		}
		if q.snap.hiddenProviders[name] > 0 {
			f.lifecycle = worstLifecycle(f.lifecycle, LifecycleUnknown) // a hidden provider may be deprecated
		}
	}
	return by
}

// providerCurrentRevision is an offering provider's current revision, the one
// its lifecycle, successor and current content are read from: the newest
// revision a target runs, else the newest readable one, as a service page's
// current revision is. So a successor shipped under the same service never
// hides the deprecation of the version still running. Nil when there is none.
func (q *Query) providerCurrentRevision(s *ServiceRecord) *ContractRevision {
	var running []*ContractRevision
	for _, tk := range s.Targets {
		if r := q.snap.Revisions[q.snap.Targets[tk].ContractRevision]; r != nil {
			running = append(running, r)
		}
	}
	if len(running) > 0 {
		return newestInUse(running, nil)
	}
	return newestReadable(q.revisionsOf(s))
}

// applyOfferingFacts fills ref, an offering row, from f.
func (q *Query) applyOfferingFacts(ref *EntityRef, f *offeringFacts, x *availIndex) {
	ref.Owner = ownerLabel(f.owners.len(), f.owners.soleKey())
	ref.Lifecycle = f.lifecycle
	ref.HiddenProviders = q.snap.hiddenProviders[ref.Key]
	ref.Consumers, ref.FailedRequests, ref.HiddenConsumers = len(f.consumers), len(f.failed), len(f.hidden)
	if f.targetsUnknown {
		ref.Targets, ref.TargetsUnknown = 0, true
	}
	av := x.offeringAvailability(ref.Key, f.providers)
	ref.Availability, ref.AvailabilityProvenance = av.State, rowProvenance(av)
}

// rowProvenance is how Pacto knows av's roll-up: unknown for an unknown
// state, else the provenance of the worst environment holding a known state,
// the one that sets the row apart, so a row never shows a state without saying
// how Pacto knows it.
func rowProvenance(av Availability) string {
	worst := EnvironmentAvailability{State: AvailabilityUnknown, Provenance: provenanceUnknown}
	if av.State == AvailabilityUnknown {
		return worst.Provenance
	}
	for _, e := range av.ByEnvironment {
		if e.State != AvailabilityUnknown && (worst.State == AvailabilityUnknown || availabilityRank[e.State] > availabilityRank[worst.State]) {
			worst = e
		}
	}
	return worst.Provenance
}

// merge adds every claim of o to c.
func (c *ownerClaimSet) merge(o ownerClaimSet) {
	for _, k := range o.keys {
		if !containsStr(c.keys, k) {
			c.keys = append(c.keys, k)
		}
	}
	for _, ow := range o.keyless {
		c.add(ow)
	}
}

// ownersClaimLabel is the owner label of a row whose revisions name n owners.
func ownersClaimLabel(n int) string { return fmt.Sprintf("%d owners claim it", n) }

// ownerByContactsLabel is the owner label of a row whose one owner names no
// team or DRI, only contacts.
const ownerByContactsLabel = "declared by contacts only"

// ownerLabel is a list row's owner label from its n owner claims and the key
// of the only one: empty only when none is declared.
func ownerLabel(n int, sole string) string {
	switch {
	case n == 1 && sole != "":
		return ownerEntityRef(sole).Label
	case n == 1:
		return ownerByContactsLabel
	case n > 1:
		return ownersClaimLabel(n)
	}
	return ""
}

// worstLifecycle is the worse of a and b: sunset, deprecated, unknown, then
// supported.
func worstLifecycle(a, b string) string {
	if lifecycleRank[b] > lifecycleRank[a] {
		return b
	}
	return a
}

// revisionLifecycle is the lifecycle r's contract declares for its service
// at now: unknown for no readable contract, supported when it declares none.
func revisionLifecycle(r *ContractRevision, now time.Time) string {
	if r == nil || r.Contract == nil {
		return LifecycleUnknown
	}
	switch l := r.Contract.Service.Lifecycle; {
	case l == nil:
		return LifecycleSupported
	case l.SunsetReached(now):
		return LifecycleSunset
	default:
		return LifecycleDeprecated
	}
}

// serviceLifecycleState is the lifecycle of s's newest revision, unknown for
// a service no readable revision describes (observed only) or whose newest
// revision is unreadable: an older revision cannot speak for it.
func (q *Query) serviceLifecycleState(s *ServiceRecord) string {
	var newest *ContractRevision
	for _, r := range q.revisionsOf(s) {
		if newest == nil || lessRevisionChrono(newest, r) {
			newest = r
		}
	}
	if newest == nil || !readableRevision(newest) {
		return LifecycleUnknown
	}
	return revisionLifecycle(newest, q.snap.GeneratedAt)
}

// revisionsOf is every revision of s in the snapshot.
func (q *Query) revisionsOf(s *ServiceRecord) []*ContractRevision {
	revs := make([]*ContractRevision, 0, len(s.Revisions))
	for _, rk := range s.Revisions {
		if r := q.snap.Revisions[rk]; r != nil {
			revs = append(revs, r)
		}
	}
	return revs
}

// maxFailedOfferings bounds EntityAggregate.FailedByOffering.
const maxFailedOfferings = 3

// tallyOfferingPhases adds the provisioning phases of the uses of every
// offering in names to agg, from the uses its rows read, so the fleet figure
// and the rows agree.
func tallyOfferingPhases(agg *EntityAggregate, names []string, facts map[string]*offeringFacts) {
	var failed []TypeCount
	for _, name := range names {
		p := offeringUsesPreview(facts[name].uses)
		agg.OfferingPhases.plus(p.Phases)
		agg.OfferingPhaseConsumers.plus(p.PhaseConsumers)
		if p.Failed.Total > 0 {
			failed = append(failed, TypeCount{Type: name, Services: p.Failed.Total})
		}
	}
	slices.SortFunc(failed, func(a, b TypeCount) int {
		return cmp.Or(cmp.Compare(b.Services, a.Services), cmp.Compare(a.Type, b.Type))
	})
	shown, total, _ := boundSlice(failed, maxFailedOfferings)
	agg.FailedByOffering, agg.FailedOfferingsBeyond = shown, total-len(shown)
}

// plus adds o to t, field by field.
func (t *PhaseTally) plus(o PhaseTally) {
	t.Requested += o.Requested
	t.Provisioning += o.Provisioning
	t.Ready += o.Ready
	t.Failed += o.Failed
	t.Deleting += o.Deleting
	t.Unknown += o.Unknown
	t.ReadyUnsigned += o.ReadyUnsigned
}
