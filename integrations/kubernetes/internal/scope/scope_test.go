/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package scope

import (
	"maps"
	"slices"
	"testing"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestNamespacesParsesList(t *testing.T) {
	got := Namespaces(" payments, ledger ,,payments", "")
	if want := []string{"ledger", "payments"}; !slices.Equal(got, want) {
		t.Fatalf("Namespaces = %v, want %v", got, want)
	}
}

// The deprecated --watch-namespace merges into the list, so a chart that still
// renders it keeps scoping the operator.
func TestNamespacesAliasMerges(t *testing.T) {
	if got, want := Namespaces("b,a", "a"), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("Namespaces = %v, want %v", got, want)
	}
	if got, want := Namespaces("", " legacy "), []string{"legacy"}; !slices.Equal(got, want) {
		t.Fatalf("Namespaces = %v, want %v", got, want)
	}
}

func TestNamespacesEmptyIsClusterWide(t *testing.T) {
	if got := Namespaces(" , ", ""); got != nil {
		t.Fatalf("Namespaces = %v, want nil (cluster-wide)", got)
	}
}

func TestCacheOptionsOneEntryPerNamespace(t *testing.T) {
	opts := CacheOptions([]string{"ledger", "payments"}, "", false)
	if got := slices.Sorted(maps.Keys(opts.DefaultNamespaces)); !slices.Equal(got, []string{"ledger", "payments"}) {
		t.Fatalf("DefaultNamespaces = %v", got)
	}
	if len(opts.ByObject) != 1 {
		t.Fatalf("ByObject has %d entries, want the Pod entry only", len(opts.ByObject))
	}
	for obj, bo := range opts.ByObject {
		if _, ok := obj.(*corev1.Pod); !ok || bo.Transform == nil {
			t.Fatalf("want only the trimmed Pod entry, got %T", obj)
		}
	}
	if cw := CacheOptions(nil, "", false); cw.DefaultNamespaces != nil {
		t.Fatalf("cluster-wide DefaultNamespaces = %v, want nil", cw.DefaultNamespaces)
	}
}

// The node agent's kinds stay on the kernel evidence namespace whatever the
// watched list says.
func TestCacheOptionsKernelNamespaceKept(t *testing.T) {
	opts := CacheOptions([]string{"payments"}, "pacto-agent", true)
	var kinds int
	for obj, bo := range opts.ByObject {
		switch obj.(type) {
		case *pactov1.NodeRuntimeObservation, *coordinationv1.Lease:
			kinds++
			if got := slices.Collect(maps.Keys(bo.Namespaces)); !slices.Equal(got, []string{"pacto-agent"}) {
				t.Fatalf("%T namespaces = %v, want [pacto-agent]", obj, got)
			}
		}
	}
	if kinds != 2 {
		t.Fatalf("want the observation and Lease entries, got %d", kinds)
	}
}
