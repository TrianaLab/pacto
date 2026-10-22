package publish

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// The signature publish attaches, as cosign 3 writes it and Flux verifies it:
// a Sigstore bundle referrer carrying a DSSE envelope over an in-toto
// statement that names the signed digest. Its config media type is the
// bundle type too (cosign's is empty), so a registry that reports a
// referrer's type from its config lists it the same way.
const (
	BundleType         = "application/vnd.dev.sigstore.bundle.v0.3+json"
	SignPredicate      = "https://sigstore.dev/cosign/sign/v1"
	AnnBundleContent   = "dev.sigstore.bundle.content"
	AnnBundlePredicate = "dev.sigstore.bundle.predicateType"
	inTotoPayload      = "application/vnd.in-toto+json"
	maxSignatures      = 32
)

// Signer signs a DSSE payload into a Sigstore bundle (pkg/signing.Signer).
type Signer interface {
	Sign(ctx context.Context, payloadType string, payload []byte) ([]byte, error)
}

// BundleVerifier checks a bundle whose statement must name digest, the raw
// sha256 of the signed manifest (pkg/signing.Policy.Verify).
type BundleVerifier func(bundle, digest []byte) error

// SignDigest signs repo@digest and attaches the signature as its referrer.
func SignDigest(ctx context.Context, reg Registry, s Signer, repo, digest string) error {
	h, err := v1.NewHash(digest)
	if err != nil {
		return err
	}
	head, err := reg.Head(ctx, repo+"@"+digest)
	if err != nil || head == nil {
		return fmt.Errorf("reading %s@%s back: %v", repo, digest, err)
	}
	statement := fmt.Appendf(nil, `{"_type":"https://in-toto.io/Statement/v1","subject":[{"digest":{"sha256":%q}}],"predicateType":%q,"predicate":{}}`,
		h.Hex, SignPredicate)
	bundle, err := s.Sign(ctx, inTotoPayload, statement)
	if err != nil {
		return err
	}
	subject := v1.Descriptor{MediaType: types.MediaType(head.MediaType), Size: head.Size, Digest: h}
	ann := map[string]string{AnnBundleContent: "dsse-envelope", AnnBundlePredicate: SignPredicate}
	_, err = reg.Write(ctx, repo, newArtifact(BundleType, BundleType, []byte("{}"), BundleType, bundle, ann, &subject))
	return err
}

// VerifyDigest returns nil when one of repo@digest's signature referrers
// verifies, and otherwise why none did.
func VerifyDigest(ctx context.Context, reg Registry, verify BundleVerifier, repo, digest string) error {
	h, err := v1.NewHash(digest)
	if err != nil {
		return err
	}
	sum, _ := hex.DecodeString(h.Hex) // NewHash checked the hex
	refs, err := reg.Referrers(ctx, repo, digest, BundleType)
	if err != nil {
		return err
	}
	var why []string
	for _, r := range refs[:min(len(refs), maxSignatures)] {
		bundle, ann, err := reg.Layer(ctx, repo, r)
		if err != nil {
			return err
		}
		if ann[AnnBundlePredicate] != SignPredicate {
			continue // an attestation, not a signature
		}
		if err := verify(bundle, sum); err != nil {
			why = append(why, r+": "+err.Error())
			continue
		}
		return nil
	}
	if len(why) == 0 {
		return errors.New("no signature")
	}
	return errors.New("no signature verifies: " + strings.Join(why, "; "))
}
