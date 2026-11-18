/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
)

const (
	thirdRepo = "https://github.com/agency/payments"
	thirdSub  = thirdRepo + "/.github/workflows/ci.yml@refs/heads/main"
)

var otherID = delivery.PublisherIdentity{Issuer: ghIssuer, Subject: otherSub, Repository: otherRepo, Ref: "refs/heads/main", CI: true}

// trustOps are the trust operations in the cluster.
func (w *watchWorld) trustOps() []dv1.Operation {
	return slices.DeleteFunc(w.e.ops(), func(o dv1.Operation) bool { return o.Spec.Type != delivery.TypeTrust })
}

// pinned makes the world's pin reader return pins of payments.
func (w *watchWorld) pinned(pins ...deliveryrecord.Pin) {
	w.p.Pins = func(context.Context, string, delivery.ServiceRef) (deliveryrecord.PinListing, error) {
		return deliveryrecord.PinListing{Pins: pins}, nil
	}
}

func pinFor(id delivery.PublisherIdentity, manifest, at string) deliveryrecord.Pin {
	return deliveryrecord.Pin{Manifest: manifest, Pin: delivery.IdentityPin{Service: delivery.ServiceRef{Name: "payments"},
		Issuer: id.Issuer, Subject: id.Subject, Repository: id.Repository, Approver: "alice", At: at}}
}

