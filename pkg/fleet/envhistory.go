package fleet

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// EnvironmentEvent is one change to a service's declared environments
// between two consecutive revisions in version order. It carries no time:
// the fleet records no revision creation time.
type EnvironmentEvent struct {
	Environment string     `json:"environment"`
	Change      string     `json:"change" enum:"present,added,removed,changed" doc:"present: declared by the oldest revision this answer holds (declared since at least that revision)"`
	Fields      []string   `json:"fields,omitempty" doc:"changed: which declared fields changed (file, lifetime, expires, owner)"`
	Revision    EntityRef  `json:"revision" doc:"The revision that made the change"`
	Previous    *EntityRef `json:"previous,omitempty" doc:"The revision it was compared with"`
}

// EnvironmentHistoryPreview is how a service's declared environments changed
// across the revisions the answer holds.
type EnvironmentHistoryPreview struct {
	Total              int                `json:"total"`
	Count              int                `json:"count"`
	Truncated          bool               `json:"truncated"`
	Items              []EnvironmentEvent `json:"items" doc:"Newest first. Differences between consecutive versions in version order, not a timeline. Declared changes read from the revisions; value changes are pacto diff's"`
	Gaps               int                `json:"gaps" doc:"Revisions whose contract could not be read; no change is reported across them"`
	Completeness       string             `json:"completeness" enum:"complete,partial" doc:"partial when this view or a source holds only some of the service's revisions"`
	CompletenessReason string             `json:"completenessReason,omitempty"`
}

// historyPartialReason never names what the view dropped.
const historyPartialReason = "this view or a source holds only some of the service's revisions"

// serviceEnvironmentHistory is the environment history of s's revisions in
// q, partial when the view dropped some of them or a source is not fully
// available.
func (q *Query) serviceEnvironmentHistory(s *ServiceRecord) EnvironmentHistoryPreview {
	partial := q.snap.revisionsHidden[s.Key] || slices.ContainsFunc(q.snap.Sources, func(src SourceState) bool { return src.Status != SourceAvailable })
	return q.environmentHistory(serviceRevisions(q.snap, s), partial)
}

// environmentHistory compares the environments each revision of revs
// declares with the previous readable one, in version order, reading each
// contract from the snapshot record. The oldest readable revision reads
// present, never added: older revisions may not be in the answer. An unread
// revision counts in Gaps and breaks the comparison across it.
func (q *Query) environmentHistory(revs []*ContractRevision, partial bool) EnvironmentHistoryPreview {
	recs := make([]*ContractRevision, 0, len(revs))
	for _, r := range revs {
		recs = append(recs, q.snap.Revisions[r.Key])
	}
	slices.SortFunc(recs, func(a, b *ContractRevision) int {
		if lessRevisionChrono(a, b) {
			return -1
		}
		return 1
	})
	var groups [][]EnvironmentEvent
	h := EnvironmentHistoryPreview{Completeness: string(CompletenessComplete)}
	var prev *ContractRevision
	first := true
	for _, r := range recs {
		if r.Contract == nil {
			h.Gaps++
			prev = nil
			continue
		}
		if prev != nil || first {
			groups = append(groups, revisionEnvironmentEvents(prev, r))
		}
		prev, first = r, false
	}
	var items []EnvironmentEvent
	for i := len(groups) - 1; i >= 0; i-- {
		items = append(items, groups[i]...)
	}
	h.Items, h.Total, h.Truncated = boundSlice(items, MaxDetailPreview)
	h.Count = len(h.Items)
	if partial {
		h.Completeness, h.CompletenessReason = string(CompletenessPartial), historyPartialReason
	}
	return h
}

// revisionEnvironmentEvents is what r changed in the environments prev
// declares, sorted by environment; every environment reads present when there
// is no prev.
func revisionEnvironmentEvents(prev, r *ContractRevision) []EnvironmentEvent {
	ref := revisionEntityRef(r)
	var prevRef *EntityRef
	before := map[string]contract.Environment{}
	if prev != nil {
		p := revisionEntityRef(prev)
		prevRef = &p
		for _, e := range prev.Contract.Environments {
			before[e.Name] = e
		}
	}
	var out []EnvironmentEvent
	event := func(name, change string, fields []string) {
		out = append(out, EnvironmentEvent{Environment: name, Change: change, Fields: fields, Revision: ref, Previous: prevRef})
	}
	for _, e := range r.Contract.Environments {
		old, had := before[e.Name]
		delete(before, e.Name)
		switch {
		case prev == nil:
			event(e.Name, "present", nil)
		case !had:
			event(e.Name, "added", nil)
		default:
			if f := changedEnvironmentFields(old, e); len(f) > 0 {
				event(e.Name, "changed", f)
			}
		}
	}
	for name := range before {
		event(name, "removed", nil)
	}
	slices.SortFunc(out, func(a, b EnvironmentEvent) int { return NaturalCompare(a.Environment, b.Environment) })
	return out
}

// changedEnvironmentFields is the declared fields that differ between o and
// n, compared as pkg/diff compares them.
func changedEnvironmentFields(o, n contract.Environment) []string {
	var out []string
	for _, f := range [][3]string{{"file", o.File, n.File}, {"lifetime", o.Lifetime, n.Lifetime}, {"expires", o.Expires, n.Expires}} {
		if f[1] != f[2] {
			out = append(out, f[0])
		}
	}
	if !reflect.DeepEqual(o.Owner, n.Owner) {
		out = append(out, "owner")
	}
	return out
}

// Heading is the line the CLI and TUI print above [EnvironmentHistoryPreview.Lines].
func (h EnvironmentHistoryPreview) Heading() string {
	s := fmt.Sprintf("Environment history, version order (%d of %d", h.Count, h.Total)
	if h.Gaps > 0 {
		s += fmt.Sprintf(", %d revision(s) unread", h.Gaps)
	}
	if h.CompletenessReason != "" {
		s += ", partial: " + h.CompletenessReason
	}
	return s + ")"
}

// Lines is one line per event: the revision, the change and the environment.
// No event has a time, and every line says so.
func (h EnvironmentHistoryPreview) Lines() []string {
	out := make([]string, 0, len(h.Items))
	for _, e := range h.Items {
		what := e.Change + " " + e.Environment
		switch {
		case e.Change == "present":
			what = e.Environment + " declared since at least " + e.Revision.Label
		case len(e.Fields) > 0:
			what += " (" + strings.Join(e.Fields, ", ") + ")"
		}
		out = append(out, "  "+e.Revision.Label+"  "+what+"  time not recorded")
	}
	return out
}
