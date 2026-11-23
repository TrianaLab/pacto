package fleet

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// maxSkippedTemplateNames bounds the names one INFERRED_TEMPLATES_SKIPPED
// lists.
const maxSkippedTemplateNames = 5

// maxTemplateSummaryRunes is the service.summary bound a template's summary
// is cut to.
const maxTemplateSummaryRunes = 200

// InferredTemplate is one golden path a catalog outside every registry holds
// (see [Collection.InferredTemplates]). It is never a service, never signed or
// declared and never offered for creation until a person accepts it.
type InferredTemplate struct {
	Name       string     `json:"name" doc:"The catalog's name for the template, the service.name accepting it gives the golden path"`
	Entity     string     `json:"entity" doc:"The catalog's own reference, such as template:default/springboot-service"`
	Title      string     `json:"title,omitempty" doc:"The catalog's display title"`
	Summary    string     `json:"summary,omitempty" doc:"The catalog's description, cut to 200 characters"`
	Type       string     `json:"type,omitempty" doc:"The catalog's template type, such as service or website"`
	Owner      string     `json:"owner,omitempty" doc:"The owner the catalog names; shown, never declared"`
	OwnerKey   string     `json:"ownerKey,omitempty" doc:"The canonical key of the owner the catalog names"`
	From       string     `json:"from" doc:"Where the template was read, as the source writes it"`
	URL        string     `json:"url,omitempty" doc:"Where a person opens the template; empty when the source gives no address a browser opens"`
	Source     string     `json:"source" doc:"The id of the source that read the template"`
	ObservedAt *time.Time `json:"observedAt,omitempty" doc:"When the source read the template"`
	Acceptable bool       `json:"acceptable" doc:"The source is one an operator configured, so a person may accept the template as a golden path"`
	DeclaredAs string     `json:"declaredAs,omitempty" doc:"The key of the declared golden path holding the same name; such an item is never listed or counted as inferred, it only gives that golden path its link"`
	NameTaken  bool       `json:"nameTaken,omitempty" doc:"A service that is no golden path already holds the name; the template is never offered for acceptance"`
	Provenance string     `json:"provenance" enum:"inferred" doc:"Always inferred"`
}

// InferredTemplateList is the inferred golden paths of one answer.
type InferredTemplateList struct {
	Items        []InferredTemplate `json:"items" doc:"The inferred golden paths, sorted by name; never paged"`
	Total        int                `json:"total" doc:"How many inferred golden paths are listed; the ones a declared golden path holds the name of are not counted"`
	AsOf         *time.Time         `json:"asOf,omitempty" doc:"When the oldest listed template was read, else when a source last listed templates"`
	Completeness Completeness       `json:"completeness" enum:"complete,partial" doc:"Partial when a source that lists templates could not read them all; a missing template is then unknown"`
	Limitations  []Limitation       `json:"limitations,omitempty" doc:"Why the list is partial and which templates were left out"`
	Declared     []InferredTemplate `json:"declared,omitempty" doc:"Catalog templates a declared golden path holds the name of: the link of that golden path, never listed or counted as inferred"`
}

// sourcedTemplate pairs a raw inferred template with the id of the source
// that reported it.
type sourcedTemplate struct {
	raw    RawInferredTemplate
	source string
}

// sourcedTemplates is the inferred templates of one source's collection. It
// records the source as a lister when the collection says it lists templates.
func sourcedTemplates(snap *FleetSnapshot, source string, col *Collection) []sourcedTemplate {
	if col.ListsTemplates {
		snap.templateSources = append(snap.templateSources, source)
	}
	out := make([]sourcedTemplate, len(col.InferredTemplates))
	for i, t := range col.InferredTemplates {
		out[i] = sourcedTemplate{raw: t, source: source}
	}
	return out
}

// foldInferredTemplates keeps the templates sources list as
// [FleetSnapshot.InferredTemplates]: only names a contract can carry, one per
// source and entity, marked against the services already holding the name,
// sorted and bounded. It runs after the golden paths are derived.
func foldInferredTemplates(snap *FleetSnapshot, raw []sourcedTemplate) {
	items, notes := validTemplates(raw)
	markNames(snap, items)
	items, capped := capTemplates(items)
	snap.InferredTemplates = items
	snap.templateNotes = append(notes, capped...)
}

// validTemplates is the templates whose name a contract can carry, one per
// source and entity, sorted by name, entity and source, with one
// INFERRED_TEMPLATES_SKIPPED per source naming those it dropped.
func validTemplates(raw []sourcedTemplate) ([]InferredTemplate, []Limitation) {
	type key struct{ source, entity string }
	seen := map[key]bool{}
	skipped := map[string][]string{}
	var sources []string
	var out []InferredTemplate
	for _, st := range raw {
		if !contract.ValidSlug(st.raw.Name) {
			if _, ok := skipped[st.source]; !ok {
				sources = append(sources, st.source)
			}
			skipped[st.source] = append(skipped[st.source], st.raw.Name)
			continue
		}
		k := key{st.source, st.raw.Entity}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, inferredTemplate(st))
	}
	slices.SortFunc(out, func(a, b InferredTemplate) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Entity, b.Entity), cmp.Compare(a.Source, b.Source))
	})
	notes := make([]Limitation, 0, len(sources))
	for _, src := range sources {
		notes = append(notes, skippedTemplates(src, skipped[src]))
	}
	return out, notes
}

