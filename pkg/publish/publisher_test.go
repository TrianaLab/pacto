package publish

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	ghIssuer    = "https://token.actions.githubusercontent.com"
	acmeRepo    = "https://github.com/acme/payments"
	acmeSubject = acmeRepo + "/.github/workflows/release.yml@refs/heads/main"
)

var paymentsRef = delivery.ServiceRef{Name: "payments"}

// testRegistry is an in-process registry, with the Referrers API unless
// referrers is false (as GHCR), and a client over it.
func testRegistry(t *testing.T, referrers bool) (string, *Client) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(referrers), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return host, NewClient(anonymous, host)
}

// push pushes a random image to repo and returns its digest.
func push(t *testing.T, reg Registry, repo string) string {
	t.Helper()
	img, _ := random.Image(32, 1)
	d, err := reg.Write(t.Context(), repo, img)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// signerFor is a keyless signer of ss for token.
func signerFor(t *testing.T, ss *testutil.Sigstore, token string) signing.Signer {
	t.Helper()
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return token, nil }, ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ciSigner(t *testing.T, ss *testutil.Sigstore, subject, repo string) signing.Signer {
	t.Helper()
	return signerFor(t, ss, testutil.CIToken(subject, ghIssuer, repo, "refs/heads/main"))
}

func acmeRules(t *testing.T) []delivery.PublisherRule {
	t.Helper()
	r, why := delivery.DeriveRule("ghcr.io/acme", "main")
	if why != "" {
		t.Fatal(why)
	}
	return []delivery.PublisherRule{r}
}

// signed pushes an image to host/payments, signs it with each signer in turn
// and returns the repository and digest.
func signed(t *testing.T, host string, reg Registry, signers ...signing.Signer) (string, string) {
	t.Helper()
	repo := host + "/payments"
	d := push(t, reg, repo)
	for _, s := range signers {
		if err := SignDigest(t.Context(), reg, s, repo, d); err != nil {
			t.Fatal(err)
		}
	}
	return repo, d
}

func TestCheckPublisherAcceptedByRule(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo))
	v, _, _ := CheckPublisher(t.Context(), reg, ss.Root, false, repo, d, paymentsRef, acmeRepo, acmeRules(t), delivery.PublisherSourceRule, nil, nil)
	if v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourceRule || v.Digest != d || v.At == "" || v.Identity.Repository != acmeRepo {
		t.Fatalf("verdict %+v", v)
	}
}

func TestCheckPublisherAcceptedByPin(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	const sub = "https://github.com/contractor/payments/.github/workflows/ci.yml@refs/heads/main"
	repo, d := signed(t, host, reg, ciSigner(t, ss, sub, "https://github.com/contractor/payments"))
	pin := delivery.IdentityPin{Service: paymentsRef, Issuer: ghIssuer, Subject: sub, Repository: "https://github.com/contractor/payments", Approver: "ana"}
	v, _, _ := CheckPublisher(t.Context(), reg, ss.Root, false, repo, d, paymentsRef, "", acmeRules(t), delivery.PublisherSourceRule, []delivery.IdentityPin{pin}, nil)
	if v.Verdict != delivery.VerdictAccepted || v.Source != delivery.PublisherSourcePin {
		t.Fatalf("verdict %+v", v)
	}
}

func TestCheckPublisherNewSigner(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	const sub = "https://github.com/contractor/payments/.github/workflows/ci.yml@refs/heads/main"
	repo, d := signed(t, host, reg, ciSigner(t, ss, sub, "https://github.com/contractor/payments"))
	v, _, _ := CheckPublisher(t.Context(), reg, ss.Root, false, repo, d, paymentsRef, "", acmeRules(t), delivery.PublisherSourceRule, nil, nil)
	if v.Verdict != delivery.VerdictNewSigner || v.Identity == nil || v.Identity.Subject != sub {
		t.Fatalf("verdict %+v", v)
	}
}

// TestCheckPublisherKeepsLookingPastOutsideSigner: a person signed first and
// the CI the rule covers second, and the digest is accepted.
func TestCheckPublisherKeepsLookingPastOutsideSigner(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, signerFor(t, ss, testutil.Token("ana@acme.example", "https://accounts.example")), ciSigner(t, ss, acmeSubject, acmeRepo))
	v, _, _ := CheckPublisher(t.Context(), reg, ss.Root, false, repo, d, paymentsRef, "", acmeRules(t), delivery.PublisherSourceRule, nil, nil)
	if v.Verdict != delivery.VerdictAccepted || v.Identity.Subject != acmeSubject {
		t.Fatalf("verdict %+v", v)
	}
}

func TestSignersOfReadsEverySignature(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo), signerFor(t, ss, testutil.Token("ana@acme.example", "https://accounts.example")))
	signers, err := SignersOf(t.Context(), reg, ss.Root, false, repo, d)
	if err != nil || len(signers) != 2 || signers[0].Class == "" || signers[0].Class == signers[1].Class {
		t.Fatalf("signers %+v %v", signers, err)
	}
}

