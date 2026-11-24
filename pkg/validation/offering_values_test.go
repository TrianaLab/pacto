package validation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/finding"
)

// offeringValuesSchema is a closed offering schema: classes, a tunable
// bounded integer, an untiered enum, a tunable boolean, a nested object and
// sensitive fields.
const offeringValuesSchema = `{
  "type": "object", "additionalProperties": false,
  "properties": {
    "class": {"type": "string", "enum": ["small", "large"]},
    "storageGi": {"type": "integer", "minimum": 10, "maximum": 500, "x-pacto-tier": "tunable"},
    "engine": {"type": "string", "enum": ["postgres"]},
    "backups": {"type": "boolean", "x-pacto-tier": "tunable"},
    "version": {"type": "string", "const": "16"},
    "replicas": {"type": "integer", "exclusiveMinimum": 0},
    "tls": {"type": "object", "additionalProperties": false, "required": ["mode"],
      "properties": {"mode": {"type": "string", "enum": ["off", "require"]}}},
    "label": {"type": "string", "oneOf": [{"type": "string"}, {"minLength": 1}]},
    "zones": {"type": "array", "maxItems": 2, "items": {"type": "string", "enum": ["a", "b", "c"]}},
    "password": {"type": "string", "writeOnly": true},
    "admin": {"type": "object", "writeOnly": true, "additionalProperties": false, "properties": {"user": {"type": "string"}}},
    "dbPassword": {"type": "string"}
  }
}`

