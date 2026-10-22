package publish

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"errors"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// echoSigner returns the payload as the "bundle", so the verifier can read
// the statement SignDigest wrote; pkg/signing tests the crypto.
type echoSigner struct{ err error }

func (e echoSigner) Sign(_ context.Context, payloadType string, payload []byte) ([]byte, error) {
	if payloadType != inTotoPayload {
		return nil, errors.New("payload type " + payloadType)
	}
	return payload, e.err
}

// TestSignAndVerifyDigest: the signature is a bundle referrer naming the
// digest, as cosign writes it, and only a bundle the verifier accepts counts.
func TestSignAndVerifyDigest(t *testing.T) {
	ctx := t.Context()
	host, reg := newRegistry(t)
	repo, digest := signedImage(t, reg, host)
	if err := VerifyDigest(ctx, reg, names, repo, digest); err == nil || err.Error() != "no signature" {
		t.Fatalf("unsigned: %v", err)
	}
	if err := SignDigest(ctx, reg, echoSigner{}, repo, digest); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigest(ctx, reg, names, repo, digest); err != nil {
		t.Fatal(err)
	}
	refs, _ := reg.Referrers(ctx, repo, digest, BundleType)
	_, ann, err := reg.Layer(ctx, repo, refs[0])
	if err != nil || ann[AnnBundleContent] != "dsse-envelope" || ann[AnnBundlePredicate] != SignPredicate {
		t.Fatalf("referrer annotations %v %v", ann, err)
	}
	if err := VerifyDigest(ctx, reg, func([]byte, []byte) (string, error) { return "", errors.New("wrong key") }, repo, digest); err == nil ||
		!strings.Contains(err.Error(), "wrong key") {
		t.Fatalf("a bundle the verifier refuses: %v", err)
	}

}

// signedImage pushes an image and returns its repository and digest.
func signedImage(t *testing.T, reg *Client, host string) (string, string) {
	t.Helper()
	img, _ := random.Image(16, 1)
	repo := host + "/app"
	digest, err := reg.Write(t.Context(), repo, img)
	if err != nil {
		t.Fatal(err)
	}
	return repo, digest
}

// names "verifies" an echoed statement: it must name the digest, and its
// predicate type is returned as the verified statement's.
func names(b, d []byte) (string, error) {
	var st struct {
		PredicateType string `json:"predicateType"`
	}
	if !bytes.Contains(b, []byte(`"sha256":"`+hex.EncodeToString(d)+`"`)) || json.Unmarshal(b, &st) != nil {
		return "", errors.New("not a signature of this digest")
	}
	return st.PredicateType, nil
}

// statementOver is a statement over digest d with predicate type pt.
func statementOver(d, pt string) []byte {
	return fmt.Appendf(nil, `{"subject":[{"digest":{"sha256":%q}}],"predicateType":%q}`, strings.TrimPrefix(d, "sha256:"), pt)
}

