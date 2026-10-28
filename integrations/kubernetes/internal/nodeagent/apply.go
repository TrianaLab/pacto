/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package nodeagent

import (
	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"
)

// agentVerbs is everything the agent's Role grants on its observation:
// server-side apply is a patch, and an apply that creates the object also needs
// create. The agent never lists, watches, updates or deletes it. leaseVerbs add
// update, the verb a Lease holder renews with. The operator must hold each of
// these verbs itself (no escalate or bind), so the chart's agent-namespace Role
// and the RBAC markers mirror both lists.
var (
	agentVerbs = []string{"get", "create", "patch"}
	leaseVerbs = []string{"get", "create", "patch", "update"}
)

// PodSecurityLevels are the Pod Security Admission labels the operator sets on
// the agent namespace: the agent pod is privileged in PSS terms (hostPID, root,
// added capabilities, a hostPath), so enforce, audit and warn all admit it.
var PodSecurityLevels = map[string]string{
	PodSecurityEnforceLabel:            "privileged",
	"pod-security.kubernetes.io/audit": "privileged",
	"pod-security.kubernetes.io/warn":  "privileged",
}

// namespaceAC returns the agent namespace's privileged Pod Security levels and
// component label. The chart creates the namespace with the same levels (the
// Roles that let the operator manage the agent live in it); the operator
// re-asserts them on every sync, so a relabelled namespace is repaired rather
// than silently rejecting the agent pods. It sets no label the chart sets to a
// different value (managed-by, name), so Helm's server-side apply and this one
// never conflict.
func namespaceAC(cfg Config) *corev1ac.NamespaceApplyConfiguration {
	return corev1ac.Namespace(cfg.Namespace).
		WithLabels(map[string]string{component.LabelComponent: ComponentValue}).
		WithLabels(PodSecurityLevels)
}

// serviceAccountAC returns the agent's ServiceAccount.
func serviceAccountAC(cfg Config) *corev1ac.ServiceAccountApplyConfiguration {
	sa := corev1ac.ServiceAccount(Name, cfg.Namespace).WithLabels(Labels())
	for _, name := range cfg.ImagePullSecrets {
		sa.WithImagePullSecrets(corev1ac.LocalObjectReference().WithName(name))
	}
	return sa
}

// roleAC returns the agent's Role: its only permissions are its own transport
// object and its heartbeat Lease, in the agent namespace. It reads no pods and
// no nodes from the API — the agent learns everything from the kernel and /proc
// on its own node. Which object of each kind an agent may write (its own node's)
// is enforced by the chart's ValidatingAdmissionPolicy, not by RBAC.
func roleAC(cfg Config) *rbacv1ac.RoleApplyConfiguration {
	return rbacv1ac.Role(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups(pactov1alpha1.GroupVersion.Group).
				WithResources("noderuntimeobservations", "noderuntimeobservations/status").
				WithVerbs(agentVerbs...),
			rbacv1ac.PolicyRule().
				WithAPIGroups(coordinationv1.GroupName).
				WithResources("leases").
				WithVerbs(leaseVerbs...),
		)
}

