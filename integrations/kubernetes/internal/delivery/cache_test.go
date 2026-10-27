/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// staleCache gives both reconcilers a cache holding objs, which the
// apiserver has since changed: an informer a watch event behind.
func (e *env) staleCache(objs ...client.Object) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()
	e.st.Cluster.Cache, e.op.Cluster.Cache = c, c
}

// TestStaleCacheNeverWitnesses: a cached apply OCIRepository showing an edit
// the apiserver no longer holds raises no witnessed override and freezes
// nothing; once the apiserver holds the edit too, it is witnessed.
func TestStaleCacheNeverWitnesses(t *testing.T) {
	e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "latest@"+dA, false),
		kustomization("payments", "payments", "True", "", "latest@"+dA, "latest@"+dA))
	e.staleCache(ociRepo("payments", dC, "latest@"+dC, false))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if ops := e.ops(); len(ops) != 0 || e.getStage().Status.Frozen {
		t.Fatalf("a stale cache witnessed an edit: %+v", ops)
	}
	u := ociRepo("payments", dC, "latest@"+dC, false)
	_ = e.c.Delete(t.Context(), u)
	e.create(u)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if ops := e.ops(); len(ops) != 1 || !ops[0].Spec.Witnessed || ops[0].Spec.Params.To != dC {
		t.Fatalf("want one witnessed override to C: %+v", ops)
	}
}

// TestStaleCacheShipsOnLiveDigest: the ship path decides on the apiserver's
// digest. A cached apply OCIRepository showing a foreign digest neither
// freezes the operation as a foreign writer nor stops the pointer write.
func TestStaleCacheShipsOnLiveDigest(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.staleCache(ociRepo("payments", dC, "latest@"+dC, true))
	e.drive("p1")
	if d := e.ociDigest("payments"); d != dB {
		t.Fatalf("the pointer write was skipped: %s", d)
	}
	e.apply(dB)
	o := e.drive("p1")
	if o.Status.Reason == "ForeignWriter" || !o.Status.Applied || e.getStage().Status.Frozen {
		t.Fatalf("a stale cache stopped the ship: %s %s", o.Status.Reason, o.Status.Message)
	}
}
