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

	// WatchNamespaces restricts the dashboard's observation scope to these namespaces.
	// Empty means cluster-wide (all namespaces). Inherited from the controller's --watch-namespaces flag.
	WatchNamespaces []string

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

	// Repositories are oci:// references the dashboard reads beside the
	// contracts it discovers from Pacto resources, such as golden paths, which
	// are never deployed and so have no Pacto resource to discover them from.
	// Deprecated: with Registry set the dashboard lists every golden path and
	// offering published under it; Repositories are still read.
	Repositories []string

	// ExternalCatalog is the organization external catalog the dashboard
	// merges over Pacto's default, as oci://REPOSITORY@sha256:DIGEST. Empty
	// uses only the default catalog.
	ExternalCatalog string

	// Observation declares offline OTLP/JSON trace files to mount read-only into
	// the dashboard, each under a stable Data Source name. Pacto reads exactly the
	// declared files and never scans the mount; the trace exports themselves are
	// produced and rotated by whoever owns their storage. These files are offline
	// input; the dashboard also receives OTLP/HTTP JSON from Collectors in the
	// cluster on OTLPPort, and Pacto deploys no collector.
	Observation []ObservationSource

	// Auth is how people sign in to the dashboard. With it, every view and
	// action runs with the signed-in person's RBAC; the delivery views need it.
	Auth AuthConfig

	// DeliveryNamespace is the namespace Pacto delivery runs in, where it
	// publishes the promoter's identity policy, which the dashboard reads;
	// empty means pacto-delivery.
	DeliveryNamespace string

	// MiraURL, when set, turns the dashboard's Mira citations into links.
	MiraURL string
	// StatsRegistry, when set, is the stats repository the Metrics page reads
	// run records from (PACTO_STATS_REGISTRY).
	StatsRegistry string
	// Index is the organization index the dashboard reads every published
	// service from (PACTO_DASHBOARD_INDEX), <registry>/pacto-index; empty
	// reads none.
	Index string
	// BackstageURL is the Backstage catalog whose owners, systems and
	// dependencies the dashboard reads as inferred values
	// (PACTO_DASHBOARD_BACKSTAGE_URL); empty reads none.
	BackstageURL string
	// BackstageToken names the Secret, in the operator namespace, holding the
	// catalog reader token (default key token). The kubelet resolves it into
	// PACTO_BACKSTAGE_TOKEN; the operator never reads it.
	BackstageToken KeyRef

	// DeliveryTrust names the ConfigMap, in the operator namespace, holding the
	// trust config the dashboard verifies delivery records with, so History
	// reads signed records. Empty Name: the dashboard verifies none.
	DeliveryTrust KeyRef

	// DetectedConfigMap names the ConfigMap, in the operator namespace, where
	// the operator publishes what it detected. The dashboard reads it as an
	// optional kubelet-mounted file, so its ServiceAccount gains no rule.
	// Empty: the dashboard shows no detected rows.
	DetectedConfigMap string

	// FingerprintsConfigMap names the ConfigMap, in the operator namespace,
	// holding the installation's workload manager and promoter fingerprints
	// (fingerprints.yaml). The dashboard reads it as a kubelet-mounted file,
	// so its ServiceAccount gains no rule. Empty: no fingerprints.
	FingerprintsConfigMap string

	// OwnerPolicyConfigMap names the immutable, content-named ConfigMap in the
	// operator namespace holding the chart's owner policy (policy.json): who
	// may set a service owner. The dashboard reads it as a kubelet-mounted
	// file. Empty: no policy, so owner changes read as unverified.
	OwnerPolicyConfigMap string

	// TrustedRootConfigMap names the immutable ConfigMap, in the operator
	// namespace, holding the Sigstore trusted root (trusted_root.json) a
	// declaration signed in the dashboard is verified with. Empty:
	// public-good Sigstore.
	TrustedRootConfigMap string

	// FulcioURL, RekorURL and TimestampURL are the Sigstore a person's
	// declaration is signed with in the dashboard; empty: public-good Sigstore.
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	// Registry is the installation registry (<host>/<path>): the dashboard
	// lists the golden paths and offerings published under it and publishes
	// declarations there; empty: the dashboard offers no Declare.
	Registry string
	// DeclareRegistrySecret names an existing kubernetes.io/dockerconfigjson
	// Secret, in the operator namespace, with push access to Registry. Only the
	// dashboard's Declare writer reads it, from its own mount; OCISecrets stay
	// read-only. Empty: the dashboard plans declarations only.
	DeclareRegistrySecret string
}

// KeyRef names one key of a ConfigMap or a Secret in the operator namespace.
type KeyRef struct {
	Name string
	Key  string
}

// AuthConfig is how people sign in to the dashboard: its OpenID Connect
// client, claim mapping, session lifetime and token sign-in. Reads and actions
// then run with the signed-in person's own token; the dashboard's
// ServiceAccount gains no right for them.
type AuthConfig struct {
	IssuerURL string
	// InternalIssuerURL is where the dashboard reaches the issuer when it
	// differs from IssuerURL, the address browsers use; ID tokens still name
	// IssuerURL.
	InternalIssuerURL string
	// DisplayName is the identity provider's name on the sign-in button;
	// empty says OIDC.
	DisplayName string
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
	// TokenLogin lets people sign in with a Kubernetes token: a ServiceAccount
	// token minted for the pacto-dashboard audience reads the fleet only, and a
	// person's own token acts as them.
	TokenLogin bool
	// AllowAnonymous serves the dashboard without sign-in; everyone who
	// reaches it reads what its ServiceAccount reads. Ignored with sign-in.
	AllowAnonymous bool
	// SignClientID is a second client at IssuerURL whose audience Fulcio
	// trusts and the API server does not: a signed-in person logs in again
	// with it to sign a declaration as themselves. Empty: the dashboard saves
	// drafts. Its secret reaches the pod by secretKeyRef, as the sign-in
	// client's does.
	SignClientID         string
	SignClientSecretName string
	SignClientSecretKey  string
	SignRedirectURL      string
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
	if err := c.Auth.validateSign(); err != nil {
		return err
	}
	return c.validateObservation()
}

// validateSign checks the signing client: a second client at the sign-in
// issuer, with its own redirect URL.
func (a AuthConfig) validateSign() error {
	switch {
	case a.SignClientID == "":
		return nil
	case a.IssuerURL == "":
		return fmt.Errorf("the dashboard's sign client %s needs the sign-in issuer: it logs in at the same issuer", a.SignClientID)
	case a.SignClientID == a.ClientID:
		return fmt.Errorf("the dashboard's sign client must differ from its sign-in client %s: "+
			"the session's token must never reach Fulcio", a.ClientID)
	case a.SignRedirectURL == "":
		return fmt.Errorf("the dashboard's sign client %s needs a redirect URL", a.SignClientID)
	}
	return nil
}

// needsClusterRole reports whether the dashboard needs a ClusterRole: it watches the whole cluster,
// or it reads delivery objects, whose shared informer is cluster-wide. A dashboard scoped to a
// namespace list without delivery gets its namespaced Roles from the chart instead.
func (c Config) needsClusterRole() bool {
	return len(c.WatchNamespaces) == 0 || c.DeliveryNamespace != ""
}
