package diff

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"sort"
	"strconv"
	"strings"
)

// schemaDirection says which side of an exchange supplies the data a schema
// describes. It has to be carried through the walk because a `required` entry
// means the opposite thing on each side: added to a request it is a new
// obligation on the caller, added to a response it is a stronger guarantee to
// the reader.
type schemaDirection int

const (
	// dirUnknown is for documents that do not say which side supplies the data —
	// a configuration or policy schema, or an AsyncAPI channel one service
	// publishes and another subscribes to. Their `required` changes take the
	// conservative answer in both directions. It is the zero value so a caller
	// that says nothing gets the safe classification.
	dirUnknown schemaDirection = iota
	// dirRequest is data the consumer sends: an OpenAPI request body.
	dirRequest
	// dirResponse is data the provider returns: an OpenAPI response body.
	dirResponse
)

// diffSchema compares two JSON Schema files and returns changes for any
// structural difference: properties, required fields, types, constraints, etc.
func diffSchema(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	if oldFS == nil || newFS == nil || oldPath == "" || newPath == "" {
		return nil
	}

	oldData, oldErr := fs.ReadFile(oldFS, oldPath)
	newData, newErr := fs.ReadFile(newFS, newPath)
	if oldErr != nil || newErr != nil {
		return nil
	}

	var oldDoc, newDoc any
	if json.Unmarshal(oldData, &oldDoc) != nil || json.Unmarshal(newData, &newDoc) != nil {
		return nil
	}

	return diffJSON(dirUnknown, "schema", oldDoc, newDoc)
}

// diffJSON recursively compares two JSON values and produces changes. dir says
// which side supplies the data, which decides how a `required` change reads.
func diffJSON(dir schemaDirection, prefix string, old, new any) []Change {
	if valueEqual(old, new) {
		return nil
	}

	oldMap, oldOk := old.(map[string]any)
	newMap, newOk := new.(map[string]any)

	// Both are objects — recurse into keys.
	if oldOk && newOk {
		return diffJSONObjects(dir, prefix, oldMap, newMap)
	}

	oldArr, oldIsArr := old.([]any)
	newArr, newIsArr := new.([]any)

	// Both are arrays — compare elements.
	if oldIsArr && newIsArr {
		return diffJSONArrays(dir, prefix, oldArr, newArr)
	}

	// Scalar or type mismatch — report as modified.
	return []Change{{
		Path:           prefix,
		Type:           Modified,
		OldValue:       jsonSafe(old),
		NewValue:       jsonSafe(new),
		Classification: classifySchemaChange(dir, prefix, Modified),
		Reason:         fmt.Sprintf("%s changed", prefix),
	}}
}

// jsonSafe returns v with everything encoding/json refuses replaced by
// something it accepts, walking nested maps and slices: the
// map[interface{}]interface{} yaml.v3 decodes a mapping with a non-string key
// into (`example: {200: ok}` is legal YAML), and a non-finite float (`.inf`,
// `.nan`, equally legal in any numeric constraint).
//
// A Change carries the decoded node itself, so one such node anywhere in a spec
// used to make the whole diff.Result unmarshalable: `pacto diff --output-format
// json` printed no diff and the dashboard's /api/diff, which copies the same
// value, answered 500.
func jsonSafe(v any) any {
	switch t := v.(type) {
	case map[any]any:
		return stringKeyedMap(t, jsonSafe)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = jsonSafe(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = jsonSafe(e)
		}
		return out
	case float64:
		// A non-finite float renders as its text form — "+Inf", "-Inf", "NaN".
		// encoding/json refuses the value itself, and rendering it as null would
		// claim the field was absent when it was present and infinite, which is a
		// lie to whoever reads the diff. float64 is the only float case worth
		// having: yaml.v3 and encoding/json both decode every number into it, so a
		// float32 branch beside this one would be reachable from tests alone.
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return strconv.FormatFloat(t, 'g', -1, 64)
		}
	}
	return v
}

