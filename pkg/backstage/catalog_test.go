package backstage

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// page is a by-query answer holding items, with next as its cursor.
func page(t *testing.T, next string, items ...map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"items": items, "totalItems": len(items), "pageInfo": map[string]any{"nextCursor": next}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// component is a Component entity with spec and annotations.
func component(name, ns string, ann map[string]string, spec map[string]any) map[string]any {
	return map[string]any{"kind": "Component", "metadata": map[string]any{"name": name, "namespace": ns, "annotations": ann}, "spec": spec}
}

func TestReadCatalogPage(t *testing.T) {
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		item map[string]any
		want fleet.RawServiceFact
		none bool
	}{
		{name: "group owner", item: component("ledger", "", nil, map[string]any{"owner": "group:default/team-ledger"}),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger", Owner: contract.Owner{Team: "team-ledger"}}},
		{name: "user owner", item: component("ledger", "", nil, map[string]any{"owner": "user:alice"}),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger", Owner: contract.Owner{DRI: "alice"}}},
		{name: "owner with no kind", item: component("ledger", "", nil, map[string]any{"owner": "team-ledger"}),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger", Owner: contract.Owner{Team: "team-ledger"}}},
		{name: "invalid owner", item: component("ledger", "", nil, map[string]any{"owner": "group:bad owner"}),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger"}},
		{name: "system slug", item: component("ledger", "", nil, map[string]any{"system": "system:default/Payments Core"}),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger", BusinessDomain: "payments-core"}},
		{name: "invalid system", item: component("ledger", "", nil, map[string]any{"system": "---"}),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger"}},
		{name: "dependsOn", item: component("ledger", "", nil, map[string]any{"dependsOn": []string{"component:default/accounts", "component:ops/audit", "resource:default/db", "accounts"}}),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger", DependsOn: []fleet.RawFactRef{
				{Name: "accounts", Entity: "component:default/accounts"}, {Name: "audit", Entity: "component:ops/audit"}}}},
		{name: "other namespace", item: component("ledger", "finance", nil, nil),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:finance/ledger"}},
		{name: "service-key annotation", item: component("ledger", "", map[string]string{AnnotationServiceKey: "ghcr.io/acme/ledger"}, nil),
			want: fleet.RawServiceFact{Name: "ledger", ServiceKey: "ghcr.io/acme/ledger", Entity: "component:default/ledger"}},
		{name: "Pacto export", item: component("ledger", "", map[string]string{AnnotationProvenance: "declared"}, nil),
			want: fleet.RawServiceFact{Name: "ledger", Entity: "component:default/ledger", Exported: true}},
		{name: "not a component", item: map[string]any{"kind": "API", "metadata": map[string]any{"name": "ledger"}}, none: true},
		{name: "no name", item: component("", "", nil, nil), none: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read, err := ReadCatalogPage(page(t, "c2", tc.item), at)
			facts := read.Facts
			if err != nil || read.Next != "c2" {
				t.Fatalf("ReadCatalogPage = %v, %q", err, read.Next)
			}
			if tc.none {
				if len(facts) != 0 {
					t.Fatalf("facts = %+v, want none", facts)
				}
				return
			}
			if len(facts) != 1 || facts[0].ObservedAt == nil || !facts[0].ObservedAt.Equal(at) {
				t.Fatalf("facts = %+v", facts)
			}
			tc.want.ObservedAt = facts[0].ObservedAt
			if got, want := fmt.Sprintf("%+v", facts[0]), fmt.Sprintf("%+v", tc.want); got != want {
				t.Errorf("fact = %s\nwant   %s", got, want)
			}
		})
	}
	if _, err := ReadCatalogPage([]byte("{"), at); err == nil {
		t.Error("bad JSON decoded")
	}
	if read, _ := ReadCatalogPage(page(t, ""), at); read.Next != "" {
		t.Errorf("last page next = %q", read.Next)
	}
}

func TestReadCatalogPageBounds(t *testing.T) {
	deps := make([]string, 80)
	for i := range deps {
		deps[i] = fmt.Sprintf("component:svc-%d", i)
	}
	long := strings.Repeat("a", 300)
	read, err := ReadCatalogPage(page(t, "", component("led\x00ger\u202e", "", map[string]string{AnnotationServiceKey: long}, map[string]any{"dependsOn": deps})), time.Now())
	facts, trimmed := read.Facts, read.Trimmed
	if err != nil || len(facts) != 1 || trimmed != 1 {
		t.Fatalf("ReadCatalogPage = %+v, %d trimmed, %v", facts, trimmed, err)
	}
	f := facts[0]
	if f.Name != "ledger" || len([]rune(string(f.ServiceKey))) != maxFactRunes || len(f.DependsOn) != MaxFactDependsOn {
		t.Errorf("fact = name %q, key %d runes, %d dependsOn", f.Name, len([]rune(string(f.ServiceKey))), len(f.DependsOn))
	}
}

func TestReadCatalogPageTemplates(t *testing.T) {
	data, err := os.ReadFile("testdata/catalog-templates.json")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	read, err := ReadCatalogPage(data, at)
	if err != nil || read.Next != "c2" {
		t.Fatalf("ReadCatalogPage = %v, %q", err, read.Next)
	}
	if len(read.Facts) != 1 || read.Facts[0].Name != "ledger" {
		t.Errorf("facts = %+v, want the one Component", read.Facts)
	}
	want := []fleet.RawInferredTemplate{
		{Name: "springboot-service", Entity: "template:default/springboot-service", Title: "Spring Boot service",
			Summary: "A Spring Boot service with Postgres and CI", Type: "service", Owner: contract.Owner{Team: "platform-team"}, ObservedAt: &at},
		{Name: "docs-site", Entity: "template:web/docs-site", Title: "Docs site", Summary: "Docs site", Type: "website", ObservedAt: &at},
		{Name: "Spring_Boot", Entity: "template:default/Spring_Boot", ObservedAt: &at},
	}
	if got, w := fmt.Sprintf("%+v", deref(read.Templates)), fmt.Sprintf("%+v", deref(want)); got != w {
		t.Errorf("templates = %s\nwant        %s", got, w)
	}
}

// deref drops the ObservedAt pointers so two lists compare by value.
func deref(ts []fleet.RawInferredTemplate) []fleet.RawInferredTemplate {
	out := slices.Clone(ts)
	for i := range out {
		if out[i].ObservedAt == nil {
			continue
		}
		at := *out[i].ObservedAt
		out[i].ObservedAt = nil
		out[i].Summary += " @" + at.Format(time.RFC3339)
	}
	return out
}

// TestReadCatalogPageComponentsUnchanged: Templates on a page change none of
// the facts its Components give.
func TestReadCatalogPageComponentsUnchanged(t *testing.T) {
	at := time.Now()
	comps := []map[string]any{
		component("ledger", "", nil, map[string]any{"owner": "group:default/team-ledger", "dependsOn": []string{"component:accounts"}}),
		component("accounts", "finance", nil, map[string]any{"system": "payments"}),
	}
	tmpl := map[string]any{"apiVersion": "scaffolder.backstage.io/v1beta3", "kind": "Template", "metadata": map[string]any{"name": "ledger"}}
	alone, _ := ReadCatalogPage(page(t, "", comps...), at)
	mixed, _ := ReadCatalogPage(page(t, "", comps[0], tmpl, comps[1]), at)
	if got, want := fmt.Sprintf("%+v", mixed.Facts), fmt.Sprintf("%+v", alone.Facts); got != want || len(mixed.Templates) != 1 {
		t.Errorf("facts with a template = %s\nwant %s (templates %d)", got, want, len(mixed.Templates))
	}
}

func TestScaffolderURLEscapesNamespace(t *testing.T) {
	for _, tc := range []struct{ base, ns, name, want string }{
		{"https://bs.example/", "default", "go-http", "https://bs.example/create/templates/default/go-http"},
		{"https://bs.example", "a/b", "x y", "https://bs.example/create/templates/a%2Fb/x%20y"},
		{"https://bs.example", "..", "x", ""},
		{"https://bs.example", ".", "x", ""},
		{"https://bs.example", "default", "", ""},
		{"https://bs.example", "../..%2F", "x", "https://bs.example/create/templates/..%2F..%252F/x"},
	} {
		got := ScaffolderURL(tc.base, tc.ns, tc.name)
		if got != tc.want {
			t.Errorf("ScaffolderURL(%q, %q, %q) = %q, want %q", tc.base, tc.ns, tc.name, got, tc.want)
		}
		if got != "" && !strings.HasPrefix(got, ScaffolderRoot(tc.base)+"/templates/") {
			t.Errorf("%q does not start with the scaffolder root %q", got, ScaffolderRoot(tc.base))
		}
	}
	if got := ScaffolderRoot("https://bs.example//"); got != "https://bs.example/create" {
		t.Errorf("ScaffolderRoot = %q", got)
	}
}