// roleBindingAC binds the Role to the agent's ServiceAccount.
func roleBindingAC(cfg Config) *rbacv1ac.RoleBindingApplyConfiguration {
	return rbacv1ac.RoleBinding(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithSubjects(rbacv1ac.Subject().
			WithKind("ServiceAccount").
			WithName(Name).
			WithNamespace(cfg.Namespace)).
		WithRoleRef(rbacv1ac.RoleRef().
			WithAPIGroup("rbac.authorization.k8s.io").
			WithKind("Role").
			WithName(Name))
}

// daemonSetAC returns the agent DaemonSet: one pod per node, every taint
// tolerated, because a node the agent skips is a node whose pods read Unknown.
//
// The pod carries exactly the measured privileges and nothing more: hostPID so
// /proc shows every process and network namespace on the node, the node's
// cgroup hierarchy read-only so a socket's cgroup id names its pod, root so
// /proc of other users' processes is readable, and the BPF, PERFMON and
// SYS_PTRACE capabilities (load the program, attach the raw tracepoint,
// readlink /proc/<pid>/ns/net). It is not `privileged`, escalates nothing,
// keeps the runtime's default seccomp profile and writes nothing to its
// filesystem.
//
// There are no probes. The agent opens no port, so an HTTP probe would add the
// only network surface it has; and a hung agent already shows as a stale
// heartbeat (its node's Lease stops being renewed), which the operator turns
// into stale evidence rather than trusting it.
func daemonSetAC(cfg Config) *appsv1ac.DaemonSetApplyConfiguration {
	res := cfg.Resources.Build(DefaultResources())

	container := corev1ac.Container().
		WithName("node-agent").
		WithImage(cfg.Image).
		WithCommand(Command).
		WithArgs("agent", "--sink=kubernetes", "--publish-interval="+cfg.PublishInterval.String(),
			"--cgroup-root="+CgroupMountPath).
		WithEnv(
			corev1ac.EnvVar().WithName("NODE_NAME").WithValueFrom(
				corev1ac.EnvVarSource().WithFieldRef(corev1ac.ObjectFieldSelector().WithFieldPath("spec.nodeName"))),
			corev1ac.EnvVar().WithName("POD_NAMESPACE").WithValueFrom(
				corev1ac.EnvVarSource().WithFieldRef(corev1ac.ObjectFieldSelector().WithFieldPath("metadata.namespace"))),
		).
		WithSecurityContext(
			corev1ac.SecurityContext().
				WithReadOnlyRootFilesystem(true).
				WithAllowPrivilegeEscalation(false).
				WithCapabilities(corev1ac.Capabilities().
					WithDrop("ALL").
					WithAdd("BPF", "PERFMON", "SYS_PTRACE")),
		).
		WithVolumeMounts(corev1ac.VolumeMount().WithName("cgroup").WithMountPath(CgroupMountPath).WithReadOnly(true)).
		WithResources(corev1ac.ResourceRequirements().WithRequests(res.Requests).WithLimits(res.Limits))

	return appsv1ac.DaemonSet(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithSpec(appsv1ac.DaemonSetSpec().
			WithSelector(metav1ac.LabelSelector().WithMatchLabels(SelectorLabels())).
			WithUpdateStrategy(appsv1ac.DaemonSetUpdateStrategy().
				WithType(appsv1.RollingUpdateDaemonSetStrategyType).
				WithRollingUpdate(appsv1ac.RollingUpdateDaemonSet().WithMaxUnavailable(intstr.FromInt32(1)))).
			WithTemplate(corev1ac.PodTemplateSpec().
				WithLabels(Labels()).
				WithSpec(corev1ac.PodSpec().
					WithServiceAccountName(Name).
					// The agent writes its observation through the API, so it needs
					// its token.
					WithAutomountServiceAccountToken(true).
					WithHostPID(true).
					WithSecurityContext(
						corev1ac.PodSecurityContext().
							WithRunAsUser(0).
							WithRunAsNonRoot(false).
							WithSeccompProfile(corev1ac.SeccompProfile().WithType(corev1.SeccompProfileTypeRuntimeDefault)),
					).
					// eBPF and /proc are Linux only; a Windows node has nothing to run it.
					WithNodeSelector(map[string]string{corev1.LabelOSStable: "linux"}).
					WithTolerations(corev1ac.Toleration().WithOperator(corev1.TolerationOpExists)).
					WithContainers(container).
					WithVolumes(corev1ac.Volume().WithName("cgroup").WithHostPath(
						corev1ac.HostPathVolumeSource().WithPath("/sys/fs/cgroup").WithType(corev1.HostPathDirectory))).
					// Shutdown is detaching one program and closing its maps.
					WithTerminationGracePeriodSeconds(10),
				),
			),
		)
}
