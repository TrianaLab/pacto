/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package component

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// Resources holds optional resource quantity overrides for a component's
// container. Each component keeps its own defaults; this is only the override
// mechanics they had each written out.
type Resources struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// Validate checks that any provided resource quantity override is parseable,
// so a bad --<component>-cpu-request (etc.) fails fast at operator startup
// instead of panicking via MustParse during the first sync. name prefixes the
// error ("invalid dashboard cpu-request quantity ...").
func (rc Resources) Validate(name string) error {
	for _, q := range []struct{ name, val string }{
		{"cpu-request", rc.CPURequest},
		{"cpu-limit", rc.CPULimit},
		{"memory-request", rc.MemoryRequest},
		{"memory-limit", rc.MemoryLimit},
	} {
		if q.val == "" {
			continue
		}
		if _, err := resource.ParseQuantity(q.val); err != nil {
			return fmt.Errorf("invalid %s %s quantity %q: %w", name, q.name, q.val, err)
		}
	}
	return nil
}

// Build returns defaults with the overrides applied. defaults must carry
// non-nil Requests and Limits lists; Build writes into them.
func (rc Resources) Build(defaults corev1.ResourceRequirements) corev1.ResourceRequirements {
	if rc.CPURequest != "" {
		defaults.Requests[corev1.ResourceCPU] = resource.MustParse(rc.CPURequest)
	}
	if rc.MemoryRequest != "" {
		defaults.Requests[corev1.ResourceMemory] = resource.MustParse(rc.MemoryRequest)
	}
	if rc.CPULimit != "" {
		defaults.Limits[corev1.ResourceCPU] = resource.MustParse(rc.CPULimit)
	}
	if rc.MemoryLimit != "" {
		defaults.Limits[corev1.ResourceMemory] = resource.MustParse(rc.MemoryLimit)
	}
	return defaults
}

// HasLatestTag reports whether an image ref uses the :latest tag or no tag at
// all (which defaults to latest). A digest-pinned ref reads as pinned.
func HasLatestTag(image string) bool {
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == ':' {
			return image[i+1:] == "latest"
		}
		if image[i] == '/' {
			break
		}
	}
	return true
}
