/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"maps"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func pod(ns, ctrlKind, ctrlName string, uid types.UID, image, imageID string) corev1.Pod {
	yes := true
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "p"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", ImageID: imageID}}},
	}
	if ctrlKind != "" {
		p.OwnerReferences = []metav1.OwnerReference{{Kind: ctrlKind, Name: ctrlName, UID: uid, Controller: &yes}}
	}
	return p
}

func TestDigestsThroughReplicaSet(t *testing.T) {
	owners := map[types.UID]Owner{"rs1": {Kind: "Deployment", Name: "payments"}}
	pods := []corev1.Pod{
		pod("shop", "ReplicaSet", "payments-abc", "rs1", "ghcr.io/acme/payments:2.3.0", "ghcr.io/acme/payments@"+dA),
		pod("shop", "ReplicaSet", "payments-abc", "rs1", "ghcr.io/acme/payments:2.3.0", "docker-pullable://ghcr.io/acme/payments@"+dA),
		pod("shop", "StatefulSet", "db", "ss1", "postgres@"+dB, "docker.io/library/postgres@"+dB),
		pod("shop", "ReplicaSet", "orphan-rs", "rs2", "ghcr.io/acme/x:1", "ghcr.io/acme/x@"+dA),
		pod("shop", "", "", "", "ghcr.io/acme/bare:1", "ghcr.io/acme/bare@"+dA),
	}
	got := Digests(pods, owners)
	want := map[Key][]RunningImage{
		{"shop", "Deployment", "payments"}: {{Repository: "ghcr.io/acme/payments", Tag: "2.3.0", Digest: dA}},
		{"shop", "StatefulSet", "db"}:      {{Repository: "docker.io/library/postgres", Digest: dB}},
	}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDigestsThroughJob(t *testing.T) {
	owners := map[types.UID]Owner{"job1": {Kind: "CronJob", Name: "export"}}
	got := Digests([]corev1.Pod{pod("ops", "Job", "export-1", "job1", "localhost:5000/export:1.0.0", "localhost:5000/export@"+dA)}, owners)
	if imgs := got[Key{"ops", "CronJob", "export"}]; len(imgs) != 1 || imgs[0].Repository != "localhost:5000/export" {
		t.Fatalf("got %+v", got)
	}
}

func TestDigestOfRefusesConfigID(t *testing.T) {
	got := Digests([]corev1.Pod{pod("shop", "DaemonSet", "agent", "ds1", "agent:1", dA)}, nil)
	if len(got) != 0 {
		t.Fatalf("a config id was matched: %+v", got)
	}
	for _, id := range []string{dA, "repo@sha256:abc", ""} {
		if _, ok := digestOf(id); ok {
			t.Errorf("%q accepted", id)
		}
	}
	if d, ok := digestOf("docker-pullable://repo@" + dA); !ok || d != dA {
		t.Errorf("docker-pullable id refused: %q", d)
	}
}
