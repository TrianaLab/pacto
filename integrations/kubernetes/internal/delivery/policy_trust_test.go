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
			theirs := w.push("0.9.0", otherRepo, w.ci(otherSub, otherRepo)) // a new signer stays one
			if st := w.reconcile(); !slices.Contains(st.Published, ours) {
				t.Fatalf("the derived rule: %v", st.Published)
			}
			w.p.Policy = policy
			w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
			fail(w)
			st := w.reconcile()
			if v := watchVerdict(t, st, theirs); v.Verdict != delivery.VerdictNewSigner {
				t.Fatalf("the new signer: %+v", v)
			}
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

// TestVerifiedPolicyKeepsNoMark: the owner check's read returns the policy
// its tag names once it verifies, and nil with why when it does not.
func TestVerifiedPolicyKeepsNoMark(t *testing.T) {
	w := newPolicyWorld(t)
	w.sign(w.key, "2026-11-18T10:00:00Z", contractorRule)
	if pol, why := w.p.VerifiedPolicy(t.Context()); pol == nil || why != "" || len(pol.Publishers) != 1 || pol.Publishers[0].Name != "contractor" {
		t.Fatalf("policy %+v why %q", pol, why)
	}
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	w.sign(stranger, "2026-11-19T10:00:00Z", contractorRule)
	if pol, why := w.p.VerifiedPolicy(t.Context()); pol != nil || why == "" {
		t.Fatalf("an unverified policy was returned: %+v %q", pol, why)
	}
}

// signPlacement pushes a placement policy signed by key to host/policy and
// tags it placement.
func (w *policyWorld) signPlacement(key ed25519.PrivateKey, pol delivery.PlacementPolicy) {
	w.t.Helper()
	payload, _ := json.Marshal(delivery.PlacementStatement{Type: delivery.StatementType, Subject: []delivery.Subject{},
		PredicateType: delivery.PredicatePlacement, Predicate: pol})
	bundle, err := edSigner(key).Sign(w.t.Context(), delivery.PayloadType, payload)
	must(w.t, err)
	img, err := mutate.AppendLayers(ggcrempty.Image, static.NewLayer(bundle, ggcrtypes.MediaType(delivery.BundleMediaType)))
	must(w.t, err)
	img = mutate.MediaType(img, ggcrtypes.OCIManifestSchema1)
	d, err := w.reg.Write(w.t.Context(), w.host+"/policy", img)
	must(w.t, err)
	must(w.t, w.reg.Tag(w.t.Context(), w.host+"/policy", d, delivery.PlacementPolicyTag))
}

func TestVerifiedPlacement(t *testing.T) {
	w := newPolicyWorld(t)
	// None at the tag: no rule, nothing unread.
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol != nil || why != "" {
		t.Fatalf("absent: %+v %q", pol, why)
	}
	rule := delivery.PlacementRule{Name: "prod", Environments: []string{"production"}, Namespace: "prod-{service}"}
	w.signPlacement(w.key, delivery.PlacementPolicy{Rules: []delivery.PlacementRule{rule}, At: "2026-11-23T10:00:00Z"})
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol == nil || why != "" || pol.Rules[0].Name != "prod" {
		t.Fatalf("verified: %+v %q", pol, why)
	}
	// A stranger's policy is unverified.
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	w.signPlacement(stranger, delivery.PlacementPolicy{At: "2026-11-24T10:00:00Z"})
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol != nil || !strings.Contains(why, "could not be verified") {
		t.Fatalf("stranger: %+v %q", pol, why)
	}
}

// An older signed placement policy pushed over a newer one is ignored: the
// newest stays and the reason names the rollback.
func TestPlacementPolicyOlderAtIgnored(t *testing.T) {
	w := newPolicyWorld(t)
	rule := delivery.PlacementRule{Name: "prod", Environments: []string{"production"}, Namespace: "prod-{service}"}
	w.signPlacement(w.key, delivery.PlacementPolicy{Rules: []delivery.PlacementRule{rule}, At: "2026-11-23T10:00:00Z"})
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol == nil || why != "" {
		t.Fatalf("verified: %+v %q", pol, why)
	}
	w.signPlacement(w.key, delivery.PlacementPolicy{At: "2026-11-22T10:00:00Z"})
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol == nil || len(pol.Rules) != 1 || pol.Rules[0].Name != "prod" || !strings.Contains(why, "older than") {
		t.Fatalf("rollback: %+v %q", pol, why)
	}
}

func TestVerifiedPlacementUnreadable(t *testing.T) {
	w := newPolicyWorld(t)
	w.signPlacement(w.key, delivery.PlacementPolicy{At: "2026-11-23T10:00:00Z"})
	w.reg.layerErr = errors.New("503")
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol != nil || !strings.Contains(why, "could not be read (503)") {
		t.Fatalf("layer: %+v %q", pol, why)
	}
	w.reg.headErr = errors.New("503")
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol != nil || !strings.Contains(why, "could not be read (503)") {
		t.Fatalf("head: %+v %q", pol, why)
	}
	// Once one verified, a read that fails keeps it, unread.
	w.reg.layerErr, w.reg.headErr = nil, nil
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol == nil || why != "" {
		t.Fatalf("verified: %+v %q", pol, why)
	}
	w.reg.headErr = errors.New("503")
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol == nil || !strings.Contains(why, "could not be read (503)") {
		t.Fatalf("kept: %+v %q", pol, why)
	}
	// No delivery policy, no placement policy.
	w.p.Policy = ""
	if pol, why := w.p.VerifiedPlacement(t.Context()); pol != nil || why != "" {
		t.Fatalf("no policy: %+v %q", pol, why)
	}
}

func TestNewestAccepted(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	svc := delivery.ServiceRef{Name: "payments"}
	repo := w.host + "/payments"
	ours := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	w.push("1.1.0", otherRepo, w.ci(otherSub, otherRepo))
	if v, why, err := w.p.NewestAccepted(t.Context(), svc, repo); v == nil || why != "" || err != nil || v.Digest != ours || v.Identity.Repository != acmeRepo {
		t.Fatalf("accepted %+v %q", v, why)
	}
	if v, why, err := w.p.NewestAccepted(t.Context(), delivery.ServiceRef{Name: "orders"}, w.host+"/orders"); v != nil || why == "" || err != nil {
		t.Fatalf("no version %+v %q", v, why)
	}
	w.p.listings = nil
	w.reg.tagsErr = errors.New("503")
	// A read that fails is an error, never an untrusted publisher.
	if v, why, err := w.p.NewestAccepted(t.Context(), svc, repo); v != nil || why != "" || err == nil || !strings.Contains(err.Error(), "cannot be listed") {
		t.Fatalf("listing %+v %q %v", v, why, err)
	}
	w.p.Root = func() (root.TrustedMaterial, error) { return nil, errors.New("tuf") }
	w.p.tm, w.p.rootNext = nil, time.Time{}
	if v, _, err := w.p.NewestAccepted(t.Context(), svc, repo); v != nil || err == nil || !strings.Contains(err.Error(), "trusted root") {
		t.Fatalf("root %+v %v", v, err)
	}
}
