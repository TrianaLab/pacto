package validation

import (
	"encoding/json"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValueAccepted reports whether the JSON Schema of one property accepts v. A
// schema that does not compile standalone, such as one holding a $ref it
// cannot resolve, accepts nothing; no reference is ever loaded.
func ValueAccepted(schema map[string]any, v any) bool {
	data, err := json.Marshal(schema)
	if err != nil {
		return false
	}
	var doc, value any
	_ = json.Unmarshal(data, &doc)
	c := jsonschema.NewCompiler()
	c.UseLoader(nil)
	_ = c.AddResource("mem:///value-schema.json", doc) // fails only on a bad or repeated URL
	s, err := c.Compile("mem:///value-schema.json")
	if err != nil {
		return false
	}
	if data, err = json.Marshal(v); err != nil {
		return false
	}
	_ = json.Unmarshal(data, &value)
	return s.Validate(value) == nil
}
