package publish

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// The signature publish attaches, as cosign 3 writes it and Flux verifies it:
// a Sigstore bundle referrer carrying a DSSE envelope over an in-toto
// statement that names the signed digest. Its config media type is the
// bundle type too (cosign's is empty), so a registry that reports a
// referrer's type from its config lists it the same way.
// The media, payload and annotation names are delivery's and deliveryrecord's,
// so published signatures match what the readers filter on.
const (
	SignPredicate = "https://sigstore.dev/cosign/sign/v1"
	maxSignatures = 32
	maxReferrers  = 256
)

// VerifyDigest's outcomes a caller can tell apart: no signature referrer was
// found, or every signature read failed verification and none was unreadable.
// Any other error means the check could not be completed.
var (
	ErrNoSignature      = errors.New("no signature")
	ErrSignatureInvalid = errors.New("no signature verifies")
)

// Signer signs a DSSE payload into a Sigstore bundle (pkg/signing.Signer).
type Signer = delivery.Signer

// BundleVerifier checks a bundle whose statement must name digest, the raw
// sha256 of the signed manifest (pkg/signing.Policy.Verify), and returns the
// predicate type of the statement it verified.
type BundleVerifier func(bundle, digest []byte) (predicateType string, err error)

// SignDigest signs repo@digest and attaches the signature as its referrer,
// named by a write-once pacto-sig tag too on a registry without the
// Referrers API.
func SignDigest(ctx context.Context, reg Registry, s Signer, repo, digest string) error {
	h, err := v1.NewHash(digest)
	if err != nil {
		return err
	}
	mode, err := reg.Mode(ctx, repo, digest)
	if err != nil {
		return err
	}
	head, err := reg.Head(ctx, repo+"@"+digest)
	if err != nil || head == nil {
		return fmt.Errorf("reading %s@%s back: %v", repo, digest, err)
	}
	statement := fmt.Appendf(nil, `{"_type":%q,"subject":[{"digest":{"sha256":%q}}],"predicateType":%q,"predicate":{}}`,
		delivery.StatementType, h.Hex, SignPredicate)
	bundle, err := s.Sign(ctx, delivery.PayloadType, statement)
	if err != nil {
		return err
	}
	subject := v1.Descriptor{MediaType: types.MediaType(head.MediaType), Size: head.Size, Digest: h}
	ann := map[string]string{deliveryrecord.AnnBundleContent: "dsse-envelope", deliveryrecord.AnnPredicateType: SignPredicate}
	img, art := newReferrer(delivery.BundleMediaType, delivery.BundleMediaType, []byte("{}"), delivery.BundleMediaType, bundle, ann, subject)
	return writeReferrer(ctx, reg, mode, repo, digest, img, art)
}

// writeReferrer pushes a referrer of repo@digest and, on a registry without
// the Referrers API, names it with its write-once tag, the only place Pacto
// readers look there. The push still updates the shared sha256-<hex> index
// on such a registry, which Flux and cosign read; Pacto never does.
func writeReferrer(ctx context.Context, reg Registry, mode capabilities.ReferrersMode, repo, digest string, img v1.Image, art ocireferrer.Artifact) error {
	if _, err := reg.Write(ctx, repo, img); err != nil {
		return err
	}
	if mode == capabilities.ReferrersWriteOnceTags {
		_, err := reg.PutSigTag(ctx, repo, digest, art)
		return err
	}
	return nil
}

// VerifyDigest returns nil when one of repo@digest's signature referrers
// verifies, and otherwise why none did: ErrNoSignature when none is a
// signature, ErrSignatureInvalid when signatures were read and none verified
// with nothing left unread, and a plain error otherwise.
func VerifyDigest(ctx context.Context, reg Registry, verify BundleVerifier, repo, digest string) error {
	return verifyReferrers(ctx, reg, verify, repo, digest, isSignPredicate)
}

// isSignPredicate accepts Pacto's signature predicate alone.
func isSignPredicate(p string) bool { return p == SignPredicate }

// verifyReferrers is VerifyDigest over the bundle referrers whose predicate
// accept takes: the unsigned annotation filters what is read, the verified
// statement's predicate type decides.
func verifyReferrers(ctx context.Context, reg Registry, verify BundleVerifier, repo, digest string, accept func(string) bool) error {
	h, err := v1.NewHash(digest)
	if err != nil {
		return err
	}
	sum, _ := hex.DecodeString(h.Hex) // NewHash checked the hex
	refs, err := reg.Referrers(ctx, repo, digest, delivery.BundleMediaType)
	if err != nil {
		return err
	}
	var why []string
	unreadable, invalid := false, 0
	// ponytail: attestations cost a fetch each before their annotation says
	// so; maxReferrers bounds that work, maxSignatures the signatures checked.
	seen := 0
	for i, r := range refs {
		if i == maxReferrers || seen == maxSignatures {
			why = append(why, fmt.Sprintf("%d referrers were not read: one check reads at most %d referrers and %d signatures",
				len(refs)-i, maxReferrers, maxSignatures))
			unreadable = true
			break
		}
		bundle, ann, err := reg.Layer(ctx, repo, r)
		if err != nil {
			why = append(why, r+": "+err.Error()) // one unreadable referrer hides no other
			unreadable = true
			continue
		}
		if !accept(ann[deliveryrecord.AnnPredicateType]) {
			continue // an attestation, not a signature
		}
		seen++
		// The annotation is unsigned: only the predicate the signature covers
		// says that this bundle signs the digest rather than attests to it.
		pt, err := verify(bundle, sum)
		if err == nil && !accept(pt) {
			err = fmt.Errorf("it signs a %q statement, not a signature", pt)
		}
		if err != nil {
			why = append(why, r+": "+err.Error())
			invalid++
			continue
		}
		return nil
	}
	if len(why) == 0 {
		return ErrNoSignature
	}
	if invalid > 0 && !unreadable {
		return fmt.Errorf("%w: %s", ErrSignatureInvalid, strings.Join(why, "; "))
	}
	return errors.New("no signature verifies: " + strings.Join(why, "; "))
}
