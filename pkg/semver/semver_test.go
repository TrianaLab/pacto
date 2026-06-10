package semver

import (
	"reflect"
	"testing"
)

func TestFilter(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"sorts descending", []string{"1.0.0", "2.1.0", "1.2.0"}, []string{"2.1.0", "1.2.0", "1.0.0"}},
		{"drops non-semver", []string{"1.0.0", "latest", "main", "2.0.0", "sha-abc"}, []string{"2.0.0", "1.0.0"}},
		{"preserves v prefix original", []string{"v1.0.0", "v1.2.0"}, []string{"v1.2.0", "v1.0.0"}},
		{"prerelease orders below release", []string{"1.0.0", "1.0.0-rc1"}, []string{"1.0.0", "1.0.0-rc1"}},
		{"all invalid -> nil", []string{"latest", "main"}, nil},
		{"empty -> nil", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Filter(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestLatest(t *testing.T) {
	if got := Latest([]string{"1.0.0", "2.3.0", "1.5.0"}); got != "2.3.0" {
		t.Errorf("Latest = %q, want 2.3.0", got)
	}
	if got := Latest([]string{"latest", "main"}); got != "" {
		t.Errorf("Latest(no semver) = %q, want empty", got)
	}
	if got := Latest(nil); got != "" {
		t.Errorf("Latest(nil) = %q, want empty", got)
	}
}

func TestLessDesc(t *testing.T) {
	// both valid: higher sorts first
	if !LessDesc("2.0.0", "1.0.0") {
		t.Error("2.0.0 should sort before 1.0.0")
	}
	if LessDesc("1.0.0", "2.0.0") {
		t.Error("1.0.0 should not sort before 2.0.0")
	}
	if LessDesc("1.0.0", "1.0.0") {
		t.Error("equal versions: a not before b")
	}
	// valid before invalid
	if !LessDesc("1.0.0", "main") {
		t.Error("valid semver should sort before invalid")
	}
	if LessDesc("main", "1.0.0") {
		t.Error("invalid should not sort before valid")
	}
	// both invalid: reverse-lexicographic
	if !LessDesc("zeta", "alpha") {
		t.Error("both invalid: reverse-lex, zeta before alpha")
	}
	if LessDesc("alpha", "zeta") {
		t.Error("both invalid: alpha not before zeta")
	}
}