func diffJSONObjects(dir schemaDirection, prefix string, old, new map[string]any) []Change {
	var changes []Change
	keys := mergedKeys(old, new)

	for _, k := range keys {
		path := prefix + "." + k
		oldVal, inOld := old[k]
		newVal, inNew := new[k]

		if !inOld {
			changes = append(changes, Change{
				Path:           path,
				Type:           Added,
				NewValue:       jsonSafe(newVal),
				Classification: classifySchemaChange(dir, path, Added),
				Reason:         fmt.Sprintf("%s added", path),
			})
		} else if !inNew {
			changes = append(changes, Change{
				Path:           path,
				Type:           Removed,
				OldValue:       jsonSafe(oldVal),
				Classification: classifySchemaChange(dir, path, Removed),
				Reason:         fmt.Sprintf("%s removed", path),
			})
		} else {
			changes = append(changes, diffJSON(dir, path, oldVal, newVal)...)
		}
	}
	return changes
}

func diffJSONArrays(dir schemaDirection, prefix string, old, new []any) []Change {
	// For small arrays (like `required`), compare as sets of strings.
	oldStrs, newStrs := toStringSlice(old), toStringSlice(new)
	if oldStrs != nil && newStrs != nil {
		return diffStringArrayAsSets(dir, prefix, oldStrs, newStrs)
	}

	// Fallback: positional comparison.
	var changes []Change
	maxLen := max(len(old), len(new))
	for i := 0; i < maxLen; i++ {
		path := fmt.Sprintf("%s[%d]", prefix, i)
		if i >= len(old) {
			changes = append(changes, Change{
				Path: path, Type: Added, NewValue: jsonSafe(new[i]),
				Classification: classifySchemaChange(dir, prefix, Added),
				Reason:         fmt.Sprintf("%s[%d] added", prefix, i),
			})
		} else if i >= len(new) {
			changes = append(changes, Change{
				Path: path, Type: Removed, OldValue: jsonSafe(old[i]),
				Classification: classifySchemaChange(dir, prefix, Removed),
				Reason:         fmt.Sprintf("%s[%d] removed", prefix, i),
			})
		} else {
			changes = append(changes, diffJSON(dir, path, old[i], new[i])...)
		}
	}
	return changes
}

func diffStringArrayAsSets(dir schemaDirection, prefix string, old, new []string) []Change {
	oldSet := make(map[string]bool, len(old))
	for _, s := range old {
		oldSet[s] = true
	}
	newSet := make(map[string]bool, len(new))
	for _, s := range new {
		newSet[s] = true
	}

	var changes []Change
	for _, s := range new {
		if !oldSet[s] {
			changes = append(changes, Change{
				Path: fmt.Sprintf("%s[%s]", prefix, s), Type: Added, NewValue: s,
				Classification: classifySchemaChange(dir, prefix, Added),
				Reason:         fmt.Sprintf("%s %s added", prefix, s),
			})
		}
	}
	for _, s := range old {
		if !newSet[s] {
			changes = append(changes, Change{
				Path: fmt.Sprintf("%s[%s]", prefix, s), Type: Removed, OldValue: s,
				Classification: classifySchemaChange(dir, prefix, Removed),
				Reason:         fmt.Sprintf("%s %s removed", prefix, s),
			})
		}
	}
	return changes
}

