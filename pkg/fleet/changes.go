package fleet

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/readiness"
)

// ChangeEvent is one declared change to a service between two consecutive
// revisions this answer holds, in version order. It carries no time: the
// fleet records no revision creation time.
type ChangeEvent struct {
	Kind       string        `json:"kind" enum:"owner,domain,offering,dependency,lifecycle,environment,finding" doc:"What changed: the owner, the business domain, the offering, one dependency, the lifecycle, one environment or a validation finding"`
	Subject    string        `json:"subject,omitempty" doc:"The dependency, environment or finding the change is about; empty for the owner, domain, offering and lifecycle"`
	Change     string        `json:"change" enum:"present,added,removed,changed" doc:"present: declared by the oldest revision this answer holds, or the first after one it could not read (declared since at least that revision). For a finding, added: it appeared, removed: it resolved, changed: the readiness score crossed its minimum"`
	Fields     []string      `json:"fields,omitempty" doc:"changed: which declared fields changed"`
	From       string        `json:"from,omitempty" doc:"The value before the change, as the previous revision declares it; for a finding, its severity or the readiness \"score N/M\" (score and minimum)"`
	To         string        `json:"to,omitempty" doc:"The value after the change, as the revision declares it"`
	Revision   EntityRef     `json:"revision" doc:"The first revision this answer holds that shows the change; versions between it and previous may not be held"`
	Previous   *EntityRef    `json:"previous,omitempty" doc:"The revision it was compared with: the change happened after it, at or before revision"`
	Provenance string        `json:"provenance" enum:"declared" doc:"declared: read from immutable contract revisions"`
	Source     string        `json:"source" enum:"contract,validation" doc:"contract: an edit the contract declares; validation: a result of Pacto's own check of the revision"`
	Reached    []ChangeReach `json:"reached,omitempty" doc:"With reached: when the revision first reached each Stage the caller may read, at most one per Stage, from delivery records"`
}

// ChangeReach is when a revision's artifact first reached one Stage, read
// from that Stage's delivery history as the caller.
type ChangeReach struct {
	Namespace   string           `json:"namespace" doc:"The Stage's namespace"`
	Stage       string           `json:"stage" doc:"The Stage's name"`
	Environment string           `json:"environment,omitempty" doc:"The environment the Stage delivers to"`
	At          string           `json:"at" doc:"When the move that brought the revision reached the Stage (applied, else finished), RFC 3339"`
	Record      string           `json:"record,omitempty" doc:"The delivery record of the move; empty when it was read from an Operation's status"`
	FromStatus  bool             `json:"fromStatus,omitempty" doc:"The move was read from an Operation's status or an unrecordable record: nothing signed it"`
	Verified    bool             `json:"verified" doc:"The record's signature was checked against a trust config: a signed move"`
	Impact      *RecordedVerdict `json:"impact,omitempty" doc:"The pacto.impact verdict the record carries; signed only when verified"`
}

// RecordedVerdict is a gate verdict a delivery record carries.
type RecordedVerdict struct {
	Gate    string `json:"gate" doc:"The gate that judged"`
	Result  string `json:"result" enum:"Pass,Fail,Unknown" doc:"What it said"`
	Message string `json:"message,omitempty" doc:"Why"`
}

// ChangeHistoryPreview is every declared change across the revisions of a
// service the answer holds.
type ChangeHistoryPreview struct {
	Total              int           `json:"total"`
	Count              int           `json:"count"`
	Truncated          bool          `json:"truncated"`
	Items              []ChangeEvent `json:"items" doc:"Newest first, in version order, not a timeline"`
	Gaps               int           `json:"gaps" doc:"Revisions whose contract could not be read; no change is reported across them, the next readable revision reads present, and the answer is partial"`
	Completeness       string        `json:"completeness" enum:"complete,partial" doc:"partial when this view or a source holds only some of the service's revisions, a revision could not be read, two revisions of one version disagree, validation ran different checks on two revisions compared, or two versions are compared: no source lists every version, so a change is placed between the versions held"`
	CompletenessReason string        `json:"completenessReason,omitempty"`
	Times              string        `json:"times" enum:"not-read,read,partial,hidden" doc:"Whether delivery records were read for the times: not-read (not asked, or no delivery to read), read (every Stage the caller may list), partial (some namespaces refused or not listed, a Stage history that may lack earlier moves, an artifact that could not be read, or a local revision no record can name), hidden (the caller may not list Stages)"`
	TimesReason        string        `json:"timesReason,omitempty" doc:"Why the times were not read in full"`
	TimesUnreadable    []string      `json:"timesUnreadable,omitempty" doc:"partial: the namespaces or Stages not read, the Stages whose history may lack earlier moves and the artifacts that could not be read, each with why"`
}

