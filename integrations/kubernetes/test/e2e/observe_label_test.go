//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
)

// TestObserveNamespaceLabel proves, against a real apiserver, that under --enable-probing=auto a Pacto is
// probed only in a namespace whose owner labelled it pacto.trianalab.io/observe with probing, that a label
// change takes effect on the next reconcile and that RuntimeObserved names where the setting came from.
func TestObserveNamespaceLabel(t *testing.T) {
	healthContract := buildContract(contractSpec{
		name:   "app",
		ifaces: []ifaceSpec{{name: "api"}},
		caps:   []capSpec{{typ: "health", iface: "api", path: "/healthz"}},
	})
	target := pactov1alpha1.TargetRef{ServiceName: "svc", InterfaceBindings: apiBinding("api")}
	auto := reconcileOpts{probing: observer.ModeAuto, metrics: observer.ModeAuto}

	// setup creates a health-capable service in a fresh namespace carrying the given observe label (none when
	// empty) and returns the namespace and a counter of health requests the genuine prober made.
	setup := func(t *testing.T, label string) (string, *atomic.Int32) {
		t.Helper()
		ns := newNamespace(t)
		if label != "" {
			setObserveLabel(t, ns, label)
		}
		createService(t, ns, "svc", port)
		createEndpointSlice(t, ns, "svc", port, 1)
		var hits atomic.Int32
		startProbeServer(t, ns, "svc", port, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				hits.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		})
		createPacto(t, "p", ns, pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: healthContract}, Target: target})
		return ns, &hits
	}

	t.Run("labelled_namespace_is_probed", func(t *testing.T) {
		ns, hits := setup(t, "probing")
		p := reconcile(t, "p", ns, auto).pacto
		if hits.Load() == 0 {
			t.Fatal("expected the labelled namespace to be probed")
		}
		requireRuntimeObservedMessage(t, p, "probing on (namespace label pacto.trianalab.io/observe=probing)")
	})

	t.Run("unlabelled_namespace_is_not_probed_until_labelled", func(t *testing.T) {
		ns, hits := setup(t, "")
		p := reconcile(t, "p", ns, auto).pacto
		if n := hits.Load(); n != 0 {
			t.Fatalf("expected no probe in an unlabelled namespace, got %d", n)
		}
		requireRuntimeObservedMessage(t, p,
			"probing off (--enable-probing=auto, namespace has no pacto.trianalab.io/observe label)")

		// The namespace owner labels it: the next reconcile probes.
		setObserveLabel(t, ns, "probing")
		p = reconcile(t, "p", ns, auto).pacto
		if hits.Load() == 0 {
			t.Fatal("expected a probe on the reconcile after the label was set")
		}
		requireRuntimeObservedMessage(t, p, "probing on (namespace label pacto.trianalab.io/observe=probing)")
	})

	t.Run("metrics_label_leaves_probing_off", func(t *testing.T) {
		ns, hits := setup(t, "metrics")
		p := reconcile(t, "p", ns, auto).pacto
		if n := hits.Load(); n != 0 {
			t.Fatalf("expected no probe when the label names only metrics, got %d", n)
		}
		requireRuntimeObservedMessage(t, p, "does not name probing")
		requireRuntimeObservedMessage(t, p, "metrics on (namespace label pacto.trianalab.io/observe=metrics")
	})
}

func setObserveLabel(t *testing.T, ns, value string) {
	t.Helper()
	n := &corev1.Namespace{}
	if err := k8sClient.Get(testCtx, client.ObjectKey{Name: ns}, n); err != nil {
		t.Fatalf("get namespace %s: %v", ns, err)
	}
	if n.Labels == nil {
		n.Labels = map[string]string{}
	}
	n.Labels[observer.LabelObserve] = value
	if err := k8sClient.Update(testCtx, n); err != nil {
		t.Fatalf("label namespace %s: %v", ns, err)
	}
}

func requireRuntimeObservedMessage(t *testing.T, p *pactov1alpha1.Pacto, want string) {
	t.Helper()
	c := apimeta.FindStatusCondition(p.Status.Conditions, pactov1alpha1.ConditionRuntimeObserved)
	if c == nil {
		t.Fatalf("no %s condition on %s/%s", pactov1alpha1.ConditionRuntimeObserved, p.Namespace, p.Name)
	}
	if !strings.Contains(c.Message, want) {
		t.Fatalf("%s message = %q, want it to contain %q", pactov1alpha1.ConditionRuntimeObserved, c.Message, want)
	}
}
