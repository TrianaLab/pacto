/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"slices"
	"testing"
)

func TestParseProfile(t *testing.T) {
	for _, tc := range []struct {
		in, err string
		want    []string
	}{
		{in: "", want: []string{"default"}},
		{in: "default", want: []string{"default"}},
		{in: "hardened", want: []string{"hardened"}},
		{in: "idp", want: []string{"idp"}},
		{in: "idp,hardened", want: []string{"hardened", "idp"}},
		{in: "idp,idp", want: []string{"idp"}},
		{in: " hardened , ", want: []string{"hardened"}},
		{in: "secure", err: "profile takes default, hardened or idp: got secure"},
		{in: "default,idp", err: "profile default stands alone"},
	} {
		got, err := ParseProfile(tc.in)
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("ParseProfile(%q) error = %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("ParseProfile(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}
