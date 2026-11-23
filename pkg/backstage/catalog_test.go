package backstage

import (
	"encoding/json"
	"fmt"
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
			facts, next, _, err := ReadCatalogPage(page(t, "c2", tc.item), at)
			if err != nil || next != "c2" {
				t.Fatalf("ReadCatalogPage = %v, %q", err, next)
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
	if _, _, _, err := ReadCatalogPage([]byte("{"), at); err == nil {
		t.Error("bad JSON decoded")
	}
	if _, next, _, _ := ReadCatalogPage(page(t, ""), at); next != "" {
		t.Errorf("last page next = %q", next)
	}
}

func TestReadCatalogPageBounds(t *testing.T) {
	deps := make([]string, 80)
	for i := range deps {
		deps[i] = fmt.Sprintf("component:svc-%d", i)
	}
	long := strings.Repeat("a", 300)
	facts, _, trimmed, err := ReadCatalogPage(page(t, "", component("led\x00ger\u202e", "", map[string]string{AnnotationServiceKey: long}, map[string]any{"dependsOn": deps})), time.Now())
	if err != nil || len(facts) != 1 || trimmed != 1 {
		t.Fatalf("ReadCatalogPage = %+v, %d trimmed, %v", facts, trimmed, err)
	}
	f := facts[0]
	if f.Name != "ledger" || len([]rune(string(f.ServiceKey))) != maxFactRunes || len(f.DependsOn) != MaxFactDependsOn {
		t.Errorf("fact = name %q, key %d runes, %d dependsOn", f.Name, len([]rune(string(f.ServiceKey))), len(f.DependsOn))
	}
}
