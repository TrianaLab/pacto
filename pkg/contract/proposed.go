package contract

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// ProposedRange is ^MAJOR.MINOR.0 of the lowest strict-semver version, ""
// when there is none. It never widens across majors: the comment lists every
// version, so a person sees the spread.
func ProposedRange(versions []string) string {
	var low *semver.Version
	for _, s := range versions {
		if v, err := semver.StrictNewVersion(s); err == nil && (low == nil || v.LessThan(low)) {
			low = v
		}
	}
	if low == nil {
		return ""
	}
	return fmt.Sprintf("^%d.%d.0", low.Major(), low.Minor())
}
