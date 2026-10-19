/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"fmt"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1" //nolint:lll // Used by Config.OwnerRef field type
)

// Config holds the operator-managed Evidence Server configuration.
type Config struct {
	// Enabled controls whether the operator manages an Evidence Server deployment.
	Enabled bool

	// Image is the runtime container image (the pacto binary) that runs
	// `pacto evidence serve`. Set at build time to couple it to the Pacto library
	// version, same as the dashboard image. Not user-configurable.
	Image string

	// Namespace is the operator's own namespace, where evidence resources live.
	Namespace string

	// Subjects are the exact, immutable contract revisions evidence is stored on,
	// each an oci://<repo>@sha256:<digest> reference. The registry holding them IS
	// the durable evidence store: accepted records are published as OCI 1.1
	// referrers of these manifests, so at least one is required. The reference
	// syntax is enforced by the chart schema at install time and authoritatively by
	// the Evidence Server at startup, which fails fast on anything else.
	Subjects []string

	// TrustSecret is the name of a Secret of trusted producer public keys, mounted
	// read-only. Signature verification is mandatory, so it is required.
	TrustSecret string

	// InsecureRegistries is the comma-separated list of registry hosts the
	// Evidence Server may reach over plain HTTP, inherited verbatim from the
	// operator's own environment. It resolves the contract ref an envelope names,
	// so a controlled in-cluster registry has to be reachable by the workload.
	// Empty means every registry is https-only.
	InsecureRegistries string

	// CredentialsSecret is the OPTIONAL name of an existing Secret holding a Docker
	// config (a kubernetes.io/dockerconfigjson Secret), mounted read-only so the
	// server reads and writes the contract registry under Pacto's ordinary
	// credential policy. Empty means anonymous or in-cluster access.
	CredentialsSecret string

	// Resources overrides the container's resource requirements.
	Resources ResourcesConfig

	// OwnerRef is an optional owner reference to the operator's own Deployment.
	// It is attached to the Deployment and Service so a GitOps tool shows them in
	// the resource tree.
	OwnerRef *metav1ac.OwnerReferenceApplyConfiguration
}

// ResourcesConfig holds optional resource quantity overrides for the evidence container.
type ResourcesConfig = component.Resources

// DefaultResources returns the built-in default resource requirements.
func DefaultResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("25m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// Validate checks the config is valid when the feature is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Image == "" {
		return fmt.Errorf("evidence image must be set at build time via ldflags")
	}
	if component.HasLatestTag(c.Image) {
		return fmt.Errorf("evidence image must not use 'latest' tag: %s", c.Image)
	}
	if c.Namespace == "" {
		return fmt.Errorf("evidence namespace must be set (defaults to operator namespace)")
	}
	if c.TrustSecret == "" {
		return fmt.Errorf("evidence enabled but no trust secret set: signature verification is mandatory (set --evidence-trust-secret)")
	}
	if len(c.Subjects) == 0 {
		return fmt.Errorf("evidence enabled but no contract subject set: the registry is the evidence store (set evidence.registry.subjects)")
	}
	return c.Resources.Validate("evidence")
}
