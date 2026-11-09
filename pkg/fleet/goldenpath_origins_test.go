package fleet

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// originsFleet is goldenFleet plus lost-cards, whose origin resolves to no
// golden path, and gift-cards made non-compliant.
func originsFleet(t *testing.T) *Query {
	t.Helper()
	lost := createdNamed("lost-cards", "sha256:"+strings.Repeat("0", 64))
	q := goldenFleet(t, lost)
	acmeService(q, "gift-cards").Status = StatusNonCompliant
	return q
}

func TestAggregateOrigins(t *testing.T) {
	q := originsFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Template: TemplateExclude})
	if err != nil {
		t.Fatal(err)
	}
	o := l.Aggregate.Origins
	path := string(NewServiceKeyDomain(acme, "http-service"))
	want := []PathCount{{Key: path, Label: "http-service", Services: 1, NonCompliant: 1}}
	if !slices.Equal(o.ByPath, want) || o.Paths != 1 || o.Beyond != (OriginCount{}) ||
		o.Unresolved != (OriginCount{Services: 1}) || o.None != (OriginCount{Services: 2}) || o.Hidden != 0 {
		t.Fatalf("origins = %+v", o)
	}
	sum := o.Beyond.Services + o.Unresolved.Services + o.None.Services
	for _, p := range o.ByPath {
		sum += p.Services
	}
	if sum != l.Aggregate.Services {
		t.Errorf("origins sum to %d of %d services", sum, l.Aggregate.Services)
	}
	checkOriginFilters(t, q, path)
	// A golden path is in no bucket: its own list counts no origins.
	paths, err := q.Entities(EntityFilter{Template: TemplateOnly})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths.Aggregate.Origins; len(got.ByPath) != 0 || got.None != (OriginCount{}) {
		t.Errorf("golden path list origins = %+v", got)
	}
	if ref := paths.Entities[0]; ref.Created != 1 || ref.NonCompliant != 1 {
		t.Errorf("path row = %+v", ref)
	}
}

// checkOriginFilters: each bucket's filter lists exactly what it counts.
func checkOriginFilters(t *testing.T, q *Query, path string) {
	t.Helper()
	for value, wantKeys := range map[string][]string{
		path:             {"gift-cards"},
		UnresolvedOrigin: {"lost-cards"},
		NoOrigin:         {"orders", "postgresql"},
	} {
		if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Template: TemplateExclude, CreatedFrom: value, Sort: SortName}); !slices.Equal(got, wantKeys) {
			t.Errorf("createdFrom %s = %v, want %v", value, got, wantKeys)
		}
	}
}

func TestAggregateOriginsRanking(t *testing.T) {
	q := goldenFleet(t)
	byPath := map[string]*OriginCount{}
	for i := range maxPathRanking + 2 {
		k := NewServiceKeyDomain(acme, fmt.Sprintf("path-%02d", i))
		q.snap.Services[k] = &ServiceRecord{Key: k, Name: fmt.Sprintf("path-%02d", i), Template: true, HiddenCreated: 1}
		byPath[string(k)] = &OriginCount{Services: 20 - i, NonCompliant: 1}
	}
	var o OriginTally
	q.rankPaths(&o, byPath)
	if len(o.ByPath) != maxPathRanking || o.Paths != maxPathRanking+2 || o.ByPath[0].Label != "path-00" || o.ByPath[0].Hidden != 1 {
		t.Fatalf("ranking = %+v", o)
	}
	if o.Beyond != (OriginCount{Services: 10 + 9, NonCompliant: 2}) || o.Hidden != maxPathRanking+2 {
		t.Errorf("beyond = %+v, hidden %d", o.Beyond, o.Hidden)
	}
}

func TestAggregateOriginsHidden(t *testing.T) {
	r := goldenRestrictFleet(t, templated("http-service", "1.0.0")).Restrict(shopOnly)
	l, err := r.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Template: TemplateExclude})
	if err != nil {
		t.Fatal(err)
	}
	o := l.Aggregate.Origins
	if len(o.ByPath) != 1 || o.ByPath[0].Services != 1 || o.ByPath[0].Hidden != 1 || o.Hidden != 1 {
		t.Fatalf("origins = %+v", o)
	}
	b, err := json.Marshal(l.Aggregate)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "gift-cards") {
		t.Errorf("the aggregate names a hidden service: %s", b)
	}
}

func TestSortCreated(t *testing.T) {
	q := goldenFleet(t, ociRev(acme, templated("grpc-service", "1.0.0"), nil), ociRev(acme, templated("a-path", "1.0.0"), nil))
	if got := entityKeys(t, q, EntityFilter{Template: TemplateOnly, Sort: SortCreated}); !slices.Equal(got, []string{"http-service", "a-path", "grpc-service"}) {
		t.Errorf("created order = %v", got)
	}
	refs := []EntityRef{{Label: "b", Created: 1}, {Label: "a", Created: 1, NonCompliant: 1}}
	sortEntityList(refs, SortCreated)
	if refs[0].Label != "a" {
		t.Errorf("ties break on non-compliant: %v", refs)
	}
}

func TestCreatedFromSentinelsValidate(t *testing.T) {
	for _, s := range []string{NoOrigin, UnresolvedOrigin, string(NewServiceKeyDomain(acme, "x"))} {
		if !validCreatedFrom(s) {
			t.Errorf("%s rejected", s)
		}
	}
	if validCreatedFrom("a/b/c") {
		t.Error("a/b/c accepted")
	}
}
