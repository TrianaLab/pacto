package fleet

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
)

// ChangeEvent is one declared change to a service between two consecutive
// revisions this answer holds, in version order. It carries no time: the
// fleet records no revision creation time.
type ChangeEvent struct {
	Kind       string     `json:"kind" enum:"owner,domain,offering,dependency,lifecycle,environment,finding" doc:"What changed: the owner, the business domain, the offering, one dependency, the lifecycle, one environment or a validation finding"`
	Subject    string     `json:"subject,omitempty" doc:"The dependency, environment or finding the change is about; empty for the owner, domain, offering and lifecycle"`
	Change     string     `json:"change" enum:"present,added,removed,changed" doc:"present: declared by the oldest revision this answer holds (declared since at least that revision)"`
	Fields     []string   `json:"fields,omitempty" doc:"changed: which declared fields changed"`
	From       string     `json:"from,omitempty" doc:"The value before the change, as the previous revision declares it"`
	To         string     `json:"to,omitempty" doc:"The value after the change, as the revision declares it"`
	Revision   EntityRef  `json:"revision" doc:"The first revision this answer holds that shows the change; versions between it and previous may not be held"`
	Previous   *EntityRef `json:"previous,omitempty" doc:"The revision it was compared with: the change happened after it, at or before revision"`
	Provenance string     `json:"provenance" enum:"declared" doc:"declared: read from immutable contract revisions"`
	Source     string     `json:"source" enum:"contract,validation" doc:"contract: an edit the contract declares; validation: a result of Pacto's own check of the revision"`
}

// ChangeHistoryPreview is every declared change across the revisions of a
// service the answer holds.
type ChangeHistoryPreview struct {
	Total              int           `json:"total"`
	Count              int           `json:"count"`
	Truncated          bool          `json:"truncated"`
	Items              []ChangeEvent `json:"items" doc:"Newest first, in version order, not a timeline"`
	Gaps               int           `json:"gaps" doc:"Revisions whose contract could not be read; no change is reported across them"`
	Completeness       string        `json:"completeness" enum:"complete,partial" doc:"partial when this view or a source holds only some of the service's revisions, or when two versions are compared: no source lists every version, so a change is placed between the versions held"`
	CompletenessReason string        `json:"completenessReason,omitempty"`
}

// Change kinds, in the order a revision's changes are listed.
var changeKindOrder = []string{"owner", "domain", "offering", "dependency", "lifecycle", "environment", "finding"}

// changeEnvironmentFields is the declared environment fields a change names,
// in pkg/diff's order; value paths inside an environment's file are pacto
// diff's.
var changeEnvironmentFields = []string{"file", "lifetime", "expires", "owner"}

// walkRevisionPairs sorts recs in version order and calls visit with the
// oldest readable revision (prev nil) and with every readable revision whose
// previous one is readable. An unread revision counts as a gap and breaks the
// comparison across it: a revision after a gap is compared with nothing and
// only becomes the previous of the next one.
func walkRevisionPairs(recs []*ContractRevision, visit func(prev, r *ContractRevision)) (gaps int) {
	slices.SortFunc(recs, func(a, b *ContractRevision) int {
		if lessRevisionChrono(a, b) {
			return -1
		}
		return 1
	})
	var prev *ContractRevision
	first := true
	for _, r := range recs {
		if r.Contract == nil {
			gaps++
			prev = nil
			continue
		}
		if prev != nil || first {
			visit(prev, r)
		}
		prev, first = r, false
	}
	return gaps
}

// revisionRecords is the snapshot record of each of revs.
func (q *Query) revisionRecords(revs []*ContractRevision) []*ContractRevision {
	recs := make([]*ContractRevision, 0, len(revs))
	for _, r := range revs {
		recs = append(recs, q.snap.Revisions[r.Key])
	}
	return recs
}

