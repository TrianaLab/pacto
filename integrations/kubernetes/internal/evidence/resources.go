/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package evidence is the operator-managed Evidence Server component. It mirrors
// the dashboard component: the operator translates chart values into controller
// configuration and reconciles a SEPARATE Evidence Server Deployment and an
// internal Service. The Evidence Server is a distinct process (it runs
// `pacto evidence serve`), never inside the controller, the dashboard or a
// sidecar. It is STATELESS: accepted evidence is published to the contract
// registry as OCI 1.1 referrers of the exact contract revision each report is
// about, so there is no PersistentVolumeClaim, no data volume and nothing to
// retain in the cluster. Evidence is retained across disablement, upgrade and
// cluster loss because the registry holds it.
package evidence

const (
	// Name is the resource name for all managed Evidence Server resources.
	Name = "pacto-evidence"

	// Labels
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelName      = "app.kubernetes.io/name"

	// ManagedByValue and ComponentValue tag every resource this component owns.
	ManagedByValue = "pacto-operator"
	ComponentValue = "evidence"

	// FieldManager is the server-side apply field manager for evidence resources.
	FieldManager = "pacto-operator-evidence"

	// EvidencePort is the Evidence Server's ingestion port.
	EvidencePort = 8686
	// ReadyPath gates on every configured subject resolving and answering native
	// Referrers discovery; HealthPath is liveness.
	ReadyPath  = "/api/evidence/v1/ready"
	HealthPath = "/api/evidence/v1/health"

	// TrustMountPath is where the trusted-producer-keys Secret is mounted.
	TrustMountPath = "/etc/pacto/trust"

	// RegistryMountPath is where an optional Docker config Secret is mounted. It is
	// a DOCKER_CONFIG directory, so the server reads the registry through exactly
	// the same credential policy as `pacto pull` — no second auth model.
	RegistryMountPath = "/etc/pacto/registry"

	// DockerConfigEnvVar points go-containerregistry's docker keychain at the
	// mounted Secret.
	DockerConfigEnvVar = "DOCKER_CONFIG"

	volumeTrust    = "trust"
	volumeRegistry = "registry-credentials"
)

// Labels returns the standard labels applied to all evidence resources.
func Labels() map[string]string {
	return map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelComponent: ComponentValue,
		LabelName:      Name,
	}
}

// SelectorLabels returns the labels used for pod selection.
func SelectorLabels() map[string]string {
	return map[string]string{
		LabelComponent: ComponentValue,
		LabelName:      Name,
	}
}
