/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package admission

import (
	"context"
	"testing"
)

// The names the promoter raises its own operations under are reserved: an
// agent or a person taking one first would keep a failure out of the records.
func TestOperationNamesThePromoterRaises(t *testing.T) {
	ctx := context.Background()
	const msg = "the Pacto delivery promoter raises its own operations under"
	for _, name := range []string{"promote-s-rollback", "promote-s-freeze", "promote-s-stepdown",
		"payments-auto-0123456789", "payments-witnessed-abcdef0123"} {
		denied(t, as("alice").Create(ctx, operation(name, "alice", nil)), msg)
	}
	for _, name := range []string{"payments-rollback-x7k2p", "undo-promote-s", "payments-auto-12"} {
		allowed(t, as("alice").Create(ctx, operation(name, "alice", nil)))
	}
	allowed(t, as(controller).Create(ctx, operation("promote-t-rollback", controller, nil)))
}