// TestTrustOperationRaisedOnce: two digests of one new signer, two passes
// and a restart raise one trust operation, named by the identity.
func TestTrustOperationRaisedOnce(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	w.push("1.1.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	w.p = &Publishers{Reg: w.p.Reg, Rule: w.p.Rule, HasRule: true, Now: w.p.Now, Root: w.p.Root} // a restart
	w.e.st.Publishers = w.p
	st := w.reconcile()
	ops := w.trustOps()
	if len(ops) != 1 || ops[0].Name != delivery.TrustOperationName("payments-prod", otherID) || ops[0].Spec.Params.Publisher.Subject != otherSub ||
		ops[0].Spec.Actor.User != promoterSA || ops[0].Spec.Reason != "new signer "+otherSub || st.Frozen {
		t.Fatalf("ops %+v frozen %v", ops, st.Frozen)
	}
}

// TestTrustRaiseNeverFreezes: the operation raised for a first digest is
// found again under its name for a second one, so the Stage never freezes
// on a name taken by itself; one someone else took freezes it.
func TestTrustRaiseNeverFreezes(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	w.e.setStage(func(s *delivery.StageStatus) { s.Pointer = dA })
	w.push("2.0.0", otherRepo, w.ci(otherSub, otherRepo))
	if st := w.reconcile(); st.Frozen || len(w.trustOps()) != 1 {
		t.Fatalf("frozen %v: %s ops %d", st.Frozen, st.FrozenReason, len(w.trustOps()))
	}
	// Someone else's operation under the next identity's name freezes.
	w2 := newWatch(t, registryAt("-"))
	name := delivery.TrustOperationName("payments-prod", otherID)
	squat := opObj(name, delivery.TypeTrust, "", dA, "mallory")
	w2.e.create(squat)
	w2.push("1.0.0", otherRepo, w2.ci(otherSub, otherRepo))
	if st := w2.reconcile(); !st.Frozen || !strings.Contains(st.FrozenReason, name) {
		t.Fatalf("a squatted name: %+v", st)
	}
}

func TestParamsEqual(t *testing.T) {
	a, b, classed := otherID, otherID, delivery.Classified(otherID, nil)
	b.Subject = "x"
	for _, c := range []struct {
		x, y delivery.Params
		typ  string
		want bool
	}{
		{delivery.Params{To: dA}, delivery.Params{To: dA}, delivery.TypePromote, true},
		{delivery.Params{To: dA, Publisher: &a}, delivery.Params{To: dB, From: dA, Publisher: &otherID}, delivery.TypeTrust, true},
		{delivery.Params{To: dA, Publisher: &a}, delivery.Params{To: dA, Publisher: &b}, delivery.TypeTrust, false},
		{delivery.Params{To: dA, Publisher: &a}, delivery.Params{To: dB, Publisher: &otherID}, delivery.TypePromote, false},
		{delivery.Params{To: dA, Publisher: &a}, delivery.Params{To: dA, Publisher: &otherID}, delivery.TypePromote, true},
		{delivery.Params{To: dA, Publisher: &a}, delivery.Params{To: dA}, delivery.TypeTrust, false},
		{delivery.Params{To: dA, Publisher: &a}, delivery.Params{To: dA, Publisher: &classed}, delivery.TypeTrust, true}, // a class names the same one
	} {
		if got := paramsEqual(c.x, c.y, c.typ); got != c.want {
			t.Errorf("%+v %+v %s: %v", c.x, c.y, c.typ, got)
		}
	}
}

// TestTrustOperationOnePendingPerService: a second new identity is only a
// new-signer verdict while the first question is open, and is asked once it
// concludes.
func TestTrustOperationOnePendingPerService(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	d := w.push("1.1.0", thirdRepo, w.ci(thirdSub, thirdRepo))
	st := w.reconcile()
	if ops := w.trustOps(); len(ops) != 1 || watchVerdict(t, st, d).Verdict != delivery.VerdictNewSigner ||
		!strings.Contains(st.Publisher.Message, "is pending, so "+thirdSub+" is asked once it concludes") {
		t.Fatalf("ops %d message %q", len(ops), st.Publisher.Message)
	}
	first := w.trustOps()[0]
	first.Status.Phase, first.Status.Outcome = delivery.PhaseConcluded, delivery.Rejected
	must(t, w.e.c.Status().Update(t.Context(), &first))
	w.reconcile()
	if ops := w.trustOps(); len(ops) != 2 {
		t.Fatalf("the next question after the first concluded: %d", len(ops))
	}
}

// TestTrustOperationHeldForAPerson: the operation waits for a person with
// the question naming the signer, and an approval concludes it without the
// pointer lock and without verifying the digest that brought it.
func TestTrustOperationHeldForAPerson(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	name := w.trustOps()[0].Name
	o := w.e.drive(name)
	if o.Status.Phase != delivery.PhaseHeld || !strings.Contains(o.Status.Question, "payments: new signer "+otherSub) ||
		!strings.Contains(o.Status.Question, "Confirm it may publish payments?") {
		t.Fatalf("phase %s question %q reason %s %s", o.Status.Phase, o.Status.Question, o.Status.Reason, o.Status.Message)
	}
	if locks := w.e.getStage().Status.Locks; len(locks) != 0 {
		t.Fatalf("a held trust operation took a lock: %v", locks)
	}
	testTrustSucceeds(t, w, o, d)
}

func testTrustSucceeds(t *testing.T, w *watchWorld, o *dv1.Operation, d string) {
	t.Helper()
	before := w.e.getStage().Status.Verified
	w.e.approve(o, "a1", "alice", delivery.Approve)
	o = w.e.drive(o.Name)
	wantOutcome(t, o, delivery.Succeeded, "SignerConfirmed")
	st := w.e.getStage().Status
	if o.Status.Verification != delivery.Unverified || slices.Contains(st.Verified, d) || !slices.Equal(st.Verified, before) || len(st.Locks) != 0 {
		t.Fatalf("verification %s verified %v locks %v", o.Status.Verification, st.Verified, st.Locks)
	}
}

// TestTrustSucceededNeverVerifiesDigest: confirming a signer never adds
// the digest to status.verified, nor publishes it: the next pass needs a pin.
func TestTrustSucceededNeverVerifiesDigest(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	testTrustSucceeds(t, w, w.e.drive(w.trustOps()[0].Name), d)
	if st := w.reconcile(); slices.Contains(st.Published, d) {
		t.Fatalf("published without a pin: %v", st.Published)
	}
}

// TestRejectedSignerStaysOut: a person's reject concludes the operation; the
// identity is not asked again and stays out while it exists.
func TestRejectedSignerStaysOut(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	o := w.e.drive(w.trustOps()[0].Name)
	w.e.approve(o, "r1", "alice", delivery.Reject)
	wantOutcome(t, w.e.drive(o.Name), delivery.Rejected, "RejectedByHuman")
	st := w.reconcile()
	if len(w.trustOps()) != 1 || slices.Contains(st.Published, d) || st.Frozen {
		t.Fatalf("ops %d published %v frozen %v", len(w.trustOps()), st.Published, st.Frozen)
	}
}

// TestAgentCannotConfirmSigner: an agent's approval never counts.
func TestAgentCannotConfirmSigner(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	o := w.e.drive(w.trustOps()[0].Name)
	w.e.approveAs(o, "a1", bot, []string{"pacto:agents"}, delivery.Approve)
	if o = w.e.drive(o.Name); o.Status.Phase != delivery.PhaseHeld || !strings.Contains(o.Status.Message, "agents cannot approve") {
		t.Fatalf("phase %s message %q", o.Status.Phase, o.Status.Message)
	}
}

// TestTrustOperationNeedsRecorder and TestTrustOperationNeedsPinSigner: with
// no way to pin, no trust operation is raised and the status says why.
func TestTrustOperationNeedsRecorder(t *testing.T) {
	testCannotPin(t, "confirming a new signer needs the recorder (delivery.recorder.enabled)")
}

func TestTrustOperationNeedsPinSigner(t *testing.T) {
	testCannotPin(t, "confirming a new signer needs delivery.recorder.signing.publicKey or keyless recorder signing")
}

func testCannotPin(t *testing.T, why string) {
	t.Helper()
	w := newWatch(t, registryAt("-"))
	w.p.CannotPin = why
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	st := w.reconcile()
	if len(w.trustOps()) != 0 || watchVerdict(t, st, d).Verdict != delivery.VerdictNewSigner || !strings.Contains(st.Publisher.Message, why) {
		t.Fatalf("ops %d message %q", len(w.trustOps()), st.Publisher.Message)
	}
}

// TestPinAcceptsNextDigest: a pinned identity is accepted on the next pass,
// source pin, and the high-water mark moves to its pin.
func TestPinAcceptsNextDigest(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.pinned(pinFor(otherID, "sha256:p1", "2026-10-20T10:00:00Z"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	st := w.reconcile()
	v := watchVerdict(t, st, d)
	if v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourcePin || !slices.Contains(st.Published, d) ||
		st.Publisher.Pins != 1 || st.Publisher.PinDigest != "sha256:p1" || st.Publisher.PinAt != "2026-10-20T10:00:00Z" || len(w.trustOps()) != 0 {
		t.Fatalf("verdict %+v status %+v", v, st.Publisher)
	}
}

// TestPinSurvivesOperationTTL: the pin, not the operation, is the proof:
// with the trust operation gone the pinned identity is still accepted.
func TestPinSurvivesOperationTTL(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	op := w.trustOps()[0]
	must(t, w.e.c.Delete(t.Context(), &op))
	w.pinned(pinFor(otherID, "sha256:p1", "2026-10-20T10:00:00Z"))
	if st := w.reconcile(); !slices.Contains(st.Published, d) {
		t.Fatalf("published %v", st.Published)
	}
}

// TestTrustOperationAgainOnChangedSigner: a pinned service whose next
// signer differs asks again, naming both, and never accepts it.
func TestTrustOperationAgainOnChangedSigner(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.pinned(pinFor(otherID, "sha256:p1", "2026-10-20T10:00:00Z"))
	d := w.push("1.0.0", thirdRepo, w.ci(thirdSub, thirdRepo))
	st := w.reconcile()
	ops := w.trustOps()
	if len(ops) != 1 || ops[0].Spec.Reason != "signer changed from "+otherSub+" to "+thirdSub || slices.Contains(st.Published, d) {
		t.Fatalf("ops %+v published %v", ops, st.Published)
	}
}

// TestPinDeletionDoesNotRollBack: once the newest pin is gone, no older pin
// is used and the message says so; the mark stays.
func TestPinDeletionDoesNotRollBack(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	old := pinFor(delivery.PublisherIdentity{Issuer: ghIssuer, Subject: thirdSub, Repository: thirdRepo}, "sha256:p1", "2026-10-19T10:00:00Z")
	w.pinned(pinFor(otherID, "sha256:p2", "2026-10-20T10:00:00Z"), old)
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	if st := w.reconcile(); !slices.Contains(st.Published, d) || st.Publisher.PinDigest != "sha256:p2" {
		t.Fatalf("both pins: %+v", st.Publisher)
	}
	w.pinned(old)
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict == delivery.VerdictAccepted || slices.Contains(st.Published, d) ||
		st.Publisher.PinDigest != "sha256:p2" || !strings.Contains(st.Publisher.Message, "the newest pin sha256:p2 signed at 2026-10-20T10:00:00Z is gone") {
		t.Fatalf("verdict %+v status %+v", v, st.Publisher)
	}
	w.pinned()
	if st := w.reconcile(); st.Publisher.Pins != 0 || !strings.Contains(st.Publisher.Message, "is gone") {
		t.Fatalf("every pin gone: %+v", st.Publisher)
	}
}

// TestNewerPinReplacesOlder: confirming a changed signer stops trusting the
// one it replaced: only the newest pin is used.
func TestNewerPinReplacesOlder(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.pinned(pinFor(otherID, "sha256:p1", "2026-10-19T10:00:00Z"))
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	if st := w.reconcile(); !slices.Contains(st.Published, d) {
		t.Fatalf("first pin: %+v", st.Publisher)
	}
	third := delivery.PublisherIdentity{Issuer: ghIssuer, Subject: thirdSub, Repository: thirdRepo}
	w.pinned(pinFor(third, "sha256:p2", "2026-10-20T10:00:00Z"), pinFor(otherID, "sha256:p1", "2026-10-19T10:00:00Z"))
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict == delivery.VerdictAccepted || slices.Contains(st.Published, d) || st.Publisher.Pins != 1 {
		t.Fatalf("the replaced signer: %+v %+v", v, st.Publisher)
	}
}

// TestPinsPartialReadNeverDecides: a pin listing that could not be read, a
// pin whose approval record could not be read, or a cut listing missing the
// newest pin leave every signer the pins may cover unknown: published as it
// was, nobody asked and Ready False.
func TestPinsPartialReadNeverDecides(t *testing.T) {
	for name, l := range map[string]func() (deliveryrecord.PinListing, error){
		"unreadable": func() (deliveryrecord.PinListing, error) { return deliveryrecord.PinListing{}, errors.New("503") },
		"incomplete": func() (deliveryrecord.PinListing, error) {
			return deliveryrecord.PinListing{Pins: []deliveryrecord.Pin{}, Unreadable: []string{"sha256:x: approval 503"}, Incomplete: true}, nil
		},
		"incomplete beside the newest": func() (deliveryrecord.PinListing, error) {
			return deliveryrecord.PinListing{Pins: []deliveryrecord.Pin{pinFor(otherID, "sha256:p1", "2026-10-19T10:00:00Z")}, Unreadable: []string{"sha256:x: approval 503"}, Incomplete: true}, nil
		},
		"cut": func() (deliveryrecord.PinListing, error) {
			return deliveryrecord.PinListing{Pins: []deliveryrecord.Pin{pinFor(otherID, "sha256:p0", "2026-10-01T10:00:00Z")}, Cut: true}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWatch(t, registryAt("-"))
			w.pinned(pinFor(otherID, "sha256:p1", "2026-10-19T10:00:00Z"))
			d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
			if st := w.reconcile(); !slices.Contains(st.Published, d) {
				t.Fatalf("pinned: %+v", st.Publisher)
			}
			w.p.Pins = func(context.Context, string, delivery.ServiceRef) (deliveryrecord.PinListing, error) { return l() }
			w.push("1.1.0", thirdRepo, w.ci(thirdSub, thirdRepo))
			st := w.reconcile()
			if v := watchVerdict(t, st, d); v.Verdict != delivery.VerdictUnknown || !slices.Contains(st.Published, d) || len(w.trustOps()) != 0 {
				t.Fatalf("verdict %+v published %v ops %d", v, st.Published, len(w.trustOps()))
			}
			if r := readyReasonOf(t, st); r.Status != "False" || r.Reason != delivery.ReadyReasonRegistryUnreadable {
				t.Fatalf("ready %+v", r)
			}
		})
	}
}

func TestPinsUnreadable(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.p.Pins = func(context.Context, string, delivery.ServiceRef) (deliveryrecord.PinListing, error) {
		return deliveryrecord.PinListing{}, errors.New("registry down")
	}
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	st := w.reconcile()
	if !strings.Contains(st.Publisher.Message, "the pins of payments could not be read (registry down)") || slices.Contains(st.Published, d) {
		t.Fatalf("status %+v", st.Publisher)
	}
	w.p.Pins = func(context.Context, string, delivery.ServiceRef) (deliveryrecord.PinListing, error) {
		return deliveryrecord.PinListing{Pins: []deliveryrecord.Pin{}, Unreadable: []string{"sha256:x: bad"}}, nil
	}
	if st := w.reconcile(); !strings.Contains(st.Publisher.Message, "1 pins of payments could not be used: sha256:x: bad") {
		t.Fatalf("status %+v", st.Publisher)
	}
}

// TestTrustAskListFails: operations that cannot be listed raise nothing and
// fail the pass, so it is tried again.
func TestTrustAskListFails(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.e.setFail("list/Operation", true)
	w.p.listings = nil
	if _, err := w.e.st.registryReady(t.Context(), w.e.getStage()); err == nil {
		t.Fatal("an unlistable cluster raised a trust operation")
	}
}

// TestExpiredSignerAskedAgain: a trust operation nobody answered before its
// deadline is asked again; a person's rejection is not.
func TestExpiredSignerAskedAgain(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	o := w.e.drive(w.trustOps()[0].Name)
	w.e.now = w.e.now.Add(25 * time.Hour)
	wantOutcome(t, w.e.drive(o.Name), delivery.Expired, "")
	w.e.fails["delete/Operation"] = true
	w.p.listings = nil
	if err := w.e.reconcileStage("payments-prod"); err == nil {
		t.Fatal("a failed delete of the expired operation was not an error")
	}
	w.e.fails = fails{}
	w.reconcile()
	// The recorder records the expired one and lets it go.
	gone := w.e.getOp(o.Name)
	if gone.DeletionTimestamp == nil {
		t.Fatal("the expired operation was not deleted")
	}
	gone.Finalizers = nil
	must(t, w.e.c.Update(t.Context(), gone))
	w.reconcile()
	ops := w.trustOps()
	if len(ops) != 1 || ops[0].DeletionTimestamp != nil || ops[0].Status.Phase == delivery.PhaseConcluded {
		t.Fatalf("not asked again: %+v", ops)
	}
}
