package fleet

import (
	"errors"
	"slices"
	"testing"
)

func TestEntityFilterTemplate(t *testing.T) {
	q := goldenFleet(t)
	path := NewServiceKeyDomain(acme, "http-service")
	q.snap.Revisions[q.snap.Services[path].Revisions[0]].Provenance = &BundleProvenance{Signature: BundleSignature{Status: SignatureVerified}}
	all := listRefs(t, q, KindService)
	checkPathRefs(t, all, path)
	everyKind := []EntityKind{KindService, KindRevision, KindTarget, KindOwner, KindSource, KindDomain, KindOffering}
	for _, kinds := range [][]EntityKind{nil, everyKind} {
		if got := entityKeys(t, q, EntityFilter{Template: TemplateOnly, Kinds: kinds, Limit: MaxEntityLimit}); !slices.Equal(got, []string{"http-service"}) {
			t.Errorf("only with kinds %v = %v", kinds, got)
		}
	}
	l, err := q.Entities(EntityFilter{Template: TemplateExclude, Kinds: everyKind, Limit: MaxEntityLimit})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[EntityKind]int{}
	for _, ref := range l.Entities {
		kinds[ref.Kind]++
		if ref.Key == string(path) {
			t.Error("exclude kept the golden path")
		}
	}
	if kinds[KindService] != 3 || kinds[KindOwner] == 0 || kinds[KindDomain] == 0 || kinds[KindRevision] != 4 {
		t.Errorf("exclude kinds = %v", kinds)
	}
	q.snap.Revisions[q.snap.Services[path].Revisions[0]].Provenance = nil
	if got := q.serviceListRef(q.snap.Services[path], 0).Signature; got != SignatureUnknown {
		t.Errorf("unchecked signature = %q", got)
	}
}

func TestEntityFilterCreatedFrom(t *testing.T) {
	q := goldenFleet(t)
	path := string(NewServiceKeyDomain(acme, "http-service"))
	if got := entityKeys(t, q, EntityFilter{CreatedFrom: path}); !slices.Equal(got, []string{"gift-cards"}) {
		t.Errorf("created from the path = %v", got)
	}
	r := hiddenPathFleet(t).Restrict(shopOnly)
	var metas []ProductMeta
	for _, key := range []string{path, string(NewServiceKeyDomain(acme, "absent")), string(NewServiceKeyDomain(acme, "postgresql"))} {
		l, err := r.Entities(EntityFilter{CreatedFrom: key})
		if err != nil {
			t.Fatal(err)
		}
		if l.Total != 0 || len(l.Entities) != 0 {
			t.Errorf("created from %s = %+v", key, l.Entities)
		}
		metas = append(metas, l.Meta)
	}
	for _, m := range metas[1:] {
		if m.SnapshotID != metas[0].SnapshotID || m.Completeness != metas[0].Completeness || len(m.Limitations) != len(metas[0].Limitations) {
			t.Errorf("meta differs: %+v vs %+v", m, metas[0])
		}
	}
}

func TestEntityFilterTemplateRejects(t *testing.T) {
	q := goldenFleet(t)
	for _, f := range []EntityFilter{
		{Template: "yes"},
		{Template: TemplateOnly, Kinds: []EntityKind{KindOwner}},
		{CreatedFrom: "a/b/c"},
		{CreatedFrom: "/"},
		{CreatedFrom: string(NewServiceKeyDomain(acme, "http-service")), Kinds: []EntityKind{KindOwner}},
	} {
		var iq *InvalidQueryError
		if _, err := q.Entities(f); !errors.As(err, &iq) {
			t.Errorf("%+v: err = %v", f, err)
		}
	}
}

func TestAggregateMatchesListedGoldenPaths(t *testing.T) {
	q := goldenFleet(t, ociRev(acme, templated("grpc-service", "1.0.0"), nil))
	l, err := q.Entities(EntityFilter{Template: TemplateOnly})
	if err != nil {
		t.Fatal(err)
	}
	if l.Total != 2 || l.Aggregate.Matched != 2 || l.Aggregate.Services != 2 || l.Aggregate.Ownership.Total() != 2 {
		t.Errorf("total %d, aggregate %+v", l.Total, l.Aggregate)
	}
}

func TestSearchTemplate(t *testing.T) {
	q := goldenFleet(t)
	names := func(f SearchFilter) []string {
		t.Helper()
		res, err := q.Search(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range res.Services {
			if h.Template != (h.Name == "http-service") {
				t.Errorf("hit %s template = %v", h.Name, h.Template)
			}
			out = append(out, h.Name)
		}
		if res.Total != len(out) {
			t.Errorf("total %d, listed %d", res.Total, len(out))
		}
		return out
	}
	if got := names(SearchFilter{}); len(got) != 4 || !slices.Contains(got, "http-service") {
		t.Errorf("default = %v", got)
	}
	if got := names(SearchFilter{Template: TemplateOnly}); !slices.Equal(got, []string{"http-service"}) {
		t.Errorf("only = %v", got)
	}
	if got := names(SearchFilter{Template: TemplateExclude}); len(got) != 3 || slices.Contains(got, "http-service") {
		t.Errorf("exclude = %v", got)
	}
	var iq *InvalidQueryError
	if _, err := q.Search(SearchFilter{Template: "all"}); !errors.As(err, &iq) {
		t.Errorf("bad template: %v", err)
	}
}

// checkPathRefs: the four services are listed and only the golden path is
// marked, with one created service and its verified signature.
func checkPathRefs(t *testing.T, refs []EntityRef, path ServiceKey) {
	t.Helper()
	for _, ref := range refs {
		isPath := ref.Key == string(path)
		if ref.Template != isPath || (ref.Created == 1) != isPath || (ref.Signature == SignatureVerified) != isPath {
			t.Errorf("ref %s: template %v, created %d, signature %q", ref.Label, ref.Template, ref.Created, ref.Signature)
		}
	}
	if len(refs) != 4 {
		t.Errorf("an empty template filter lists every service: %d", len(refs))
	}
}
