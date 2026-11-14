/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// statsHook captures every operation the reconciler hands to Stats, and
// checks the conclusion is already stored when it does.
func statsHook(e *env, calls *[]dv1.Operation) func(*dv1.Operation) {
	return func(op *dv1.Operation) {
		stored := &dv1.Operation{}
		if err := e.c.Get(context.Background(), keyOf(op.Name), stored); err != nil || stored.Status.Phase != delivery.PhaseConcluded {
			e.t.Fatalf("Stats ran before the conclusion was stored: %v %s", err, stored.Status.Phase)
		}
		*calls = append(*calls, *op.DeepCopy())
	}
}

// shipAndVerify drives a promote from A to B to Succeeded Verified.
func shipAndVerify(e *env) *dv1.Operation {
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "Compliant")
	return e.drive("p1")
}

func TestConcludedEmitsOnce(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	var calls []dv1.Operation
	e.op.Stats = statsHook(e, &calls)
	wantOutcome(t, shipAndVerify(e), delivery.Succeeded, "Verified")
	e.drive("p1")
	if len(calls) != 1 || calls[0].Status.Verification != delivery.Verified {
		t.Fatalf("want one Stats call for one conclusion, got %d", len(calls))
	}
}

func TestConcludedEmitsOnceAcrossWriteRetry(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	var calls []dv1.Operation
	e.op.Stats = statsHook(e, &calls)
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	failed := false
	r := *e.op
	r.Client = interceptor.NewClient(e.c, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if o, ok := obj.(*dv1.Operation); ok && o.Status.Phase == delivery.PhaseConcluded && !failed {
				failed = true
				return apierrors.NewInternalError(errors.New("etcd timeout"))
			}
			return c.Status().Update(ctx, obj, opts...)
		},
	})
	e.op = &r
	if err := e.reconcileOpErr("p1"); err == nil || !failed {
		t.Fatalf("the injected write failure did not surface: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("a conclusion whose write failed must not reach Stats: %d calls", len(calls))
	}
	wantOutcome(t, e.drive("p1"), delivery.Failed, "VerifyFailed")
	if len(calls) != 1 {
		t.Fatalf("want one Stats call across the write retry, got %d", len(calls))
	}
}

// TestEveryOperationTypeRecords: every type reaches Stats through the one
// line in finish, with no per-type code. A stale intent is refused at
// admission for any type, which is the cheapest conclusion to reach.
func TestEveryOperationTypeRecords(t *testing.T) {
	for _, typ := range delivery.Types() {
		t.Run(typ.ID, func(t *testing.T) {
			op := opObj("o1", typ.ID, dA, dB, "alice")
			op.Spec.IntentDigest = "sha256:0"
			e := baseEnv(t, op)
			var calls []dv1.Operation
			e.op.Stats = statsHook(e, &calls)
			if o := e.drive("o1"); o.Status.Phase != delivery.PhaseConcluded {
				t.Fatalf("not concluded: %s %s", o.Status.Phase, o.Status.Reason)
			}
			if len(calls) != 1 || calls[0].Spec.Type != typ.ID || calls[0].Status.Phase != delivery.PhaseConcluded {
				t.Fatalf("want one concluded %s, got %d calls", typ.ID, len(calls))
			}
		})
	}
}

func TestNoStageEmitsWithNilPolicy(t *testing.T) {
	op := opObj("p1", delivery.TypePromote, dA, dB, bot)
	op.Spec.Stage = "missing"
	e := baseEnv(t, op)
	var calls []dv1.Operation
	e.op.Stats = statsHook(e, &calls)
	wantOutcome(t, e.drive("p1"), delivery.Rejected, "NoStage")
	if len(calls) != 1 || calls[0].Status.Policy != nil {
		t.Fatalf("want one Stats call with no policy, got %d", len(calls))
	}
}

// challenge is the random nonce an evaluation gate cites; it differs per run.
var challenge = regexp.MustCompile(`challenge\\":\\"[0-9a-f]+`)

func TestStatsHookNilChangesNothing(t *testing.T) {
	run := func(hook bool) string {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		if hook {
			var calls []dv1.Operation
			e.op.Stats = statsHook(e, &calls)
		}
		b, err := json.Marshal([]any{shipAndVerify(e).Status, e.getStage().Status})
		if err != nil {
			t.Fatal(err)
		}
		return challenge.ReplaceAllString(string(b), "challenge")
	}
	if off, on := run(false), run(true); off != on {
		t.Fatalf("Stats changed the conclusion:\n%s\n%s", off, on)
	}
}