// revisionsPartial reports whether the view dropped some of s's revisions or
// a source is not fully available.
func (q *Query) revisionsPartial(s *ServiceRecord) bool {
	return q.snap.revisionsHidden[s.Key] || slices.ContainsFunc(q.snap.Sources, func(src SourceState) bool { return src.Status != SourceAvailable })
}

// newestFirst flattens per-revision groups, newest revision first.
func newestFirst[T any](groups [][]T) []T {
	var items []T
	for i := len(groups) - 1; i >= 0; i-- {
		items = append(items, groups[i]...)
	}
	return items
}

// historyCompleteness is partial when the view or a source holds only some
// revisions, or when more than one revision was compared.
func historyCompleteness(partial bool, groups int) (string, string) {
	switch {
	case partial:
		return string(CompletenessPartial), historyPartialReason
	case groups > 1:
		return string(CompletenessPartial), versionsHeldReason
	}
	return string(CompletenessComplete), ""
}

// serviceChanges is the declared change history of s's revisions in q.
func (q *Query) serviceChanges(s *ServiceRecord) ChangeHistoryPreview {
	var groups [][]ChangeEvent
	h := ChangeHistoryPreview{}
	h.Gaps = walkRevisionPairs(q.revisionRecords(serviceRevisions(q.snap, s)), func(prev, r *ContractRevision) {
		groups = append(groups, revisionChanges(prev, r))
	})
	h.Items, h.Total, h.Truncated = boundSlice(newestFirst(groups), MaxDetailPreview)
	h.Count = len(h.Items)
	h.Completeness, h.CompletenessReason = historyCompleteness(q.revisionsPartial(s), len(groups))
	return h
}

// revisionChanges is every declared change r makes to prev, or what r
// declares when there is no prev, sorted by kind then subject.
func revisionChanges(prev, r *ContractRevision) []ChangeEvent {
	var out []ChangeEvent
	if prev == nil {
		out = presentEvents(r.Contract)
	} else {
		out = append(diffEvents(context.Background(), prev, r), dependencyEvents(prev.Contract, r.Contract)...)
	}
	ref := revisionEntityRef(r)
	var prevRef *EntityRef
	if prev != nil {
		p := revisionEntityRef(prev)
		prevRef = &p
	}
	for i := range out {
		out[i].Revision, out[i].Previous = ref, prevRef
		out[i].Provenance, out[i].Source = "declared", "contract"
	}
	slices.SortStableFunc(out, func(a, b ChangeEvent) int {
		if c := slices.Index(changeKindOrder, a.Kind) - slices.Index(changeKindOrder, b.Kind); c != 0 {
			return c
		}
		return NaturalCompare(a.Subject, b.Subject)
	})
	return out
}

// presentEvents is the owner, domain and environments the oldest revision
// held declares.
func presentEvents(c *contract.Contract) []ChangeEvent {
	var out []ChangeEvent
	if o := ownerString(c.Service.Owner); o != "" {
		out = append(out, ChangeEvent{Kind: "owner", Change: "present", To: o})
	}
	if c.Service.Domain != "" {
		out = append(out, ChangeEvent{Kind: "domain", Change: "present", To: c.Service.Domain})
	}
	for _, e := range c.Environments {
		out = append(out, ChangeEvent{Kind: "environment", Subject: e.Name, Change: "present"})
	}
	return out
}

// ownerString is an owner's key, or its display form when it has no key.
func ownerString(o contract.Owner) string {
	if k := o.KeyString(); k != "" {
		return k
	}
	return o.DisplayString()
}

