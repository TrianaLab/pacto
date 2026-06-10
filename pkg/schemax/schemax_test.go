package schemax

import (
	"reflect"
	"testing"
)

func TestProperties(t *testing.T) {
	tests := []struct {
		name string
		data string
		path string
		want []Property
	}{
		{
			name: "top-level properties with type and default",
			path: "config/schema.json",
			data: `{"properties":{"LOG_LEVEL":{"type":"string","default":"info"},"PORT":{"type":"number"}}}`,
			want: []Property{
				{Key: "LOG_LEVEL", Value: "info", Type: "string"},
				{Key: "PORT", Value: "(any)", Type: "number"},
			},
		},
		{
			name: "keys are sorted",
			path: "s.json",
			data: `{"properties":{"zeta":{"type":"string"},"alpha":{"type":"string"}}}`,
			want: []Property{
				{Key: "alpha", Value: "(any)", Type: "string"},
				{Key: "zeta", Value: "(any)", Type: "string"},
			},
		},
		{
			name: "nested object recurses with dot-notation",
			path: "s.json",
			data: `{"properties":{"cors":{"type":"object","properties":{"enabled":{"type":"boolean","default":true}}}}}`,
			want: []Property{
				{Key: "cors.enabled", Value: "true", Type: "boolean"},
			},
		},
		{
			name: "object type without properties is a leaf",
			path: "s.json",
			data: `{"properties":{"meta":{"type":"object"}}}`,
			want: []Property{
				{Key: "meta", Value: "(any)", Type: "object"},
			},
		},
		{
			name: "object type with non-map properties is a leaf",
			path: "s.json",
			data: `{"properties":{"meta":{"type":"object","properties":"oops"}}}`,
			want: []Property{
				{Key: "meta", Value: "(any)", Type: "object"},
			},
		},
		{
			name: "non-object property value is skipped",
			path: "s.json",
			data: `{"properties":{"good":{"type":"string"},"bad":"notanobject"}}`,
			want: []Property{
				{Key: "good", Value: "(any)", Type: "string"},
			},
		},
		{
			name: "yaml schema (non-json path)",
			path: "config/schema.yaml",
			data: "properties:\n  NAME:\n    type: string\n    default: svc\n",
			want: []Property{
				{Key: "NAME", Value: "svc", Type: "string"},
			},
		},
		{
			name: "no properties key returns nil",
			path: "s.json",
			data: `{"type":"object"}`,
			want: nil,
		},
		{
			name: "properties not a map returns nil",
			path: "s.json",
			data: `{"properties":"oops"}`,
			want: nil,
		},
		{
			name: "unparseable returns nil",
			path: "s.json",
			data: `{not json`,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Properties([]byte(tt.data), tt.path)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Properties() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestValues(t *testing.T) {
	got := Values(map[string]any{
		"s":   "hello",
		"f":   3.5,
		"i":   7,
		"b":   true,
		"n":   nil,
		"obj": map[string]any{"k": "v"},
	})
	want := []Property{
		{Key: "b", Value: "true", Type: "boolean"},
		{Key: "f", Value: "3.5", Type: "number"},
		{Key: "i", Value: "7", Type: "number"},
		{Key: "n", Value: "(any)", Type: "any"},
		{Key: "obj", Value: "map[k:v]", Type: "object"},
		{Key: "s", Value: "hello", Type: "string"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Values() = %#v, want %#v", got, want)
	}
}

func TestValuesEmpty(t *testing.T) {
	got := Values(map[string]any{})
	if len(got) != 0 {
		t.Errorf("Values(empty) = %#v, want empty", got)
	}
}

func TestMeta(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		path      string
		wantTitle string
		wantDesc  string
	}{
		{
			name:      "title and description",
			path:      "policy/schema.json",
			data:      `{"title":"Redis Policy","description":"Redis hardening rules"}`,
			wantTitle: "Redis Policy",
			wantDesc:  "Redis hardening rules",
		},
		{
			name:      "title only",
			path:      "s.json",
			data:      `{"title":"Only Title"}`,
			wantTitle: "Only Title",
			wantDesc:  "",
		},
		{
			name:      "yaml meta",
			path:      "s.yaml",
			data:      "title: Y\ndescription: D\n",
			wantTitle: "Y",
			wantDesc:  "D",
		},
		{
			name:      "missing fields",
			path:      "s.json",
			data:      `{"type":"object"}`,
			wantTitle: "",
			wantDesc:  "",
		},
		{
			name:      "unparseable",
			path:      "s.json",
			data:      `{bad`,
			wantTitle: "",
			wantDesc:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, desc := Meta([]byte(tt.data), tt.path)
			if title != tt.wantTitle || desc != tt.wantDesc {
				t.Errorf("Meta() = (%q, %q), want (%q, %q)", title, desc, tt.wantTitle, tt.wantDesc)
			}
		})
	}
}
