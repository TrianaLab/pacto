/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ServiceFor is the one Service whose selector matches podLabels, "" when
// none or several do: the interface checks then stay unknown, never violated.
func ServiceFor(services []corev1.Service, podLabels map[string]string) string {
	found := ""
	for _, s := range services {
		if len(s.Spec.Selector) == 0 || !labels.SelectorFromSet(s.Spec.Selector).Matches(labels.Set(podLabels)) {
			continue
		}
		if found != "" {
			return ""
		}
		found = s.Name
	}
	return found
}
