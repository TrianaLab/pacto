package diff

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Bump is the smallest version change a classification needs.
type Bump string

// The bumps, smallest first.
const (
	BumpPatch Bump = "patch"
	BumpMinor Bump = "minor"
	BumpMajor Bump = "major"
)

// RequiredBump is the smallest bump a change of classification c needs over
// the published version base. Breaking needs a major bump (a minor below 1.0),
// PotentialBreaking at least a minor (never a major: Pacto could not tell) and
// NonBreaking any bump. An unparseable base is treated as 1.0 or later.
func RequiredBump(c Classification, base string) Bump {
	switch c {
	case Breaking:
		if v, err := semver.NewVersion(base); err == nil && v.Major() == 0 {
			return BumpMinor
		}
		return BumpMajor
	case PotentialBreaking:
		return BumpMinor
	}
	return BumpPatch
}

// CheckBump returns nil when next is at least the bump need above base, and an
// error that names the smallest acceptable version otherwise. Both must be
// semver; next must be higher than base.
func CheckBump(base, next string, need Bump) error {
	b, err := semver.NewVersion(base)
	if err != nil {
		return fmt.Errorf("the published version %q is not semver", base)
	}
	n, err := semver.NewVersion(next)
	if err != nil {
		return fmt.Errorf("version %q is not semver", next)
	}
	var floor semver.Version
	switch need {
	case BumpMajor:
		floor = b.IncMajor()
	case BumpMinor:
		floor = b.IncMinor()
	default:
		floor = b.IncPatch()
	}
	// A prerelease of the floor (2.0.0-rc.1) is the same bump as the floor.
	if n.Major() > floor.Major() || n.Major() == floor.Major() && (n.Minor() > floor.Minor() ||
		n.Minor() == floor.Minor() && n.Patch() >= floor.Patch() && n.GreaterThan(b)) {
		return nil
	}
	return fmt.Errorf("%s needs a %s bump over %s: publish %s or higher", next, need, base, floor.String())
}
