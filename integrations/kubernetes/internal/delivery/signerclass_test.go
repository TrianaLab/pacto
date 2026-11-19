/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"strings"
	"testing"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	ciApprover       = "https://github.com/acme/app/.github/workflows/ship.yml@refs/heads/main"
	workloadApprover = "https://kubernetes.io/namespaces/agents/serviceaccounts/reviewer"
)

// ciToken is a token of acme's CI workflow, issued by personIssuer so the
// installation's signer verifies its certificate.
func ciToken() string {
	return sigtest.CIToken(ciApprover, personIssuer, "https://github.com/acme/app", "refs/heads/main")
}

// signedRefWith is signedRef signed with the OIDC token tok.
func (e *env) signedRefWith(reg *testRegistry, ss *sigtest.Sigstore, tok string, o *dv1.Operation) string {
	e.t.Helper()
	const stream = "cid/" + ns + "/payments-prod"
	return pushApproval(e.t, reg.host+"/delivery/approvals", stream, o, signedApprovalWith(e.t, ss, tok, o, stream))
}

// TestKeylessCISignedApprovalIsProposal and the workload twin: a verified
// approval record a CI workflow or a Kubernetes service account signed is a
// proposal, so it never counts, whatever the identity policy says of the
// identity.
func TestKeylessCISignedApprovalIsProposal(t *testing.T) {
	for name, tc := range map[string]struct{ tok, class string }{
		"ci":       {ciToken(), "ci"},
		"workload": {sigtest.Token(workloadApprover, personIssuer), "agent"},
	} {
		t.Run(name, func(t *testing.T) {
			e, o, ss, reg := heldKeylessEnv(t)
			e.op.Policy.Signers[0].Subjects = ".*"
			e.view("v", o, e.signedRefWith(reg, ss, tc.tok, o))
			o = e.drive("p1")
			if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "it is a proposal: signed by "+tc.class+" ") ||
				!strings.Contains(o.Status.Message, "(default:") {
				t.Fatalf("%+v %q", o.Status.Approvals, o.Status.Message)
			}
		})
	}
}

// TestPersonApprovalStillCounts: a person's verified keyless approval is a
// decision and counts as before.
func TestPersonApprovalStillCounts(t *testing.T) {
	e, o, ss, reg := heldKeylessEnv(t)
	e.view("v", o, e.signedRef(reg, ss, alice, o))
	if o = e.drive("p1"); len(o.Status.Approvals) != 1 || o.Status.Approvals[0].By != aliceUser {
		t.Fatalf("a person's approval did not count: %+v %q", o.Status.Approvals, o.Status.Message)
	}
}

// TestTrustOperationNeedsPersonApproval: a CI-signed approval of a trust
// operation never confirms the signer: the operation stays held, so the
// recorder writes no pin, and a pin that cites such an approval is never
// used.
func TestTrustOperationNeedsPersonApproval(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	o := w.e.drive(w.trustOps()[0].Name)
	w.e.op.Policy = withSigners(w.e.op.Policy, w.ss)
	w.e.op.Policy.TeamNarrowing = false
	reg := newTestRegistry(t)
	w.e.view("v", o, w.e.signedRefWith(reg, w.ss, ciToken(), o))
	if o = w.e.drive(o.Name); o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 0 ||
		!strings.Contains(o.Status.Message, "it is a proposal: signed by ci") {
		t.Fatalf("phase %s approvals %+v message %q", o.Status.Phase, o.Status.Approvals, o.Status.Message)
	}

	re := newRecEnv(t)
	re.stage(1)
	op := &dv1.Operation{ObjectMeta: objMeta(delivery.TrustOperationName("payments-prod", otherID))}
	ss := sigtest.New(t)
	ref := pushApproval(t, re.rec.Store.Streams, recStream, op, signedApprovalWith(t, ss, ciToken(), op, recStream))
	policy := withSigners(delivery.ClassPolicy{}, ss)
	policy.Signers[0].Subjects = ".*"
	if _, _, err := approvalAt(ocireferrer.Options{}, policy, nil)(context.Background(), ref); err == nil || !strings.Contains(err.Error(), "it is a proposal") {
		t.Fatalf("a pin citing a CI-signed approval: %v", err)
	}
}

// TestScanSignedCreatesNoViewForProposal: the recorder creates no view of a
// workload-signed approval record.
func TestScanSignedCreatesNoViewForProposal(t *testing.T) {
	re, ar, o, ss := keylessRecEnv(t)
	re.rec.Policy.Signers[0].Subjects = ".*"
	re.recordApprovals(ar)
	re.pushSigned(ss, workloadApprover, o)
	re.recordApprovals(ar)
	if len(re.views()) != 0 {
		t.Fatalf("a proposal was viewed: %+v", re.views())
	}
}

// TestWatchClassifiesSigners: every verdict's signer carries its class; the
// signed policy's signer rules classify before the default rule does.
func TestWatchClassifiesSigners(t *testing.T) {
	w := newPolicyWorld(t)
	w.signPolicy(w.key, delivery.PublisherPolicy{Publishers: []delivery.PublisherRule{contractorRule}, At: "2026-11-18T10:00:00Z",
		Signers: []delivery.SignerClassRule{{Name: "contractor bots", Issuer: ghIssuer, Subject: contractorRule.Subject, Class: delivery.ClassAgent}}})
	theirs := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	ours := w.push("1.1.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	st := w.reconcile()
	if v := watchVerdict(t, st, theirs); v.Identity == nil || v.Identity.Class != delivery.ClassAgent || v.Identity.ClassifiedBy != "policy:contractor bots" {
		t.Fatalf("the policy's class: %+v", v.Identity)
	}
	if v := watchVerdict(t, st, ours); v.Identity == nil || v.Identity.Class != delivery.ClassCI || !strings.HasPrefix(v.Identity.ClassifiedBy, "default:") {
		t.Fatalf("the default class: %+v", v.Identity)
	}
}