// Change kinds, in the order a revision's changes are listed.
var changeKindOrder = []string{"owner", "domain", "offering", "dependency", "lifecycle", "environment", "finding"}

// changeEnvironmentFields is the declared environment fields a change names,
// in pkg/diff's order; value paths inside an environment's file are pacto
// diff's.
var changeEnvironmentFields = []string{"file", "lifetime", "expires", "owner"}

// walkRevisionPairs sorts recs in version order and calls visit with every
// readable revision and the readable one before it. An unread revision
// counts as a gap and breaks the comparison across it: the oldest readable
// revision and each one after a gap are compared with nothing (prev nil), so
// what they declare still reads as present.
func walkRevisionPairs(recs []*ContractRevision, visit func(prev, r *ContractRevision)) (gaps int) {
	slices.SortFunc(recs, func(a, b *ContractRevision) int {
		if lessRevisionChrono(a, b) {
			return -1
		}
		return 1
	})
	var prev *ContractRevision
	for _, r := range recs {
		if r.Contract == nil {
			gaps++
			prev = nil
			continue
		}
		visit(prev, r)
		prev = r
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

// historyPartialReason never names what the view dropped.
const historyPartialReason = "this view or a source holds only some of the service's revisions"

// versionsHeldReason is why a history that compares versions is partial: no
// source lists every version of a service, so versions between two held
// ones may be missing.
const versionsHeldReason = "the sources hold only some versions; a change is placed between the versions held"

// historyBreaks is what left changes unreported between revisions held.
type historyBreaks struct {
	// gaps is the revisions that could not be read; ties the versions held
	// as revisions that disagree, which no version order can place; checks
	// the pairs whose validation did not run the same stages.
	gaps, ties, checks int
}

// historyCompleteness is partial when the view or a source holds only some
// revisions, when something left a change unreported (b), or when more than
// one revision was compared; each reason is named.
func historyCompleteness(partial bool, groups int, b historyBreaks) (string, string) {
	var why []string
	if partial {
		why = append(why, historyPartialReason)
	}
	if b.gaps > 0 {
		why = append(why, fmt.Sprintf("%d revision(s) could not be read; changes across them are unknown", b.gaps))
	}
	if b.ties > 0 {
		why = append(why, fmt.Sprintf("%d version(s) are held as revisions that disagree; no change is reported between them", b.ties))
	}
	if b.checks > 0 {
		why = append(why, fmt.Sprintf("validation did not run the same checks on %d pair(s) of revisions; no finding change is reported between them", b.checks))
	}
	if groups > 1 {
		why = append(why, versionsHeldReason)
	}
	if len(why) == 0 {
		return string(CompletenessComplete), ""
	}
	return string(CompletenessPartial), strings.Join(why, "; ")
}

// serviceChanges is the declared change history of s's revisions in q. Two
// revisions of one version have no order between them: when they disagree,
// no change is reported between them, never a direction their keys decide.
func (q *Query) serviceChanges(s *ServiceRecord) ChangeHistoryPreview {
	var groups [][]ChangeEvent
	var b historyBreaks
	h := ChangeHistoryPreview{}
	b.gaps = walkRevisionPairs(q.revisionRecords(serviceRevisions(q.snap, s)), func(prev, r *ContractRevision) {
		evs, checked := revisionChanges(prev, r)
		if prev != nil && prev.Version == r.Version {
			if len(evs) > 0 || !checked {
				b.ties++
			}
			return
		}
		if !checked {
			b.checks++
		}
		groups = append(groups, evs)
	})
	h.Gaps = b.gaps
	h.Items, h.Total, h.Truncated = boundSlice(newestFirst(groups), MaxDetailPreview)
	h.Count = len(h.Items)
	h.Completeness, h.CompletenessReason = historyCompleteness(q.revisionsPartial(s), len(groups), b)
	h.Times, h.TimesReason = TimesNotRead, "ask with reached to read delivery records"
	return h
}

// The values of ChangeHistoryPreview.Times.
const (
	TimesNotRead = "not-read"
	TimesRead    = "read"
	TimesPartial = "partial"
	TimesHidden  = "hidden"
)

// AttachReached sets on each event of h when its revision reached each
// Stage, byContract keyed by the revision's contract digest, and how the
// times were read. It never adds or drops an event: delivery decides only
// the times. A local revision is keyed by its files, never by an artifact
// digest, so no record can name it: its time is not read, never "not
// recorded".
func (q *Query) AttachReached(h *ChangeHistoryPreview, byContract map[string][]ChangeReach, times, reason string, unreadable []string) {
	h.Times, h.TimesReason, h.TimesUnreadable = times, reason, unreadable
	local, matchable := 0, 0
	for i, e := range h.Items {
		r := q.snap.Revisions[RevisionKey(e.Revision.Key)]
		switch {
		case r == nil || r.Digest == "":
		case strings.HasPrefix(r.RequestedRef, "file://"): // ponytail: file:// is the one source keyed by content today
			local++
		default:
			matchable++
			h.Items[i].Reached = byContract[r.Digest]
		}
	}
	if local == 0 || times != TimesRead && times != TimesPartial {
		return
	}
	if matchable == 0 {
		h.Times, h.TimesReason, h.TimesUnreadable = TimesNotRead, localRevisionReason, nil
		return
	}
	h.Times, h.TimesReason = TimesPartial, strings.Join(slices.DeleteFunc([]string{reason, localRevisionReason}, func(s string) bool { return s == "" }), "; ")
}

// localRevisionReason is why a local revision has no time.
const localRevisionReason = "a local revision is keyed by its files, not an artifact digest, so no delivery record can name it"

// ReachedTimes is how complete the times read are: partial when a namespace
// or Stage could not be read (unreadable) or what was read leaves a time in
// doubt (doubts, see delivery.Arrivals), naming both.
func ReachedTimes(unreadable, doubts []string) (times, reason string, names []string) {
	if len(unreadable)+len(doubts) == 0 {
		return TimesRead, "", nil
	}
	var why []string
	if len(unreadable) > 0 {
		why = append(why, fmt.Sprintf("%d namespaces or Stages could not be read", len(unreadable)))
	}
	if len(doubts) > 0 {
		why = append(why, fmt.Sprintf("%d Stages or artifacts were read only in part, so a time may be missing or later than the first", len(doubts)))
	}
	return TimesPartial, strings.Join(why, "; "), append(slices.Clone(unreadable), doubts...)
}

// revisionChanges is every declared change r makes to prev, or what r
// declares when there is no prev, sorted by kind then subject; checked is
// false when validation ran different stages on them, so their findings were
// not compared (findingEvents).
func revisionChanges(prev, r *ContractRevision) (out []ChangeEvent, checked bool) {
	checked = true
	if prev == nil {
		out = presentEvents(r.Contract)
	} else {
		var fs []ChangeEvent
		out = append(diffEvents(context.Background(), prev, r), dependencyEvents(prev.Contract, r.Contract)...)
		fs, checked = findingEvents(prev, r)
		out = append(out, fs...)
	}
	ref := revisionEntityRef(r)
	var prevRef *EntityRef
	if prev != nil {
		p := revisionEntityRef(prev)
		prevRef = &p
	}
	for i := range out {
		out[i].Revision, out[i].Previous = ref, prevRef
		out[i].Provenance = "declared"
		if out[i].Source == "" {
			out[i].Source = "contract"
		}
	}
	slices.SortStableFunc(out, func(a, b ChangeEvent) int {
		if c := slices.Index(changeKindOrder, a.Kind) - slices.Index(changeKindOrder, b.Kind); c != 0 {
			return c
		}
		return NaturalCompare(a.Subject, b.Subject)
	})
	return out, checked
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

// findingEvents is the validation findings that appeared in r or resolved
// since prev, keyed by code and subject, plus one readiness event when the
// score crosses the minimum. Nothing when either revision was never
// validated: an unvalidated revision holds no findings to compare. Nothing
// either, and checked false, when validation did not run the same stages on
// both: a finding a stage that did not run would raise is unknown, never
// resolved or new.
func findingEvents(prev, r *ContractRevision) (_ []ChangeEvent, checked bool) {
	if len(prev.Stages) == 0 || len(r.Stages) == 0 {
		return nil, true
	}
	if !slices.Equal(stagesNotRun(prev.Stages), stagesNotRun(r.Stages)) {
		return nil, false
	}
	before, after := findingSet(prev.Validation), findingSet(r.Validation)
	var out []ChangeEvent
	for k, sev := range after {
		if _, had := before[k]; !had {
			out = append(out, ChangeEvent{Kind: "finding", Subject: k, Change: "added", To: sev, Source: "validation"})
		}
	}
	for k, sev := range before {
		if _, has := after[k]; !has {
			out = append(out, ChangeEvent{Kind: "finding", Subject: k, Change: "removed", From: sev, Source: "validation"})
		}
	}
	if o, n := prev.Readiness, r.Readiness; o != nil && n != nil && (o.Score >= o.MinScore) != (n.Score >= n.MinScore) {
		out = append(out, ChangeEvent{Kind: "finding", Subject: "readiness", Change: "changed", From: scoreString(o), To: scoreString(n), Source: "validation"})
	}
	return out, true
}

// stagesNotRun is the names of the validation stages whose findings are
// unknown, in order: skipped after an earlier stage failed, or unverified. A
// stage skipped for having nothing to check ran and found nothing.
func stagesNotRun(stages []ValidationStage) []string {
	var out []string
	failed := false
	for _, s := range stages {
		if s.Status == "skip" && (failed || s.Unverified) {
			out = append(out, s.Name)
		}
		failed = failed || s.Status == "fail"
	}
	return out
}

// findingSet is each finding's subject line ("<code>" or "<code> (<subject
// kind> <name>)") to its severity.
func findingSet(fs []finding.Finding) map[string]string {
	out := make(map[string]string, len(fs))
	for _, f := range fs {
		k := string(f.Code)
		if s := strings.TrimSpace(f.Subject.Kind + " " + f.Subject.Name); s != "" {
			k += " (" + s + ")"
		}
		out[k] = string(f.Severity)
	}
	return out
}

// scoreString is a readiness score against its minimum, "score N/M".
func scoreString(r *readiness.Result) string { return fmt.Sprintf("score %d/%d", r.Score, r.MinScore) }

// Heading is the line the CLI and TUI print above [ChangeHistoryPreview.Lines].
func (h ChangeHistoryPreview) Heading() string {
	s := fmt.Sprintf("Changes, version order (%d of %d", h.Count, h.Total)
	if h.Gaps > 0 {
		s += fmt.Sprintf(", %d revision(s) unread", h.Gaps)
	}
	if h.CompletenessReason != "" {
		s += ", partial: " + h.CompletenessReason
	}
	if h.Times != TimesRead && h.TimesReason != "" {
		s += ", times " + h.Times + ": " + h.TimesReason
	}
	return s + ")"
}

// Lines is one line per event: the revision, the change, the revision it was
// compared with and when it reached each Stage, or why no time is shown.
func (h ChangeHistoryPreview) Lines() []string {
	out := make([]string, 0, len(h.Items))
	for _, e := range h.Items {
		out = append(out, "  "+e.Revision.Label+"  "+e.what()+"  "+h.timeOf(e))
	}
	return out
}

// timeOf is when e reached each Stage, or why it has no time.
func (h ChangeHistoryPreview) timeOf(e ChangeEvent) string {
	if len(e.Reached) > 0 {
		parts := make([]string, len(e.Reached))
		for i, r := range e.Reached {
			parts[i] = r.line()
		}
		return strings.Join(parts, "; ")
	}
	switch h.Times {
	case TimesRead:
		return "time not recorded"
	case TimesPartial:
		return "time not recorded in the Stages read (" + h.TimesReason + ")"
	case TimesHidden:
		return "time hidden (" + h.TimesReason + ")"
	}
	return "time not read"
}

// line is the reach as "reached <stage> <time> (<how it is known>)".
func (r ChangeReach) line() string {
	how := "record " + shortRecord(r.Record)
	switch {
	case r.FromStatus:
		how = "from status, not signed"
	case !r.Verified:
		how = "recorded, signature not checked"
	}
	return "reached " + r.Stage + " " + r.At + " (" + how + ")"
}

// shortRecord is a record id as short as the CLI's history table prints it.
func shortRecord(id string) string { return id[:min(len(id), 19)] }

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
