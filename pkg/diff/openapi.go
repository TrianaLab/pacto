package diff

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true,
	"patch": true, "options": true, "head": true, "trace": true,
}

// diffOpenAPI compares two OpenAPI spec files and returns changes for
// paths, methods, request bodies, and responses.
func diffOpenAPI(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	if oldFS == nil || newFS == nil || oldPath == "" || newPath == "" {
		return nil
	}

	oldSpec, oldErr := readOpenAPISpec(oldFS, oldPath)
	newSpec, newErr := readOpenAPISpec(newFS, newPath)

	if oldErr != nil || newErr != nil {
		return nil
	}

	refs := newSpecRefs(oldFS, newFS, oldPath, newPath)
	changes := refs.merge(diffOpenAPISpecs(refs.at("/paths"), oldSpec, newSpec))
	// webhooks are not compared yet, but a component one uses is still used.
	return append(changes, refs.unused("openapi", "path or webhook", "paths", "webhooks")...)
}

// openAPISpec holds the parsed paths from an OpenAPI document.
type openAPISpec struct {
	Paths map[string]map[string]any
}

// readOpenAPISpec parses an OpenAPI file and extracts paths with their methods.
func readOpenAPISpec(fsys fs.FS, path string) (*openAPISpec, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	return &openAPISpec{Paths: raw.Paths}, nil
}

// diffOpenAPISpecs compares two parsed OpenAPI specs. at stands at `paths`.
func diffOpenAPISpecs(at *refAt, old, new *openAPISpec) []Change {
	var changes []Change

	for _, path := range slices.Sorted(maps.Keys(old.Paths)) {
		oldMethods := old.Paths[path]
		newMethods, exists := new.Paths[path]
		if !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s]", path),
				Type:           Removed,
				OldValue:       path,
				Classification: classify("openapi.paths", Removed),
				Reason:         fmt.Sprintf("API path %s removed", path),
			})
			continue
		}
		changes = append(changes, diffPathMethods(at.child(path), path, oldMethods, newMethods)...)
	}

	for _, path := range slices.Sorted(maps.Keys(new.Paths)) {
		if _, exists := old.Paths[path]; !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s]", path),
				Type:           Added,
				NewValue:       path,
				Classification: classify("openapi.paths", Added),
				Reason:         fmt.Sprintf("API path %s added", path),
			})
		}
	}

	return changes
}

// diffPathMethods compares the HTTP methods within a single API path.
func diffPathMethods(at *refAt, path string, oldMethods, newMethods map[string]any) []Change {
	var changes []Change

	// Path-item-level parameters are shared by every operation under the path
	// (OpenAPI spec), so fold them into each method's effective set below — a
	// shared parameter added/removed/made-required must be diffed, not ignored.
	pathParamsOld := params(at, 0, oldMethods["parameters"])
	pathParamsNew := params(at, 1, newMethods["parameters"])

	for _, method := range slices.Sorted(maps.Keys(oldMethods)) {
		if !httpMethods[method] {
			continue
		}
		oldOp := oldMethods[method]
		upper := strings.ToUpper(method)
		newOp, exists := newMethods[method]
		if !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s]", path, upper),
				Type:           Removed,
				OldValue:       fmt.Sprintf("%s %s", upper, path),
				Classification: classify("openapi.methods", Removed),
				Reason:         fmt.Sprintf("%s %s method removed", upper, path),
			})
			continue
		}
		changes = append(changes, diffOperation(at.child(method), path, upper, oldOp, newOp, pathParamsOld, pathParamsNew)...)
	}

	for _, method := range slices.Sorted(maps.Keys(newMethods)) {
		if !httpMethods[method] {
			continue
		}
		if _, exists := oldMethods[method]; !exists {
			upper := strings.ToUpper(method)
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s]", path, upper),
				Type:           Added,
				NewValue:       fmt.Sprintf("%s %s", upper, path),
				Classification: classify("openapi.methods", Added),
				Reason:         fmt.Sprintf("%s %s method added", upper, path),
			})
		}
	}

	return changes
}