func TestCheckPublisherUnsigned(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg)
	v, _, _ := CheckPublisher(t.Context(), reg, ss.Root, false, repo, d, paymentsRef, "", acmeRules(t), delivery.PublisherSourceRule, nil, nil)
	if v.Verdict != delivery.VerdictUnsigned || v.Source != delivery.PublisherSourceNone || !strings.Contains(v.Reason, d) {
		t.Fatalf("verdict %+v", v)
	}
}

func TestCheckPublisherUnreachable(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo))
	v, _, _ := CheckPublisher(t.Context(), failingReferrers{reg}, ss.Root, false, repo, d, paymentsRef, "", acmeRules(t), delivery.PublisherSourceRule, nil, nil)
	if v.Verdict != delivery.VerdictUnknown || !strings.Contains(v.Reason, "registry down") || len([]rune(v.Reason)) > maxVerdictReason+1 {
		t.Fatalf("verdict %+v", v)
	}
}

// TestCheckPublisherWrongRoot: a signature from another certificate
// authority is never accepted, and neither is one with no root at all.
func TestCheckPublisherWrongRoot(t *testing.T) {
	ss, other := testutil.New(t), testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, ciSigner(t, other, acmeSubject, acmeRepo))
	for _, root := range []*testutil.Sigstore{ss, nil} {
		tm := signing.Policy{}.Root
		if root != nil {
			tm = root.Root
		}
		v, _, _ := CheckPublisher(t.Context(), reg, tm, false, repo, d, paymentsRef, "", acmeRules(t), delivery.PublisherSourceRule, nil, nil)
		if v.Verdict != delivery.VerdictUnknown {
			t.Fatalf("verdict %+v", v)
		}
	}
}

// TestCheckPublisherReadsSigTags: on a registry without the Referrers API the
// signature is read from its pacto-sig tag.
func TestCheckPublisherReadsSigTags(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, false)
	repo, d := signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo))
	v, _, _ := CheckPublisher(t.Context(), reg, ss.Root, false, repo, d, paymentsRef, "", acmeRules(t), delivery.PublisherSourceRule, nil, nil)
	if v.Verdict != delivery.VerdictAccepted {
		t.Fatalf("verdict %+v", v)
	}
}

// TestSignersOfSkipsAttestation: a verified statement that is not a
// signature, under an annotation that claims one, names no signer.
func TestSignersOfSkipsAttestation(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg)
	h, _ := v1.NewHash(d)
	stmt := fmt.Appendf(nil, `{"_type":%q,"subject":[{"digest":{"sha256":%q}}],"predicateType":"https://slsa.dev/provenance/v1","predicate":{}}`, delivery.StatementType, h.Hex)
	b, err := ciSigner(t, ss, acmeSubject, acmeRepo).Sign(t.Context(), delivery.PayloadType, stmt)
	if err != nil {
		t.Fatal(err)
	}
	signers, err := SignersOf(t.Context(), oneReferrer{reg, b}, ss.Root, false, repo, d)
	if len(signers) != 0 || !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("signers %+v %v", signers, err)
	}
}

// failingReferrers is a registry whose referrers cannot be listed.
type failingReferrers struct{ *Client }

func (failingReferrers) Referrers(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("registry down")
}

// oneReferrer lists one referrer whose bundle is b, annotated as a signature.
type oneReferrer struct {
	*Client
	b []byte
}

func (oneReferrer) Referrers(context.Context, string, string, string) ([]string, error) {
	return []string{"sha256:" + strings.Repeat("0", 64)}, nil
}

func (o oneReferrer) Layer(context.Context, string, string) ([]byte, map[string]string, error) {
	return o.b, map[string]string{deliveryrecord.AnnPredicateType: SignPredicate}, nil
}

