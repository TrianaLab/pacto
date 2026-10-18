/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package nodeagent

import (
	"fmt"
	"time"

	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/component"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1" //nolint:lll // Used by Config.OwnerRef field type
)

// Config holds the operator-managed node agent configuration.
type Config struct {
	// Enabled controls whether the operator manages the node agent DaemonSet.
	Enabled bool

	// Image is the pacto runtime image the agent runs `pacto agent` from: the
	// dashboard image the operator was built with (the Evidence Server reuses it
	// too), unless --node-agent-image overrides it.
	Image string

	// Namespace is the operator's own namespace: the agents run there and publish
	// their NodeRuntimeObservation objects there.
	Namespace string

	// PublishInterval is how often each agent publishes its node's status.
	PublishInterval time.Duration

	// Resources overrides the container's resource requirements.
	Resources ResourcesConfig

	// ImagePullSecrets names the Secrets the agent's ServiceAccount pulls Image
	// with. The chart passes the controller's own imagePullSecrets, so a private
	// registry pulls on every node the agent lands on.
	ImagePullSecrets []string

	// OwnerRef is an optional owner reference to the operator's own Deployment,
	// attached to every managed object so a GitOps tool shows them in its tree.
	OwnerRef *metav1ac.OwnerReferenceApplyConfiguration
}

// ResourcesConfig holds optional resource quantity overrides for the node agent container.
type ResourcesConfig = component.Resources

// DefaultResources returns the built-in default resource requirements. The
// agent's memory is bounded by its aggregator (pod and connection caps), and the
// ring buffer is accounted to the pod's memory cgroup, so the limit has headroom
// for both.
func DefaultResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
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
		return fmt.Errorf("node agent enabled but no image set (set --node-agent-image to the pacto runtime image)")
	}
	if component.HasLatestTag(c.Image) {
		return fmt.Errorf("node agent image must not use 'latest' tag: %s", c.Image)
	}
	if c.Namespace == "" {
		return fmt.Errorf("node agent namespace must be set (defaults to operator namespace)")
	}
	// The agent publishes the interval as whole seconds (publishIntervalSeconds, CRD Minimum=1), and
	// readers derive staleness from it, so anything else would publish a value the agent does not keep.
	if c.PublishInterval < time.Second || c.PublishInterval%time.Second != 0 {
		return fmt.Errorf("node agent publish interval must be a whole number of seconds, at least 1s, got %s", c.PublishInterval)
	}
	return c.Resources.Validate("node agent")
}