// classifySchemaChange assigns a classification to one difference the JSON walk
// found, from the shape of its path, the change type and which side of the
// exchange supplies the data.
func classifySchemaChange(dir schemaDirection, path string, ct ChangeType) Classification {
	// Removing a whole response header grades PotentialBreaking, so nothing
	// nested inside one may grade Breaking: dropping a single field of a header
	// would otherwise be worse than dropping the header entirely. A header owns a
	// `schema` and may own a `content` block of its own, so both escalations below
	// have to exclude it. The payload sits beside `headers`, never inside it.
	inResponseHeader := dir == dirResponse && strings.Contains(path, ".headers.")
	// The property test comes first because a schema may hold a property
	// literally named `required`, and reading that as the `required` LIST would
	// let dropping it from a request body downgrade to non-breaking.
	if isSchemaProperty(path) {
		// A property a response used to carry is gone from the consumer's point of
		// view whether or not it was ever listed in `required` — most response
		// schemas omit `required` entirely, so gating on it would miss the
		// commonest shape of this break.
		if dir == dirResponse && ct == Removed && !inResponseHeader {
			return Breaking
		}
		return PotentialBreaking
	}
	if strings.HasSuffix(path, ".required") {
		// Relaxing what the caller must send, and strengthening what the provider
		// promises to return, both cost an existing consumer nothing. Every other
		// required transition is breaking — including any on a document whose
		// direction is unknown, where the mirrored reading may be the true one.
		if (dir == dirRequest && ct == Removed) || (dir == dirResponse && ct == Added) {
			return NonBreaking
		}
		return Breaking
	}
	// Dropping a response's whole payload — its `content`, one media type under
	// it, an entire schema, or that schema's whole `properties` block — takes
	// away every field at once. That is strictly worse than dropping one
	// property, so it cannot be graded more leniently.
	if dir == dirResponse && ct == Removed && !inResponseHeader && isResponsePayload(path) {
		return Breaking
	}
	// Other schema changes are potentially breaking by default.
	return PotentialBreaking
}

// mediaTypeFields are the keys OpenAPI allows inside a media type object. They
// are what tells a removed media type apart from a removed field of one, since
// a media type may itself contain dots (application/vnd.api+json) and so cannot
// be found by splitting on the last one.
var mediaTypeFields = []string{"schema", "example", "examples", "encoding"}

// isResponsePayload reports whether path names the payload of a response as a
// whole: its `content` block, one media type inside it, that media type's
// schema, or the `properties` block holding every field of one. A documentation
// field beside the schema (`example`, `examples`) and any other node within the
// schema are not payloads and keep the default grade.
//
// Only a schema reached through `.content.<type>/<subtype>.` counts: a response
// HEADER also has a `schema`, and escalating that graded losing one header's
// schema worse than losing the whole header.
func isResponsePayload(path string) bool {
	if strings.HasSuffix(path, ".content") {
		return true
	}
	i := strings.Index(path, ".content.")
	if i < 0 {
		return false
	}
	tail := path[i+len(".content."):]
	// Every media type carries the slash between its type and subtype.
	if !strings.Contains(tail, "/") {
		return false
	}
	// The whole schema, and the `properties` block of one, each take away at
	// least as much as one property does, and one property is already breaking.
	// The schema has to be the media type's OWN, though: a `schema` reached
	// through an `example` is a schema drawn as documentation — an endpoint that
	// serves JSON Schema documents has one — and rewriting an example changes
	// nothing a consumer parses. So the segments leading up to the schema must
	// name no media type field of their own.
	if i := strings.LastIndex(tail, ".schema"); i >= 0 {
		rest := tail[i+len(".schema"):]
		if (rest == "" || strings.HasSuffix(rest, ".properties")) && !hasMediaTypeField(tail[:i]) {
			return true
		}
	}
	return !hasMediaTypeField(tail)
}

// hasMediaTypeField reports whether s names a media type field or passes
// through one.
func hasMediaTypeField(s string) bool {
	for _, f := range mediaTypeFields {
		if strings.HasSuffix(s, "."+f) || strings.Contains(s, "."+f+".") {
			return true
		}
	}
	return false
}

// isSchemaProperty reports whether path names one entry of a `properties` map,
// i.e. the segment before its last is "properties".
func isSchemaProperty(path string) bool {
	i := strings.LastIndex(path, ".")
	return i > 0 && strings.HasSuffix(path[:i], ".properties")
}

func toStringSlice(arr []any) []string {
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

// valueEqual reports whether two decoded documents are identical. It compares
// JSON encodings, which is the cheap answer for the string-keyed data a JSON
// decoder produces, but encoding/json cannot marshal the
// map[interface{}]interface{} yaml.v3 decodes any mapping with a non-string key
// into (`example: {200: ok}` is legal YAML). Treating that failure as equality
// dropped whole response diffs, so it falls back to the encoding that can
// represent the value instead of guessing.
func valueEqual(a, b any) bool {
	aj, aErr := json.Marshal(a)
	bj, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return yamlEqual(a, b)
	}
	return string(aj) == string(bj)
}

func mergedKeys(a, b map[string]any) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
