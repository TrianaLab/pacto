/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package detection

import (
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

// ParseRecorderState reads the --idp-recorder value the chart rendered under
// profile idp: what it decided about the delivery recorder. Empty (outside
// idp) is empty; anything outside detect.RecorderStates is an error.
func ParseRecorderState(s string) (string, error) {
	if s == "" || slices.Contains(detect.RecorderStates, s) {
		return s, nil
	}
	return "", fmt.Errorf("idp-recorder takes %s: got %s", strings.Join(detect.RecorderStates, ", "), s)
}
