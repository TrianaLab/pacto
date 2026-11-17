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

func TestParse(t *testing.T) {
	doc, err := Parse([]byte(`{"title":"T","properties":{"a":{"type":"string"}}}`), "s.json")
	if err != nil {
		t.Errorf("valid JSON: %v", err)
	}
	if title, _ := doc.Meta(); title != "T" || len(doc.Properties()) != 1 {
		t.Errorf("parsed doc = %v", doc)
	}
	if _, err := Parse([]byte("type: object\n"), "s.yaml"); err != nil {
		t.Errorf("valid YAML: %v", err)
	}
	if _, err := Parse([]byte(`{"type":`), "s.json"); err == nil {
		t.Error("broken JSON parsed")
	}
}

func TestSensitiveKey(t *testing.T) {
	for name, want := range map[string]bool{
		"DB_PASSWORD": true, "DB_PASSWD": true, "client_secret": true, "GITHUB_TOKEN": true, "API_KEY": true,
		"AWS_CREDENTIALS": true, "PRIVATE_PEM": true, "SENTRY_DSN": true,
		"PORT": false, "LOG_LEVEL": false, "HOST": false,
		"MONKEY": true, // the documented ceiling of a substring match
	} {
		if got := SensitiveKey(name); got != want {
			t.Errorf("SensitiveKey(%q) = %v, want %v", name, got, want)
		}
	}
}

// A writeOnly property, and every property under a writeOnly object, reads
// (sensitive) whatever its default holds.
func TestPropertiesMaskWriteOnly(t *testing.T) {
	data := `{"properties":{
	  "DB_PASSWORD":{"type":"string","writeOnly":true,"default":"hunter2"},
	  "LOG_LEVEL":{"type":"string","writeOnly":false,"default":"info"},
	  "cache":{"type":"object","properties":{"host":{"type":"string","default":"redis"},"token":{"type":"string","writeOnly":true,"default":"hunter2"}}},
	  "db":{"type":"object","writeOnly":true,"properties":{"password":{"type":"string","default":"hunter2"}}}}}`
	want := []Property{
		{Key: "DB_PASSWORD", Value: "(sensitive)", Type: "string"},
		{Key: "LOG_LEVEL", Value: "info", Type: "string"},
		{Key: "cache.host", Value: "redis", Type: "string"},
		{Key: "cache.token", Value: "(sensitive)", Type: "string"},
		{Key: "db.password", Value: "(sensitive)", Type: "string"},
	}
	if got := Properties([]byte(data), "s.json"); !reflect.DeepEqual(got, want) {
		t.Errorf("Properties() = %+v, want %+v", got, want)
	}
}

// TestSensitiveValue: a URL carries a credential when its userinfo holds a
// non-empty password or a query parameter has a sensitive name; a password
// url.Parse rejects, or a scheme-less user:password@host, still counts.
func TestSensitiveValue(t *testing.T) {
	for v, want := range map[string]bool{
		"postgres://app:pw@db/x":                     true,
		"redis://:pw@cache:6379":                     true,
		"https://api/x?token=abc":                    true,
		"postgres://app:pa%ss@db:5432/x":             true,
		"postgres://app:p%zz@orders-db:5432/x":       true,
		"postgres://app:p w@db/x":                    true,
		"postgres://app:pa/ss@orders-db:5432/orders": true,
		"postgres://app:pa#ss@db:5432/x":             true,
		"redis://:s3cr?t@redis:6379":                 true,
		"app:pw@db:5432/x":                           true,
		"postgres://app@db/x":                        false,
		"postgres://app:@db/x":                       false,
		"http://billing:8080":                        false,
		"localhost:8080":                             false,
		"Note: email me@x.com":                       false,
		"bad%zz":                                     false,
		"info":                                       false,
		"":                                           false,
	} {
		if got := SensitiveValue(v); got != want {
			t.Errorf("SensitiveValue(%q) = %v, want %v", v, got, want)
		}
	}
}
