package schemax

import (
	"encoding/json"
	"testing"
)

func TestSchemaxClose(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{
	  "type": "object",
	  "properties": {
	    "nested": {"type": "object", "properties": {"deep": {"type": ["object", "null"]}}},
	    "list": {"type": "array", "items": {"type": "object"}},
	    "tuple": {"type": "array", "prefixItems": [{"type": "object"}]},
	    "map": {"type": "object", "additionalProperties": {"type": "object"}},
	    "open": {"type": "object", "additionalProperties": true},
	    "untyped": {"properties": {"x": {"type": "string"}}},
	    "scalar": {"type": "string"},
	    "neg": {"not": {"type": "object"}}
	  },
	  "patternProperties": {"^p": {"type": "object"}},
	  "$defs": {"d": {"type": "object"}},
	  "definitions": {"e": {"type": "object"}},
	  "allOf": [{"type": "object"}, "not-a-schema"],
	  "anyOf": [{"type": "object"}],
	  "oneOf": [{"type": "object"}]
	}`), &doc); err != nil {
		t.Fatal(err)
	}
	Close(doc)
	at := func(path ...any) map[string]any {
		var n any = doc
		for _, p := range path {
			switch k := p.(type) {
			case string:
				n = n.(map[string]any)[k]
			case int:
				n = n.([]any)[k]
			}
		}
		return n.(map[string]any)
	}
	closed := [][]any{
		{}, {"properties", "nested"}, {"properties", "nested", "properties", "deep"}, {"properties", "list", "items"},
		{"properties", "tuple", "prefixItems", 0}, {"properties", "map", "additionalProperties"},
		{"properties", "neg", "not"}, {"patternProperties", "^p"}, {"$defs", "d"}, {"definitions", "e"},
		{"allOf", 0}, {"anyOf", 0}, {"oneOf", 0},
	}
	for _, p := range closed {
		if got := at(p...)["additionalProperties"]; got != false {
			t.Errorf("%v: additionalProperties = %v, want false", p, got)
		}
	}
	if _, ok := at("properties", "map")["additionalProperties"].(map[string]any); !ok {
		t.Error("an explicit schema-valued additionalProperties was replaced")
	}
	if got := at("properties", "open")["additionalProperties"]; got != true {
		t.Errorf("explicit additionalProperties: true became %v", got)
	}
	for _, p := range [][]any{{"properties", "untyped"}, {"properties", "scalar"}, {"properties", "tuple"}} {
		if _, set := at(p...)["additionalProperties"]; set {
			t.Errorf("%v: a node that is no object was closed", p)
		}
	}
	Close("not a schema") // no panic
}