// inferredTemplate is the snapshot item of one valid raw template.
func inferredTemplate(st sourcedTemplate) InferredTemplate {
	r := st.raw
	summary := r.Summary
	if rs := []rune(summary); len(rs) > maxTemplateSummaryRunes {
		summary = string(rs[:maxTemplateSummaryRunes])
	}
	return InferredTemplate{Name: r.Name, Entity: r.Entity, Title: r.Title, Summary: summary, Type: r.Type,
		Owner: r.Owner.DisplayString(), OwnerKey: r.Owner.KeyString(), From: r.From, URL: r.URL, Source: st.source,
		ObservedAt: copyTime(r.ObservedAt), Acceptable: r.Acceptable, Provenance: ProvenanceInferred}
}

// skippedTemplates is the INFERRED_TEMPLATES_SKIPPED of one source, naming at
// most maxSkippedTemplateNames of its dropped names (quoted; a name with a
// control character or past the slug bound is counted, never named).
func skippedTemplates(source string, names []string) Limitation {
	var shown []string
	for _, n := range names {
		if len(shown) < maxSkippedTemplateNames && cleanText(n, 253) {
			shown = append(shown, strconv.Quote(n))
		}
	}
	return Limitation{Code: LimitationInferredTemplatesSkipped, Source: source,
		Message: fmt.Sprintf("%d templates have a name a contract cannot carry and are not listed: %s", len(names), strings.Join(shown, ", "))}
}

// markNames sets DeclaredAs on an item whose name (case-insensitively) is a
// golden path's and NameTaken on one whose name another service holds.
func markNames(snap *FleetSnapshot, items []InferredTemplate) {
	paths, taken := map[string]ServiceKey{}, map[string]bool{}
	for k, s := range snap.Services {
		n := strings.ToLower(s.Name)
		if s.Template {
			if prev, ok := paths[n]; !ok || k < prev {
				paths[n] = k
			}
			continue
		}
		taken[n] = true
	}
	for i := range items {
		n := strings.ToLower(items[i].Name)
		if k, ok := paths[n]; ok {
			items[i].DeclaredAs = string(k)
			continue
		}
		items[i].NameTaken = taken[n]
	}
}

// capTemplates keeps every item a golden path holds the name of and the first
// MaxInferredTemplates of the others, with INFERRED_TEMPLATES_CAPPED past it.
func capTemplates(items []InferredTemplate) ([]InferredTemplate, []Limitation) {
	out := items[:0:0]
	listed := 0
	for _, it := range items {
		if it.DeclaredAs == "" {
			listed++
			if listed > MaxInferredTemplates {
				continue
			}
		}
		out = append(out, it)
	}
	if listed <= MaxInferredTemplates {
		return out, nil
	}
	return out, []Limitation{{Code: LimitationInferredTemplatesCapped,
		Message: fmt.Sprintf("the catalogs list %d golden paths; the first %d by name are shown", listed, MaxInferredTemplates)}}
}

// InferredTemplates is the inferred golden paths whose name, title, summary
// or entity contains text (case-insensitively), with the declared golden
// paths' links in Declared. It is nil when no source lists templates: no
// lister means nothing is known, never "no templates".
func (q *Query) InferredTemplates(text string) *InferredTemplateList {
	if len(q.snap.templateSources) == 0 {
		return nil
	}
	l := &InferredTemplateList{Items: []InferredTemplate{}, Completeness: CompletenessComplete}
	needle := strings.ToLower(text)
	for _, it := range q.snap.InferredTemplates {
		switch {
		case it.DeclaredAs != "":
			l.Declared = append(l.Declared, it)
		case templateMatchesText(it, needle):
			l.Items = append(l.Items, it)
		}
	}
	l.Total = len(l.Items)
	newest := q.templateCompleteness(l)
	l.Limitations = append(l.Limitations, q.snap.templateNotes...)
	l.AsOf = oldestTemplate(q.snap.InferredTemplates)
	if l.AsOf == nil {
		l.AsOf = newest
	}
	return l
}

// templateMatchesText reports whether an item's name, title, summary or entity
// contains the lower-cased needle.
func templateMatchesText(it InferredTemplate, needle string) bool {
	return slices.ContainsFunc([]string{it.Name, it.Title, it.Summary, it.Entity}, func(v string) bool {
		return strings.Contains(strings.ToLower(v), needle)
	})
}

// templateCompleteness marks l partial for each lister that is not available,
// with its reason, and returns the newest time a lister was observed. A
// lister with no state is unknown, so partial.
func (q *Query) templateCompleteness(l *InferredTemplateList) *time.Time {
	var newest *time.Time
	for _, id := range q.snap.templateSources {
		st := SourceState{Status: SourceUnavailable}
		if i := slices.IndexFunc(q.snap.Sources, func(s SourceState) bool { return s.ID == id }); i >= 0 {
			st = q.snap.Sources[i]
		}
		if st.ObservedAt != nil && (newest == nil || st.ObservedAt.After(*newest)) {
			newest = st.ObservedAt
		}
		switch st.Status {
		case SourceAvailable:
			continue
		case SourceUnavailable:
			msg := "source " + id + " is unavailable; its templates are unknown"
			if st.Error != nil {
				msg = st.Error.Message
			}
			l.Limitations = append(l.Limitations, Limitation{Code: LimitationSourceUnavailable, Source: id, Message: msg})
		default:
			for _, lim := range q.snap.Limitations {
				if lim.Source == id {
					l.Limitations = append(l.Limitations, lim)
				}
			}
		}
		l.Completeness = CompletenessPartial
	}
	return copyTime(newest)
}

// oldestTemplate is the oldest ObservedAt of items, nil when none has one.
func oldestTemplate(items []InferredTemplate) *time.Time {
	var oldest *time.Time
	for _, it := range items {
		if it.ObservedAt != nil && (oldest == nil || it.ObservedAt.Before(*oldest)) {
			oldest = it.ObservedAt
		}
	}
	return copyTime(oldest)
}
