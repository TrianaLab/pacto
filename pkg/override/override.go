// Package override merges values into a contract's YAML from CLI flags. It
// applies value files and --set key=value pairs with strict precedence
// (base < files < --set), supporting nested and array-indexed paths while
// preserving the original YAML formatting.
package override

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Overrides holds the override configuration from CLI flags.
type Overrides struct {
	ValueFiles []string // -f / --values file paths
	SetValues  []string // --set key=value pairs
}

// IsEmpty returns true if no overrides are configured.
func (o Overrides) IsEmpty() bool {
	return len(o.ValueFiles) == 0 && len(o.SetValues) == 0
}

// Apply merges overrides into raw YAML data and returns the merged result.
// Precedence (lowest to highest): base YAML < value files (in order) < --set values.
func Apply(base []byte, overrides Overrides) ([]byte, error) {
	if overrides.IsEmpty() {
		return base, nil
	}

	var baseMap map[string]interface{}
	if err := yaml.Unmarshal(base, &baseMap); err != nil {
		return nil, fmt.Errorf("failed to parse base YAML: %w", err)
	}
	if baseMap == nil {
		baseMap = make(map[string]interface{})
	}

	// Apply value files in order.
	for _, f := range overrides.ValueFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("failed to read values file %q: %w", f, err)
		}
		var vals map[string]interface{}
		if err := yaml.Unmarshal(data, &vals); err != nil {
			return nil, fmt.Errorf("failed to parse values file %q: %w", f, err)
		}
		deepMerge(baseMap, vals)
	}

	// Apply --set values.
	for _, sv := range overrides.SetValues {
		key, value, ok := strings.Cut(sv, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --set format %q: expected key=value", sv)
		}
		if err := setNestedValue(baseMap, key, parseValue(value)); err != nil {
			return nil, fmt.Errorf("failed to set %q: %w", key, err)
		}
	}

	// Normalize time.Time values back to date strings before marshaling.
	// yaml.Unmarshal resolves bare date scalars (2099-12-31) to time.Time,
	// and yaml.Marshal formats them as RFC3339 (2099-12-31T00:00:00Z).
	// Contract readiness dates are strict YYYY-MM-DD strings, so we normalize.
	normalizeTimestamps(baseMap)

	return yaml.Marshal(baseMap)
}

// deepMerge recursively merges src into dst. Values in src take precedence.
func deepMerge(dst, src map[string]interface{}) {
	for k, srcVal := range src {
		dstVal, exists := dst[k]
		if !exists {
			dst[k] = srcVal
			continue
		}

		dstMap, dstIsMap := dstVal.(map[string]interface{})
		srcMap, srcIsMap := srcVal.(map[string]interface{})
		if dstIsMap && srcIsMap {
			deepMerge(dstMap, srcMap)
		} else {
			dst[k] = srcVal
		}
	}
}

// setNestedValue sets a value at a dot-separated key path in a nested map.
// Supports array indexing with bracket notation (e.g. "interfaces[0].port").
func setNestedValue(m map[string]interface{}, keyPath string, value interface{}) error {
	parts := splitKeyPath(keyPath)
	current := interface{}(m)
	for i, part := range parts[:len(parts)-1] {
		next, err := traversePart(current, part, parts[:i+1])
		if err != nil {
			return err
		}
		current = next
	}

	return setAtPart(current, parts[len(parts)-1], value)
}

// traversePart resolves a single path segment, returning the next node.
// Creates intermediate maps for non-array key parts that don't exist yet.
func traversePart(current interface{}, part string, contextPath []string) (interface{}, error) {
	obj, ok := current.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cannot traverse into non-object at %q", strings.Join(contextPath[:len(contextPath)-1], "."))
	}

	name, idx, isArray := parseArrayIndex(part)
	if isArray {
		arr, ok := obj[name].([]interface{})
		if !ok {
			return nil, fmt.Errorf("expected array at %q", strings.Join(contextPath, "."))
		}
		if idx < 0 || idx >= len(arr) {
			return nil, fmt.Errorf("index %d out of bounds at %q (length %d)", idx, strings.Join(contextPath, "."), len(arr))
		}
		return arr[idx], nil
	}

	next, exists := obj[name]
	if !exists {
		newMap := make(map[string]interface{})
		obj[name] = newMap
		return newMap, nil
	}
	return next, nil
}

// setAtPart sets a value at the final path segment within the current node.
func setAtPart(current interface{}, part string, value interface{}) error {
	obj, ok := current.(map[string]interface{})
	if !ok {
		return fmt.Errorf("cannot set key in non-object")
	}

	name, idx, isArray := parseArrayIndex(part)
	if !isArray {
		obj[name] = value
		return nil
	}

	arr, ok := obj[name].([]interface{})
	if !ok {
		return fmt.Errorf("expected array at %q", name)
	}
	if idx < 0 || idx >= len(arr) {
		return fmt.Errorf("index %d out of bounds at %q (length %d)", idx, name, len(arr))
	}
	arr[idx] = value
	return nil
}

// splitKeyPath splits a dot-separated key path.
// "service.chart.ref" → ["service", "chart", "ref"]
// "interfaces[0].port" → ["interfaces[0]", "port"]
func splitKeyPath(path string) []string {
	return strings.Split(path, ".")
}

// parseArrayIndex checks if a path part has array notation (e.g. "interfaces[0]").
func parseArrayIndex(part string) (name string, index int, isArray bool) {
	bracketIdx := strings.Index(part, "[")
	if bracketIdx == -1 || !strings.HasSuffix(part, "]") {
		return part, 0, false
	}
	name = part[:bracketIdx]
	idxStr := part[bracketIdx+1 : len(part)-1]
	idx, err := strconv.Atoi(idxStr)
	if err != nil {
		return part, 0, false
	}
	return name, idx, true
}

// parseValue attempts to parse a string value into its most specific type.
// Order: integer → float → boolean → string.
func parseValue(s string) interface{} {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
}

// normalizeTimestamps recursively walks a data structure and converts time.Time
// values to date strings (YYYY-MM-DD) if they represent a bare date (midnight UTC
// with zero sub-second), or to RFC3339 strings otherwise.
// This prevents yaml.Marshal from formatting bare dates as RFC3339 timestamps.
func normalizeTimestamps(v interface{}) interface{} {
	switch val := v.(type) {
	case time.Time:
		// Check if this is a bare date (midnight UTC, no sub-second precision).
		if val.Hour() == 0 && val.Minute() == 0 && val.Second() == 0 && val.Nanosecond() == 0 && val.Location() == time.UTC {
			return val.Format("2006-01-02")
		}
		return val.Format(time.RFC3339)
	case map[string]interface{}:
		for k, mv := range val {
			val[k] = normalizeTimestamps(mv)
		}
		return val
	case []interface{}:
		for i, elem := range val {
			val[i] = normalizeTimestamps(elem)
		}
		return val
	default:
		return v
	}
}
