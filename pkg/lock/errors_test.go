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
	}
	for _, c := range cases {
		if !strings.HasPrefix(c.err.Error(), c.code) {
			t.Errorf("error %T = %q, want prefix %q", c.err, c.err.Error(), c.code)
		}
	}
}
