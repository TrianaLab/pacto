/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package admission

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The operation that shipped what runs is what re-checks, rollbacks and
// history cite: once its pointer moved, nobody but the promoter (its TTL)
// and the namespace controller deletes it, not even the person who raised it.
func TestCancelKeepsAnAppliedOperation(t *testing.T) {
	ctx := context.Background()
	const msg = "the pointer moved for this operation"
	shipped := func(phase string) *unstructured.Unstructured {
		op := mustCreate(t, as("alice"), operation(uniq("shipped"), "alice", nil))
		allowed(t, setStatus(as(controller), op, map[string]any{"applied": true, "phase": phase}))
		return op
	}
	running := shipped("Verifying")
	for _, c := range []client.Client{as("alice"), as("root", breakGlass), as(controller)} {
		denied(t, c.Delete(ctx, running), msg)
	}
	done := shipped("Concluded")
	denied(t, as("alice").Delete(ctx, done), msg)
	denied(t, as("root", breakGlass).Delete(ctx, done), msg)
	allowed(t, as(controller).Delete(ctx, done))
	allowed(t, as(nsCtrl).Delete(ctx, shipped("Concluded")))

	// An operation that never moved the pointer is cancelled by its raiser.
	allowed(t, as("alice").Delete(ctx, mustCreate(t, as("alice"), operation(uniq("pending"), "alice", nil))))
}
