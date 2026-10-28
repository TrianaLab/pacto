package delivery

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestAppliedMatchesSourceNamespace: applied() finds the Kustomization that
// applies the OCIRepository by its sourceRef namespace, the rule mediated()
// and the watch keys use. A Kustomization in another namespace whose sourceRef
// names this one counts; one in another namespace applying its own same-named
// repository does not.
func TestAppliedMatchesSourceNamespace(t *testing.T) {
	ctx := context.Background()
	repo := ociRepo("payments", dB, "latest@"+dB, true)

	// Cross-namespace applier: the only Kustomization sits in "elsewhere".
	cross := kustomization("payments", "payments", "True", "ReconciliationSucceeded", "latest@"+dB, "latest@"+dB)
	cross.SetNamespace("elsewhere")
	cross.Object["spec"].(map[string]any)["sourceRef"].(map[string]any)["namespace"] = ns
	e := newEnv(t, repo, cross)
	c := Cluster{Client: e.c}
	a, err := c.freshApplier(ctx, ns, delivery.ApplyRef{Name: "payments"})
	if err != nil {
		t.Fatal(err)
	}
	if ok, failed, msg, err := c.applied(ctx, ns, a, dB); err != nil || !ok || failed {
		t.Fatalf("a cross-namespace applier that applied B: ok=%v failed=%v %q %v", ok, failed, msg, err)
	}

	// Same-named repository in another namespace: its failure is not ours.
	own := kustomization("payments", "payments", "True", "ReconciliationSucceeded", "latest@"+dB, "latest@"+dB)
	other := kustomization("payments", "payments", "False", "BuildFailed", "", "latest@"+dB)
	other.SetNamespace("elsewhere")
	e = newEnv(t, ociRepo("payments", dB, "latest@"+dB, true), own, other)
	c = Cluster{Client: e.c}
	if a, err = c.freshApplier(ctx, ns, delivery.ApplyRef{Name: "payments"}); err != nil {
		t.Fatal(err)
	}
	if ok, failed, msg, err := c.applied(ctx, ns, a, dB); err != nil || !ok || failed {
		t.Fatalf("another namespace's applier must not count: ok=%v failed=%v %q %v", ok, failed, msg, err)
	}
}
