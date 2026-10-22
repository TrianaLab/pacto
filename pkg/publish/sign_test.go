package publish

import (
	"bytes"
	"context"
	"encoding/hex"
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
	if err := VerifyDigest(ctx, reg, func([]byte, []byte) error { return errors.New("wrong key") }, repo, digest); err == nil ||
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

func names(b, d []byte) error {
	if !bytes.Contains(b, []byte(`"sha256":"`+hex.EncodeToString(d)+`"`)) || !bytes.Contains(b, []byte(SignPredicate)) {
		return errors.New("not a signature of this digest")
	}
	return nil
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
