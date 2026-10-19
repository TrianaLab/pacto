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
	ok, _ := r.Check(version)
	return ok
}

// Check reports whether version satisfies this range, with an error when version is not semver, so a
// caller can tell "outside the range" from "no usable version".
func (r Range) Check(version string) (bool, error) {
	v, err := semver.NewVersion(version)
	if err != nil {
		return false, fmt.Errorf("invalid semver version %q: %w", version, err)
	}
	return r.constraint.Check(v), nil
}

// String returns the original range string.
func (r Range) String() string {
	return r.raw
}
