package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const tmplSource = "catalog-t"

var tmplAt = fixedNow().Add(-2 * time.Hour)

// tmpl is a catalog template named name, acceptable and observed at tmplAt.
func tmpl(name string) RawInferredTemplate {
	return RawInferredTemplate{Name: name, Entity: "template:default/" + name, Title: name + " title", Summary: "Builds a " + name,
		Type: "service", Owner: contract.Owner{Team: "platform"}, From: "Catalog (catalog.acme.io)",
		URL: "https://catalog.acme.io/create/templates/default/" + name, Acceptable: true, ObservedAt: &tmplAt}
}

// tmplCatalog is an advisory source listing templates.
func tmplCatalog(ts ...RawInferredTemplate) Source {
	return NewMemorySource(tmplSource, "idp-catalog", &Collection{Advisory: true, ListsTemplates: true, InferredTemplates: ts})
}

// tmplFleet is goldenFleet's revisions (the http-service golden path,
// gift-cards, postgresql, orders) read from one source, plus extra.
func tmplFleet(t *testing.T, extra ...Source) *Query {
	t.Helper()
	revs := []RawRevision{ociRev(acme, templated("http-service", "1.0.0"), pathFiles), createdFrom(pathContentHash(t)), postgres(),
		ociRev(acme, usesPostgres("orders", "1.0.0"), nil)}
	return NewQuery(buildClusterSnap(t, append([]Source{NewMemorySource("src", "oci", &Collection{Revisions: revs})}, extra...)...))
}

