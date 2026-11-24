package schemax

import (
	"encoding/json"
	"slices"
	"testing"
)

func schemaDoc(t *testing.T, s string) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOpenPathsNamesEachOpenPath(t *testing.T) {
	doc := schemaDoc(t, `{"type":"object","properties":{
		"backups":{"type":"object","properties":{"days":{"type":"integer"}}},
		"tags":{"description":"anything"},
		"class":{"type":"string"},
		"labels":{"type":"object","additionalProperties":false,"patternProperties":{"^x-":{"type":"string"}}},
		"extra":{"type":"object","additionalProperties":false,"x-kubernetes-preserve-unknown-fields":true},
		"zones":{"type":"array","items":{"type":"object"}},
		"pair":{"type":"array","prefixItems":[{"type":"string"},true]},
		"mode":{"anyOf":[{"const":"a"},{"type":"object","additionalProperties":false}]},
		"never":false,
		"size":{"enum":["s","m"]}
	}}`)
	want := []string{"schema", "schema.properties.backups", "schema.properties.class", "schema.properties.extra",
		"schema.properties.labels", "schema.properties.pair.prefixItems[1]", "schema.properties.tags", "schema.properties.zones.items"}
	if got := OpenPaths(doc); !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestOpenPathsClosedSchemaEmpty(t *testing.T) {
	doc := schemaDoc(t, `{"type":"object","additionalProperties":false,"required":["class"],"properties":{
		"class":{"type":"string","enum":["small","large"]},
		"storageGB":{"type":"integer","minimum":1,"maximum":100},
		"backup":{"type":"object","additionalProperties":false,"properties":{"on":{"type":"boolean"}}},
		"zones":{"type":"array","items":{"type":"string"}}
	}}`)
	if got := OpenPaths(doc); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestOpenPathsUntypedJunctorMembersOfATypedNodeClosed(t *testing.T) {
	doc := schemaDoc(t, `{"type":"object","additionalProperties":false,
		"properties":{"a":{"type":"string"},"b":{"type":"string"}},
		"oneOf":[{"required":["a"]},{"required":["b"]}],
		"allOf":[{"properties":{"a":{"maxLength":3}}}]}`)
	if got := OpenPaths(doc); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if got := OpenPaths(schemaDoc(t, `{"anyOf":[{}]}`)); !slices.Equal(got, []string{"schema.anyOf[0]"}) {
		t.Fatalf("an untyped node's junctor members still count: got %v", got)
	}
}
