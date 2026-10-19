/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"fmt"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"

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

	// EvidenceSourceURL, when set, is the internal URL of the operator-managed
	// Evidence Server. The operator sets it when the evidence component is also
	// enabled, so the dashboard consumes the evidence Operational Graph
	// contribution read-only over HTTP (never mounting its bucket). Empty means
	// no evidence source is configured (the dashboard reports it as unconfigured,
	// not unavailable).
	EvidenceSourceURL string

	// InsecureRegistries is the comma-separated list of registry hosts the
	// dashboard may reach over plain HTTP, inherited verbatim from the operator's
	// own environment. The dashboard resolves contract revisions itself, so a
	// controlled in-cluster registry has to be reachable by the workload, not just
	// by the controller. Empty means every registry is https-only.
	InsecureRegistries string

	// Observation declares offline OTLP/JSON trace files to mount read-only into
	// the dashboard, each under a stable Data Source name. Pacto reads exactly the
	// declared files and never scans the mount; the trace exports themselves are
	// produced and rotated by whoever owns their storage. This configures OFFLINE
	// input only — Pacto ships no OTLP receiver and no collector.
	Observation []ObservationSource
}

// ResourcesConfig holds optional resource quantity overrides for the dashboard container.
type ResourcesConfig = component.Resources

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
	if component.HasLatestTag(c.Image) {
		return fmt.Errorf("dashboard image must not use 'latest' tag: %s", c.Image)
	}
	if err := c.Resources.Validate("dashboard"); err != nil {
		return err
	}
	return c.validateObservation()
}
