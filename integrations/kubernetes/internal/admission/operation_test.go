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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
// admission refuses an agent that names nobody, itself, a service account, a
// system user or the promoter. The promoter and break-glass need no one.
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
	denied(t, onBehalf(bot, "system:kube-scheduler", agents), msg)
	denied(t, onBehalf(runner, "", saGroups...), msg)
	// The promoter trims and case-folds the name, and admission mirrors it.
	denied(t, onBehalf(bot, "   ", agents), msg)
	denied(t, onBehalf(bot, " DEPLOY-BOT", agents), msg)
	denied(t, onBehalf(bot, " "+runner, agents), msg)
	denied(t, onBehalf(bot, " "+strings.ToUpper(controller)+" ", agents), msg)
	allowed(t, onBehalf(bot, "dana@example.com", agents))
	allowed(t, onBehalf(runner, "dana@example.com", saGroups...))
	allowed(t, onBehalf(controller, ""))
	allowed(t, onBehalf("bo", "", agents, breakGlass))
	allowed(t, onBehalf("alice", ""))
}

// trustOp is an Operation of the trust type user may create, asking to pin
// a CI identity.
func trustOp(name, user string, groups ...string) *unstructured.Unstructured {
	op := operation(name, user, groups)
	spec := op.Object["spec"].(map[string]any)
	spec["type"] = "delivery.pacto.trianalab.io/trust"
	spec["facet"] = "trust"
	spec["params"] = map[string]any{"to": digestB, "publisher": map[string]any{
		"issuer":     "https://token.actions.githubusercontent.com",
		"subject":    "https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/main",
		"repository": "https://github.com/acme/payments",
	}}
	return op
}

// Only the promoter asks a person to confirm a new signer: a person or an
// agent raising a trust operation could put an identity of their choosing
// in front of an approver.
func TestTrustOnlyFromPromoter(t *testing.T) {
	ctx := context.Background()
	const msg = "only the Pacto delivery promoter raises a trust operation"
	denied(t, as("alice").Create(ctx, trustOp(uniq("trust"), "alice")), msg)
	bot := trustOp(uniq("trust"), "deploy-bot", agents)
	bot.Object["spec"].(map[string]any)["onBehalfOf"] = map[string]any{"user": "dana@example.com"}
	denied(t, as("deploy-bot", agents).Create(ctx, bot), msg)
	allowed(t, as(controller).Create(ctx, trustOp(uniq("trust"), controller)))
}

// A <stage>-trust-<hex> name is the promoter's: a person taking one first,
// for any type, would keep the promoter from raising the question.
func TestTrustNameReserved(t *testing.T) {
	ctx := context.Background()
	const msg = "the Pacto delivery promoter raises its own operations under"
	denied(t, as("alice").Create(ctx, operation("payments-trust-0123456789", "alice", nil)), msg)
	allowed(t, as("alice").Create(ctx, operation("payments-trust-12", "alice", nil)))
}
