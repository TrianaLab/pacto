/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RevisionSource specifies where the contract for this revision was loaded from.
type RevisionSource struct {
	// OCI is the fully resolved OCI reference (including tag/digest).
	// Example: ghcr.io/org/service-pacto:1.2.0
	// +optional
	OCI string `json:"oci,omitempty"`

	// Digest is the OCI manifest digest (sha256:...) at the time of resolution.
	// Used to detect force-pushes (tag overwrites) on the registry.
	// +optional
	Digest string `json:"digest,omitempty"`

	// Inline indicates the contract was provided inline (no external source).
	// +optional
	Inline bool `json:"inline,omitempty"`
}

// PactoRevisionSpec defines the desired state of PactoRevision.
// A PactoRevision is an immutable snapshot of a resolved contract version.
type PactoRevisionSpec struct {
	// Version is the contract version (from contract.service.version).
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// Source specifies where this revision's contract was loaded from.
	// +required
	Source RevisionSource `json:"source"`

	// PactoRef is the name of the parent Pacto resource that owns this revision.
	// +kubebuilder:validation:MinLength=1
	PactoRef string `json:"pactoRef"`

	// ServiceName is the service name from the contract.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`
}

// PactoRevisionStatus defines the observed state of PactoRevision.
type PactoRevisionStatus struct {
	// Resolved indicates whether this revision has been successfully resolved and parsed.
	Resolved bool `json:"resolved"`

	// ContractHash is the SHA-256 hash of the raw contract YAML.
	// Used to detect content changes across versions.
	// +optional
	ContractHash string `json:"contractHash,omitempty"`

	// CreatedAt is the timestamp when this revision was first resolved.
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// conditions represent the current state of the PactoRevision resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
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
	Spec PactoRevisionSpec `json:"spec"`

	// status defines the observed state of PactoRevision.
	// +optional
	Status PactoRevisionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PactoRevisionList contains a list of PactoRevision.
type PactoRevisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PactoRevision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PactoRevision{}, &PactoRevisionList{})
}
