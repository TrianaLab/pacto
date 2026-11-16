package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
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
	Phases         PhaseTally `json:"phases" doc:"The phase of each request it reports on, as the badge shows it when its own current, observed report says that phase; otherwise Unknown (reporters disagree, its report failed or is not current)"`
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

// NoAdapterVisible is the empty adapters line of a view that hides consumers.
const NoAdapterVisible = "No adapter has reported on the requests you can see. Provisioning status is Unknown."

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
					a.countSlot(st, r)
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

// countSlot adds one status r, this adapter's report, speaks for: the
// effective phase only when r is a current Observed report of that phase,
// otherwise Unknown, so no adapter is credited a phase it did not report (Ready
// apart when its reports are unsigned); and a disagreement only when r's own
// phase is one of the conflicting ones.
func (a *OfferingAdapter) countSlot(st ProvisioningStatus, r provisioningReporter) {
	observed := r.Current && r.Outcome == string(evidence.Observed)
	phase := StatusUnknown
	if observed && len(st.Conflict) == 0 && r.Phase == st.Phase {
		phase = st.Phase
	}
	a.Phases.addStatus(ProvisioningStatus{Phase: phase, Verified: a.Verified})
	if observed && slices.Contains(st.Conflict, r.Phase) {
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

// DisplayName is the adapter's slug, or "unnamed adapter" when its reports
// name none, so no surface prints an empty name.
func (a OfferingAdapter) DisplayName() string {
	return cmp.Or(a.Provider, "unnamed adapter")
}

// Line renders one adapter: slug, producer, signature, environments, phases,
// disagreements and the last report time.
func (a OfferingAdapter) Line() string {
	s := a.DisplayName()
	if a.Producer != "" {
		s += " by " + a.Producer
	}
	if a.Verified {
		s += ", signed"
	} else {
		s += ", unsigned target-state file"
	}
	parts, _ := a.Phases.parts()
	s += ": " + strings.Join(a.Environments, ", ") + "; " + strings.Join(parts, ", ")
	if a.Disagrees > 0 {
		s += fmt.Sprintf(", disagrees on %d", a.Disagrees)
	}
	if a.LastReportedAt != nil {
		s += "; last reported " + a.LastReportedAt.UTC().Format(time.RFC3339)
	}
	return s
}

// Lines renders the preview: one line per adapter, what the bound left out,
// the requests whose adapters disagree and the requests nobody reports on.
// With no adapter it says the status is Unknown, never that there is none, and
// with hiddenConsumers above 0 it speaks only for the requests the viewer sees.
func (p OfferingAdaptersPreview) Lines(hiddenConsumers int) []string {
	lines := make([]string, 0, len(p.Items))
	for _, a := range p.Items {
		lines = append(lines, a.Line())
	}
	if p.Truncated {
		lines = append(lines, fmt.Sprintf("%d more", p.Total-p.Count))
	}
	switch {
	case len(p.Items) > 0:
	case hiddenConsumers > 0:
		lines = append(lines, NoAdapterVisible)
	default:
		lines = append(lines, "No adapter has reported. Provisioning status is Unknown.")
	}
	if p.Conflicting > 0 {
		lines = append(lines, fmt.Sprintf("%d requests where adapters disagree, so their status is Unknown", p.Conflicting))
	}
	if p.Unattributed > 0 {
		lines = append(lines, fmt.Sprintf("%d requests no adapter reports on", p.Unattributed))
	}
	return lines
}