func goldenPaths(t *testing.T, q *Query, f EntityFilter) *EntityList {
	t.Helper()
	f.Template = TemplateOnly
	l, err := q.Entities(f)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestInferredTemplatesFoldKeepsProvenanceAndAsOf(t *testing.T) {
	q := tmplFleet(t, tmplCatalog(tmpl("springboot-service")))
	l := q.InferredTemplates("")
	if l == nil || len(l.Items) != 1 || l.Total != 1 {
		t.Fatalf("list %+v", l)
	}
	it := l.Items[0]
	want := InferredTemplate{Name: "springboot-service", Entity: "template:default/springboot-service", Title: "springboot-service title",
		Summary: "Builds a springboot-service", Type: "service", Owner: contract.Owner{Team: "platform"}.DisplayString(),
		OwnerKey: "team:platform", From: "Catalog (catalog.acme.io)", URL: "https://catalog.acme.io/create/templates/default/springboot-service",
		Source: tmplSource, ObservedAt: &tmplAt, Acceptable: true, Provenance: ProvenanceInferred}
	if !reflect.DeepEqual(it, want) {
		t.Fatalf("item\n got %+v\nwant %+v", it, want)
	}
	if l.AsOf == nil || !l.AsOf.Equal(tmplAt) || l.Completeness != CompletenessComplete {
		t.Fatalf("asOf %v completeness %s", l.AsOf, l.Completeness)
	}
}

func TestInferredTemplatesAsOfOldestThenLister(t *testing.T) {
	older := tmplAt.Add(-time.Hour)
	a, b := tmpl("a-path"), tmpl("b-path")
	b.ObservedAt = &older
	if l := tmplFleet(t, tmplCatalog(a, b)).InferredTemplates(""); !l.AsOf.Equal(older) {
		t.Fatalf("asOf %v, want the oldest item", l.AsOf)
	}
	at := fixedNow()
	none := NewMemorySource(tmplSource, "idp-catalog", &Collection{Advisory: true, ListsTemplates: true,
		State: &SourceState{Status: SourceAvailable, ObservedAt: &at}})
	if l := tmplFleet(t, none).InferredTemplates(""); l.AsOf == nil || !l.AsOf.Equal(at) {
		t.Fatalf("asOf %v, want the lister's state time", l.AsOf)
	}
	if l := tmplFleet(t, tmplCatalog()).InferredTemplates(""); !l.AsOf.Equal(fixedNow()) {
		t.Fatalf("asOf %v, want the build time Build gives the lister", l.AsOf)
	}
	q := tmplFleet(t, tmplCatalog())
	snap := *q.snap
	snap.Sources = nil
	if l := NewQuery(&snap).InferredTemplates(""); l.AsOf != nil {
		t.Fatalf("asOf %v with nothing observed", l.AsOf)
	}
}

func TestInferredTemplatesNeverGoldenPathEntities(t *testing.T) {
	plain := tmplFleet(t)
	q := tmplFleet(t, tmplCatalog(tmpl("springboot-service"), tmpl("node-service")))
	l := goldenPaths(t, q, EntityFilter{})
	if len(l.Entities) != 1 || l.Entities[0].Label != "http-service" {
		t.Fatalf("golden path entities %+v", l.Entities)
	}
	if len(q.snap.Services) != len(plain.snap.Services) {
		t.Fatalf("services %d, want %d", len(q.snap.Services), len(plain.snap.Services))
	}
	if l.InferredTemplates == nil || len(l.InferredTemplates.Items) != 2 {
		t.Fatalf("inferred block %+v", l.InferredTemplates)
	}
	if l.InferredTemplates.Items[0].Name != "node-service" {
		t.Fatalf("not sorted by name: %+v", l.InferredTemplates.Items)
	}
}

func TestInferredTemplateDeclaredKeepsLink(t *testing.T) {
	q := tmplFleet(t, tmplCatalog(tmpl("http-service"), tmpl("springboot-service")))
	l := q.InferredTemplates("")
	if l.Total != 1 || len(l.Items) != 1 || l.Items[0].Name != "springboot-service" {
		t.Fatalf("items %+v total %d", l.Items, l.Total)
	}
	if len(l.Declared) != 1 {
		t.Fatalf("declared %+v", l.Declared)
	}
	d := l.Declared[0]
	if d.DeclaredAs != string(NewServiceKeyDomain(acme, "http-service")) || d.URL == "" {
		t.Fatalf("declared item %+v", d)
	}
	if got := q.InferredTemplates("springboot").Declared; len(got) != 1 {
		t.Fatalf("declared narrowed by text: %+v", got)
	}
}

func TestInferredTemplateNameTakenByService(t *testing.T) {
	it := tmplFleet(t, tmplCatalog(tmpl("orders"))).InferredTemplates("").Items
	if len(it) != 1 || !it[0].NameTaken || it[0].DeclaredAs != "" {
		t.Fatalf("items %+v", it)
	}
}

func TestInferredTemplateAcceptableCarried(t *testing.T) {
	raw := tmpl("springboot-service")
	raw.Acceptable = false
	if it := tmplFleet(t, tmplCatalog(raw)).InferredTemplates("").Items; it[0].Acceptable {
		t.Fatalf("acceptable became true: %+v", it[0])
	}
}

func TestInferredTemplatesInvalidNameSkippedAndNoted(t *testing.T) {
	l := tmplFleet(t, tmplCatalog(tmpl("Spring_Boot"), tmpl("a.b"), tmpl("ok-path"), tmpl("ok-path"))).InferredTemplates("")
	if len(l.Items) != 1 || l.Items[0].Name != "ok-path" {
		t.Fatalf("items %+v", l.Items)
	}
	skipped := factLimitations(l.Limitations, LimitationInferredTemplatesSkipped)
	if len(skipped) != 1 || skipped[0].Source != tmplSource ||
		!strings.Contains(skipped[0].Message, `"Spring_Boot"`) || !strings.Contains(skipped[0].Message, `"a.b"`) || !strings.HasPrefix(skipped[0].Message, "2 ") {
		t.Fatalf("skipped %+v", skipped)
	}
	if l.Completeness != CompletenessComplete {
		t.Fatalf("a skipped name made the list %s", l.Completeness)
	}
}

func TestInferredTemplatesSkippedNamesBounded(t *testing.T) {
	var ts []RawInferredTemplate
	for i := range 7 {
		ts = append(ts, tmpl(fmt.Sprintf("Bad_%d", i)))
	}
	ts = append(ts, tmpl("bad\x00name"))
	l := tmplFleet(t, tmplCatalog(ts...)).InferredTemplates("")
	msg := factLimitations(l.Limitations, LimitationInferredTemplatesSkipped)[0].Message
	if !strings.HasPrefix(msg, "8 ") || strings.Count(msg, `"`) != 2*maxSkippedTemplateNames || strings.Contains(msg, "\x00") {
		t.Fatalf("message %q", msg)
	}
}

func TestInferredTemplatesCapped(t *testing.T) {
	var ts []RawInferredTemplate
	for i := range MaxInferredTemplates + 1 {
		ts = append(ts, tmpl(fmt.Sprintf("path-%03d", i)))
	}
	ts = append(ts, tmpl("http-service"))
	l := tmplFleet(t, tmplCatalog(ts...)).InferredTemplates("")
	if len(l.Items) != MaxInferredTemplates || l.Total != MaxInferredTemplates || len(l.Declared) != 1 {
		t.Fatalf("items %d total %d declared %d", len(l.Items), l.Total, len(l.Declared))
	}
	capped := factLimitations(l.Limitations, LimitationInferredTemplatesCapped)
	if len(capped) != 1 || !strings.Contains(capped[0].Message, fmt.Sprint(MaxInferredTemplates+1)) {
		t.Fatalf("capped %+v", capped)
	}
}

func TestInferredTemplatesCompletenessFollowsSource(t *testing.T) {
	at := fixedNow()
	cases := []struct {
		name  string
		col   *Collection
		want  Completeness
		cause string
	}{
		{"available", &Collection{State: &SourceState{Status: SourceAvailable, ObservedAt: &at}}, CompletenessComplete, ""},
		{"partial", &Collection{State: &SourceState{Status: SourcePartial, ObservedAt: &at},
			Limitations: []Limitation{{Code: LimitationSourcePartial, Source: tmplSource, Message: "the read stopped at 5000 components"}}},
			CompletenessPartial, "the read stopped at 5000 components"},
		{"unavailable", &Collection{State: &SourceState{Status: SourceUnavailable, ObservedAt: &at,
			Error: &SourceError{Code: "AUTH_FAILED", Message: "the catalog refused the request"}}}, CompletenessPartial, "the catalog refused the request"},
		{"unavailable without a reason", &Collection{State: &SourceState{Status: SourceUnavailable, ObservedAt: &at}},
			CompletenessPartial, "source " + tmplSource + " is unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.col.Advisory, c.col.ListsTemplates, c.col.InferredTemplates = true, true, []RawInferredTemplate{tmpl("springboot-service")}
			l := tmplFleet(t, NewMemorySource(tmplSource, "idp-catalog", c.col)).InferredTemplates("")
			if l.Completeness != c.want {
				t.Fatalf("completeness %s, want %s", l.Completeness, c.want)
			}
			if c.cause != "" && !strings.Contains(fmt.Sprint(l.Limitations), c.cause) {
				t.Fatalf("limitations %+v lack %q", l.Limitations, c.cause)
			}
		})
	}
}

