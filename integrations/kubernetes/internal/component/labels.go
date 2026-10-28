/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package component

import corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

// The labels every operator-managed component puts on its resources. The node
// agent's NodeRuntimeObservations and Leases carry the same set (the core
// module's kubesink writes them), so the values must not change.
const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelName      = "app.kubernetes.io/name"
	ManagedByValue = "pacto-operator"
)

// Labels are the standard labels on every resource of the component (its
// app.kubernetes.io/component value) named name.
func Labels(component, name string) map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue, LabelComponent: component, LabelName: name}
}

// SelectorLabels are the labels the component's pods are selected by.
func SelectorLabels(component, name string) map[string]string {
	return map[string]string{LabelComponent: component, LabelName: name}
}

// Owned is the label set Prune matches to find what the component owns.
func Owned(component string) map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue, LabelComponent: component}
}

// RestrictedContainerSecurityContext is the security context of an
// unprivileged component container: read-only root filesystem, no privilege
// escalation, every capability dropped.
func RestrictedContainerSecurityContext() *corev1ac.SecurityContextApplyConfiguration {
	return corev1ac.SecurityContext().
		WithReadOnlyRootFilesystem(true).
		WithAllowPrivilegeEscalation(false).
		WithCapabilities(corev1ac.Capabilities().WithDrop("ALL"))
}
