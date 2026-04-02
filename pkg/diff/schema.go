package diff

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
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

	return diffJSON("schema", oldDoc, newDoc)
}

// diffJSON recursively compares two JSON values and produces changes.
func diffJSON(prefix string, old, new any) []Change {
	if jsonEqual(old, new) {
		return nil
	}

	oldMap, oldOk := old.(map[string]any)
	newMap, newOk := new.(map[string]any)

	// Both are objects — recurse into keys.
	if oldOk && newOk {
		return diffJSONObjects(prefix, oldMap, newMap)
	}

	oldArr, oldIsArr := old.([]any)
	newArr, newIsArr := new.([]any)

	// Both are arrays — compare elements.
	if oldIsArr && newIsArr {
		return diffJSONArrays(prefix, oldArr, newArr)
	}

	// Scalar or type mismatch — report as modified.
	return []Change{{
		Path:           prefix,
		Type:           Modified,
		OldValue:       old,
		NewValue:       new,
		Classification: classifySchemaChange(prefix),
		Reason:         fmt.Sprintf("%s changed", prefix),
	}}
}

func diffJSONObjects(prefix string, old, new map[string]any) []Change {
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
				NewValue:       newVal,
				Classification: classifySchemaChange(path),
				Reason:         fmt.Sprintf("%s added", path),
			})
		} else if !inNew {
			changes = append(changes, Change{
				Path:           path,
				Type:           Removed,
				OldValue:       oldVal,
				Classification: classifySchemaChange(path),
				Reason:         fmt.Sprintf("%s removed", path),
			})
		} else {
			changes = append(changes, diffJSON(path, oldVal, newVal)...)
		}
	}
	return changes
}

func diffJSONArrays(prefix string, old, new []any) []Change {
	// For small arrays (like `required`), compare as sets of strings.
	oldStrs, newStrs := toStringSlice(old), toStringSlice(new)
	if oldStrs != nil && newStrs != nil {
		return diffStringArrayAsSets(prefix, oldStrs, newStrs)
	}

	// Fallback: positional comparison.
	var changes []Change
	maxLen := max(len(old), len(new))
	for i := 0; i < maxLen; i++ {
		path := fmt.Sprintf("%s[%d]", prefix, i)
		if i >= len(old) {
			changes = append(changes, Change{
				Path: path, Type: Added, NewValue: new[i],
				Classification: classifySchemaChange(prefix),
				Reason:         fmt.Sprintf("%s[%d] added", prefix, i),
			})
		} else if i >= len(new) {
			changes = append(changes, Change{
				Path: path, Type: Removed, OldValue: old[i],
				Classification: classifySchemaChange(prefix),
				Reason:         fmt.Sprintf("%s[%d] removed", prefix, i),
			})
		} else {
			changes = append(changes, diffJSON(path, old[i], new[i])...)
		}
	}
	return changes
}

func diffStringArrayAsSets(prefix string, old, new []string) []Change {
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
				Classification: classifySchemaChange(prefix),
				Reason:         fmt.Sprintf("%s %s added", prefix, s),
			})
		}
	}
	for _, s := range old {
		if !newSet[s] {
			changes = append(changes, Change{
				Path: fmt.Sprintf("%s[%s]", prefix, s), Type: Removed, OldValue: s,
				Classification: classifySchemaChange(prefix),
				Reason:         fmt.Sprintf("%s %s removed", prefix, s),
			})
		}
	}
	return changes
}

// classifySchemaChange assigns a classification based on the JSON path.
func classifySchemaChange(path string) Classification {
	// Changes to required constraints are breaking (adding or removing
	// required fields affects consumers).
	if len(path) >= len(".required") && path[len(path)-len(".required"):] == ".required" {
		return Breaking
	}
	// Other schema changes are potentially breaking by default.
	return PotentialBreaking
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

func jsonEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
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

// readSchemaProperties reads a JSON Schema and extracts property keys,
// recursively flattening nested objects using dot notation (e.g. "postgres.host").
func readSchemaProperties(fsys fs.FS, path string) (map[string]bool, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}

	props := make(map[string]bool)
	flattenRawProps("", schema.Properties, props)
	return props, nil
}

func flattenRawProps(prefix string, properties map[string]json.RawMessage, out map[string]bool) {
	for name, raw := range properties {
		fullName := prefix + name
		var node struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if json.Unmarshal(raw, &node) == nil && len(node.Properties) > 0 {
			flattenRawProps(fullName+".", node.Properties, out)
		} else {
			out[fullName] = true
		}
	}
}
