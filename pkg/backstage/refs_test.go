package backstage

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestEntityRef(t *testing.T) {
	for ref, want := range map[string][2]string{
		"component:default/ledger": {"component", "ledger"},
		"Component:acme/ledger":    {"component", "acme/ledger"},
		"ledger":                   {"", "ledger"},
		"default/ledger":           {"", "ledger"},
		"resource:db":              {"resource", "db"},
	} {
		if kind, name := EntityRef(ref); kind != want[0] || name != want[1] {
			t.Errorf("EntityRef(%q) = %q, %q, want %q, %q", ref, kind, name, want[0], want[1])
		}
	}
}

func TestOwnerFromRef(t *testing.T) {
	for ref, want := range map[string]contract.Owner{
		"group:default/team-payments": {Team: "team-payments"},
		"group:acme/payments":         {Team: "acme/payments"},
		"user:default/alice":          {DRI: "alice"},
		"user:bob":                    {DRI: "bob"},
		"team-a":                      {Team: "team-a"},
		"group:bad owner":             {},
		"user:bad owner":              {},
		"system:payments":             {},
		"unknown":                     {},
		"group:default/unknown":       {},
		"user:Unknown":                {},
	} {
		got, ok := OwnerFromRef(ref)
		if got.Team != want.Team || got.DRI != want.DRI || ok != !want.IsEmpty() {
			t.Errorf("OwnerFromRef(%q) = %+v, %v, want %+v", ref, got, ok, want)
		}
	}
}

func TestDomainFromRef(t *testing.T) {
	for ref, want := range map[string]string{
		"system:default/Billing": "billing",
		"acme/billing":           "acme-billing",
		"!!!":                    "",
		strings.Repeat("a", 64):  "",
		strings.Repeat("a", 63):  strings.Repeat("a", 63),
	} {
		got, ok := DomainFromRef(ref)
		if got != want || ok != (want != "") {
			t.Errorf("DomainFromRef(%q) = %q, %v, want %q", ref, got, ok, want)
		}
	}
}