// diffEvents maps pkg/diff's changes between prev and r to owner, domain,
// offering, lifecycle and environment events; every other path is pacto
// diff's. No bundle file is read.
func diffEvents(ctx context.Context, prev, r *ContractRevision) []ChangeEvent {
	var owner, lifecycle []string
	var out []ChangeEvent
	envs := map[string]*ChangeEvent{}
	for _, c := range diff.Compare(ctx, prev.Contract, r.Contract, nil, nil).Changes {
		switch {
		case strings.HasPrefix(c.Path, "service.owner."):
			owner = appendField(owner, strings.TrimPrefix(c.Path, "service.owner."))
		case c.Path == "service.domain" || c.Path == "service.offering":
			out = append(out, ChangeEvent{Kind: strings.TrimPrefix(c.Path, "service."), Change: changeOf(c.Type), From: valueString(c.OldValue), To: valueString(c.NewValue)})
		case c.Path == "service.lifecycle" || strings.HasPrefix(c.Path, "service.lifecycle."):
			lifecycle = appendField(lifecycle, strings.TrimPrefix(strings.TrimPrefix(c.Path, "service.lifecycle"), "."))
		default:
			out = appendEnvironmentEvent(out, envs, c)
		}
	}
	if owner != nil {
		out = append(out, ownerEvent(prev.Contract.Service.Owner, r.Contract.Service.Owner, owner))
	}
	if lifecycle != nil {
		out = append(out, lifecycleEvent(prev.Contract.Service.Lifecycle, r.Contract.Service.Lifecycle, lifecycle))
	}
	for _, e := range envs {
		out = append(out, *e)
	}
	return out
}

// appendField appends f to fields once, folding a contact path to
// "contacts"; an empty f (a whole block added or removed) names no field
// but still marks the change.
func appendField(fields []string, f string) []string {
	if fields == nil {
		fields = []string{}
	}
	if strings.HasPrefix(f, "contacts[") {
		f = "contacts"
	}
	if f == "" || slices.Contains(fields, f) {
		return fields
	}
	return append(fields, f)
}

// appendEnvironmentEvent folds an environments change into out (an added or
// removed environment) or envs (a changed declared field, one event per
// environment).
func appendEnvironmentEvent(out []ChangeEvent, envs map[string]*ChangeEvent, c diff.Change) []ChangeEvent {
	if c.Path == "environments" {
		name := valueString(c.NewValue)
		if c.Type == diff.Removed {
			name = valueString(c.OldValue)
		}
		return append(out, ChangeEvent{Kind: "environment", Subject: name, Change: changeOf(c.Type)})
	}
	rest, ok := strings.CutPrefix(c.Path, "environments[")
	if !ok {
		return out
	}
	i := strings.LastIndex(rest, "].")
	if i < 0 || !slices.Contains(changeEnvironmentFields, rest[i+2:]) {
		return out
	}
	name := rest[:i]
	e := envs[name]
	if e == nil {
		e = &ChangeEvent{Kind: "environment", Subject: name, Change: "changed"}
		envs[name] = e
	}
	e.Fields = append(e.Fields, rest[i+2:])
	return out
}

// ownerEvent is one owner event for the owner sub-fields that changed.
func ownerEvent(o, n contract.Owner, fields []string) ChangeEvent {
	e := ChangeEvent{Kind: "owner", Change: "changed", Fields: fields, From: ownerString(o), To: ownerString(n)}
	switch {
	case o.IsEmpty():
		e.Change = "added"
	case n.IsEmpty():
		e.Change = "removed"
	}
	return e
}

// lifecycleEvent is one lifecycle event for the lifecycle fields that
// changed; a whole schedule added or removed names no field.
func lifecycleEvent(o, n *contract.InterfaceLifecycle, fields []string) ChangeEvent {
	e := ChangeEvent{Kind: "lifecycle", Change: "changed", Fields: fields}
	switch {
	case o == nil:
		e.Change, e.Fields = "added", nil
	case n == nil:
		e.Change, e.Fields = "removed", nil
	}
	if o != nil {
		e.From = o.String()
	}
	if n != nil {
		e.To = n.String()
	}
	return e
}

// changeOf is a pkg/diff change type in this answer's words.
func changeOf(t diff.ChangeType) string {
	switch t {
	case diff.Added:
		return "added"
	case diff.Removed:
		return "removed"
	}
	return "changed"
}