func TestPolicyVerifierPredicateType(t *testing.T) {
	host, reg := testRegistry(t, true)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	repo := host + "/acme/x"
	b := bundleOf(host, "1.0.0", workloadService)
	d, err := oci.BundleDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := oci.BundleImage(b)
	h, _ := v1.NewHash(d)
	if _, err := reg.Write(t.Context(), repo, streamed{img, h}); err != nil {
		t.Fatal(err)
	}
	if err := SignDigest(t.Context(), reg, signing.Keyed(kp, ""), repo, d); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigest(t.Context(), reg, PolicyVerifier(signing.Policy{Key: pub}), repo, d); err != nil {
		t.Errorf("own key: %v", err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyDigest(t.Context(), reg, PolicyVerifier(signing.Policy{Key: other}), repo, d); err == nil {
		t.Error("another key verified")
	}
}

// seenTransport records every request a client sends.
type seenTransport struct {
	mu   sync.Mutex
	reqs []string
}

func (s *seenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

func TestNewScopedClientTransport(t *testing.T) {
	host, _ := testRegistry(t, true)
	rt := &seenTransport{}
	reg := NewScopedClient(anonymous, oci.NewPushScope("", host+"/acme"), rt)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	repo := host + "/acme/x"
	b := bundleOf(host, "1.0.0", workloadService)
	d, _ := oci.BundleDigest(b)
	img, _ := oci.BundleImage(b)
	h, _ := v1.NewHash(d)
	if _, err := reg.Write(t.Context(), repo, streamed{img, h}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Tag(t.Context(), repo, d, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := SignDigest(t.Context(), reg, signing.Keyed(kp, ""), repo, d); err != nil {
		t.Fatal(err)
	}
	var write, tag, referrer bool
	for _, r := range rt.reqs {
		write = write || r == "PUT /v2/acme/x/manifests/"+d
		tag = tag || r == "PUT /v2/acme/x/manifests/1.0.0"
		referrer = referrer || (strings.HasPrefix(r, "PUT /v2/acme/x/manifests/sha256:") && !strings.HasSuffix(r, d))
	}
	if !write || !tag || !referrer {
		t.Errorf("the transport saw write %v, tag %v, referrer %v: %v", write, tag, referrer, rt.reqs)
	}
}

// slsaAttested pushes an image signed by nobody and attaches a keyless SLSA
// provenance attestation by the CI identity of acmeRepo, annotated as GitHub
// annotates one.
func slsaAttested(t *testing.T, ss *testutil.Sigstore) (*Client, string, string) {
	t.Helper()
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg)
	h, _ := v1.NewHash(d)
	stmt := fmt.Appendf(nil, `{"_type":%q,"subject":[{"digest":{"sha256":%q}}],"predicateType":"https://slsa.dev/provenance/v1","predicate":{}}`, delivery.StatementType, h.Hex)
	b, err := ciSigner(t, ss, acmeSubject, acmeRepo).Sign(t.Context(), delivery.PayloadType, stmt)
	if err != nil {
		t.Fatal(err)
	}
	writeBundle(t, reg, repo, d, "https://slsa.dev/provenance/v1", b)
	return reg, repo, d
}

func TestProvenanceSignersAcceptsSLSA(t *testing.T) {
	ss := testutil.New(t)
	reg, repo, d := slsaAttested(t, ss)
	signers, err := ProvenanceSigners(t.Context(), reg, ss.Root, false, repo, d)
	if err != nil || len(signers) != 1 || signers[0].Repository != acmeRepo || !signers[0].CI {
		t.Fatalf("signers %+v %v", signers, err)
	}
}

func TestProvenanceSignersAcceptsSignature(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo))
	signers, err := ProvenanceSigners(t.Context(), reg, ss.Root, false, repo, d)
	if err != nil || len(signers) != 1 || signers[0].Repository != acmeRepo {
		t.Fatalf("signers %+v %v", signers, err)
	}
}

func TestProvenanceSignersUnsigned(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg)
	if signers, err := ProvenanceSigners(t.Context(), reg, ss.Root, false, repo, d); signers != nil || !errors.Is(err, ErrNoSignature) {
		t.Fatalf("an unsigned image: signers %+v %v", signers, err)
	}
}

func TestVerifyDigestStillSkipsSLSA(t *testing.T) {
	ss := testutil.New(t)
	reg, repo, d := slsaAttested(t, ss)
	if err := VerifyDigest(t.Context(), reg, PolicyVerifier(signing.Policy{AnyIdentity: true, Root: ss.Root}), repo, d); !errors.Is(err, ErrNoSignature) {
		t.Fatalf("an SLSA attestation counted as a signature: %v", err)
	}
}

func TestSignersOfStillSkipsSLSA(t *testing.T) {
	ss := testutil.New(t)
	reg, repo, d := slsaAttested(t, ss)
	if signers, err := SignersOf(t.Context(), reg, ss.Root, false, repo, d); len(signers) != 0 || !errors.Is(err, ErrNoSignature) {
		t.Fatalf("signers %+v %v", signers, err)
	}
}

// layerFailsAfter fails every Layer read after the first n.
type layerFailsAfter struct {
	Registry
	n *int
}

func (r layerFailsAfter) Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error) {
	if *r.n == 0 {
		return nil, nil, errors.New("blob unknown")
	}
	*r.n--
	return r.Registry.Layer(ctx, repo, digest)
}

// A signer read that skipped an unreadable signature says so: the unread
// one may name another repository.
func TestProvenanceSignersSaysPartial(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo), ciSigner(t, ss, "https://github.com/evil/x/.github/workflows/ci.yml@refs/heads/main", "https://github.com/evil/x"))
	n := 1
	signers, err := ProvenanceSigners(t.Context(), layerFailsAfter{reg, &n}, ss.Root, false, repo, d)
	if len(signers) != 1 || !errors.Is(err, ErrSignersPartial) {
		t.Fatalf("signers %+v %v", signers, err)
	}
	if signers, err := ProvenanceSigners(t.Context(), reg, ss.Root, false, repo, d); len(signers) != 2 || err != nil {
		t.Fatalf("a complete read: %+v %v", signers, err)
	}
}
