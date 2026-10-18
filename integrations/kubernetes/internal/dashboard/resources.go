/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

const (
	// Resource naming
	Name = "pacto-dashboard"

	// ManagedSecretName is the name of the operator-managed Secret that holds
	// merged OCI credentials for the dashboard pod.
	ManagedSecretName = "pacto-dashboard-oci-creds"

	// Labels
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelName      = "app.kubernetes.io/name"

	// Values
	ManagedByValue = "pacto-operator"
	ComponentValue = "dashboard"

	// FieldManager is the server-side apply field manager name for dashboard resources.
	FieldManager = "pacto-operator-dashboard"

	// Dashboard defaults
	DashboardPort = 3000
	HealthPath    = "/health"
)

// Labels returns the standard labels applied to all dashboard resources.
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