// diffOperation compares two operations (same path + method) for parameter,
// request body, and response changes.
func diffOperation(at *refAt, path, method string, oldOp, newOp any, pathParamsOld, pathParamsNew []param) []Change {
	oldMap := toStringMap(oldOp)
	newMap := toStringMap(newOp)
	if oldMap == nil && newMap == nil {
		return nil
	}

	var changes []Change

	// Compare the EFFECTIVE parameter set: path-item-level parameters overlaid by
	// operation-level ones. indexParams keeps the last entry per name+in, so listing
	// operation-level params after path-level ones makes the operation override the
	// path-level parameter of the same identity (per the OpenAPI spec).
	effOld := append(append([]param{}, pathParamsOld...), params(at, 0, oldMap["parameters"])...)
	effNew := append(append([]param{}, pathParamsNew...), params(at, 1, newMap["parameters"])...)
	changes = append(changes, diffParameters(at, path, method, effOld, effNew)...)

	// Compare request body.
	oldBody, oldHas := oldMap["requestBody"]
	newBody, newHas := newMap["requestBody"]
	bodyPath := fmt.Sprintf("openapi.paths[%s].methods[%s].request-body", path, method)

	if oldHas && !newHas {
		changes = append(changes, Change{
			Path:           bodyPath,
			Type:           Removed,
			OldValue:       fmt.Sprintf("%s %s", method, path),
			Classification: classify("openapi.request-body", Removed),
			Reason:         fmt.Sprintf("%s %s request body removed", method, path),
		})
	} else if !oldHas && newHas {
		changes = append(changes, Change{
			Path:           bodyPath,
			Type:           Added,
			NewValue:       fmt.Sprintf("%s %s", method, path),
			Classification: classify("openapi.request-body", Added),
			Reason:         fmt.Sprintf("%s %s request body added", method, path),
		})
	} else if oldHas && newHas {
		// Deep-diff the request body so field-level changes are classified
		// precisely — notably a newly required property (Breaking, via the schema
		// differ) rather than one shallow POTENTIAL_BREAKING for the whole body.
		changes = append(changes, diffJSON(dirRequest, at.child("requestBody"), bodyPath, oldBody, newBody)...)
	}

	// Compare responses.
	oldResponses := toStringMap(oldMap["responses"])
	newResponses := toStringMap(newMap["responses"])
	changes = append(changes, diffResponses(at.child("responses"), path, method, oldResponses, newResponses)...)

	return changes
}

// paramIdentityKeys are parameter fields already encoded in the diff path.
var paramIdentityKeys = map[string]bool{"name": true, "in": true}

// paramKey returns a unique key for an OpenAPI parameter: "name:in".
func paramKey(param map[string]any) string {
	name, _ := param["name"].(string)
	in, _ := param["in"].(string)
	return name + ":" + in
}

// paramLabel returns a human-readable label like "query param 'filter'".
func paramLabel(param map[string]any) string {
	if ref := refOf(param); ref != "" {
		return "param $ref " + ref + ", which Pacto could not resolve,"
	}
	name, _ := param["name"].(string)
	in, _ := param["in"].(string)
	return fmt.Sprintf("%s param '%s'", in, name)
}

// paramRequired reports whether an OpenAPI parameter is marked required.
func paramRequired(param map[string]any) bool {
	r, _ := param["required"].(bool)
	return r
}

// param is one OpenAPI parameter, its $ref resolved, and where it lives in
// its revision.
type param struct {
	m   map[string]any
	loc string
}

// params lists the parameters v holds, side 0 or 1 of at. A $ref is
// resolved; one Pacto cannot resolve stays a bare reference, which
// indexParams keys by its text.
func params(at *refAt, side int, v any) []param {
	var out []param
	for i, p := range toSlice(v) {
		loc := ""
		if at != nil {
			s := at.refs.side[side]
			loc = fmt.Sprintf("%s/parameters/%d", at.loc[side], i)
			if t, tLoc, err := s.deref(loc, p); err == nil {
				p, loc = t, tLoc
			}
		}
		out = append(out, param{toStringMap(p), loc})
	}
	return out
}

