/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// RecordFinalizer keeps a concluded Operation until its record is written. The
// client sets it and admission requires it. The recorder removes it once the
// record is in the registry; without a recorder, the promoter removes it when
// the concluded operation is deleted.
const RecordFinalizer = "delivery.pacto.trianalab.io/record"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=pacto-delivery
// +kubebuilder:printcolumn:name="Service",type=string,JSONPath=`.spec.service.name`
// +kubebuilder:printcolumn:name="Env",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Level",type=string,JSONPath=`.status.level`
// +kubebuilder:printcolumn:name="Pointer",type=string,JSONPath=`.status.pointer`
// +kubebuilder:printcolumn:name="Frozen",type=boolean,JSONPath=`.status.frozen`
// +kubebuilder:printcolumn:name="Hold",type=boolean,JSONPath=`.status.autoHold`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Stage is one service in one environment: which version it runs and how new
// versions reach it.
type Stage struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`
	// +required
	Spec delivery.StageSpec `json:"spec"`
	// +optional
	Status delivery.StageStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// StageList contains a list of Stage.
type StageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Stage `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=pacto-delivery
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Stage",type=string,JSONPath=`.spec.stage`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Outcome",type=string,JSONPath=`.status.outcome`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:selectablefield:JSONPath=`.spec.stage`
// +kubebuilder:selectablefield:JSONPath=`.status.outcome`

// Operation is one decision to change one facet of one Stage. Its spec is the
// intent and never changes after admission; to change your mind, cancel and
// propose again.
type Operation struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="an operation's intent is immutable: cancel it and propose again"
	Spec delivery.OperationSpec `json:"spec"`
	// +optional
	Status delivery.OperationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OperationList contains a list of Operation.
type OperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Operation `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:categories=pacto-delivery
// +kubebuilder:printcolumn:name="Operation",type=string,JSONPath=`.spec.operation`
// +kubebuilder:printcolumn:name="Decision",type=string,JSONPath=`.spec.decision`
// +kubebuilder:printcolumn:name="By",type=string,JSONPath=`.spec.by`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Approval is a human decision on one operation. It matches only on the
// operation's uid and intent digest, never on its name.
type Approval struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="an approval is immutable"
	Spec delivery.ApprovalSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ApprovalList contains a list of Approval.
type ApprovalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Approval `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Stage{}, &StageList{}, &Operation{}, &OperationList{}, &Approval{}, &ApprovalList{})
}
