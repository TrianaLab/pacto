/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CollectionState is the node agent's ability to observe the kernel.
// +kubebuilder:validation:Enum=Running;Degraded;Failed
type CollectionState string

const (
	// CollectionRunning means the eBPF program is attached and events flow.
	CollectionRunning CollectionState = "Running"
	// CollectionDegraded means events flow but some were lost or not attributed
	// (dropped events since the last resync, pod limit reached, /proc scan failed).
	CollectionDegraded CollectionState = "Degraded"
	// CollectionFailed means the eBPF program could not be loaded or attached.
	// Kernel facts on this node must not be read as evidence of absence.
	CollectionFailed CollectionState = "Failed"
)

// RuntimeCollection reports the health of the kernel collection on one node.
type RuntimeCollection struct {
	// State is Running, Degraded or Failed.
	State CollectionState `json:"state"`

	// Reason is a CamelCase machine-readable cause for a non-Running state.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable explanation of a non-Running state.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`

	// DroppedEvents is the cumulative number of kernel events lost because the
	// ring buffer was full, since the agent started.
	// +kubebuilder:validation:Minimum=0
	// +optional
	DroppedEvents int64 `json:"droppedEvents,omitempty"`

	// LastDropAt is when the agent last saw DroppedEvents increase.
	// +optional
	LastDropAt *metav1.Time `json:"lastDropAt,omitempty"`

	// LastResyncAt is when the agent last completed a /proc baseline scan.
	// +optional
	LastResyncAt *metav1.Time `json:"lastResyncAt,omitempty"`
}

// RuntimeListener is a TCP socket in LISTEN state inside a pod's network namespace.
type RuntimeListener struct {
	// Port is the local (container) port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Family is the socket address family.
	// +kubebuilder:validation:Enum=IPv4;IPv6
	Family string `json:"family"`

	// Address is the bound local address (0.0.0.0 or :: for all addresses). A
	// loopback-only listener does not serve traffic sent to the pod IP.
	// +kubebuilder:validation:MaxLength=45
	// +optional
	Address string `json:"address,omitempty"`

	// Process is the command name of the process that opened the listener, when
	// the agent saw it happen (empty when only the /proc baseline knows it).
	// +kubebuilder:validation:MaxLength=16
	// +optional
	Process string `json:"process,omitempty"`
}

// RuntimeConnection aggregates outbound TCP connection attempts from a pod to
// one peer address and port within the retention window.
type RuntimeConnection struct {
	// Address is the peer IP address.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	Address string `json:"address"`

	// Port is the peer port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Successes counts handshakes that reached ESTABLISHED.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Successes int64 `json:"successes,omitempty"`

	// Failures counts handshakes that went from SYN_SENT straight to CLOSE
	// (refused, reset or timed out).
	// +kubebuilder:validation:Minimum=0
	// +optional
	Failures int64 `json:"failures,omitempty"`

	// LastSuccessAt is the time of the most recent success.
	// +optional
	LastSuccessAt *metav1.Time `json:"lastSuccessAt,omitempty"`

	// LastFailureAt is the time of the most recent failure.
	// +optional
	LastFailureAt *metav1.Time `json:"lastFailureAt,omitempty"`
}

// PodRuntimeObservation is what the kernel showed for one pod on the node.
type PodRuntimeObservation struct {
	// UID is the pod UID, read from the pod's cgroup path.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=36
	UID string `json:"uid"`

	// NetnsShared is true when the pod's network namespace is not its own (the
	// host namespace for hostNetwork pods). Socket events in it cannot be
	// attributed to the pod, so listeners and connections stay empty.
	// +optional
	NetnsShared bool `json:"netnsShared,omitempty"`

	// ObservedSince is when the agent first mapped the pod's network namespace.
	// +optional
	ObservedSince *metav1.Time `json:"observedSince,omitempty"`

	// BaselineAt is the last /proc scan that saw the pod.
	// +optional
	BaselineAt *metav1.Time `json:"baselineAt,omitempty"`

	// Truncated is true when a per-pod or per-node bound was hit, so listeners or
	// connections may be missing entries.
	// +optional
	Truncated bool `json:"truncated,omitempty"`

	// Listeners are the pod's TCP listening sockets.
	// +kubebuilder:validation:MaxItems=64
	// +listType=atomic
	// +optional
	Listeners []RuntimeListener `json:"listeners,omitempty"`

	// Connections are the pod's outbound TCP peers within the retention window.
	// +kubebuilder:validation:MaxItems=128
	// +listType=atomic
	// +optional
	Connections []RuntimeConnection `json:"connections,omitempty"`
}

// NodeRuntimeObservationStatus is the node agent's published view of one node.
type NodeRuntimeObservationStatus struct {
	// NodeName is the Kubernetes node this object describes.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// AgentVersion is the node agent build version.
	// +optional
	AgentVersion string `json:"agentVersion,omitempty"`

	// KernelRelease is the node's kernel release (uname -r).
	// +optional
	KernelRelease string `json:"kernelRelease,omitempty"`

	// PublishIntervalSeconds is how often the agent publishes; readers derive
	// staleness from it.
	// +kubebuilder:validation:Minimum=1
	// +optional
	PublishIntervalSeconds int32 `json:"publishIntervalSeconds,omitempty"`

	// LastPublished is the heartbeat: the time of the most recent publish.
	// +optional
	LastPublished *metav1.Time `json:"lastPublished,omitempty"`

	// ObservingSince is when this agent process started.
	// +optional
	ObservingSince *metav1.Time `json:"observingSince,omitempty"`

	// Collection is the health of the kernel collection.
	// +optional
	Collection RuntimeCollection `json:"collection,omitzero"`

	// Pods are the pods running on the node, sorted by UID.
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	// +optional
	Pods []PodRuntimeObservation `json:"pods,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
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
	Status NodeRuntimeObservationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NodeRuntimeObservationList contains a list of NodeRuntimeObservation.
type NodeRuntimeObservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NodeRuntimeObservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NodeRuntimeObservation{}, &NodeRuntimeObservationList{})
}
