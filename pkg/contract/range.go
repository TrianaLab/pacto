package contract

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Range represents a parsed semver constraint range.
type Range struct {
	constraint *semver.Constraints
	raw        string
}

// ParseRange parses a semver range string (npm-style: ^, ~, exact, range).
func ParseRange(s string) (Range, error) {
	c, err := semver.NewConstraint(s)
	if err != nil {
		return Range{}, fmt.Errorf("invalid semver range %q: %w", s, err)
	}
	return Range{constraint: c, raw: s}, nil
}

// Contains returns true if the given version string satisfies this range.
func (r Range) Contains(version string) bool {
	v, err := semver.NewVersion(version)
	if err != nil {
		return false
	}
	return r.constraint.Check(v)
}

// String returns the original range string.
func (r Range) String() string {
	return r.raw
}
