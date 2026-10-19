/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package nodeagent is the operator-managed node agent component. It mirrors the
// Evidence Server component: the operator translates chart values into
// controller configuration and reconciles a DaemonSet that runs `pacto agent
// --sink kubernetes` from the pacto runtime image (the dashboard image the
// operator was built with) on every node, plus the ServiceAccount, Role and
// RoleBinding that let each agent write exactly two kinds of object — its
// node's NodeRuntimeObservation and heartbeat Lease. Everything lives in the
// agent's own namespace (DefaultNamespace unless configured), the only one
// labelled privileged for Pod Security, so the operator namespace keeps the
// level its owner chose. The agent observes TCP socket state with eBPF; it is
// not a trace pipeline and opens no port.
package nodeagent

const (
	// Name is the resource name for every managed node agent resource.
	Name = "pacto-node-agent"

	// DefaultNamespace is the agent namespace when none is configured. The
	// RBAC markers name it, and the chart renders its Roles there by default.
	DefaultNamespace = "pacto-node-agent"

	// The labels are the agent's own: the NodeRuntimeObservation objects and
	// Leases it publishes carry the same set (the core module's kubesink writes
	// them), so disabling the component prunes them with the workload that wrote
	// them.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelName      = "app.kubernetes.io/name"
	ManagedByValue = "pacto-operator"
	ComponentValue = "node-agent"

	// FieldManager is the server-side apply field manager for node agent
	// resources. It differs from the agent's own field owner on purpose: the
	// operator owns the workload, the agent owns its observations.
	FieldManager = "pacto-operator-node-agent"

	// Command is the pacto binary in the runtime image.
	Command = "pacto"

	// CgroupMountPath is where the host's cgroup v2 hierarchy is mounted in the
	// agent pod. Not /sys/fs/cgroup: the runtime mounts the container's own
	// cgroup there, which shows none of the node's workloads.
	CgroupMountPath = "/host/sys/fs/cgroup"

	// PodSecurityEnforceLabel is the namespace label Pod Security Admission
	// enforces. The agent pod is privileged in PSS terms (hostPID, root, added
	// capabilities, a hostPath), so only an unset or `privileged` level admits it.
	// The operator sets it (with audit and warn) on the agent namespace.
	PodSecurityEnforceLabel = "pod-security.kubernetes.io/enforce"
)

// Labels returns the standard labels applied to all node agent resources.
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
