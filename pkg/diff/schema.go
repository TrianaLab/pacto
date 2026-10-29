package diff

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"slices"
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
	// dirUnknown is for documents that do not say which side supplies the data:
	// a configuration or policy schema. Their `required` changes take the
	// conservative answer in both directions and every other structural edit is
	// POTENTIAL_BREAKING. It is the zero value so a caller that says nothing gets
	// the safe classification.
	dirUnknown schemaDirection = iota
	// dirRequest is data the consumer sends: an OpenAPI request body.
	dirRequest
	// dirResponse is data the provider returns: an OpenAPI response body.
	dirResponse
	// dirRecord is data writers and readers exchange while old and new code
	// coexist: an AsyncAPI message. A change has to be safe both ways, so
	// widening and narrowing both break.
	dirRecord
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
		Classification: classifySchemaChange(dir, prefix, false, Modified, old, new),
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
				Classification: classifySchemaChange(dir, path, false, Added, nil, newVal),
				Reason:         fmt.Sprintf("%s added", path),
			})
		} else if !inNew {
			changes = append(changes, Change{
				Path:           path,
				Type:           Removed,
				OldValue:       jsonSafe(oldVal),
				Classification: classifySchemaChange(dir, path, false, Removed, oldVal, nil),
				Reason:         fmt.Sprintf("%s removed", path),
			})
		} else {
			changes = append(changes, diffJSON(dir, path, oldVal, newVal)...)
		}
	}
	closedObject(dir, prefix, old, changes)
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
				Classification: classifySchemaChange(dir, prefix, true, Added, nil, new[i]),
				Reason:         fmt.Sprintf("%s[%d] added", prefix, i),
			})
		} else if i >= len(new) {
			changes = append(changes, Change{
				Path: path, Type: Removed, OldValue: jsonSafe(old[i]),
				Classification: classifySchemaChange(dir, prefix, true, Removed, old[i], nil),
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
				Classification: classifySchemaChange(dir, prefix, true, Added, nil, s),
				Reason:         fmt.Sprintf("%s %s added", prefix, s),
			})
		}
	}
	for _, s := range old {
		if !newSet[s] {
			changes = append(changes, Change{
				Path: fmt.Sprintf("%s[%s]", prefix, s), Type: Removed, OldValue: s,
				Classification: classifySchemaChange(dir, prefix, true, Removed, s, nil),
				Reason:         fmt.Sprintf("%s %s removed", prefix, s),
			})
		}
	}
	return changes
}

