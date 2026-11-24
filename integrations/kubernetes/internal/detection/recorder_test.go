/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

func TestParseRecorderState(t *testing.T) {
	for _, s := range append([]string{""}, detect.RecorderStates...) {
		if got, err := ParseRecorderState(s); err != nil || got != s {
			t.Errorf("%q: got %q, %v", s, got, err)
		}
	}
	if _, err := ParseRecorderState("maybe"); err == nil || !strings.Contains(err.Error(), "no-signer") {
		t.Errorf("maybe: %v", err)
	}
}
