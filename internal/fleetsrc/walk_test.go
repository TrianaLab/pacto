package fleetsrc

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func TestRelPathSafe(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "svc", "pacto.yaml")
	if got := relPathSafe(root, p); got != filepath.Join("svc", "pacto.yaml") {
		t.Errorf("relative = %q, want svc/pacto.yaml", got)
	}
	// An absolute root with a relative path cannot be made relative → base fallback.
	if got := relPathSafe("/abs/root", "svc/pacto.yaml"); got != "pacto.yaml" {
		t.Errorf("fallback = %q, want pacto.yaml", got)
	}
}

// TestUnreadableDirs covers the cases a real walk cannot produce on demand: a
// failure at the root itself, and a walk error that is not a *fs.PathError. Both
// filesystem sources share this policy, so it is tested once here and exercised
// against a genuinely refused directory in local_test.go.
func TestUnreadableDirs(t *testing.T) {
	u := unreadableDirs{source: "local", root: "/root"}
	boom := errors.New("boom")

	// At the root nothing was read at all, so there is no partial answer to
	// report and an unavailable source is the honest result.
	if err := u.note("/root", boom); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the walk error", err)
	}
	if len(u.limitations()) != 0 {
		t.Errorf("limitations = %+v, want none", u.limitations())
	}

	// Below it, the walk carries on and an error with no path inside it keeps
	// its whole text.
	if err := u.note("/root/sub", boom); !errors.Is(err, fs.SkipDir) {
		t.Errorf("err = %v, want SkipDir", err)
	}
	lims := u.limitations()
	if len(lims) != 1 {
		t.Fatalf("limitations = %+v, want exactly one", lims)
	}
	if lims[0].Code != fleet.LimitationSourcePartial || lims[0].Source != "local" {
		t.Errorf("limitation = %+v, want SOURCE_PARTIAL from local", lims[0])
	}
	if got := lims[0].Message; got != "could not read sub: boom" {
		t.Errorf("message = %q, want `could not read sub: boom`", got)
	}

	// Past the cap the gap is still skipped, but silently: the count speaks for
	// the rest.
	for range maxUnreadableNotes {
		if err := u.note("/root/more", boom); !errors.Is(err, fs.SkipDir) {
			t.Errorf("err = %v, want SkipDir", err)
		}
	}
	lims = u.limitations()
	if len(lims) != maxUnreadableNotes+1 {
		t.Fatalf("limitations = %d, want %d named plus one summary", len(lims), maxUnreadableNotes+1)
	}
	if last := lims[len(lims)-1].Message; !strings.Contains(last, "11 directories could not be read") {
		t.Errorf("summary = %q, want the total 11", last)
	}
}
