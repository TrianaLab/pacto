/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"testing"
)

func TestLabelsConsistency(t *testing.T) {
	labels := Labels()
	selectorLabels := SelectorLabels()

	// All selector labels must be present in the full labels
	for k, v := range selectorLabels {
		if labels[k] != v {
			t.Errorf("selector label %q=%q not found in full labels", k, v)
		}
	}
}
