package contract

import (
	"strings"
	"testing"
)

func TestDidYouMean(t *testing.T) {
	for _, tc := range []struct {
		got        string
		candidates []string
		want       string
	}{
		{"servce", []string{"job", "service", "scheduled"}, ` (did you mean "service"?)`},
		{"persistance", []string{"type", "persistence", "dataCriticality"}, ` (did you mean "persistence"?)`},
		{"banana", []string{"service", "job"}, ""},
		{"", []string{"on"}, ""},
		{"ab", nil, ""},
	} {
		if got := DidYouMean(tc.got, tc.candidates); got != tc.want {
			t.Errorf("DidYouMean(%q) = %q, want %q", tc.got, got, tc.want)
		}
	}
}

func TestSuggestFields(t *testing.T) {
	msg := "line 3: field nmae not found in type contract.Service\nline 4: field x not found in type other.Type"
	got := suggestFields(msg)
	want := "line 3: field nmae not found in type contract.Service (did you mean \"name\"?)\nline 4: field x not found in type other.Type"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParse_SuggestsUnknownField(t *testing.T) {
	_, err := Parse(strings.NewReader("pactoVersion: \"2.0\"\nservice:\n  nmae: svc\n"))
	if err == nil || !strings.Contains(err.Error(), `did you mean "name"?`) {
		t.Errorf("err = %v", err)
	}
}

func TestLocate(t *testing.T) {
	raw := []byte("service:\n  name: svc\ndependencies:\n  - name: a\n    ref: oci://x\n")
	for _, tc := range []struct {
		path      string
		line, col int
		raw       []byte
	}{
		{path: "service.name", line: 2, col: 3},
		{path: "dependencies[0].ref", line: 5, col: 5},
		{path: "dependencies.0.ref", line: 5, col: 5},
		{path: "dependencies[0]", line: 4, col: 5},
		{path: "dependencies[3].ref", line: 3, col: 1},
		{path: "dependencies[x]", line: 3, col: 1},
		{path: "service.name.deeper", line: 2, col: 3},
		{path: "missing", line: 0, col: 0},
		{path: "", line: 0, col: 0},
		{path: "service", raw: []byte("a: [\n"), line: 0, col: 0},
		{path: "service", raw: []byte{}, line: 0, col: 0},
	} {
		in := raw
		if tc.raw != nil {
			in = tc.raw
		}
		if line, col := Locate(in, tc.path); line != tc.line || col != tc.col {
			t.Errorf("Locate(%q) = %d:%d, want %d:%d", tc.path, line, col, tc.line, tc.col)
		}
	}
}
