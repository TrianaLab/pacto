package lock

import (
	"strings"
	"testing"
)

func TestErrorsCarryCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&DriftError{Name: "x", Locked: "sha256:a", Current: "sha256:b"}, "LOCK_DIGEST_MISMATCH"},
		{&LocalDriftError{Name: "x", Locked: "sha256:old", Current: "sha256:new"}, "LOCK_LOCAL_DRIFT"},
		{&StaleError{Detail: "added dep y"}, "LOCK_STALE"},
		{&ConflictError{Service: "x"}, "LOCK_CONFLICT"},
		{&UnresolvedError{Ref: "oci://r/x", Reason: "not found"}, "LOCK_UNRESOLVED"},
		{&MissingError{Path: "pacto.lock"}, "LOCK_MISSING"},
		{&AmbiguousError{Occurrence: Occurrence{Kind: "config", Name: "settings"}, First: "a", Second: "b"}, "LOCK_AMBIGUOUS_REFERENCE"},
	}
	for _, c := range cases {
		if !strings.HasPrefix(c.err.Error(), c.code) {
			t.Errorf("error %T = %q, want prefix %q", c.err, c.err.Error(), c.code)
		}
	}
}

// The ambiguity message has to be actionable on its own: which declaration, who
// declared it and both of the resolutions it would have to hold at once.
func TestAmbiguousErrorNamesBothResolutions(t *testing.T) {
	e := &AmbiguousError{
		Occurrence: Occurrence{From: "oci:sha256:parent", Kind: "config", Name: "settings"},
		First:      "../one (local:sha256:aaa)",
		Second:     "../two (local:sha256:bbb)",
	}
	for _, want := range []string{"settings", "oci:sha256:parent", e.First, e.Second} {
		if !strings.Contains(e.Error(), want) {
			t.Errorf("message %q omits %q", e.Error(), want)
		}
	}
}
