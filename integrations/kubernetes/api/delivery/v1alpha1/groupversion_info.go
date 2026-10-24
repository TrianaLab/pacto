/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package v1alpha1 contains the Pacto delivery API: Stage, Operation and
// Approval. It is a separate group from pacto.trianalab.io so its CRDs ship only
// with the opt-in pacto-delivery chart, never with the observing operator.
// +kubebuilder:object:generate=true
// +groupName=delivery.pacto.trianalab.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion} //nolint:staticcheck // matches the pacto group's kubebuilder scaffold.

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
