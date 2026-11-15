package fleet

import (
	"cmp"
	"maps"
	"slices"
	"time"
)

// OfferingAdapter is one program that reports provisioning for an offering's
// requests, as the reports name it: the adapter slug it chose, the producer
// that sent the reports and whether they were signed. It is observed: the
// slug is free text, never an identity, and the provider of the offering is
// the service that declares it.
type OfferingAdapter struct {
	Provider       string     `json:"provider" doc:"The adapter slug the reports carry; free text the adapter chooses. Empty when the reports name none: surfaces render it as unnamed adapter"`
	Producer       string     `json:"producer,omitempty" doc:"The producer that sent the reports"`
	Verified       bool       `json:"verified" doc:"True when the reports were accepted through signed envelopes; false for the unsigned target-state file"`
	Environments   []string   `json:"environments" doc:"The environments its reports cover; default when a request names none"`
	Phases         PhaseTally `json:"phases" doc:"The effective phase of each request it reports on, as the badges show it (Unknown where reporters disagree)"`
	Disagrees      int        `json:"disagrees" doc:"Requests where its report disagrees with another adapter's, so the status is Unknown"`
	LastReportedAt *time.Time `json:"lastReportedAt,omitempty" doc:"When its newest report was observed"`
	Provenance     string     `json:"provenance" enum:"observed" doc:"Adapters are observed from the reports"`
}

// OfferingAdaptersPreview is a bounded preview of an offering's adapters.
// Conflicting counts the requests whose current reporters disagree (they are
// listed, with Disagrees); Unattributed counts the requests nobody reported
// on. Both are tallied over every use, never the preview.
type OfferingAdaptersPreview struct {
	Total        int               `json:"total" doc:"Every adapter that reports on the offering's requests"`
	Count        int               `json:"count" doc:"The adapters listed in items"`
	Truncated    bool              `json:"truncated" doc:"True when items lists fewer than total"`
	Items        []OfferingAdapter `json:"items" doc:"Signed adapters first, then by slug (an empty slug last) and producer"`
	Conflicting  int               `json:"conflicting" doc:"Requests whose adapters disagree, so their status is Unknown; their adapters are listed"`
	Unattributed int               `json:"unattributed" doc:"Requests no adapter reports on; their status is Unknown"`
}

// MaxOfferingAdapters bounds OfferingAdaptersPreview.Items.
const MaxOfferingAdapters = 10

// adapterKey is what groups reporters into one adapter.
type adapterKey struct {
	provider, producer string
	verified           bool
}

// offeringAdapters groups the reporters behind every provisioning status of
// uses into adapters. It reads the statuses the badges show, so the two can
// never disagree, and a restricted view's uses hold only visible consumers.
func offeringAdapters(uses []OfferingUse) OfferingAdaptersPreview {
	var out OfferingAdaptersPreview
	byKey := map[adapterKey]*OfferingAdapter{}
	envs := map[adapterKey]map[string]bool{}
	for _, u := range uses {
		for _, st := range u.Provisioning {
			switch {
			case len(st.Conflict) > 0:
				out.Conflicting++
			case len(st.reporters) == 0:
				out.Unattributed++
			}
			seen := map[adapterKey]bool{}
			for _, r := range st.reporters {
				k := adapterKey{r.Provider, r.Producer, r.Verified}
				a := byKey[k]
				if a == nil {
					a = &OfferingAdapter{Provider: r.Provider, Producer: r.Producer, Verified: r.Verified, Provenance: ProvenanceObserved}
					byKey[k], envs[k] = a, map[string]bool{}
				}
				if a.LastReportedAt == nil || a.LastReportedAt.Before(r.ObservedAt) {
					a.LastReportedAt = new(r.ObservedAt)
				}
				if !seen[k] {
					seen[k] = true
					envs[k][cmp.Or(st.Environment, "default")] = true
					a.countSlot(st)
				}
			}
		}
	}
	items := make([]OfferingAdapter, 0, len(byKey))
	for k, a := range byKey {
		a.Environments = slices.Sorted(maps.Keys(envs[k]))
		items = append(items, *a)
	}
	slices.SortFunc(items, compareAdapters)
	out.Items, out.Total, out.Truncated = boundSlice(items, MaxOfferingAdapters)
	out.Count = len(out.Items)
	return out
}

// countSlot adds one status it reports on: its effective phase, Ready apart
// when this adapter's reports are unsigned, and a disagreement.
func (a *OfferingAdapter) countSlot(st ProvisioningStatus) {
	a.Phases.addStatus(ProvisioningStatus{Phase: st.Phase, Verified: a.Verified})
	if len(st.Conflict) > 0 {
		a.Disagrees++
	}
}

// compareAdapters orders signed adapters first, then named slugs before an
// empty one, then slug, then producer.
func compareAdapters(a, b OfferingAdapter) int {
	return cmp.Or(compareBool(b.Verified, a.Verified), compareBool(a.Provider == "", b.Provider == ""),
		cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Producer, b.Producer))
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	default:
		return -1
	}
}
