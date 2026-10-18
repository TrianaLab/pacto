/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package nodeagent

import (
	"maps"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestDaemonSetAC_PodShapeAndPrivileges(t *testing.T) {
	ds := daemonSetAC(enabledCfg())
	pod := ds.Spec.Template.Spec
	c := pod.Containers[0]

	if *c.Image != "ghcr.io/trianalab/pacto/dashboard:3.5.0" || !slices.Equal(c.Command, []string{"pacto"}) {
		t.Errorf("image/command: %s %v", *c.Image, c.Command)
	}
	if !slices.Equal(c.Args, []string{"agent", "--sink=kubernetes", "--publish-interval=30s", "--cgroup-root=/host/sys/fs/cgroup"}) {
		t.Errorf("args: %v", c.Args)
	}
	env := map[string]string{}
	for _, e := range c.Env {
		env[*e.Name] = *e.ValueFrom.FieldRef.FieldPath
	}
	if env["NODE_NAME"] != "spec.nodeName" || env["POD_NAMESPACE"] != "metadata.namespace" {
		t.Errorf("env: %v", env)
	}

	// Exactly the measured privileges: hostPID, root, three capabilities.
	if !*pod.HostPID || *pod.SecurityContext.RunAsUser != 0 || *pod.SecurityContext.RunAsNonRoot {
		t.Error("pod must run hostPID as root")
	}
	if *pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("seccomp must stay RuntimeDefault")
	}
	sc := c.SecurityContext
	if !*sc.ReadOnlyRootFilesystem || *sc.AllowPrivilegeEscalation || sc.Privileged != nil {
		t.Error("container must be read-only, non-escalating and not privileged")
	}
	if !slices.Equal(sc.Capabilities.Drop, []corev1.Capability{"ALL"}) ||
		!slices.Equal(sc.Capabilities.Add, []corev1.Capability{"BPF", "PERFMON", "SYS_PTRACE"}) {
		t.Errorf("capabilities: %+v", sc.Capabilities)
	}
	if pod.HostNetwork != nil || len(c.Ports) > 0 {
		t.Error("no host network and no ports")
	}
	// One volume: the node's cgroup hierarchy, read-only, beside the runtime's
	// own /sys/fs/cgroup.
	if len(pod.Volumes) != 1 || *pod.Volumes[0].HostPath.Path != "/sys/fs/cgroup" ||
		*pod.Volumes[0].HostPath.Type != corev1.HostPathDirectory || len(c.VolumeMounts) != 1 ||
		*c.VolumeMounts[0].Name != *pod.Volumes[0].Name || *c.VolumeMounts[0].MountPath != CgroupMountPath ||
		!*c.VolumeMounts[0].ReadOnly {
		t.Errorf("volumes %+v, mounts %+v", pod.Volumes, c.VolumeMounts)
	}
	if c.LivenessProbe != nil || c.ReadinessProbe != nil || c.StartupProbe != nil {
		t.Error("the agent opens no port, so it carries no probe")
	}

	if *pod.ServiceAccountName != Name || !*pod.AutomountServiceAccountToken {
		t.Error("pod must run as the agent ServiceAccount with its token")
	}
	if len(pod.NodeSelector) != 1 || pod.NodeSelector["kubernetes.io/os"] != "linux" {
		t.Errorf("nodeSelector: %v, want linux nodes only", pod.NodeSelector)
	}
	if len(pod.Tolerations) != 1 || *pod.Tolerations[0].Operator != corev1.TolerationOpExists {
		t.Errorf("tolerations: %+v", pod.Tolerations)
	}
	if pod.PriorityClassName != nil || *pod.TerminationGracePeriodSeconds != 10 {
		t.Error("no priority class, short grace period")
	}
	if ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable.IntValue() != 1 {
		t.Error("rolling update one node at a time")
	}
	if ds.OwnerReferences != nil {
		t.Error("no owner reference: the operator's Deployment is in another namespace")
	}
}

func TestRoleAC_GrantsOnlyTheTransportObjects(t *testing.T) {
	role := roleAC(enabledCfg())
	if len(role.Rules) != 2 {
		t.Fatalf("want two rules, got %d", len(role.Rules))
	}
	r := role.Rules[0]
	if !slices.Equal(r.APIGroups, []string{"pacto.trianalab.io"}) ||
		!slices.Equal(r.Resources, []string{"noderuntimeobservations", "noderuntimeobservations/status"}) ||
		!slices.Equal(r.Verbs, []string{"get", "create", "patch"}) || !slices.Equal(r.Verbs, agentVerbs) {
		t.Errorf("observation rule: %+v", r)
	}
	l := role.Rules[1]
	if !slices.Equal(l.APIGroups, []string{"coordination.k8s.io"}) || !slices.Equal(l.Resources, []string{"leases"}) ||
		!slices.Equal(l.Verbs, []string{"get", "create", "patch", "update"}) || !slices.Equal(l.Verbs, leaseVerbs) {
		t.Errorf("lease rule: %+v", l)
	}
	rb := roleBindingAC(enabledCfg())
	if *rb.RoleRef.Name != Name || *rb.Subjects[0].Name != Name || *rb.Subjects[0].Namespace != "test-ns" {
		t.Errorf("binding: %+v", rb)
	}
}

// The agent namespace carries the privileged Pod Security levels and the
// component label, and no label the chart sets to another value.
func TestNamespaceAC_PrivilegedLevels(t *testing.T) {
	ns := namespaceAC(enabledCfg())
	if *ns.Name != "test-ns" {
		t.Fatalf("name = %s", *ns.Name)
	}
	want := map[string]string{
		LabelComponent:                       ComponentValue,
		"pod-security.kubernetes.io/enforce": "privileged",
		"pod-security.kubernetes.io/audit":   "privileged",
		"pod-security.kubernetes.io/warn":    "privileged",
	}
	if !maps.Equal(ns.Labels, want) {
		t.Errorf("labels = %v, want %v", ns.Labels, want)
	}
}

// No managed object carries an owner reference: the operator's Deployment
// lives in another namespace, and a cross-namespace owner is garbage-collected
// as missing.
func TestApplyConfigs_NoOwnerReference(t *testing.T) {
	cfg := enabledCfg()
	for name, refs := range map[string]int{
		"daemonset":      len(daemonSetAC(cfg).OwnerReferences),
		"serviceaccount": len(serviceAccountAC(cfg).OwnerReferences),
		"role":           len(roleAC(cfg).OwnerReferences),
		"rolebinding":    len(roleBindingAC(cfg).OwnerReferences),
	} {
		if refs != 0 {
			t.Errorf("%s: want no owner reference, got %d", name, refs)
		}
	}
}

func TestServiceAccountAC_ImagePullSecrets(t *testing.T) {
	if got := serviceAccountAC(enabledCfg()).ImagePullSecrets; len(got) != 0 {
		t.Errorf("default: want no pull secrets, got %+v", got)
	}
	cfg := enabledCfg()
	cfg.ImagePullSecrets = []string{"regcred", "mirror"}
	got := serviceAccountAC(cfg).ImagePullSecrets
	if len(got) != 2 || *got[0].Name != "regcred" || *got[1].Name != "mirror" {
		t.Errorf("pull secrets: %+v", got)
	}
}
