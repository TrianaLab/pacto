/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1" //nolint:lll // Used by Config.OwnerRef field type
)

// Config holds the global dashboard deployment configuration.
type Config struct {
	// Enabled controls whether the operator manages a dashboard deployment.
	Enabled bool

	// Image is the full dashboard container image reference, set at build time
	// to couple the dashboard version to the Pacto library dependency.
	// Not user-configurable.
	Image string

	// Namespace is the Kubernetes namespace where the dashboard resources are deployed.
	// Always set to the operator's own namespace.
	Namespace string

	// WatchNamespace restricts the dashboard's observation scope to a single namespace.
	// Empty means cluster-wide (all namespaces). Inherited from the controller's --watch-namespace flag.
	WatchNamespace string

	// OCISecret is the optional name of a single Kubernetes Secret (in the operator namespace)
	// containing OCI registry credentials. Kept for backward compatibility.
	// If OCISecrets is also set, OCISecret is ignored.
	OCISecret string

	// OCISecrets is an optional list of Secret names (in the operator namespace) containing
	// OCI registry credentials. Supports Opaque and kubernetes.io/dockerconfigjson secrets.
	// When set, the operator reads all referenced secrets, merges credentials, and creates
	// a managed dockerconfigjson secret mounted into the dashboard pod.
	OCISecrets []string

	// Resources overrides the dashboard container's resource requirements.
	// Zero-value fields fall back to built-in defaults.
	Resources ResourcesConfig

	// OwnerRef is an optional owner reference to the operator's own Deployment.
	// When set, all namespaced dashboard resources are created with this owner,
	// enabling ArgoCD to display them in the resource tree.
	OwnerRef *metav1ac.OwnerReferenceApplyConfiguration
}

// EffectiveOCISecrets returns the resolved list of OCI secret names.
// OCISecrets takes precedence; if empty, falls back to OCISecret.
func (c Config) EffectiveOCISecrets() []string {
	if len(c.OCISecrets) > 0 {
		return c.OCISecrets
	}
	if c.OCISecret != "" {
		return []string{c.OCISecret}
	}
	return nil
}

// ResourcesConfig holds optional resource quantity overrides for the dashboard container.
type ResourcesConfig struct {
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// Validate checks that any provided resource quantity overrides are parseable,
// so an invalid --dashboard-cpu-request (etc.) fails fast at operator startup
// instead of panicking via MustParse during the first reconcile.
func (rc ResourcesConfig) Validate() error {
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
			return fmt.Errorf("invalid dashboard %s quantity %q: %w", q.name, q.val, err)
		}
	}
	return nil
}

// DefaultResources returns the built-in default resource requirements.
func DefaultResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

// BuildResources returns resource requirements, applying any overrides from the config.
func (rc ResourcesConfig) BuildResources() corev1.ResourceRequirements {
	res := DefaultResources()
	if rc.CPURequest != "" {
		res.Requests[corev1.ResourceCPU] = resource.MustParse(rc.CPURequest)
	}
	if rc.MemoryRequest != "" {
		res.Requests[corev1.ResourceMemory] = resource.MustParse(rc.MemoryRequest)
	}
	if rc.CPULimit != "" {
		res.Limits[corev1.ResourceCPU] = resource.MustParse(rc.CPULimit)
	}
	if rc.MemoryLimit != "" {
		res.Limits[corev1.ResourceMemory] = resource.MustParse(rc.MemoryLimit)
	}
	return res
}

// Validate checks that the config is valid when the feature is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Image == "" {
		return fmt.Errorf("dashboard image must be set at build time via ldflags")
	}
	if c.Namespace == "" {
		return fmt.Errorf("dashboard namespace must be set (defaults to operator namespace)")
	}
	// Reject "latest" tag
	if hasLatestTag(c.Image) {
		return fmt.Errorf("dashboard image must not use 'latest' tag: %s", c.Image)
	}
	if err := c.Resources.Validate(); err != nil {
		return err
	}
	return nil
}

func hasLatestTag(image string) bool {
	// Check for :latest suffix or no tag at all (which defaults to latest)
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == ':' {
			return image[i+1:] == "latest"
		}
		if image[i] == '/' {
			break
		}
	}
	// No tag found — treated as implicit latest
	return true
}
