package schemax

import (
	"reflect"
	"slices"
	"testing"
)

const paramSchema = `{"type":"object","additionalProperties":false,"required":["engine","class","storageGB"],"properties":{
"engine":{"type":"string","enum":["postgres"],"x-pacto-tier":"contract"},
"class":{"type":"string","enum":["small","large"]},
"storageGB":{"type":"integer","minimum":10,"maximum":500,"default":20,"x-pacto-tier":"tunable"},
"ratio":{"type":["number","null"],"exclusiveMinimum":0,"exclusiveMaximum":1.5},
"name":{"type":"string","minLength":3,"maxLength":63,"pattern":"^[a-z]+$"},
"mode":{"const":"ha"},
"backup":{"type":"object","x-pacto-tier":"tunable","required":["days"],"properties":{"days":{"type":"integer"},"keep":{"type":"integer","x-pacto-tier":"contract"}}},
"secret":{"type":"object","writeOnly":true,"properties":{"password":{"type":"string","default":"x","enum":["x","y"]}}},
"token":{"type":"string","writeOnly":true,"default":"t","enum":["t"]},
"keys":{"type":"array","items":{"type":"string","writeOnly":true},"default":["k"]},
"skip":true}}`

func fp(v float64) *float64 { return &v }
func ip(v int) *int         { return &v }

func byPath(ps []Parameter) map[string]Parameter {
	out := map[string]Parameter{}
	for _, p := range ps {
		out[p.Path] = p
	}
	return out
}

func TestParametersTiersAndBounds(t *testing.T) {
	ps := Parameters(decode(t, paramSchema))
	got := byPath(ps)
	want := map[string]Parameter{
		"engine":    {Path: "engine", Type: "string", Tier: TierContract, Required: true, Enum: []any{"postgres"}},
		"class":     {Path: "class", Type: "string", Tier: TierContract, Required: true, Enum: []any{"small", "large"}},
		"storageGB": {Path: "storageGB", Type: "integer", Tier: TierTunable, Required: true, Minimum: fp(10), Maximum: fp(500), Default: float64(20)},
		"ratio":     {Path: "ratio", Type: "number", Tier: TierContract, ExclusiveMinimum: fp(0), ExclusiveMaximum: fp(1.5)},
		"name":      {Path: "name", Type: "string", Tier: TierContract, MinLength: ip(3), MaxLength: ip(63), Pattern: "^[a-z]+$"},
		"mode":      {Path: "mode", Tier: TierContract, Enum: []any{"ha"}},
	}
	for path, w := range want {
		if !reflect.DeepEqual(got[path], w) {
			t.Errorf("%s = %+v, want %+v", path, got[path], w)
		}
	}
	if _, ok := got["skip"]; ok {
		t.Error("a boolean schema is no parameter")
	}
	if ps[0].Path != "backup.days" {
		t.Errorf("parameters are sorted by path, first = %s", ps[0].Path)
	}
}

func TestParametersUntieredIsContract(t *testing.T) {
	for _, p := range Parameters(decode(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string","x-pacto-tier":"bogus"}}}`)) {
		if p.Tier != TierContract {
			t.Errorf("%s tier = %s, want contract", p.Path, p.Tier)
		}
	}
	if Parameters("not a schema") != nil || Parameters(Doc{"properties": map[string]any{"a": map[string]any{"type": "string"}}})[0].Path != "a" {
		t.Error("Parameters reads a map or a Doc, nothing else")
	}
}

func TestParametersNestedInheritsTier(t *testing.T) {
	got := byPath(Parameters(decode(t, paramSchema)))
	if d := got["backup.days"]; d.Tier != TierTunable || !d.Required {
		t.Errorf("backup.days = %+v, want tunable from its parent, required by it", d)
	}
	if k := got["backup.keep"]; k.Tier != TierContract || k.Required {
		t.Errorf("backup.keep = %+v, want its own contract tier", k)
	}
}

func TestParametersSensitiveUnderWriteOnlyAncestor(t *testing.T) {
	got := byPath(Parameters(decode(t, paramSchema)))
	for _, path := range []string{"secret.password", "token", "keys"} {
		p := got[path]
		if !p.Sensitive || p.Default != nil || p.Enum != nil {
			t.Errorf("%s = %+v, want sensitive with no default or enum", path, p)
		}
	}
	if got["storageGB"].Sensitive {
		t.Error("a sibling of a writeOnly field stays plain")
	}
}

func TestHoldsWriteOnly(t *testing.T) {
	if !HoldsWriteOnly([]any{map[string]any{"writeOnly": true}}) || HoldsWriteOnly(map[string]any{"type": "string"}) {
		t.Error("HoldsWriteOnly")
	}
}

func TestNumberKinds(t *testing.T) {
	for _, v := range []any{int(2), int64(2), uint64(2), float64(2)} {
		if p := number(v); p == nil || *p != 2 {
			t.Errorf("number(%T) = %v", v, p)
		}
	}
	if number("2") != nil || length("2") != nil {
		t.Error("a string is no number")
	}
}

// TestComposedSchemas: a schema OpenPaths reads closed through a junctor is
// named by Composed unless its oneOf or anyOf choices are consts or enums,
// which Parameters reads as the field's enum.
func TestComposedSchemas(t *testing.T) {
	root := map[string]any{"allOf": []any{map[string]any{"type": "object", "additionalProperties": false, "required": []any{"size"},
		"properties": map[string]any{"size": map[string]any{"type": "integer", "x-pacto-tier": "tunable"}}}}}
	if got := Composed(root); !slices.Equal(got, []string{""}) {
		t.Errorf("root allOf = %q", got)
	}
	choice := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"size": map[string]any{"oneOf": []any{map[string]any{"const": "s"}, map[string]any{"enum": []any{"m", "l"}}}},
		"tier": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}}},
	}}
	if got := Composed(choice); !slices.Equal(got, []string{"tier"}) {
		t.Errorf("choices = %q", got)
	}
	if ps := Parameters(choice); len(ps) != 2 || !slices.Equal(ps[0].Enum, []any{"s", "m", "l"}) || ps[1].Enum != nil {
		t.Errorf("parameters = %+v", ps)
	}
	if got := Composed(map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}); got != nil {
		t.Errorf("plain = %q", got)
	}
	if got := Composed(Doc(root)); !slices.Equal(got, []string{""}) {
		t.Errorf("Doc allOf = %q", got)
	}
	if got := Composed(map[string]any{"properties": map[string]any{"a": true}}); got != nil {
		t.Errorf("non-object property = %q", got)
	}
	both := map[string]any{"oneOf": []any{map[string]any{"const": "a"}}, "anyOf": []any{map[string]any{"const": "b"}}}
	if got := choices(both); got != nil {
		t.Errorf("oneOf and anyOf choices = %v", got)
	}
	if got := choices(map[string]any{"allOf": []any{}, "oneOf": []any{map[string]any{"const": "a"}}}); got != nil {
		t.Errorf("allOf choices = %v", got)
	}
}
