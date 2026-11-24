/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const (
	memberOperator = "system:serviceaccount:pacto:pacto-operator"
	memberAsker    = "oidc:alice@acme.com"
)

// requestedOp is the promote a member's intake raises for a request alice
// signed: the operator's own service account as the actor, alice as the
// person it acts for, on an advise Stage.
func requestedOp(t *testing.T) *env {
	t.Helper()
	o := opObj("billing-req-0123456789", delivery.TypePromote, dA, dB, memberOperator, "system:serviceaccounts")
	o.Spec.OnBehalfOf = &delivery.Principal{User: memberAsker}
	o.Spec.Reason = "requested by " + memberAsker + " from hub hub-uid (request sha256:0123456789ab)"
	o.Spec.IntentDigest = delivery.IntentDigest(o.Spec, stageObj("").Spec)
	e := baseEnv(t, o)
	e.setStage(func(s *delivery.StageStatus) { s.Level = delivery.LevelAdvise })
	return e
}

// TestRequestedOperationAdmittedAsAgent: the existing admission reads a
// member request's Operation as an agent's proposal on alice's behalf, and
// an advise Stage holds it for a person.
func TestRequestedOperationAdmittedAsAgent(t *testing.T) {
	e := requestedOp(t)
	o := e.drive("billing-req-0123456789")
	if o.Status.ActorClass != delivery.ClassAgent || o.Status.Phase != delivery.PhaseHeld || o.Spec.OnBehalfOf.User != memberAsker {
		t.Fatalf("%s %s %s", o.Status.ActorClass, o.Status.Phase, o.Status.Message)
	}
}

// TestRequesterCannotApproveOwnRequest: the person who signed the request
// never decides the Operation it raised; someone else does.
func TestRequesterCannotApproveOwnRequest(t *testing.T) {
	e := requestedOp(t)
	o := e.drive("billing-req-0123456789")
	e.approve(o, "own", memberAsker, delivery.Approve)
	o = e.drive(o.Name)
	if o.Status.Phase != delivery.PhaseHeld || !strings.Contains(o.Status.Message, memberAsker+" asked for this change") {
		t.Fatalf("%s %q", o.Status.Phase, o.Status.Message)
	}
	e.approve(o, "bob", "bob", delivery.Approve)
	if o = e.drive(o.Name); o.Status.Phase == delivery.PhaseHeld || len(o.Status.Approvals) != 1 || o.Status.Approvals[0].By != "bob" {
		t.Fatalf("%s %+v", o.Status.Phase, o.Status.Approvals)
	}
}
