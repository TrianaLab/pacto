package contract

import "testing"

// TestProposedRange: ^MAJOR.MINOR.0 of the lowest strict-semver version,
// never widened across majors, and nothing when there is no version.
func TestProposedRange(t *testing.T) {
	for _, tt := range []struct {
		versions []string
		want     string
	}{
		{nil, ""},
		{[]string{"v2.2"}, ""},
		{[]string{"2.3.1"}, "^2.3.0"},
		{[]string{"2.4.0", "2.3.1"}, "^2.3.0"},
		{[]string{"2.3.1", "3.0.0"}, "^2.3.0"},
		{[]string{"2.4.0-rc.1"}, "^2.4.0"},
		{[]string{"0.3.2"}, "^0.3.0"},
	} {
		if got := ProposedRange(tt.versions); got != tt.want {
			t.Errorf("ProposedRange(%v) = %q, want %q", tt.versions, got, tt.want)
		}
	}
}
