package validation

import (
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestValidateInterfaceLifecycle(t *testing.T) {
	cases := map[string]struct {
		lc   contract.InterfaceLifecycle
		code string // "" = valid
	}{
		"deprecated only":          {contract.InterfaceLifecycle{Deprecated: "2026-01-15"}, ""},
		"full local successor":     {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Sunset: "2026-07-01", Successor: "api-v2"}, ""},
		"same-day sunset":          {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Sunset: "2026-01-15"}, ""},
		"url successor":            {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Successor: "oci://ghcr.io/acme/orders-v2"}, ""},
		"bad deprecated":           {contract.InterfaceLifecycle{Deprecated: "2026-1-15"}, "INVALID_INTERFACE_LIFECYCLE"},
		"bad sunset":               {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Sunset: "July"}, "INVALID_INTERFACE_LIFECYCLE"},
		"sunset before deprecated": {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Sunset: "2026-01-14"}, "INVALID_INTERFACE_LIFECYCLE"},
		"unknown successor":        {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Successor: "api-v3"}, "INTERFACE_SUCCESSOR_UNKNOWN"},
		"self successor":           {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Successor: "api"}, "INTERFACE_SUCCESSOR_UNKNOWN"},
		"url without host":         {contract.InterfaceLifecycle{Deprecated: "2026-01-15", Successor: "oci://"}, "INTERFACE_SUCCESSOR_UNKNOWN"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := validV20Contract()
			lc := tc.lc
			c.Interfaces = []contract.Interface{
				{Name: "api", Type: "openapi", Ref: "a.yaml", Lifecycle: &lc},
				{Name: "api-v2", Type: "openapi", Ref: "b.yaml"},
			}
			var result ValidationResult
			validateInterfaceLifecycle(c, &result)
			if tc.code == "" {
				if !result.IsValid() {
					t.Fatalf("want valid, got %+v", result.Errors)
				}
				return
			}
			if !hasErrorCode(result, tc.code) || len(result.Errors) != 1 {
				t.Fatalf("want one %s, got %+v", tc.code, result.Errors)
			}
		})
	}
}

// TestNeeds22Lifecycle: interfaces[].lifecycle is 2.2 only. Under 2.0 and 2.1
// it reports NEEDS_PACTO_2_2 in place of the schema's generic error, the rest of
// the interface is still checked and the caller's data is left alone.
func TestNeeds22Lifecycle(t *testing.T) {
	lc := map[string]any{"deprecated": "2026-01-15", "sunset": "2026-07-01", "successor": "v2"}
	for _, v := range []string{"2.0", "2.1", "2.2"} {
		data := map[string]any{
			"pactoVersion": v,
			"service":      map[string]any{"name": "svc", "version": "1.0.0"},
			"interfaces": []any{
				map[string]any{"name": "v1", "type": "openapi", "ref": "v1.yaml", "lifecycle": lc},
				map[string]any{"name": "v2", "type": "openapi", "ref": "v2.yaml", "visibility": "nonsense"},
			},
		}
		var got []string
		for _, e := range ValidateStructural(data).Errors {
			got = append(got, e.Code+" "+e.Path)
		}
		joined := strings.Join(got, "\n")
		if v == "2.2" {
			if joined != "SCHEMA_VIOLATION interfaces.1.visibility" {
				t.Fatalf("2.2 accepts lifecycle:\n%s", joined)
			}
			continue
		}
		if joined != "NEEDS_PACTO_2_2 interfaces.0.lifecycle\nSCHEMA_VIOLATION interfaces.1.visibility" {
			t.Fatalf("%s:\n%s", v, joined)
		}
		if _, ok := data["interfaces"].([]any)[0].(map[string]any)["lifecycle"]; !ok {
			t.Fatalf("%s: the caller's data was changed", v)
		}
	}
	if r := ValidateStructural(map[string]any{"pactoVersion": "2.1", "service": map[string]any{"name": "svc", "version": "1.0.0"}}); !r.IsValid() {
		t.Fatalf("a 2.1 contract without interfaces: %+v", r.Errors)
	}
}

func TestDeprecatedInterfaceWarnings(t *testing.T) {
	provider := &contract.Contract{Service: contract.Service{Name: "orders", Version: "1.4.0"}, Interfaces: []contract.Interface{
		{Name: "plain", Type: "openapi", Ref: "p.yaml"},
		{Name: "v1", Type: "openapi", Ref: "v1.yaml", Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-15", Sunset: "2026-07-01", Successor: "v2"}},
		{Name: "events", Type: "asyncapi", Ref: "e.yaml", Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-15"}},
		{Name: "bad", Type: "openapi", Ref: "b.yaml", Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-15", Sunset: "soon"}},
	}}
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d.Add(12 * time.Hour) }

	before := DeprecatedInterfaceWarnings("dependencies[0]", provider, day("2026-06-30"))
	if len(before) != 3 {
		t.Fatalf("want three warnings, got %+v", before)
	}
	for _, w := range before {
		if w.Code != "DEPENDENCY_INTERFACE_DEPRECATED" || w.Path != "dependencies[0]" {
			t.Fatalf("before the sunset: %+v", w)
		}
	}
	for _, want := range []string{"orders@1.4.0", "interface v1", "deprecated since 2026-01-15", "sunset 2026-07-01", "successor v2"} {
		if !strings.Contains(before[0].Message, want) {
			t.Fatalf("message %q lacks %q", before[0].Message, want)
		}
	}
	if strings.Contains(before[1].Message, "sunset") || strings.Contains(before[1].Message, "successor") {
		t.Fatalf("no sunset or successor declared: %q", before[1].Message)
	}

	// The sunset is reached on its own day.
	if w := DeprecatedInterfaceWarnings("d", provider, day("2026-06-30")); w[0].Code != "DEPENDENCY_INTERFACE_DEPRECATED" {
		t.Fatalf("the day before the sunset: %+v", w[0])
	}
	after := DeprecatedInterfaceWarnings("d", provider, day("2026-07-01"))
	if after[0].Code != "DEPENDENCY_INTERFACE_SUNSET" || !strings.Contains(after[0].Message, "reached its sunset 2026-07-01") {
		t.Fatalf("on the sunset day: %+v", after[0])
	}
	if after[1].Code != "DEPENDENCY_INTERFACE_DEPRECATED" || after[2].Code != "DEPENDENCY_INTERFACE_DEPRECATED" {
		t.Fatalf("no readable sunset never passes: %+v", after)
	}
	if w := DeprecatedInterfaceWarnings("d", &contract.Contract{}, day("2026-07-02")); w != nil {
		t.Fatalf("nothing deprecated: %+v", w)
	}
}
