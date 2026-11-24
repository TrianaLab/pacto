/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:deprecatedversion:warning="pacto.trianalab.io/v1alpha1 is deprecated; use v1 (removed in chart 8.0)"
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.contractStatus`
// +kubebuilder:printcolumn:name="Service",type=string,JSONPath=`.spec.target.serviceName`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.contractVersion`
// +kubebuilder:printcolumn:name="Errors",type=integer,JSONPath=`.status.summary.errorCount`
// +kubebuilder:printcolumn:name="Warnings",type=integer,JSONPath=`.status.summary.warningCount`
// +kubebuilder:printcolumn:name="Last Reconciled",type=date,JSONPath=`.status.lastReconciledAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Pacto is the Schema for the pactos API.
type Pacto struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Pacto.
	// +required
	Spec v1.PactoSpec `json:"spec"`

	// status defines the observed state of Pacto.
	// +optional
	Status v1.PactoStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PactoList contains a list of Pacto.
type PactoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Pacto `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:deprecatedversion:warning="pacto.trianalab.io/v1alpha1 is deprecated; use v1 (removed in chart 8.0)"
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Pacto",type=string,JSONPath=`.spec.pactoRef`
// +kubebuilder:printcolumn:name="Resolved",type=boolean,JSONPath=`.status.resolved`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PactoRevision is the Schema for the pactorevisions API.
// It represents an immutable snapshot of a resolved contract version.
type PactoRevision struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PactoRevision.
	// +required
	Spec v1.PactoRevisionSpec `json:"spec"`

	// status defines the observed state of PactoRevision.
	// +optional
	Status v1.PactoRevisionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PactoRevisionList contains a list of PactoRevision.
type PactoRevisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PactoRevision `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:deprecatedversion:warning="pacto.trianalab.io/v1alpha1 is deprecated; use v1 (removed in chart 8.0)"
// +kubebuilder:printcolumn:name="Node",type=string,JSONPath=`.status.nodeName`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.collection.state`
// +kubebuilder:printcolumn:name="Last Published",type=date,JSONPath=`.status.lastPublished`

// NodeRuntimeObservation carries the socket facts the Pacto node agent observed
// on one node. It is written only by that node's agent (status subresource) and
// read by the operator. Its name is the node name.
type NodeRuntimeObservation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// status is the agent's published observation.
	// +optional
	Status v1.NodeRuntimeObservationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NodeRuntimeObservationList contains a list of NodeRuntimeObservation.
type NodeRuntimeObservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NodeRuntimeObservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Pacto{}, &PactoList{})
	SchemeBuilder.Register(&PactoRevision{}, &PactoRevisionList{})
	SchemeBuilder.Register(&NodeRuntimeObservation{}, &NodeRuntimeObservationList{})
}
