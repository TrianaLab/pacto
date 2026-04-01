package contract_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
	"gopkg.in/yaml.v3"
)

// ── Construction & basic accessors ──

func TestOwner_Zero(t *testing.T) {
	var o contract.Owner
	if !o.IsEmpty() {
		t.Error("zero Owner should be empty")
	}
	if o.IsStructured() {
		t.Error("zero Owner should not be structured")
	}
	if o.DisplayString() != "" {
		t.Errorf("zero Owner display = %q; want empty", o.DisplayString())
	}
}

func TestOwner_FromString(t *testing.T) {
	o := contract.NewOwnerFromString("team/payments")
	if o.IsEmpty() {
		t.Error("string Owner should not be empty")
	}
	if o.IsStructured() {
		t.Error("string Owner should not be structured")
	}
	if o.String() != "team/payments" {
		t.Errorf("String() = %q; want team/payments", o.String())
	}
	if o.Team() != "team/payments" {
		t.Errorf("Team() = %q; want team/payments", o.Team())
	}
	if o.DRI() != "" {
		t.Errorf("DRI() = %q; want empty", o.DRI())
	}
	if o.Contacts() != nil {
		t.Error("Contacts() should be nil for string owner")
	}
	if o.DisplayString() != "team/payments" {
		t.Errorf("DisplayString() = %q; want team/payments", o.DisplayString())
	}
}

func TestOwner_FromInfo(t *testing.T) {
	info := contract.OwnerInfo{
		Team: "foundations",
		DRI:  "eduardo.diaz",
		Contacts: []contract.OwnerContact{
			{Type: "email", Value: "foundations@acme.com", Purpose: "ownership"},
		},
	}
	o := contract.NewOwnerFromInfo(info)
	if o.IsEmpty() {
		t.Error("structured Owner should not be empty")
	}
	if !o.IsStructured() {
		t.Error("structured Owner should be structured")
	}
	if o.Team() != "foundations" {
		t.Errorf("Team() = %q; want foundations", o.Team())
	}
	if o.DRI() != "eduardo.diaz" {
		t.Errorf("DRI() = %q; want eduardo.diaz", o.DRI())
	}
	if len(o.Contacts()) != 1 {
		t.Fatalf("Contacts() len = %d; want 1", len(o.Contacts()))
	}
	if o.DisplayString() != "foundations" {
		t.Errorf("DisplayString() = %q; want foundations", o.DisplayString())
	}
}

func TestOwner_FromInfo_DRIOnly(t *testing.T) {
	o := contract.NewOwnerFromInfo(contract.OwnerInfo{DRI: "someone"})
	if o.DisplayString() != "someone" {
		t.Errorf("DisplayString() = %q; want someone", o.DisplayString())
	}
}

// ── Equal ──

func TestOwner_Equal(t *testing.T) {
	tests := []struct {
		name string
		a, b contract.Owner
		want bool
	}{
		{"both empty", contract.Owner{}, contract.Owner{}, true},
		{"same string", contract.NewOwnerFromString("x"), contract.NewOwnerFromString("x"), true},
		{"diff string", contract.NewOwnerFromString("x"), contract.NewOwnerFromString("y"), false},
		{"string vs empty", contract.NewOwnerFromString("x"), contract.Owner{}, false},
		{"string vs struct", contract.NewOwnerFromString("x"), contract.NewOwnerFromInfo(contract.OwnerInfo{Team: "x"}), false},
		{
			"same struct",
			contract.NewOwnerFromInfo(contract.OwnerInfo{Team: "a", DRI: "b"}),
			contract.NewOwnerFromInfo(contract.OwnerInfo{Team: "a", DRI: "b"}),
			true,
		},
		{
			"diff struct team",
			contract.NewOwnerFromInfo(contract.OwnerInfo{Team: "a"}),
			contract.NewOwnerFromInfo(contract.OwnerInfo{Team: "b"}),
			false,
		},
		{
			"diff contacts len",
			contract.NewOwnerFromInfo(contract.OwnerInfo{Team: "a", Contacts: []contract.OwnerContact{{Type: "email", Value: "x"}}}),
			contract.NewOwnerFromInfo(contract.OwnerInfo{Team: "a"}),
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Errorf("Equal() = %v; want %v", got, tt.want)
			}
		})
	}
}

// ── MatchesFilter ──

func TestOwner_MatchesFilter(t *testing.T) {
	strOwner := contract.NewOwnerFromString("team/payments")
	structOwner := contract.NewOwnerFromInfo(contract.OwnerInfo{
		Team: "foundations",
		DRI:  "eduardo.diaz",
		Contacts: []contract.OwnerContact{
			{Type: "email", Value: "foundations@acme.com"},
		},
	})

	tests := []struct {
		name  string
		owner contract.Owner
		query string
		want  bool
	}{
		{"empty owner", contract.Owner{}, "x", false},
		{"string match", strOwner, "pay", true},
		{"string no match", strOwner, "xyz", false},
		{"struct team match", structOwner, "found", true},
		{"struct dri match", structOwner, "eduardo", true},
		{"struct contact match", structOwner, "acme.com", true},
		{"struct no match", structOwner, "xyz", false},
		{"case insensitive", strOwner, "PAY", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.owner.MatchesFilter(tt.query); got != tt.want {
				t.Errorf("MatchesFilter(%q) = %v; want %v", tt.query, got, tt.want)
			}
		})
	}
}

// ── JSON round-trip ──