// writeBundle attaches bundle to repo@d as a signature-type referrer annotated
// with predicate.
func writeBundle(t *testing.T, reg *Client, repo, d, predicate string, bundle []byte) string {
	t.Helper()
	ref, err := reg.Write(t.Context(), repo, newArtifact(BundleType, BundleType, []byte("{}"), BundleType, bundle,
		map[string]string{AnnBundlePredicate: predicate}, subjectOf(t, reg, repo, d)))
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// TestVerifyDigestChecksTheSignedPredicate: an attestation over the right
// digest by the right signer, annotated as a signature, is no signature: the
// annotation is unsigned, the predicate type is not.
func TestVerifyDigestChecksTheSignedPredicate(t *testing.T) {
	ctx := t.Context()
	host, reg := newRegistry(t)
	repo, d := signedImage(t, reg, host)
	writeBundle(t, reg, repo, d, SignPredicate, statementOver(d, "https://slsa.dev/provenance/v1"))
	if err := VerifyDigest(ctx, reg, names, repo, d); err == nil || !strings.Contains(err.Error(), "slsa.dev") {
		t.Fatalf("a re-annotated attestation counted as a signature: %v", err)
	}
}

// memReferrers serves signature-type referrers from memory, in order: a
// referrer with no bundle fails to read.
type memReferrers struct {
	*Client
	refs []memRef
}

type memRef struct {
	predicate string
	bundle    []byte
}

func (m memReferrers) Referrers(context.Context, string, string, string) ([]string, error) {
	var out []string
	for i := range m.refs {
		out = append(out, fmt.Sprint(i))
	}
	return out, nil
}

func (m memReferrers) Layer(_ context.Context, _, digest string) ([]byte, map[string]string, error) {
	var i int
	_, _ = fmt.Sscan(digest, &i)
	if m.refs[i].bundle == nil {
		return nil, nil, errTest
	}
	return m.refs[i].bundle, map[string]string{AnnBundlePredicate: m.refs[i].predicate}, nil
}

// TestVerifyDigestReadsPastBadReferrers: an unreadable referrer and more
// attestations than signatures are checked hide no valid signature, and
// more forged signatures than are checked stop the check, refusing.
func TestVerifyDigestReadsPastBadReferrers(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	const slsa = "https://slsa.dev/provenance/v1"
	refs := []memRef{{predicate: SignPredicate}}
	for range maxSignatures + 1 {
		refs = append(refs, memRef{slsa, statementOver(d, slsa)})
	}
	reg := memReferrers{refs: refs}
	if err := VerifyDigest(t.Context(), reg, names, "r", d); err == nil || !strings.Contains(err.Error(), errTest.Error()) {
		t.Fatalf("verified with the only signature unreadable: %v", err)
	}
	reg.refs = append(refs, memRef{SignPredicate, statementOver(d, SignPredicate)})
	if err := VerifyDigest(t.Context(), reg, names, "r", d); err != nil {
		t.Fatalf("an unreadable referrer or the attestations hid a valid signature: %v", err)
	}
	reg.refs = nil
	for i := range maxSignatures + 1 {
		reg.refs = append(reg.refs, memRef{SignPredicate, fmt.Appendf(nil, "forged %d", i)})
	}
	reg.refs = append(reg.refs, memRef{SignPredicate, statementOver(d, SignPredicate)})
	if err := VerifyDigest(t.Context(), reg, names, "r", d); err == nil || strings.Count(err.Error(), "not a signature") != maxSignatures {
		t.Fatalf("checked other than %d signatures: %v", maxSignatures, err)
	}
}


// TestVerifyDigestSkipsAttestations: a bundle with another predicate is an
// attestation, not a signature.
func TestVerifyDigestSkipsAttestations(t *testing.T) {
	ctx := t.Context()
	host, reg := newRegistry(t)
	repo, _ := signedImage(t, reg, host)
	other, _ := random.Image(16, 1)
	od, _ := reg.Write(ctx, repo, other)
	att := newArtifact(BundleType, BundleType, []byte("{}"), BundleType, []byte("{}"),
		map[string]string{AnnBundlePredicate: "https://slsa.dev/provenance/v1"}, subjectOf(t, reg, repo, od))
	if _, err := reg.Write(ctx, repo, att); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigest(ctx, reg, names, repo, od); err == nil || err.Error() != "no signature" {
		t.Fatalf("an attestation counted: %v", err)
	}
}

func TestSignDigestFailures(t *testing.T) {
	ctx := t.Context()
	host, reg := newRegistry(t)
	repo, digest := signedImage(t, reg, host)
	_ = SignDigest(ctx, reg, echoSigner{}, repo, digest)

	for name, err := range map[string]error{
		"bad digest sign":   SignDigest(ctx, reg, echoSigner{}, repo, "nope"),
		"bad digest verify": VerifyDigest(ctx, reg, names, repo, "nope"),
		"unknown digest":    SignDigest(ctx, reg, echoSigner{}, repo, "sha256:"+strings.Repeat("0", 64)),
		"signer fails":      SignDigest(ctx, reg, echoSigner{err: errTest}, repo, digest),
		"referrers fail":    VerifyDigest(ctx, brokenRegistry{reg, "referrers"}, names, repo, digest),
		"layer fails":       VerifyDigest(ctx, brokenRegistry{reg, "layer"}, names, repo, digest),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestClientLayer(t *testing.T) {
	ctx := t.Context()
	host, reg := newRegistry(t)
	repo := host + "/app"
	two, _ := random.Image(16, 2)
	d, _ := reg.Write(ctx, repo, two)
	if _, _, err := reg.Layer(ctx, repo, d); err == nil {
		t.Error("read one layer of a two-layer image")
	}
	if _, _, err := reg.Layer(ctx, "BAD REF", d); err == nil {
		t.Error("a broken reference")
	}
	if _, _, err := reg.Layer(ctx, repo, "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Error("a missing manifest")
	}
	one, _ := random.Image(16, 1)
	d, _ = reg.Write(ctx, repo, one)
	layers, _ := one.Layers()
	ld, _ := layers[0].Digest()
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, "http://"+host+"/v2/app/blobs/"+ld.String(), nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	if _, _, err := reg.Layer(ctx, repo, d); err == nil {
		t.Error("read a layer the registry lost")
	}
}

func subjectOf(t *testing.T, reg *Client, repo, d string) *v1.Descriptor {
	t.Helper()
	head, err := reg.Head(t.Context(), repo+"@"+d)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := v1.NewHash(d)
	return &v1.Descriptor{MediaType: types.MediaType(head.MediaType), Size: head.Size, Digest: h}
}
