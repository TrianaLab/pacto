package override

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestIsEmpty(t *testing.T) {
	if !(Overrides{}).IsEmpty() {
		t.Error("zero-value Overrides should be empty")
	}
	if (Overrides{SetValues: []string{"a=b"}}).IsEmpty() {
		t.Error("Overrides with SetValues should not be empty")
	}
	if (Overrides{ValueFiles: []string{"f.yaml"}}).IsEmpty() {
		t.Error("Overrides with ValueFiles should not be empty")
	}
}

func TestApply_Empty(t *testing.T) {
	base := []byte("service:\n  name: svc\n")
	out, err := Apply(base, Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(base) {
		t.Error("empty overrides should return base unchanged")
	}
}

func mustParseYAML(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to parse YAML: %v", err)
	}
	return m
}

func TestApply_SetValue(t *testing.T) {
	base := []byte("service:\n  name: svc\n  version: \"1.0.0\"\n")
	out, err := Apply(base, Overrides{SetValues: []string{"service.version=2.0.0"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := mustParseYAML(t, out)
	svc := m["service"].(map[string]interface{})
	if svc["version"] != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %v", svc["version"])
	}
}

func TestApply_SetNewNestedKey(t *testing.T) {
	base := []byte("service:\n  name: svc\n  version: \"1.0.0\"\n")
	out, err := Apply(base, Overrides{SetValues: []string{"service.owner=team-a"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := mustParseYAML(t, out)
	svc := m["service"].(map[string]interface{})
	if svc["owner"] != "team-a" {
		t.Errorf("expected owner team-a, got %v", svc["owner"])
	}
}

func TestApply_ValueFile(t *testing.T) {
	base := []byte("service:\n  name: svc\n  version: \"1.0.0\"\n")

	valuesFile := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(valuesFile, []byte("service:\n  version: \"3.0.0\"\n  owner: team-b\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := Apply(base, Overrides{ValueFiles: []string{valuesFile}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := mustParseYAML(t, out)
	svc := m["service"].(map[string]interface{})
	if svc["version"] != "3.0.0" {
		t.Errorf("expected version 3.0.0, got %v", svc["version"])
	}
	if svc["owner"] != "team-b" {
		t.Errorf("expected owner team-b, got %v", svc["owner"])
	}
	if svc["name"] != "svc" {
		t.Errorf("expected name svc preserved, got %v", svc["name"])
	}
}

func TestApply_Precedence(t *testing.T) {
	base := []byte("service:\n  name: svc\n  version: \"1.0.0\"\n")

	valuesFile := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(valuesFile, []byte("service:\n  version: \"2.0.0\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// --set should win over -f
	out, err := Apply(base, Overrides{
		ValueFiles: []string{valuesFile},
		SetValues:  []string{"service.version=3.0.0"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := mustParseYAML(t, out)
	svc := m["service"].(map[string]interface{})
	if svc["version"] != "3.0.0" {
		t.Errorf("--set should take precedence, got %v", svc["version"])
	}
}

func TestApply_MultipleValueFiles(t *testing.T) {
	base := []byte("service:\n  name: svc\n  version: \"1.0.0\"\n")
	dir := t.TempDir()

	f1 := filepath.Join(dir, "v1.yaml")
	if err := os.WriteFile(f1, []byte("service:\n  version: \"2.0.0\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f2 := filepath.Join(dir, "v2.yaml")
	if err := os.WriteFile(f2, []byte("service:\n  version: \"3.0.0\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Last file wins
	out, err := Apply(base, Overrides{ValueFiles: []string{f1, f2}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := mustParseYAML(t, out)
	svc := m["service"].(map[string]interface{})
	if svc["version"] != "3.0.0" {
		t.Errorf("last file should win, got %v", svc["version"])
	}
}

func TestApply_InvalidSetFormat(t *testing.T) {
	base := []byte("service:\n  name: svc\n")
	_, err := Apply(base, Overrides{SetValues: []string{"no-equals-sign"}})
	if err == nil {
		t.Error("expected error for invalid --set format")
	}
}

func TestApply_MissingValueFile(t *testing.T) {
	base := []byte("service:\n  name: svc\n")
	_, err := Apply(base, Overrides{ValueFiles: []string{"/nonexistent/values.yaml"}})
	if err == nil {
		t.Error("expected error for missing values file")
	}
}

func TestApply_InvalidValueFile(t *testing.T) {
	base := []byte("service:\n  name: svc\n")
	valuesFile := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(valuesFile, []byte(":::invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Apply(base, Overrides{ValueFiles: []string{valuesFile}})
	if err == nil {
		t.Error("expected error for invalid values file")
	}
}

func TestParseValue(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"42", int64(42)},
		{"3.14", 3.14},
		{"true", true},
		{"false", false},
		{"hello", "hello"},
		{"1.0.0", "1.0.0"},
	}
	for _, tt := range tests {
		got := parseValue(tt.input)
		if got != tt.expected {
			t.Errorf("parseValue(%q) = %v (%T), want %v (%T)", tt.input, got, got, tt.expected, tt.expected)
		}
	}
}

func TestSetNestedValue_ArrayIndex(t *testing.T) {
	m := map[string]interface{}{
		"items": []interface{}{"a", "b", "c"},
	}
	if err := setNestedValue(m, "items[1]", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	arr := m["items"].([]interface{})
	if arr[1] != "x" {
		t.Errorf("expected items[1]=x, got %v", arr[1])
	}
}

func TestSetNestedValue_OutOfBounds(t *testing.T) {
	m := map[string]interface{}{
		"items": []interface{}{"a"},
	}
	if err := setNestedValue(m, "items[5]", "x"); err == nil {
		t.Error("expected error for out-of-bounds index")
	}
}

func TestSetNestedValue_TraverseNonObject(t *testing.T) {
	m := map[string]interface{}{
		"key": "scalar",
	}
	if err := setNestedValue(m, "key.nested", "val"); err == nil {
		t.Error("expected error traversing into non-object")
	}
}

func TestSetNestedValue_TraverseArrayNotFound(t *testing.T) {
	m := map[string]interface{}{
		"key": "not-an-array",
	}
	if err := setNestedValue(m, "key[0].nested", "val"); err == nil {
		t.Error("expected error for array traversal on non-array")
	}
}

func TestSetNestedValue_TraverseArrayOutOfBounds(t *testing.T) {
	m := map[string]interface{}{
		"items": []interface{}{"a"},
	}
	if err := setNestedValue(m, "items[5].nested", "val"); err == nil {
		t.Error("expected error for out-of-bounds array traversal")
	}
}

func TestSetNestedValue_SetKeyInNonObject(t *testing.T) {
	m := map[string]interface{}{
		"items": []interface{}{"a", "b"},
	}
	if err := setNestedValue(m, "items[0]", "x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	arr := m["items"].([]interface{})
	if arr[0] != "x" {
		t.Errorf("expected items[0]=x, got %v", arr[0])
	}
}

func TestSetNestedValue_SetArrayOutOfBounds(t *testing.T) {
	m := map[string]interface{}{
		"items": []interface{}{"a"},
	}
	if err := setNestedValue(m, "items[5]", "x"); err == nil {
		t.Error("expected error for out-of-bounds set")
	}
}

func TestSetNestedValue_SetArrayNotArray(t *testing.T) {
	m := map[string]interface{}{
		"key": "scalar",
	}
	if err := setNestedValue(m, "key[0]", "x"); err == nil {
		t.Error("expected error for array set on non-array")
	}
}

func TestSetNestedValue_SingleKey(t *testing.T) {
	m := map[string]interface{}{}
	if err := setNestedValue(m, "key", "val"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["key"] != "val" {
		t.Errorf("expected key=val, got %v", m["key"])
	}
}

func TestSetNestedValue_TraverseArrayThenSet(t *testing.T) {
	m := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"name": "a"},
			map[string]interface{}{"name": "b"},
		},
	}
	if err := setNestedValue(m, "items[1].name", "updated"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	arr := m["items"].([]interface{})
	item := arr[1].(map[string]interface{})
	if item["name"] != "updated" {
		t.Errorf("expected items[1].name=updated, got %v", item["name"])
	}
}

func TestSetNestedValue_TraverseIntoNonObjectViaArray(t *testing.T) {
	// Array element is a scalar, not a map — further traversal should fail.
	m := map[string]interface{}{
		"items": []interface{}{"scalar"},
	}
	// 3 segments: items[0] -> "scalar" (not a map) -> key.sub fails at traversePart
	if err := setNestedValue(m, "items[0].key.sub", "val"); err == nil {
		t.Error("expected error traversing into non-object array element")
	}
}

func TestApply_SetNestedValueError(t *testing.T) {
	// Set a nested key on a scalar — triggers setNestedValue error inside Apply.
	base := []byte("key: scalar\n")
	_, err := Apply(base, Overrides{SetValues: []string{"key.nested=val"}})
	if err == nil {
		t.Error("expected error from setNestedValue in Apply")
	}
}

func TestSetNestedValue_CreateIntermediateMaps(t *testing.T) {
	m := map[string]interface{}{}
	if err := setNestedValue(m, "a.b.c", "val"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := m["a"].(map[string]interface{})
	b := a["b"].(map[string]interface{})
	if b["c"] != "val" {
		t.Errorf("expected a.b.c=val, got %v", b["c"])
	}
}

func TestSplitKeyPath(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"a.b.c", []string{"a", "b", "c"}},
		{"interfaces[0].port", []string{"interfaces[0]", "port"}},
		{"single", []string{"single"}},
	}
	for _, tt := range tests {
		got := splitKeyPath(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("splitKeyPath(%q) = %v, want %v", tt.input, got, tt.expected)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("splitKeyPath(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestDeepMerge(t *testing.T) {
	dst := map[string]interface{}{
		"a": map[string]interface{}{
			"x": 1,
			"y": 2,
		},
		"b": "keep",
	}
	src := map[string]interface{}{
		"a": map[string]interface{}{
			"y": 3,
			"z": 4,
		},
		"c": "new",
	}
	deepMerge(dst, src)

	a := dst["a"].(map[string]interface{})
	if a["x"] != 1 {
		t.Errorf("expected a.x=1, got %v", a["x"])
	}
	if a["y"] != 3 {
		t.Errorf("expected a.y=3 (overridden), got %v", a["y"])
	}
	if a["z"] != 4 {
		t.Errorf("expected a.z=4 (new), got %v", a["z"])
	}
	if dst["b"] != "keep" {
		t.Errorf("expected b=keep, got %v", dst["b"])
	}
	if dst["c"] != "new" {
		t.Errorf("expected c=new, got %v", dst["c"])
	}
}

func TestApply_EmptyBaseYAML(t *testing.T) {
	// Empty YAML produces nil map — should still work.
	out, err := Apply([]byte(""), Overrides{SetValues: []string{"a=b"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := mustParseYAML(t, out)
	if m["a"] != "b" {
		t.Errorf("expected a=b, got %v", m["a"])
	}
}

func TestParseArrayIndex_InvalidIndex(t *testing.T) {
	name, _, isArray := parseArrayIndex("items[abc]")
	if isArray {
		t.Error("expected non-array for invalid index")
	}
	if name != "items[abc]" {
		t.Errorf("expected name items[abc], got %s", name)
	}
}

func TestApply_InvalidBaseYAML(t *testing.T) {
	_, err := Apply([]byte(":::invalid"), Overrides{SetValues: []string{"a=b"}})
	if err == nil {
		t.Error("expected error for invalid base YAML")
	}
}
