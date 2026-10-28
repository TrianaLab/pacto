package diff

import (
	"io/fs"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffChecks compares the suites two versions ship, by name. A suite added
// only adds a check. One removed, moved or edited can loosen the very check
// the release ships behind (drop the cases it fails), so a person reviews it:
// PotentialBreaking. Bytes are compared when both bundles are given; the diff
// cannot tell a stricter suite from a weaker one.
func diffChecks(old, new []contract.Check, oldFS, newFS fs.FS) []Change {
	var changes []Change
	for _, o := range old {
		i := slices.IndexFunc(new, func(n contract.Check) bool { return n.Name == o.Name })
		if i < 0 {
			changes = append(changes, newChange("checks", Removed, o.Name, nil))
			continue
		}
		n := new[i]
		if o.File != n.File {
			changes = append(changes, newChange("checks.file", Modified, o.Name+": "+o.File, o.Name+": "+n.File))
			continue
		}
		if oldFS == nil || newFS == nil {
			continue
		}
		// Every file beside the suite counts: a runner can read its cases
		// from there, so suite.yaml alone could stay the same while they weaken.
		od, oerr := contract.SuiteDigest(oldFS, o.File)
		nd, nerr := contract.SuiteDigest(newFS, n.File)
		if (oerr == nil) != (nerr == nil) || od != nd {
			c := newChange("checks.file", Modified, o.Name+": "+o.File, o.Name+": "+n.File)
			c.Reason = "the suite " + n.File + " of check " + n.Name + " or a file beside it changed"
			changes = append(changes, c)
		}
	}
	for _, n := range new {
		if !slices.ContainsFunc(old, func(o contract.Check) bool { return o.Name == n.Name }) {
			changes = append(changes, newChange("checks", Added, nil, n.Name))
		}
	}
	return changes
}
