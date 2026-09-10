package fleetsrc

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// maxUnreadableNotes bounds how many refused directories are named one by one
// before the rest are summarised as a count. One line instead of eighty: a home
// directory on macOS refuses around a hundred TCC-guarded paths, and a limitation
// per refusal would bury the gaps a reader can act on under privacy directories
// they cannot.
const maxUnreadableNotes = 10

// unreadableDirs accumulates the directories a filesystem walk could not read and
// renders them as limitations.
//
// One refused directory is not a reason to throw away every bundle beside it. A
// scan rooted at a macOS home directory reaches TCC-guarded paths like
// ~/Library/Accounts within milliseconds, and a source that aborted there
// reported itself unavailable -- zero services -- while hundreds of readable
// bundles sat further down. A shared OCI cache does the same to a user who once
// ran pacto under sudo: the root-owned entries refuse, and the whole offline
// baseline disappears rather than the entries that are actually unreachable.
//
// Both filesystem-walking sources in this package route their walk errors here,
// so a refusal costs the same thing in each: the gap is named, the walk carries
// on, and the answer is partial instead of empty. That is the package's stated
// policy for a bundle it cannot parse, and a directory it cannot open is the same
// kind of gap.
type unreadableDirs struct {
	source string // fleet source id the limitations are attributed to
	root   string // walk root, for relative display and for the one fatal case
	seen   int
	notes  []fleet.Limitation
}

// note records a directory the walk could not read and returns what the walk
// should do next: skip it and keep going.
//
// The root is the one exception. Nothing was read at all, so there is no partial
// answer to report and an unavailable source is the honest result -- returning
// the error aborts the walk and fails the source.
func (u *unreadableDirs) note(p string, err error) error {
	if p == u.root {
		return err
	}
	u.seen++
	if u.seen > maxUnreadableNotes {
		return fs.SkipDir
	}
	// The reason, not the wrapper: a *fs.PathError stringifies to "open
	// <absolute path>: permission denied", which would put the caller's home
	// directory back into a message the relative path was chosen to keep it out
	// of. Everything else keeps its whole text -- there is no path in it to strip.
	reason := err.Error()
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		reason = pathErr.Err.Error()
	}
	u.notes = append(u.notes, fleet.Limitation{
		Code: fleet.LimitationSourcePartial, Source: u.source,
		Message: "could not read " + relPathSafe(u.root, p) + ": " + reason,
	})
	return fs.SkipDir
}

// limitations returns one limitation per named refusal, followed by a count of
// the rest when the walk met more than it was willing to name.
func (u *unreadableDirs) limitations() []fleet.Limitation {
	if u.seen <= maxUnreadableNotes {
		return u.notes
	}
	return append(u.notes, fleet.Limitation{
		Code: fleet.LimitationSourcePartial, Source: u.source,
		Message: fmt.Sprintf("%d directories could not be read in total; only the first %d are listed", u.seen, maxUnreadableNotes),
	})
}

// relPathSafe returns a scanned path relative to the scan root for display,
// falling back to its last element when it cannot be made relative. Only the
// relative form is shown: a scan root is usually an absolute path off the
// caller's machine, and the message is the same message an agent reads.
func relPathSafe(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return filepath.Base(p)
}