func TestInferredTemplatesListerWithoutStateIsPartial(t *testing.T) {
	q := tmplFleet(t, tmplCatalog(tmpl("springboot-service")))
	snap := *q.snap
	snap.Sources = nil
	if l := NewQuery(&snap).InferredTemplates(""); l.Completeness != CompletenessPartial {
		t.Fatalf("completeness %s with no lister state", l.Completeness)
	}
}

func TestInferredTemplatesAbsentWithoutLister(t *testing.T) {
	quiet := NewMemorySource(tmplSource, "idp-catalog", &Collection{Advisory: true, InferredTemplates: []RawInferredTemplate{tmpl("springboot-service")}})
	if l := goldenPaths(t, tmplFleet(t, quiet), EntityFilter{}); l.InferredTemplates != nil {
		t.Fatalf("block without a lister: %+v", l.InferredTemplates)
	}
	if l := goldenPaths(t, tmplFleet(t, NewFailingSource(tmplSource, "idp-catalog", errors.New("down"))), EntityFilter{}); l.InferredTemplates != nil {
		t.Fatalf("block from a failed source: %+v", l.InferredTemplates)
	}
	l := goldenPaths(t, tmplFleet(t, tmplCatalog()), EntityFilter{}).InferredTemplates
	if l == nil || l.Total != 0 || l.Items == nil || l.Completeness != CompletenessComplete {
		t.Fatalf("empty lister block %+v", l)
	}
}

