package diff

import (
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestDiffGuarantees(t *testing.T) {
	g := func(v contract.Guarantees) *contract.Guarantees { return &v }
	tests := []struct {
		name     string
		old, new *contract.Guarantees
		want     []string // path type classification old -> new
	}{
		{"none", nil, nil, nil},
		{"unchanged", g(contract.Guarantees{Delivery: "at-least-once"}), g(contract.Guarantees{Delivery: "at-least-once"}), nil},
		{"added", nil, g(contract.Guarantees{Idempotent: true}),
			[]string{"interfaces.guarantees added NON_BREAKING <nil> -> api: idempotent true"}},
		{"removed", g(contract.Guarantees{Timeout: "2s"}), nil,
			[]string{"interfaces.guarantees removed BREAKING api: timeout 2s -> <nil>"}},
		{"weakened", g(contract.Guarantees{DedupeWindow: "5m", Ordering: contract.OrderingGlobal}), g(contract.Guarantees{DedupeWindow: "1m", Ordering: contract.OrderingPerKey}),
			[]string{
				"interfaces.guarantees.weakened modified BREAKING api: ordering global -> api: ordering per-key",
				"interfaces.guarantees.weakened modified BREAKING api: dedupeWindow 5m -> api: dedupeWindow 1m",
			}},
		{"strengthened", g(contract.Guarantees{Retention: "24h", Timeout: "2s"}), g(contract.Guarantees{Retention: "168h", Timeout: "1s"}),
			[]string{
				"interfaces.guarantees.strengthened modified NON_BREAKING api: retention 24h -> api: retention 168h",
				"interfaces.guarantees.strengthened modified NON_BREAKING api: timeout 2s -> api: timeout 1s",
			}},
		{"delivery flip is reported once, weakened", g(contract.Guarantees{Delivery: "at-most-once"}), g(contract.Guarantees{Delivery: "at-least-once"}),
			[]string{"interfaces.guarantees.weakened modified BREAKING api: delivery at-most-once -> api: delivery at-least-once"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, c := range diffGuarantees("api", tt.old, tt.new) {
				got = append(got, c.Path+" "+c.Type.String()+" "+c.Classification.String()+" "+str(c.OldValue)+" -> "+str(c.NewValue))
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func str(v any) string {
	if v == nil {
		return "<nil>"
	}
	return v.(string)
}

func TestDiffInterfaces_Guarantees(t *testing.T) {
	old, new := minimalContract(), minimalContract()
	old.Interfaces[0].Guarantees = &contract.Guarantees{Idempotent: true}
	changes := diffInterfaces(old, new, nil, nil)
	if len(changes) != 1 || changes[0].Path != "interfaces.guarantees" || changes[0].Classification != Breaking {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestDiffRequires(t *testing.T) {
	r := []contract.Requirement{{Interface: "events", Guarantees: contract.Guarantees{Delivery: "at-least-once"}}}
	r2 := []contract.Requirement{{Interface: "events", Guarantees: contract.Guarantees{DedupeWindow: "5m"}}}
	tests := []struct {
		name     string
		old, new []contract.Requirement
		want     string
	}{
		{"none", nil, []contract.Requirement{}, ""},
		{"added", nil, r, `added POTENTIAL_BREAKING <nil> -> orders: [{"interface":"events","delivery":"at-least-once"}]`},
		{"removed", r, nil, `removed NON_BREAKING orders: [{"interface":"events","delivery":"at-least-once"}] -> <nil>`},
		{"modified", r, r2, `modified POTENTIAL_BREAKING orders: [{"interface":"events","delivery":"at-least-once"}] -> orders: [{"interface":"events","dedupeWindow":"5m"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ""
			for _, c := range diffRequires("orders", tt.old, tt.new) {
				got = c.Type.String() + " " + c.Classification.String() + " " + str(c.OldValue) + " -> " + str(c.NewValue)
				if c.Path != "dependencies.requires" {
					t.Errorf("path %s", c.Path)
				}
			}
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
	old, new := minimalContract(), minimalContract()
	old.Dependencies = []contract.Dependency{{Name: "orders", Requires: r}}
	new.Dependencies = []contract.Dependency{{Name: "orders"}}
	if changes := diffDependencies(old, new); len(changes) != 1 || changes[0].Path != "dependencies.requires" {
		t.Fatalf("changes = %+v", changes)
	}
}
