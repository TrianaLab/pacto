package publish

import (
	"slices"
	"testing"
)

func TestBases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tags    []string
		version string
		want    []string
	}{
		{"final", []string{"1.4.2", "1.5.0", "latest"}, "1.6.0", []string{"1.5.0"}},
		{"final plus candidate", []string{"1.5.0", "2.0.0-rc.1"}, "2.0.0", []string{"1.5.0", "2.0.0-rc.1"}},
		{"candidate only", []string{"2.0.0-rc.1"}, "2.0.0", []string{"2.0.0-rc.1"}},
		{"only tags above", []string{"3.0.0"}, "2.0.0", nil},
		{"equal tag is not a base", []string{"1.4.2", "1.5.0"}, "1.5.0", []string{"1.4.2"}},
		{"empty version is above every tag", []string{"1.4.2", "1.5.0"}, "", []string{"1.5.0"}},
		{"empty version over a candidate", []string{"1.5.0", "2.0.0-rc.1"}, "", []string{"1.5.0", "2.0.0-rc.1"}},
		{"no tags", nil, "1.0.0", nil},
	} {
		if got := Bases(tc.tags, tc.version); !slices.Equal(got, tc.want) {
			t.Errorf("%s: Bases = %v, want %v", tc.name, got, tc.want)
		}
	}
}
