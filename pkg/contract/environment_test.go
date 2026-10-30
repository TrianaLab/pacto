package contract

import "testing"

func TestDeepMerge(t *testing.T) {
	dst := map[string]any{
		"a": map[string]any{
			"x": 1,
			"y": 2,
		},
		"b": "keep",
	}
	src := map[string]any{
		"a": map[string]any{
			"y": 3,
			"z": 4,
		},
		"c": "new",
	}
	DeepMerge(dst, src)

	a := dst["a"].(map[string]any)
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

func TestParseEnvironmentValues(t *testing.T) {
	got, err := ParseEnvironmentValues([]byte("app:\n  replicas: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v := got["app"].(map[string]any)["replicas"]; v != float64(3) {
		t.Fatalf("replicas = %#v, want float64(3) as JSON decodes it", v)
	}
	if got, err := ParseEnvironmentValues(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty file = %v, %v; want no values", got, err)
	}
	if got, err := ParseEnvironmentValues([]byte(`{"app": {"a": 1}}`)); err != nil || got["app"] == nil {
		t.Fatalf("JSON file = %v, %v", got, err)
	}
	for name, data := range map[string]string{
		"invalid YAML":       "app: [",
		"not a mapping":      "- app\n",
		"non-string key":     "1: a\n",
		"nested numeric key": "app:\n  1: a\n",
	} {
		if _, err := ParseEnvironmentValues([]byte(data)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