func TestEntitiesInferredTemplatesBlockFilters(t *testing.T) {
	q := tmplFleet(t, tmplCatalog(tmpl("springboot-service"), tmpl("node-service")))
	if l := goldenPaths(t, q, EntityFilter{Declaration: DeclarationDeclared}); l.InferredTemplates != nil {
		t.Fatalf("declaration=declared kept the block")
	}
	l := goldenPaths(t, q, EntityFilter{Declaration: DeclarationInferred})
	if len(l.Entities) != 0 || l.InferredTemplates == nil || l.InferredTemplates.Total != 2 {
		t.Fatalf("declaration=inferred: entities %d block %+v", len(l.Entities), l.InferredTemplates)
	}
	for _, text := range []string{"NODE-SERVICE TITLE", "template:default/node", "builds a node"} {
		if got := goldenPaths(t, q, EntityFilter{Text: text}).InferredTemplates; got.Total != 1 || got.Items[0].Name != "node-service" {
			t.Fatalf("text %q: %+v", text, got)
		}
	}
	if l, _ := q.Entities(EntityFilter{}); l.InferredTemplates != nil {
		t.Fatalf("block without template=only")
	}
}

func TestInferredTemplatesNeverCountInSummaries(t *testing.T) {
	plain := tmplFleet(t, tmplCatalog())
	q := tmplFleet(t, tmplCatalog(tmpl("springboot-service"), tmpl("orders"), tmpl("http-service")))
	jsonOf := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if a, b := plain.Overview().Summary, q.Overview().Summary; jsonOf(a) != jsonOf(b) {
		t.Fatalf("overview summary changed\n%s\n%s", jsonOf(a), jsonOf(b))
	}
	for _, f := range []EntityFilter{{}, {Template: TemplateOnly}, {Kinds: []EntityKind{KindService}}} {
		a, _ := plain.Entities(f)
		b, _ := q.Entities(f)
		if jsonOf(a.Aggregate) != jsonOf(b.Aggregate) || a.Total != b.Total {
			t.Fatalf("filter %+v: aggregate or total changed", f)
		}
	}
}

func TestEntityListJSONKeepsFields(t *testing.T) {
	l, err := tmplFleet(t).Entities(EntityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(l)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"meta", "total", "count", "limit", "offset", "truncated", "entities", "aggregate"} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %q missing", k)
		}
	}
	if _, ok := m["inferredTemplates"]; ok {
		t.Errorf("inferredTemplates present when nil")
	}
}

func TestInferredTemplatesSurviveBuildOrder(t *testing.T) {
	// Two listers: each keeps its own items and the list stays complete.
	other := NewMemorySource("catalog-u", "idp-catalog", &Collection{Advisory: true, ListsTemplates: true,
		InferredTemplates: []RawInferredTemplate{tmpl("springboot-service")}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, tmplCatalog(tmpl("springboot-service")), other)
	if err != nil {
		t.Fatal(err)
	}
	l := NewQuery(snap).InferredTemplates("")
	if l.Total != 2 || l.Items[0].Source != tmplSource || l.Items[1].Source != "catalog-u" || l.Completeness != CompletenessComplete {
		t.Fatalf("list %+v", l)
	}
}

func TestInferredTemplateSummaryCut(t *testing.T) {
	raw := tmpl("springboot-service")
	raw.Summary = strings.Repeat("é", maxTemplateSummaryRunes+10)
	if s := tmplFleet(t, tmplCatalog(raw)).InferredTemplates("").Items[0].Summary; s != strings.Repeat("é", maxTemplateSummaryRunes) {
		t.Fatalf("summary %d runes", len([]rune(s)))
	}
}
