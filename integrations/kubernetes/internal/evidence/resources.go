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
// sidecar. Its persistent evidence is retained across disablement and upgrade —
// the managed PVC carries no owner reference, so it is never garbage-collected
// with the operator.
package evidence

const (
	// Name is the resource name for all managed Evidence Server resources.
	Name = "pacto-evidence"
	// PVCName is the default PersistentVolumeClaim name backing a file:// bucket.
	PVCName = "pacto-evidence-data"

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
	// ReadyPath gates on completed storage recovery; HealthPath is liveness.
	ReadyPath  = "/api/evidence/v1/ready"
	HealthPath = "/api/evidence/v1/health"

	// TrustMountPath is where the trusted-producer-keys Secret is mounted.
	TrustMountPath = "/etc/pacto/trust"

	volumeTrust = "trust"
	volumeData  = "data"
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