// diffParameters compares operation parameters identified by name+in.
func diffParameters(at *refAt, path, method string, oldParams, newParams []param) []Change {
	oldByKey := indexParams(oldParams)
	newByKey := indexParams(newParams)

	// A parameter Pacto could not resolve may be the one that looks removed
	// or added on the other side, so neither is a confirmed break.
	unknownOld, unknownNew := hasUnresolved(oldByKey), hasUnresolved(newByKey)

	var changes []Change

	for _, key := range slices.Sorted(maps.Keys(oldByKey)) {
		oldParam := oldByKey[key].m
		newP, exists := newByKey[key]
		newParam := newP.m
		if !exists {
			cls := classify("openapi.parameters", Removed)
			if unknownNew {
				cls = PotentialBreaking
			}
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s].parameters[%s]", path, method, key),
				Type:           Removed,
				OldValue:       fmt.Sprintf("%s %s %s", method, path, paramLabel(oldParam)),
				Classification: cls,
				Reason:         fmt.Sprintf("%s %s %s removed", method, path, paramLabel(oldParam)),
			})
			continue
		}
		// A parameter's schema is in request position, so narrowing it (an
		// enum value dropped, a tighter limit) breaks existing callers. Each
		// parameter gets its own walk: its changes fold into this one entry, so
		// a component already walked for another parameter must not hide.
		paramPath := fmt.Sprintf("openapi.paths[%s].methods[%s].parameters[%s]", path, method, key)
		schemaChanges := diffJSON(dirRequest, at.fresh(oldByKey[key].loc+"/schema", newP.loc+"/schema"),
			paramPath+".schema", oldParam["schema"], newParam["schema"])
		if len(schemaChanges) > 0 || !yamlEqual(withoutSchema(oldParam), withoutSchema(newParam)) {
			oldSummary, newSummary := mapDelta(oldParam, newParam, paramIdentityKeys)
			cls := classify("openapi.parameters", Modified)
			// optional -> required is breaking: existing clients omit the parameter.
			if !paramRequired(oldParam) && paramRequired(newParam) {
				cls = Breaking
			}
			for _, c := range schemaChanges {
				cls = max(cls, c.Classification)
			}
			changes = append(changes, Change{
				Path:           paramPath,
				Type:           Modified,
				OldValue:       oldSummary,
				NewValue:       newSummary,
				Classification: cls,
				Reason:         fmt.Sprintf("%s %s %s modified", method, path, paramLabel(oldParam)),
			})
		}
	}

	for _, key := range slices.Sorted(maps.Keys(newByKey)) {
		if _, exists := oldByKey[key]; !exists {
			newParam := newByKey[key].m
			cls := classify("openapi.parameters", Added)
			// adding a required parameter is breaking: existing clients omit it.
			if paramRequired(newParam) && !unknownOld {
				cls = Breaking
			}
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s].parameters[%s]", path, method, key),
				Type:           Added,
				NewValue:       fmt.Sprintf("%s %s %s", method, path, paramLabel(newParam)),
				Classification: cls,
				Reason:         fmt.Sprintf("%s %s %s added", method, path, paramLabel(newParam)),
			})
		}
	}

	return changes
}

func hasUnresolved(params map[string]param) bool {
	for _, p := range params {
		if refOf(p.m) != "" {
			return true
		}
	}
	return false
}

func withoutSchema(m map[string]any) map[string]any {
	m = maps.Clone(m)
	delete(m, "schema")
	return m
}

// indexParams builds a map keyed by "name:in" from a parameter slice. A
// parameter whose $ref did not resolve is keyed by its ref text.
func indexParams(params []param) map[string]param {
	m := make(map[string]param, len(params))
	for _, p := range params {
		key := paramKey(p.m)
		if ref := refOf(p.m); ref != "" {
			key = "$ref:" + ref
		}
		if key != ":" {
			m[key] = p
		}
	}
	return m
}

// diffResponses compares response status codes and their definitions.
func diffResponses(at *refAt, path, method string, oldResp, newResp map[string]any) []Change {
	var changes []Change

	for _, code := range slices.Sorted(maps.Keys(oldResp)) {
		oldVal := oldResp[code]
		respPath := fmt.Sprintf("openapi.paths[%s].methods[%s].responses[%s]", path, method, code)
		newVal, exists := newResp[code]
		if !exists {
			changes = append(changes, Change{
				Path:           respPath,
				Type:           Removed,
				OldValue:       fmt.Sprintf("%s %s %s", method, path, code),
				Classification: classify("openapi.responses", Removed),
				Reason:         fmt.Sprintf("%s %s response %s removed", method, path, code),
			})
			continue
		}
		// Deep-diff the response for the same reason the request body is,
		// and with more at stake: dropping a property a success response
		// guaranteed is the commonest way a provider breaks its consumers,
		// and one flat POTENTIAL_BREAKING for the whole response hides it
		// from the BREAKING-only CI gate. dirResponse mirrors the required
		// semantics, which point the other way on this side.
		changes = append(changes, diffJSON(dirResponse, at.child(code), respPath, oldVal, newVal)...)
	}

	for _, code := range slices.Sorted(maps.Keys(newResp)) {
		if _, exists := oldResp[code]; !exists {
			respPath := fmt.Sprintf("openapi.paths[%s].methods[%s].responses[%s]", path, method, code)
			changes = append(changes, Change{
				Path:           respPath,
				Type:           Added,
				NewValue:       fmt.Sprintf("%s %s %s", method, path, code),
				Classification: classify("openapi.responses", Added),
				Reason:         fmt.Sprintf("%s %s response %s added", method, path, code),
			})
		}
	}

	return changes
}