func TestOwner_JSON_String(t *testing.T) {
	o := contract.NewOwnerFromString("team/payments")
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"team/payments"` {
		t.Errorf("JSON = %s; want string", data)
	}

	var o2 contract.Owner
	if err := json.Unmarshal(data, &o2); err != nil {
		t.Fatal(err)
	}
	if !o.Equal(o2) {
		t.Error("round-trip should be equal")
	}
}

func TestOwner_JSON_Structured(t *testing.T) {
	o := contract.NewOwnerFromInfo(contract.OwnerInfo{
		Team: "foundations",
		DRI:  "eduardo.diaz",
		Contacts: []contract.OwnerContact{
			{Type: "email", Value: "f@acme.com", Purpose: "ownership"},
		},
	})
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}

	var o2 contract.Owner
	if err := json.Unmarshal(data, &o2); err != nil {
		t.Fatal(err)
	}
	if !o.Equal(o2) {
		t.Errorf("round-trip failed: got %+v", o2.Info())
	}
}

func TestOwner_JSON_Null(t *testing.T) {
	var o contract.Owner
	if err := json.Unmarshal([]byte("null"), &o); err != nil {
		t.Fatal(err)
	}
	if !o.IsEmpty() {
		t.Error("null JSON should produce empty Owner")
	}
}

// ── YAML round-trip ──

func TestOwner_YAML_String(t *testing.T) {
	o := contract.NewOwnerFromString("team/payments")
	data, err := yaml.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "team/payments\n" {
		t.Errorf("YAML = %q; want 'team/payments\\n'", got)
	}

	var o2 contract.Owner
	if err := yaml.Unmarshal(data, &o2); err != nil {
		t.Fatal(err)
	}
	if !o.Equal(o2) {
		t.Error("round-trip should be equal")
	}
}

func TestOwner_YAML_Structured(t *testing.T) {
	o := contract.NewOwnerFromInfo(contract.OwnerInfo{
		Team: "foundations",
		DRI:  "eduardo.diaz",
	})
	data, err := yaml.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}

	var o2 contract.Owner
	if err := yaml.Unmarshal(data, &o2); err != nil {
		t.Fatal(err)
	}
	if !o.Equal(o2) {
		t.Error("round-trip should be equal")
	}
}

func TestOwner_Info(t *testing.T) {
	s := contract.NewOwnerFromString("x")
	if s.Info() != nil {
		t.Error("Info() should be nil for string owner")
	}
	info := contract.OwnerInfo{Team: "t"}
	o := contract.NewOwnerFromInfo(info)
	if o.Info() == nil {
		t.Error("Info() should not be nil for structured owner")
	}
}

func TestOwner_DisplayString_NoTeamNoDRI(t *testing.T) {
	o := contract.NewOwnerFromInfo(contract.OwnerInfo{
		Contacts: []contract.OwnerContact{{Type: "email", Value: "x@y"}},
	})
	if o.DisplayString() != "(structured owner)" {
		t.Errorf("DisplayString() = %q; want (structured owner)", o.DisplayString())
	}
}

func TestOwner_Equal_DiffContacts(t *testing.T) {
	a := contract.NewOwnerFromInfo(contract.OwnerInfo{
		Team:     "x",
		Contacts: []contract.OwnerContact{{Type: "email", Value: "a"}},
	})
	b := contract.NewOwnerFromInfo(contract.OwnerInfo{
		Team:     "x",
		Contacts: []contract.OwnerContact{{Type: "email", Value: "b"}},
	})
	if a.Equal(b) {
		t.Error("owners with different contacts should not be equal")
	}
}

func TestOwner_JSON_Empty(t *testing.T) {
	var o contract.Owner
	data, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "null" {
		t.Errorf("JSON = %s; want null", data)
	}
}

func TestOwner_JSON_Invalid(t *testing.T) {
	var o contract.Owner
	err := json.Unmarshal([]byte("123"), &o)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestOwner_YAML_Empty(t *testing.T) {
	var o contract.Owner
	data, err := yaml.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "null\n" {
		t.Errorf("YAML = %q; want null", string(data))
	}
}

func TestOwner_YAML_Invalid(t *testing.T) {
	var o contract.Owner
	// YAML coerces scalars to strings, so use a nested array to trigger error
	err := yaml.Unmarshal([]byte("- [nested]"), &o)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// ── Parse integration: structured owner in pacto.yaml ──

func TestParse_StructuredOwner(t *testing.T) {
	f, err := os.Open("testdata/valid_structured_owner.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	o := c.Service.Owner
	if !o.IsStructured() {
		t.Fatal("expected structured owner")
	}
	if o.Team() != "foundations" {
		t.Errorf("Team() = %q; want foundations", o.Team())
	}
	if o.DRI() != "eduardo.diaz" {
		t.Errorf("DRI() = %q; want eduardo.diaz", o.DRI())
	}
	if len(o.Contacts()) != 2 {
		t.Fatalf("Contacts() len = %d; want 2", len(o.Contacts()))
	}
	if o.Contacts()[0].Type != "email" {
		t.Errorf("contact[0].Type = %q; want email", o.Contacts()[0].Type)
	}
	if o.Contacts()[0].Purpose != "ownership" {
		t.Errorf("contact[0].Purpose = %q; want ownership", o.Contacts()[0].Purpose)
	}
	if o.Contacts()[1].Type != "chat" {
		t.Errorf("contact[1].Type = %q; want chat", o.Contacts()[1].Type)
	}
}

// ── Parse integration: legacy string owner still works ──

func TestParse_StringOwner(t *testing.T) {
	f, err := os.Open("testdata/valid_minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	o := c.Service.Owner
	if o.IsStructured() {
		t.Error("expected legacy string owner")
	}
	if o.String() != "team/payments" {
		t.Errorf("String() = %q; want team/payments", o.String())
	}
}
