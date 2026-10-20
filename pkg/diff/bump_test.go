package diff

import (
	"strings"
	"testing"
)

func TestRequiredBump(t *testing.T) {
	cases := []struct {
		c    Classification
		base string
		want Bump
	}{
		{Breaking, "1.4.0", BumpMajor},
		{Breaking, "0.4.0", BumpMinor},
		{Breaking, "not-semver", BumpMajor},
		{PotentialBreaking, "1.4.0", BumpMinor},
		{PotentialBreaking, "0.4.0", BumpMinor},
		{NonBreaking, "1.4.0", BumpPatch},
	}
	for _, c := range cases {
		if got := RequiredBump(c.c, c.base); got != c.want {
			t.Errorf("%s over %s: got %s, want %s", c.c, c.base, got, c.want)
		}
	}
}

func TestCheckBump(t *testing.T) {
	ok := []struct {
		base, next string
		need       Bump
	}{
		{"2.4.0", "3.0.0", BumpMajor},
		{"2.4.0", "3.0.0-rc.1", BumpMajor},
		{"2.4.0", "2.5.0", BumpMinor},
		{"2.4.0", "3.1.0", BumpMinor},
		{"2.4.0", "2.4.1", BumpPatch},
		{"0.4.0", "0.5.0", BumpMinor},
		{"2.4.1", "2.5.0", BumpPatch},
	}
	for _, c := range ok {
		if err := CheckBump(c.base, c.next, c.need); err != nil {
			t.Errorf("%s -> %s (%s): %v", c.base, c.next, c.need, err)
		}
	}
	bad := []struct {
		base, next string
		need       Bump
		msg        string
	}{
		{"2.4.0", "2.5.0", BumpMajor, "publish 3.0.0 or higher"},
		{"2.4.0", "2.4.1", BumpMinor, "publish 2.5.0 or higher"},
		{"2.4.0", "2.4.0", BumpPatch, "publish 2.4.1 or higher"},
		{"2.4.0-rc.1", "2.4.0-rc.0", BumpPatch, "patch bump"},
		{"x", "1.0.0", BumpPatch, "published version"},
		{"1.0.0", "x", BumpPatch, `version "x" is not semver`},
	}
	for _, c := range bad {
		if err := CheckBump(c.base, c.next, c.need); err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s -> %s (%s): %v, want %q", c.base, c.next, c.need, err, c.msg)
		}
	}
}
