package validation

import "testing"

func TestValueAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		schema map[string]any
		v      any
		want   bool
	}{
		{"integer at its minimum", map[string]any{"type": "integer", "minimum": 10}, 10, true},
		{"integer below its minimum", map[string]any{"type": "integer", "minimum": 10}, 0, false},
		{"string too short", map[string]any{"type": "string", "minLength": 3}, "", false},
		{"string off its pattern", map[string]any{"type": "string", "pattern": "^[a-z]+$"}, "A1", false},
		{"string on its pattern", map[string]any{"type": "string", "pattern": "^[a-z]+$"}, "abc", true},
		{"enum member", map[string]any{"enum": []any{"small", "large"}}, "small", true},
		{"enum outsider", map[string]any{"enum": []any{"small", "large"}}, "medium", false},
		{"empty schema", map[string]any{}, "", true},
		{"schema that does not compile", map[string]any{"type": 7}, "", false},
		{"a reference that cannot resolve standalone", map[string]any{"$ref": "#/$defs/size"}, "", false},
		{"a reference to a local file is never loaded", map[string]any{"$ref": "file:///etc/hosts"}, "", false},
		{"a value JSON cannot encode", map[string]any{}, func() {}, false},
		{"a schema JSON cannot encode", map[string]any{"default": func() {}}, "", false},
	} {
		if got := ValueAccepted(tc.schema, tc.v); got != tc.want {
			t.Errorf("%s: ValueAccepted = %v, want %v", tc.name, got, tc.want)
		}
	}
}
