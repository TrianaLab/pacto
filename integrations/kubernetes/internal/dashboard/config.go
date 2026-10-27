/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"fmt"
	"time"

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

	// Auth is how people sign in to the dashboard. Without an issuer the
	// shared dashboard identifies nobody, so it serves no delivery data and no
	// actions; its fleet views are unchanged.
	Auth AuthConfig

	// MiraURL, when set, turns the dashboard's Mira citations into links.
	MiraURL string

	// DeliveryTrust names the ConfigMap, in the operator namespace, holding the
	// trust config the dashboard verifies delivery records with, so History
	// reads signed records. Empty Name: the dashboard verifies none.
	DeliveryTrust KeyRef
}

// KeyRef names one key of a ConfigMap or a Secret in the operator namespace.
type KeyRef struct {
	Name string
	Key  string
}

// AuthConfig is the dashboard's OpenID Connect client. Delivery actions then
// run with the signed-in person's own token: the dashboard's ServiceAccount
// gains no right for it.
type AuthConfig struct {
	IssuerURL string
	// InternalIssuerURL is where the dashboard reaches the issuer when it
	// differs from IssuerURL, the address browsers use; ID tokens still name
	// IssuerURL.
	InternalIssuerURL string
	// CAConfigMap or CASecret (never both) holds, under CAKey, the PEM bundle
	// of the CAs that sign the issuer's certificate, trusted on top of the
	// system roots.
	CAConfigMap string
	CASecret    string
	CAKey       string
	ClientID    string
	RedirectURL string
	ExtraScopes []string
	// ClientSecretName and ClientSecretKey name the Secret, in the operator
	// namespace, holding the client secret. It reaches the pod through
	// secretKeyRef, never as a literal value.
	ClientSecretName string
	ClientSecretKey  string
	// UsernameClaim, UsernamePrefix, GroupsClaim and GroupsPrefix name the
	// person the way the API server's claim mapping does; empty keeps the
	// dashboard's defaults (issuer#sub and the groups claim).
	UsernameClaim  string
	UsernamePrefix string
	GroupsClaim    string
	GroupsPrefix   string
	// SessionIdleTimeout and SessionMaxAge bound a session; zero keeps the
	// dashboard's defaults.
	SessionIdleTimeout time.Duration
	SessionMaxAge      time.Duration
	// TokenLogin lets people sign in by pasting a Kubernetes bearer token.
	TokenLogin bool
	// DeliveryNamespace is the namespace Pacto delivery runs in, where it publishes the
	// promoter's identity policy, which the dashboard reads; empty means
	// pacto-delivery.
	DeliveryNamespace string
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
	if a := c.Auth; a.IssuerURL != "" && (a.ClientID == "" || a.RedirectURL == "") {
		return fmt.Errorf("dashboard sign-in needs a client id and a redirect URL beside the issuer %s", a.IssuerURL)
	}
	if a := c.Auth; a.CAConfigMap != "" && a.CASecret != "" {
		return fmt.Errorf("the dashboard's issuer CA bundle comes from a ConfigMap or a Secret, not both")
	}
	return c.validateObservation()
}