// valueString is a pkg/diff value as text, "" when absent.
func valueString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// dependencyEvents compares the dependencies prev and r declare by name:
// added, removed, and changed with the fields that differ.
func dependencyEvents(prev, r *contract.Contract) []ChangeEvent {
	before := map[string]contract.Dependency{}
	for _, d := range prev.Dependencies {
		before[d.Name] = d
	}
	var out []ChangeEvent
	for _, d := range r.Dependencies {
		old, had := before[d.Name]
		delete(before, d.Name)
		if !had {
			out = append(out, ChangeEvent{Kind: "dependency", Subject: d.Name, Change: "added", To: dependencyString(d)})
			continue
		}
		if e, ok := changedDependency(old, d); ok {
			out = append(out, e)
		}
	}
	for name, d := range before {
		out = append(out, ChangeEvent{Kind: "dependency", Subject: name, Change: "removed", From: dependencyString(d)})
	}
	return out
}

// dependencyString is a dependency's ref and version range.
func dependencyString(d contract.Dependency) string {
	return strings.Join(strings.Fields(d.Ref+" "+d.Compatibility+" "+d.Version), " ")
}

// changedDependency is the changed event for one dependency on both sides;
// From and To list the values of the changed fields.
func changedDependency(o, n contract.Dependency) (ChangeEvent, bool) {
	e := ChangeEvent{Kind: "dependency", Subject: n.Name, Change: "changed"}
	var from, to []string
	for _, f := range [][3]string{
		{"ref", o.Ref, n.Ref},
		{"compatibility", o.Compatibility, n.Compatibility},
		{"version", o.Version, n.Version},
		{"external", fmt.Sprint(o.External), fmt.Sprint(n.External)},
		{"required", fmt.Sprint(o.Required), fmt.Sprint(n.Required)},
	} {
		if f[1] != f[2] {
			e.Fields = append(e.Fields, f[0])
			from, to = append(from, f[0]+"="+f[1]), append(to, f[0]+"="+f[2])
		}
	}
	if !reflect.DeepEqual(o.Requires, n.Requires) {
		e.Fields = append(e.Fields, "requires")
	}
	e.From, e.To = strings.Join(from, ", "), strings.Join(to, ", ")
	return e, len(e.Fields) > 0
}

// Heading is the line the CLI and TUI print above [ChangeHistoryPreview.Lines].
func (h ChangeHistoryPreview) Heading() string {
	s := fmt.Sprintf("Changes, version order (%d of %d", h.Count, h.Total)
	if h.Gaps > 0 {
		s += fmt.Sprintf(", %d revision(s) unread", h.Gaps)
	}
	if h.CompletenessReason != "" {
		s += ", partial: " + h.CompletenessReason
	}
	return s + ")"
}

// Lines is one line per event: the revision, the change and the revision it
// was compared with. No event has a time, and every line says so.
func (h ChangeHistoryPreview) Lines() []string {
	out := make([]string, 0, len(h.Items))
	for _, e := range h.Items {
		out = append(out, "  "+e.Revision.Label+"  "+e.what()+"  time not recorded")
	}
	return out
}

// what is the event without its revision and time.
func (e ChangeEvent) what() string {
	subject := strings.TrimSpace(e.Kind + " " + e.Subject)
	if e.Change == "present" {
		return strings.TrimSpace(subject+" "+e.To) + " declared since at least " + e.Revision.Label
	}
	s := subject + " " + e.Change
	if len(e.Fields) > 0 {
		s += " (" + strings.Join(e.Fields, ", ") + ")"
	}
	switch {
	case e.From != "" && e.To != "":
		s += ": " + e.From + " -> " + e.To
	case e.From+e.To != "":
		s += ": " + e.From + e.To
	}
	if e.Previous != nil {
		s += ", compared with " + e.Previous.Label
	}
	return s
}