// classifySchemaChange assigns a classification to one difference the JSON walk
// found, from the shape of its path, the change type, which side of the
// exchange supplies the data and the values on each side. elem says the change
// is one entry of the array at path (an enum value, a oneOf variant) rather
// than the keyword itself.
//
// A doc-only edit never counts. A record, and anything inside a request or
// response schema, is judged by the position model in positional. Everything
// else keeps the path rules below.
func classifySchemaChange(dir schemaDirection, path string, elem bool, ct ChangeType, old, new any) Classification {
	segs := strings.Split(indexRe.ReplaceAllString(path, ""), ".")
	if c, ok := rules[classificationKey{strings.Join(segs, "."), ct}]; ok {
		return c
	}
	if isDocOnly(segs) {
		return NonBreaking
	}
	if isExtension(segs) {
		return PotentialBreaking
	}
	// Removing a whole response header grades PotentialBreaking, so nothing
	// nested inside one may grade Breaking: dropping a single field of a header
	// would otherwise be worse than dropping the header entirely. A header owns a
	// `schema` and may own a `content` block of its own, so both escalations below
	// have to exclude it. The payload sits beside `headers`, never inside it.
	inResponseHeader := dir == dirResponse && strings.Contains(path, ".headers.")
	if dir == dirRecord || (dir != dirUnknown && inSchema(segs)) {
		c := positional(dir, segs, elem, ct, old, new)
		if inResponseHeader {
			c = min(c, PotentialBreaking)
		}
		return c
	}
	// What is left is a configuration or policy schema, which has no direction,
	// and the OpenAPI structure around a request or response schema.
	//
	// The property test comes first because a schema may hold a property
	// literally named `required`, and reading that as the `required` LIST would
	// let dropping it downgrade.
	if isSchemaProperty(path) {
		return PotentialBreaking
	}
	// With no direction to read, the mirrored reading of a `required` entry may
	// be the true one, so both transitions break. Outside a schema the only
	// `required` is a request body's or a header's flag, and flipping it either
	// way breaks somebody.
	if strings.HasSuffix(path, ".required") {
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

// docKeywords annotate a schema or a spec without changing what data is
// valid, so editing one never breaks anybody. Everything nested under one
// (an example's body, a named example) is documentation too.
var docKeywords = map[string]bool{
	"description": true, "title": true, "summary": true, "$comment": true,
	"example": true, "examples": true, "externalDocs": true, "deprecated": true,
}

// isDocOnly reports whether segs passes through a documentation keyword. A
// segment right after `properties` is a property name, so a property called
// `description` is data, not documentation.
func isDocOnly(segs []string) bool {
	for i, s := range segs {
		if docKeywords[s] && (i == 0 || segs[i-1] != "properties") {
			return true
		}
	}
	return false
}

// nameKeys hold maps keyed by a name the author chose, so a key under one
// that starts with x- is a name, not an extension.
//
// ponytail: a fixed list; a name under any other map (an AsyncAPI channel, a
// security scheme) that starts with x- reads as an extension.
var nameKeys = map[string]bool{"properties": true, "patternProperties": true,
	"$defs": true, "definitions": true, "schemas": true, "headers": true}

// isExtension reports whether segs passes through an x- extension keyword.
// Tools read extensions (code generators, gateways), but no reader of the
// data has to, so Pacto cannot tell whether an edit to one breaks anybody:
// it is POTENTIAL_BREAKING in every position.
func isExtension(segs []string) bool {
	for i, s := range segs {
		if strings.HasPrefix(s, "x-") && (i == 0 || !nameKeys[segs[i-1]]) {
			return true
		}
	}
	return false
}

// schemaRoots are the keys that hold a JSON Schema inside an OpenAPI or MCP
// document. A change below one is a schema change; the root itself, and
// everything around it (a media type, a header, a status code), keeps the path
// rules.
var schemaRoots = map[string]bool{"schema": true, "inputSchema": true, "outputSchema": true}

func inSchema(segs []string) bool {
	return slices.ContainsFunc(segs[:len(segs)-1], func(s string) bool { return schemaRoots[s] })
}

// effect is what a schema edit does to the set of values the schema accepts.
type effect int

const (
	effectUnknown effect = iota // the edit cannot be ranked; it fails closed
	effectNone                  // the accepted set is unchanged
	effectWiden                 // more values are accepted than before
	effectNarrow                // fewer values are accepted than before
)

// positional classifies a schema edit by the position the schema sits in. A
// request is read by the new provider from an old caller, so widening is safe
// and narrowing breaks. A response is read by an old consumer from the new
// provider, so narrowing is safe and widening breaks: a consumer that switches
// exhaustively over an enum has no case for a value added to it. A record
// travels both ways, so only an edit that is neither, such as a new optional
// property, passes. Anything the model cannot rank breaks.
func positional(dir schemaDirection, segs []string, elem bool, ct ChangeType, old, new any) Classification {
	kw := segs[len(segs)-1]
	// Inside `not` or `if` the accepted set inverts, so no ranking holds.
	if slices.ContainsFunc(segs[:len(segs)-1], func(s string) bool { return s == "not" || s == "if" }) {
		return Breaking
	}
	// A property, or a whole properties block, that appears or goes away. Every
	// reader tolerates a field it does not know, so a new one passes (a closed
	// object is the exception, see closedObject). A field gone from a response
	// or a record is data taken from its reader whether or not `required`
	// listed it, because most payload schemas list nothing. A field a request
	// stops declaring is dropped unread, which may or may not matter.
	if !elem && ct != Modified && (kw == "properties" || isSchemaProperty(strings.Join(segs, "."))) {
		switch {
		case ct == Added:
			return NonBreaking
		case dir == dirRequest:
			return PotentialBreaking
		}
		return Breaking
	}
	return byPosition(dir, keywordEffect(kw, elem, ct, old, new))
}

// byPosition turns an effect into a classification for the position dir.
func byPosition(dir schemaDirection, e effect) Classification {
	switch {
	case e == effectNone,
		e == effectWiden && dir == dirRequest,
		e == effectNarrow && dir == dirResponse:
		return NonBreaking
	}
	return Breaking
}

// closedObject regrades the property changes directly under an object whose
// old schema sets additionalProperties: false. Such an object accepts no field
// it does not declare, so declaring one more widens it and dropping one
// narrows it. It only ever raises a grade.
func closedObject(dir schemaDirection, prefix string, old map[string]any, changes []Change) {
	if dir == dirUnknown || old["additionalProperties"] != false {
		return
	}
	props := prefix + ".properties."
	for i, c := range changes {
		name, ok := strings.CutPrefix(c.Path, props)
		if ok && c.Type != Modified && !strings.Contains(name, ".") {
			changes[i].Classification = max(c.Classification, byPosition(dir, grows(c.Type)))
		}
	}
}

// Bounds: an upper bound widens as it grows and a lower bound as it shrinks.
// An absent bound is no bound.
var (
	upperBounds = map[string]bool{"maxLength": true, "maximum": true, "maxItems": true,
		"maxProperties": true, "maxContains": true, "exclusiveMaximum": true}
	lowerBounds = map[string]bool{"minLength": true, "minimum": true, "minItems": true,
		"minProperties": true, "minContains": true, "exclusiveMinimum": true}
	// presenceKeywords constrain by being there: adding one narrows, dropping
	// one widens, and rewriting one cannot be ranked.
	presenceKeywords = map[string]bool{"pattern": true, "format": true, "const": true,
		"enum": true, "multipleOf": true, "allOf": true, "anyOf": true, "oneOf": true,
		"not": true, "contains": true, "propertyNames": true}
)

// keywordEffect says how editing keyword kw changes the accepted set.
func keywordEffect(kw string, elem bool, ct ChangeType, old, new any) effect {
	switch {
	case elem:
		return entryEffect(kw, ct)
	case upperBounds[kw] || lowerBounds[kw]:
		return boundEffect(kw, old, new)
	case kw == "type":
		return typeEffect(old, new)
	case kw == "additionalProperties" || kw == "unevaluatedProperties" || kw == "items":
		return rank(openness(old), openness(new))
	case kw == "uniqueItems":
		return rank(flag(old, true), flag(new, true))
	case kw == "nullable":
		return rank(flag(old, false), flag(new, false))
	case presenceKeywords[kw] && (old == nil || new == nil):
		return rank(presence(old), presence(new))
	}
	return effectUnknown
}

// entryEffect says how adding or removing one entry of keyword kw's array
// changes the accepted set.
func entryEffect(kw string, ct ChangeType) effect {
	switch kw {
	case "enum", "type", "oneOf", "anyOf": // one more value or variant accepted
		return grows(ct)
	case "required", "allOf": // one more condition to meet
		if ct == Added {
			return effectNarrow
		}
		return effectWiden
	}
	return effectUnknown
}

// boundEffect ranks a change to an upper or lower bound.
func boundEffect(kw string, old, new any) effect {
	_, oldFlag := old.(bool)
	_, newFlag := new.(bool)
	switch {
	case (oldFlag || newFlag) && strings.HasPrefix(kw, "exclusive"):
		// OpenAPI 3.0 spells exclusivity as a flag on maximum/minimum.
		return rank(flag(old, true), flag(new, true))
	case upperBounds[kw]:
		return rank(number(old, 1), number(new, 1))
	}
	return rank(number(old, -1), number(new, -1))
}

// grows maps an added array entry to widening and a removed one to narrowing.
func grows(ct ChangeType) effect {
	if ct == Added {
		return effectWiden
	}
	return effectNarrow
}

// rank compares how open a keyword is on each side; NaN cannot be ranked.
func rank(old, new float64) effect {
	switch {
	case math.IsNaN(old) || math.IsNaN(new):
		return effectUnknown
	case new > old:
		return effectWiden
	case new < old:
		return effectNarrow
	}
	return effectNone
}

// number ranks a bound: sign 1 for an upper bound, -1 for a lower one. yaml.v3
// decodes an integer as int, encoding/json as float64.
func number(v any, sign float64) float64 {
	switch n := v.(type) {
	case nil:
		return math.Inf(1)
	case int:
		return sign * float64(n)
	case float64:
		return sign * n
	}
	return math.NaN()
}

// flag ranks a boolean keyword, absent meaning false. narrowsWhenTrue says
// which value accepts less.
func flag(v any, narrowsWhenTrue bool) float64 {
	if v == nil {
		v = false
	}
	b, ok := v.(bool)
	switch {
	case !ok:
		return math.NaN()
	case b == narrowsWhenTrue:
		return 0
	}
	return 1
}

// openness ranks additionalProperties, unevaluatedProperties and items: false
// accepts nothing extra, a schema accepts what it allows, and true or absent
// accepts anything.
func openness(v any) float64 {
	switch t := v.(type) {
	case nil:
		return 2
	case bool:
		return flag(t, false) * 2
	case map[string]any:
		return 1
	}
	return math.NaN()
}

// presence ranks a keyword that constrains by being there.
func presence(v any) float64 {
	if v == nil {
		return 1
	}
	return 0
}

// typeEffect compares two `type` values, a string or a list, absent meaning
// any type. integer is a subset of number.
func typeEffect(old, new any) effect {
	o, oOK := typeSet(old)
	n, nOK := typeSet(new)
	if !oOK || !nOK {
		return effectUnknown
	}
	switch w, s := typesCover(n, o), typesCover(o, n); {
	case w && s:
		return effectNone
	case w:
		return effectWiden
	case s:
		return effectNarrow
	}
	return effectUnknown
}

// typeSet reads a `type` value; nil is the universal set.
func typeSet(v any) (map[string]bool, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case string:
		return map[string]bool{t: true}, true
	case []any:
		if strs := toStringSlice(t); strs != nil {
			set := map[string]bool{}
			for _, s := range strs {
				set[s] = true
			}
			return set, true
		}
	}
	return nil, false
}

// typesCover reports whether every type in b is accepted by a.
func typesCover(a, b map[string]bool) bool {
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	for t := range b {
		if !a[t] && (t != "integer" || !a["number"]) {
			return false
		}
	}
	return true
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
