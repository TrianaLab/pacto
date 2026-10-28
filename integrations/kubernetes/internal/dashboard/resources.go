/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import "github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"

const (
	// Resource naming
	Name = "pacto-dashboard"

	// ManagedSecretName is the name of the operator-managed Secret that holds
	// merged OCI credentials for the dashboard pod.
	ManagedSecretName = "pacto-dashboard-oci-creds"

	// ComponentValue is the dashboard's app.kubernetes.io/component label.
	ComponentValue = "dashboard"

	// FieldManager is the server-side apply field manager name for dashboard resources.
	FieldManager = "pacto-operator-dashboard"

	// Dashboard defaults
	DashboardPort = 3000
	HealthPath    = "/health"
)

// Labels returns the standard labels applied to all dashboard resources.
func Labels() map[string]string { return component.Labels(ComponentValue, Name) }

// SelectorLabels returns the labels used for pod selection.
func SelectorLabels() map[string]string { return component.SelectorLabels(ComponentValue, Name) }
