/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package admission

import (
	"context"
	"strings"
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

// A correlation id is printed raw by other users' CLIs and signed into
// records: admission refuses one the client would refuse.
func TestOperationCorrelationIDBound(t *testing.T) {
	ctx := context.Background()
	const msg = "spec.correlationID is at most 128 printable ASCII characters"
	withID := func(id string) error {
		op := operation(uniq("corr"), "alice", nil)
		op.Object["spec"].(map[string]any)["correlationID"] = id
		return as("alice").Create(ctx, op)
	}
	denied(t, withID(strings.Repeat("x", 129)), msg)
	denied(t, withID("CHG-\u202e2041"), msg)
	allowed(t, withID("CHG-2041"))
	allowed(t, withID(strings.Repeat("x", 128)))
}
