package contract

import (
	"testing"
	"time"
)

func TestEnvironmentExpiresAt(t *testing.T) {
	if at, ok := (Environment{Expires: "2026-11-20T00:00:00Z"}).ExpiresAt(); !ok || !at.Equal(time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("valid: %v %v", at, ok)
	}
	for _, s := range []string{"", "2026-11-20", "soon"} {
		if _, ok := (Environment{Expires: s}).ExpiresAt(); ok {
			t.Errorf("%q parsed", s)
		}
	}
}

func TestEnvironmentExpiredAt(t *testing.T) {
	e := Environment{Expires: "2026-11-20T00:00:00Z"}
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	if !e.ExpiredAt(at) || e.ExpiredAt(at.Add(-time.Second)) || e.ExpiredAt(time.Time{}) || (Environment{Expires: "soon"}).ExpiredAt(at) {
		t.Fatal("expired at or after Expires only, never with no time or an unparsed Expires")
	}
}

func TestEnvironmentSummary(t *testing.T) {
	for _, tc := range []struct {
		env  Environment
		want string
	}{
		{Environment{Name: "prod", File: "p.yaml"}, "prod: p.yaml, lifetime not declared"},
		{Environment{Name: "prod", File: "p.yaml", Lifetime: EnvironmentPersistent}, "prod: p.yaml, lifetime persistent"},
		{Environment{Name: "pr-1", File: "e.yaml", Lifetime: EnvironmentTemporary, Expires: "2026-11-20T00:00:00Z", Owner: &Owner{Team: "perf"}},
			"pr-1: e.yaml, lifetime temporary, expires 2026-11-20T00:00:00Z, owner perf"},
	} {
		if got := tc.env.Summary(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

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
		"merge of a scalar":  "app:\n  <<: 1\n",
	} {
		if _, err := ParseEnvironmentValues([]byte(data)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseEnvironmentValues_KeepsDatesAsWritten(t *testing.T) {
	got, err := ParseEnvironmentValues([]byte("chart:\n  since: 2024-01-01\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v := got["chart"].(map[string]any)["since"]; v != "2024-01-01" {
		t.Fatalf("since = %#v, want the text as written", v)
	}
}

func TestParseEnvironmentValues_NonStringKey(t *testing.T) {
	_, err := ParseEnvironmentValues([]byte("chart:\n  1: a\n"))
	if err == nil || err.Error() != `line 2: key 1 is not a string; quote it, as in "1"` {
		t.Fatalf("err = %v", err)
	}
}