func decodeSchema(t *testing.T, text string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// refusal is the part of an OfferingValueRefusal a row asserts.
type refusal struct {
	code    finding.Code
	field   string
	closest any
	allowed []any
}

func short(rs []OfferingValueRefusal) []refusal {
	var out []refusal
	for _, r := range rs {
		out = append(out, refusal{r.Code, r.Field, r.Closest, r.Allowed})
	}
	return out
}

func TestCheckOfferingValues(t *testing.T) {
	schema := decodeSchema(t, offeringValuesSchema)
	for _, tc := range []struct {
		name   string
		values map[string]any
		perEnv map[string]map[string]any
		want   []refusal
	}{
		{"above the maximum", map[string]any{"storageGi": 900}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.storageGi", float64(500), nil}}},
		{"below the minimum", map[string]any{"storageGi": 2}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.storageGi", float64(10), nil}}},
		{"class not offered", map[string]any{"class": "medium"}, nil,
			[]refusal{{finding.CodeClassNotOffered, "values.class", "small", []any{"small", "large"}}}},
		{"near undeclared name", map[string]any{"storegeGi": 20}, nil,
			[]refusal{{finding.CodeParameterNotDeclared, "values.storegeGi", "storageGi", nil}}},
		{"far undeclared name", map[string]any{"zzz": 1}, nil,
			[]refusal{{finding.CodeParameterNotDeclared, "values.zzz", nil, nil}}},
		{"enum miss", map[string]any{"engine": "mysql"}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.engine", "postgres", []any{"postgres"}}}},
		{"const miss", map[string]any{"version": "15"}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.version", "16", nil}}},
		{"wrong type has no closest", map[string]any{"backups": "yes"}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.backups", nil, nil}}},
		{"exclusive bound has no closest", map[string]any{"replicas": 0}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.replicas", nil, nil}}},
		{"an array bound", map[string]any{"zones": []any{"a", "b", "c"}}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.zones", nil, nil}}},
		{"a keyword no parameter code covers still refuses", map[string]any{"label": "x"}, nil,
			[]refusal{{finding.CodeParameterOutOfRange, "values.label", nil, nil}}},
		{"untiered per environment reads contract", nil, map[string]map[string]any{"prod": {"engine": "postgres"}},
			[]refusal{{finding.CodeTierNotTunable, "environments[prod].values.engine", nil, nil}}},
		{"tunable per environment passes", nil, map[string]map[string]any{"prod": {"storageGi": 50}}, nil},
		{"a missing required value is left to the contract", map[string]any{"tls": map[string]any{}}, nil, nil},
		{"valid set", map[string]any{"class": "large", "storageGi": 100, "engine": "postgres", "backups": true}, nil, nil},
		{"several bad values in field order", map[string]any{"zzz": 1, "class": "medium", "storageGi": 900},
			map[string]map[string]any{"prod": {"storageGi": 1}, "dev": {"engine": "postgres"}},
			[]refusal{
				{finding.CodeTierNotTunable, "environments[dev].values.engine", nil, nil},
				{finding.CodeParameterOutOfRange, "environments[prod].values.storageGi", float64(10), nil},
				{finding.CodeClassNotOffered, "values.class", "small", []any{"small", "large"}},
				{finding.CodeParameterOutOfRange, "values.storageGi", float64(500), nil},
				{finding.CodeParameterNotDeclared, "values.zzz", nil, nil},
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := short(CheckOfferingValues(schema, tc.values, tc.perEnv))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

func TestCheckOfferingValuesMessages(t *testing.T) {
	schema := decodeSchema(t, offeringValuesSchema)
	got := CheckOfferingValues(schema, map[string]any{"storageGi": 900, "class": "medium", "zzz": 1},
		map[string]map[string]any{"prod": {"engine": "postgres"}})
	for i, want := range []string{
		"environments[prod].values.engine is contract-tier in this offering, so it cannot change per environment: set it once in values, or ask the offering owner to mark it x-pacto-tier: tunable",
		"values.class names a class this offering does not offer: use one of small, large",
		"values.storageGi is outside this offering's parameters: use a value of type integer, >= 10, <= 500",
		"values.zzz is not a parameter of this offering",
	} {
		if got[i].Message != want {
			t.Errorf("message %d = %q, want %q", i, got[i].Message, want)
		}
	}
}

func TestCheckOfferingValuesRefusesSensitiveLiteral(t *testing.T) {
	schema := decodeSchema(t, offeringValuesSchema)
	got := CheckOfferingValues(schema, map[string]any{
		"password": 12345, "admin": map[string]any{"user": "root-secret"}, "dbPassword": "s3cr3t", "unsetPassword": nil,
	}, map[string]map[string]any{"prod": {"password": "hunter2"}})
	want := []refusal{
		{finding.CodeParameterSensitive, "environments[prod].values.password", nil, nil},
		{finding.CodeParameterSensitive, "values.admin.user", nil, nil},
		{finding.CodeParameterSensitive, "values.dbPassword", nil, nil},
		{finding.CodeParameterSensitive, "values.password", nil, nil},
		{finding.CodeParameterNotDeclared, "values.unsetPassword", nil, nil}, // a null sets no literal
	}
	if !reflect.DeepEqual(short(got), want) {
		t.Fatalf("got %#v\nwant %#v", short(got), want)
	}
	for _, r := range got[:4] {
		if !strings.HasSuffix(r.Message, " is sensitive: set it through a Secret reference, never a literal") {
			t.Errorf("message %q", r.Message)
		}
		for _, secret := range []string{"hunter2", "root-secret", "s3cr3t"} {
			if strings.Contains(r.Message, secret) {
				t.Errorf("message %q echoes the value", r.Message)
			}
		}
	}
}

func TestCheckOfferingValuesNested(t *testing.T) {
	schema := decodeSchema(t, offeringValuesSchema)
	got := short(CheckOfferingValues(schema, map[string]any{"tls": map[string]any{"mode": "strict", "mdoe": "x"}}, nil))
	want := []refusal{
		{finding.CodeParameterNotDeclared, "values.tls.mdoe", "mode", nil},
		{finding.CodeParameterOutOfRange, "values.tls.mode", "off", []any{"off", "require"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	// A violation of the whole set names the values themselves.
	root := decodeSchema(t, `{"type": "object", "additionalProperties": false, "maxProperties": 1,
	  "properties": {"a": {"type": "string"}, "b": {"type": "string"}}}`)
	if got := short(CheckOfferingValues(root, map[string]any{"a": "x", "b": "y"}, nil)); !reflect.DeepEqual(got, []refusal{{finding.CodeParameterOutOfRange, "values", nil, nil}}) {
		t.Fatalf("root: got %#v", got)
	}
}

func TestCheckOfferingValuesOpenSchemaRefusesAll(t *testing.T) {
	for name, text := range map[string]string{
		"open":       `{"type": "object", "properties": {"storageGi": {"type": "integer"}}}`,
		"unreadable": `{"type": "object", "additionalProperties": false, "properties": {"size": {"type": "string", "pattern": "("}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := CheckOfferingValues(decodeSchema(t, text), map[string]any{"storageGi": 20}, nil)
			if len(got) != 1 || got[0].Code != "OfferingSchemaOpen" || got[0].Field != "values" ||
				!strings.HasSuffix(got[0].Message, ": its owner closes it before anyone consumes it") {
				t.Fatalf("got %#v", got)
			}
		})
	}
}
