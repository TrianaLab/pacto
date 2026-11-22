/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func svc(name string, selector map[string]string) corev1.Service {
	return corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: corev1.ServiceSpec{Selector: selector}}
}

func TestServiceForWorkloadNeedsExactlyOne(t *testing.T) {
	pods := map[string]string{"app": "payments", "tier": "api"}
	one := make([]corev1.Service, 0, 4)
	one = append(one,
		svc("payments", map[string]string{"app": "payments"}),
		svc("billing", map[string]string{"app": "billing"}),
		svc("headless", nil),
	)
	if got := ServiceFor(one, pods); got != "payments" {
		t.Fatalf("one match: got %q", got)
	}
	two := append(one, svc("payments-api", map[string]string{"tier": "api"}))
	if got := ServiceFor(two, pods); got != "" {
		t.Fatalf("two matches: got %q", got)
	}
	if got := ServiceFor(one[1:], pods); got != "" {
		t.Fatalf("no match: got %q", got)
	}
}