// toStringMap converts an interface{} to map[string]any.
//
// It has to accept the map[interface{}]interface{} yaml.v3 decodes a mapping
// with a non-string key into, because a spec that writes its status codes
// unquoted (`200:` rather than `"200":`) produces exactly that for `responses`.
// Returning nil for those dropped every response change in such a document.
func toStringMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case map[any]any:
		return stringKeyedMap(m, func(val any) any { return val })
	}
	return nil
}

// stringKeyedMap rewrites a yaml-decoded mapping's keys as strings, passing each
// value through conv.
//
// Two keys of one mapping can stringify to the same string — `1:` and `1.0:`
// side by side decode to an int and a float that print alike, and yaml.v3 keeps
// both, since its duplicate-key check compares the raw scalar text — and only
// one can survive. Entries are visited in sorted order, by stringified key and by
// Go-syntax rendering of the value, and the FIRST in that order wins, so it resolves
// the same way on every run. Ranging the map directly picked a different winner
// per run, which made the same pair of specs diff differently on consecutive
// runs.
func stringKeyedMap(m map[any]any, conv func(any) any) map[string]any {
	type entry struct {
		key string
		val any
	}
	entries := make([]entry, 0, len(m))
	for k, val := range m {
		entries = append(entries, entry{fmt.Sprintf("%v", k), val})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].key != entries[j].key {
			return entries[i].key < entries[j].key
		}
		// %#v, not %v: `true` and `"true"` print alike under %v, so a tie there
		// left the winner to the map iteration again — and those two marshal to
		// different JSON, so the same specs diffed differently run to run.
		return fmt.Sprintf("%#v", entries[i].val) < fmt.Sprintf("%#v", entries[j].val)
	})
	out := make(map[string]any, len(entries))
	for _, e := range entries {
		if _, taken := out[e.key]; !taken {
			out[e.key] = conv(e.val)
		}
	}
	return out
}

// toSlice converts an interface{} to []any.
func toSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// yamlEqual compares two values by serializing to YAML. yaml.v3 produces
// deterministic output with sorted map keys. A value it cannot encode reads as
// CHANGED, never as equal: a swallowed marshal error here would silence a diff
// rather than report a spurious one, and only one of those is recoverable.
func yamlEqual(a, b any) bool {
	aBytes, aErr := yaml.Marshal(a)
	bBytes, bErr := yaml.Marshal(b)
	return aErr == nil && bErr == nil && string(aBytes) == string(bBytes)
}

// flattenMap recursively flattens a map into dot-separated key paths.
// e.g. {"schema": {"type": "string"}} → {"schema.type": "string"}.
func flattenMap(m map[string]any, prefix string) map[string]string {
	out := make(map[string]string)
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok {
			for sk, sv := range flattenMap(sub, key) {
				out[sk] = sv
			}
		} else {
			out[key] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

// mapDelta compares two maps and returns compact summaries showing only the
// properties that differ. Keys in skip are excluded from the output.
func mapDelta(old, new map[string]any, skip map[string]bool) (string, string) {
	oldFlat := flattenMap(old, "")
	newFlat := flattenMap(new, "")

	var oldParts, newParts []string

	// Collect all keys in sorted order for deterministic output.
	var keys []string
	for k := range oldFlat {
		keys = append(keys, k)
	}
	for k := range newFlat {
		if _, ok := oldFlat[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		if skip[k] {
			continue
		}
		ov, inOld := oldFlat[k]
		nv, inNew := newFlat[k]
		if inOld && inNew && ov == nv {
			continue
		}
		if inOld {
			oldParts = append(oldParts, k+"="+ov)
		}
		if inNew {
			newParts = append(newParts, k+"="+nv)
		}
	}

	return strings.Join(oldParts, ", "), strings.Join(newParts, ", ")
}
