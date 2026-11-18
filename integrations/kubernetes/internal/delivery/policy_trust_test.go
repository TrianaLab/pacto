/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	ggcrempty "github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	ggcrtypes "github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// contractorRule covers the contractor's CI, which the derived rule does not.
var contractorRule = delivery.PublisherRule{Name: "contractor", Issuer: ghIssuer,
	Subject: `^https://github\.com/contractor/payments/\.github/workflows/ci\.yml@refs/heads/main$`, Repository: `^https://github\.com/contractor/payments$`}

// policyWorld is a watch world whose installation names a signed policy at
// host/policy:v1, verified with key's public half.
type policyWorld struct {
	*watchWorld
	key ed25519.PrivateKey
}

func newPolicyWorld(t *testing.T) *policyWorld {
	w := newWatch(t, registryAt("-"))
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	w.p.Policy = w.host + "/policy:v1"
	w.p.PolicyVerify = func(b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
	return &policyWorld{watchWorld: w, key: key}
}

// sign pushes a policy signed by key to host/policy, tags it v1 and returns
// its digest.
func (w *policyWorld) sign(key ed25519.PrivateKey, at string, rules ...delivery.PublisherRule) string {
	w.t.Helper()
	return w.signPolicy(key, delivery.PublisherPolicy{Publishers: rules, At: at})
}

// signPolicy is sign for a whole policy.
func (w *policyWorld) signPolicy(key ed25519.PrivateKey, pol delivery.PublisherPolicy) string {
	w.t.Helper()
	payload, _ := json.Marshal(delivery.PolicyStatement{Type: delivery.StatementType, Subject: []delivery.Subject{},
		PredicateType: delivery.PredicatePublisherPolicy, Predicate: pol})
	bundle, err := edSigner(key).Sign(w.t.Context(), delivery.PayloadType, payload)
	must(w.t, err)
	img, err := mutate.AppendLayers(ggcrempty.Image, static.NewLayer(bundle, ggcrtypes.MediaType(delivery.BundleMediaType)))
	must(w.t, err)
	img = mutate.MediaType(img, ggcrtypes.OCIManifestSchema1)
	d, err := w.reg.Write(w.t.Context(), w.host+"/policy", img)
	must(w.t, err)
	must(w.t, w.reg.Tag(w.t.Context(), w.host+"/policy", d, "v1"))
	return d
}

func TestPolicyReplacesDerivedRule(t *testing.T) {
	w := newPolicyWorld(t)
	w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	theirs := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	ours := w.push("1.1.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	st := w.reconcile()
	if v := watchVerdict(t, st, theirs); v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourcePolicy {
		t.Fatalf("the policy's rule: %+v", v)
	}
	if v := watchVerdict(t, st, ours); v.Verdict == delivery.VerdictAccepted {
		t.Fatalf("the derived rule no longer decides: %+v", v)
	}
	if st.Publisher.Source != delivery.PublisherSourcePolicy || st.Publisher.Rule != nil {
		t.Fatalf("status %+v", st.Publisher)
	}
}

func TestPolicyDigestInStatus(t *testing.T) {
	w := newPolicyWorld(t)
	d := w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	st := w.reconcile()
	if p := st.Publisher; p.PolicyRef != w.host+"/policy:v1" || p.PolicyDigest != d || p.PolicyAt != "2026-11-18T10:00:00Z" || p.Message != "" {
		t.Fatalf("status %+v", p)
	}
}

// TestPolicyUnverifiedOnlyPinsAccept: a configured policy that never
// verified gives no rule: only a pin accepts.
func TestPolicyUnverifiedOnlyPinsAccept(t *testing.T) {
	w := newPolicyWorld(t)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	w.sign(stranger, "2026-11-18T10:00:00Z", contractorRule)
	ours := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	st := w.reconcile()
	if v := watchVerdict(t, st, ours); v.Verdict == delivery.VerdictAccepted || st.Publisher.Source != delivery.PublisherSourceNone ||
		!strings.Contains(st.Publisher.Message, "could not be verified") || !strings.Contains(st.Publisher.Message, "only pinned signers are accepted") {
		t.Fatalf("verdict %+v status %+v", v, st.Publisher)
	}
	w.pinned(pinFor(delivery.PublisherIdentity{Issuer: ghIssuer, Subject: acmeSubject, Repository: acmeRepo}, "sha256:p", "2026-10-20T10:00:00Z"))
	if st := w.reconcile(); !slices.Contains(st.Published, ours) {
		t.Fatalf("a pin accepts: %+v", st.Publisher)
	}
}

// TestPolicyMovedTagUnverifiedKeepsDigest: a moved tag that fails to read
// or verify keeps the last verified policy.
func TestPolicyMovedTagUnverifiedKeepsDigest(t *testing.T) {
	w := newPolicyWorld(t)
	good := w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	theirs := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	w.sign(stranger, "2026-11-19T10:00:00Z")
	st := w.reconcile()
	if st.Publisher.PolicyDigest != good || !slices.Contains(st.Published, theirs) ||
		!strings.Contains(st.Publisher.Message, "could not be verified") || !strings.Contains(st.Publisher.Message, "the policy "+good+" stays") {
		t.Fatalf("status %+v", st.Publisher)
	}
	w.reg.tagsErr = nil
	w.p.Policy = w.host + "/policy:gone"
	if st := w.reconcile(); st.Publisher.PolicyDigest != good || !strings.Contains(st.Publisher.Message, "does not exist") {
		t.Fatalf("a vanished tag: %+v", st.Publisher)
	}
}

// TestPolicyRollbackRefused: a tag moved to an older, validly signed policy
// is refused; the newer stays.
func TestPolicyRollbackRefused(t *testing.T) {
	w := newPolicyWorld(t)
	old := w.sign(w.key, "2026-11-17T10:00:00Z")
	newer := w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	theirs := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	must(t, w.reg.Tag(t.Context(), w.host+"/policy", old, "v1"))
	st := w.reconcile()
	if st.Publisher.PolicyDigest != newer || !slices.Contains(st.Published, theirs) ||
		!strings.Contains(st.Publisher.Message, "now names "+old+" signed at 2026-11-17T10:00:00Z, older than 2026-11-18T10:00:00Z") {
		t.Fatalf("status %+v", st.Publisher)
	}
}

// TestPolicyReverifiedAfterRestart: a fresh promoter verifies the digest a
// status names before it uses its rules.
func TestPolicyReverifiedAfterRestart(t *testing.T) {
	w := newPolicyWorld(t)
	good := w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	// The tag becomes unreadable and the process restarts with another key.
	w.p.policies = nil
	w.p.PolicyVerify = func([]byte) error { return errors.New("not ours") }
	w.p.Policy = w.host + "/policy:gone"
	st := w.reconcile()
	if st.Publisher.Source != delivery.PublisherSourceNone || len(st.Published) != 0 || !strings.Contains(st.Publisher.Message, "only pinned signers") ||
		st.Publisher.PolicyDigest != good {
		t.Fatalf("status %+v", st.Publisher)
	}
}

// TestWatchPolicyNarrowingRejudges: a digest the derived rule accepted
// leaves status.published once a narrower policy verifies.
func TestWatchPolicyNarrowingRejudges(t *testing.T) {
	w := newPolicyWorld(t)
	policy := w.p.Policy
	w.p.Policy = ""
	ours := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	if st := w.reconcile(); !slices.Contains(st.Published, ours) {
		t.Fatalf("the derived rule: %v", st.Published)
	}
	w.p.Policy = policy
	w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	if st := w.reconcile(); slices.Contains(st.Published, ours) {
		t.Fatalf("the narrower policy: %v", st.Published)
	}
}

func TestPolicyRepo(t *testing.T) {
	for ref, want := range map[string]string{"r.io:5000/p:v1": "r.io:5000/p", "r.io/p@sha256:1": "r.io/p", "r.io:5000/p": "r.io:5000/p"} {
		if got := policyRepo(ref); got != want {
			t.Errorf("%s: %s", ref, got)
		}
	}
}

// TestRejudgedWhenRegistryUnreadable: while the registry or the trusted root
// cannot be read, the verdicts judged before are judged again under the
// policy now in force, so a narrower policy stops accepting a signer at
// once.
func TestRejudgedWhenRegistryUnreadable(t *testing.T) {
	for name, fail := range map[string]func(w *policyWorld){
		"registry": func(w *policyWorld) { w.reg.tagsErr = errors.New("connection refused") },
		"root": func(w *policyWorld) {
			w.p.tm, w.p.rootNext = nil, time.Time{}
			w.p.Root = func() (root.TrustedMaterial, error) { return nil, errors.New("tuf unreachable") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newPolicyWorld(t)
			policy := w.p.Policy
			w.p.Policy = ""
			ours := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
			if st := w.reconcile(); !slices.Contains(st.Published, ours) {
				t.Fatalf("the derived rule: %v", st.Published)
			}
			w.p.Policy = policy
			w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
			fail(w)
			st := w.reconcile()
			if v := watchVerdict(t, st, ours); v.Verdict == delivery.VerdictAccepted || v.At == "" || slices.Contains(st.Published, ours) || readyReasonOf(t, st).Status != "False" {
				t.Fatalf("verdict %+v published %v", v, st.Published)
			}
		})
	}
}

// TestPolicyUnreadableNeverDecides: a configured policy that could not be
// read, with no last one verified, leaves a signer it may cover unknown:
// nobody is asked and Ready is False.
func TestPolicyUnreadableNeverDecides(t *testing.T) {
	w := newPolicyWorld(t)
	w.reg.headErr = errors.New("503")
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict != delivery.VerdictUnknown || len(w.trustOps()) != 0 {
		t.Fatalf("verdict %+v ops %d", v, len(w.trustOps()))
	}
	if c := readyReasonOf(t, st); c.Status != "False" || c.Reason != delivery.ReadyReasonPublisherUnverifiable {
		t.Fatalf("ready %+v", c)
	}
}

// TestPolicyLastGoodUnreadableNeverDecides: after a restart, a policy tag
// and the last verified digest that both could not be read leave the signers
// unknown; one that was read and does not verify is decided.
func TestPolicyLastGoodUnreadableNeverDecides(t *testing.T) {
	w := newPolicyWorld(t)
	w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	d := w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	w.p.policies = nil // a restart
	w.reg.headErr, w.reg.layerErr = errors.New("503"), errors.New("503")
	st := w.reconcile()
	if v := watchVerdict(t, st, d); v.Verdict != delivery.VerdictUnknown || !slices.Contains(st.Published, d) {
		t.Fatalf("verdict %+v published %v", v, st.Published)
	}
}
