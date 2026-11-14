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

// An agent names the person it acts for, and that person may not approve:
// admission refuses an agent that names nobody, itself, a service account or
// the promoter. The promoter and break-glass need no one.
func TestOperationAgentNamesAPerson(t *testing.T) {
	ctx := context.Background()
	const msg = "must name the person it acts for"
	const bot = "deploy-bot"
	const runner = "system:serviceaccount:ci:runner"
	saGroups := []string{"system:serviceaccounts", "system:serviceaccounts:ci"}
	onBehalf := func(user, who string, groups ...string) error {
		op := operation(uniq("agent"), user, groups)
		if who != "" {
			op.Object["spec"].(map[string]any)["onBehalfOf"] = map[string]any{"user": who}
		}
		return as(user, groups...).Create(ctx, op)
	}
	denied(t, onBehalf(bot, "", agents), msg)
	denied(t, onBehalf(bot, bot, agents), msg)
	denied(t, onBehalf(bot, runner, agents), msg)
	denied(t, onBehalf(bot, controller, agents), msg)
	denied(t, onBehalf(runner, "", saGroups...), msg)
	allowed(t, onBehalf(bot, "dana@example.com", agents))
	allowed(t, onBehalf(runner, "dana@example.com", saGroups...))
	allowed(t, onBehalf(controller, ""))
	allowed(t, onBehalf("bo", "", agents, breakGlass))
	allowed(t, onBehalf("alice", ""))
}
