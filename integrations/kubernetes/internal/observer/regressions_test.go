/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// TestHealthTierB_DeclaredPath is the regression for spec 6.3: a readiness probe proves the declared health
// endpoint only when it probes that path.
func TestHealthTierB_DeclaredPath(t *testing.T) {
	spec := &corev1.PodSpec{Containers: []corev1.Container{{
		Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{Path: "/ready"},
		}},
	}}}
	o := New(fake.NewClientBuilder().Build())
	for path, want := range map[string]bool{"": true, "/ready": true, "/healthz": false} {
		if got := o.hasHTTPGetReadinessProbe(spec, 8080, path); got != want {
			t.Errorf("declared path %q: got %v, want %v", path, got, want)
		}
	}
}

// TestDependency_OCIRefMatchesProductionResolvedRef is the regression for spec 6.2: contracts write OCI
// dependencies with the oci:// scheme while the loader stores resolvedRef without it.
func TestDependency_OCIRefMatchesProductionResolvedRef(t *testing.T) {
	f := newKernelFixture()
	c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(f.objects(false)...).Build()
	in := f.input(false)
	in.Contract.Dependencies = []contract.Dependency{{Name: "payments", Ref: "oci://registry/payments:1.2.0", Required: true}}
	set, _, _ := New(c).Collect(context.Background(), in)
	o, p := depObs(t, set, "payments")
	if o.Outcome != evidence.Observed || !p.Reachable || p.Version != "1.2.0" {
		t.Fatalf("got %s %+v, want the oci:// dependency resolved to the payments sibling", o.Outcome, p)
	}
}

// TestDependency_VersionTieBreak is the regression for a version rollout side by side: among several
// targets matching the ref, exactly one inside dep.Compatibility is the target; none or several inside it
// stays ambiguous; a single ref match is reported whatever its version, so Evaluate can flag it.
func TestDependency_VersionTieBreak(t *testing.T) {
	v1 := kPacto("payments-v1", "payments-v1", "payments", "1.4.0", "registry/payments:1.4.0")
	v2 := kPacto("payments-v2", "payments-v2", "payments", "2.0.0", "registry/payments:2.0.0")
	v1b := kPacto("payments-v1b", "payments-v1b", "payments", "1.5.0", "registry/payments:1.5.0")
	tests := []struct {
		name   string
		compat string
		objs   []client.Object
		want   string // serviceName, "" for no target
		ambig  bool
	}{
		{"one compatible of two", "^1.0.0", []client.Object{v1, v2}, "payments-v1", false},
		{"two compatible", "^1.0.0", []client.Object{v1, v2, v1b}, "", true},
		{"none compatible", "^3.0.0", []client.Object{v1, v2}, "", true},
		{"invalid range", "not a range", []client.Object{v1, v2}, "", true},
		{"single incompatible target is still reported", "^3.0.0", []client.Object{v2}, "payments-v2", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(tt.objs...).Build()
			dep := contract.Dependency{Name: "payments", Ref: "oci://registry/payments", Compatibility: tt.compat}
			got, ambig, err := New(c).resolveDependencyToSibling(context.Background(), kNS, dep)
			if err != nil || ambig != tt.ambig || (got == nil) != (tt.want == "") || (got != nil && got.serviceName != tt.want) {
				t.Fatalf("got %+v ambiguous=%v err=%v, want %q ambiguous=%v", got, ambig, err, tt.want, tt.ambig)
			}
		})
	}
}

// TestSharedClaim_ReadOnlyClaimsAreNotShared is the regression for a read-only claim counted as a shared
// writer: a claim opened read-only, or mounted read-only everywhere it is mounted, does not make the
// scope shared; one writable mount does.
func TestSharedClaim_ReadOnlyClaimsAreNotShared(t *testing.T) {
	pvc := func(readOnly bool) corev1.Volume {
		return corev1.Volume{Name: "data", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data", ReadOnly: readOnly},
		}}
	}
	mount := func(readOnly bool) []corev1.VolumeMount {
		return []corev1.VolumeMount{{Name: "other"}, {Name: "data", ReadOnly: readOnly}}
	}
	tests := []struct {
		name string
		vol  corev1.Volume
		init []corev1.VolumeMount
		app  []corev1.VolumeMount
		want bool
	}{
		{"claim opened read-only", pvc(true), nil, mount(false), false},
		{"mounted read-only everywhere", pvc(false), mount(true), mount(true), false},
		{"one writable init mount", pvc(false), mount(false), mount(true), true},
		{"one writable app mount", pvc(false), nil, mount(false), true},
		{"not mounted stays conservative", pvc(false), nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := makeDeployment(kNS, "checkout", &tt.vol)
			d.Spec.Replicas = new(int32(3))
			d.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "init", VolumeMounts: tt.init}}
			d.Spec.Template.Spec.Containers[0].VolumeMounts = tt.app
			c := fake.NewClientBuilder().WithScheme(kernelScheme()).WithObjects(d).Build()
			ctr := &contract.Contract{
				Service: contract.Service{Name: "checkout"},
				State:   &contract.State{Persistence: contract.Persistence{Durability: "persistent", Scope: "local"}},
			}
			set, _, _ := New(c).Collect(context.Background(), CollectInput{
				Namespace: kNS, WorkloadName: "checkout", WorkloadKind: workloadDeployment, Contract: ctr, Now: kNow,
			})
			p, err := obsFor(t, set, evidence.PersistenceObserved, "checkout").GetPersistenceObservation()
			if err != nil || !p.Durable || p.Shared == nil || *p.Shared != tt.want {
				t.Fatalf("got %+v err=%v, want durable shared=%v", p, err, tt.want)
			}
		})
	}
}
