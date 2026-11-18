package publish

import (
	"context"
	"errors"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// maxVerdictReason bounds a verdict reason that embeds a registry or
// verifier error.
const maxVerdictReason = 256

// errSignerRead makes VerifyDigest read on past a signature whose signer was
// recorded, so every signature of a digest is read.
var errSignerRead = errors.New("signer recorded")

// SignersOf is every identity whose keyless signature of repo@digest verifies
// under root, whoever it names: who signed is judged by the caller.
// ErrNoSignature means no signature is attached; any other error with
// no signer means none could be verified.
func SignersOf(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string) ([]delivery.PublisherIdentity, error) {
	p := signing.Policy{AnyIdentity: true, Root: root, Tlog: tlog}
	var signers []delivery.PublisherIdentity
	err := VerifyDigest(ctx, reg, func(b, d []byte) (string, error) {
		res, err := p.Verify(b, d)
		if err != nil {
			return "", err
		}
		if pt := res.Statement.GetPredicateType(); pt != SignPredicate {
			return pt, nil // VerifyDigest refuses an attestation that is not a signature
		}
		signers = append(signers, signing.IdentityOf(res))
		return "", errSignerRead
	}, repo, digest)
	if len(signers) > 0 {
		return signers, nil
	}
	return nil, err
}

// CheckPublisher judges who signed repo@digest against rules and pins: it is
// SignersOf then delivery.JudgeSigners. Unsigned and unreadable are never
// accepted.
func CheckPublisher(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string,
	service delivery.ServiceRef, source string, rules []delivery.PublisherRule, ruleSource string, pins []delivery.IdentityPin) delivery.PublisherVerdict {
	signers, err := SignersOf(ctx, reg, root, tlog, repo, digest)
	var v delivery.PublisherVerdict
	switch {
	case err == nil:
		v = delivery.JudgeSigners(service, signers, source, rules, ruleSource, pins)
	case errors.Is(err, ErrNoSignature):
		v = delivery.PublisherVerdict{Verdict: delivery.VerdictUnsigned, Source: delivery.PublisherSourceNone,
			Reason: "no Sigstore signature is attached to " + digest}
	default:
		v = delivery.PublisherVerdict{Verdict: delivery.VerdictUnknown, Source: delivery.PublisherSourceNone,
			Reason: capVerdictReason("no signature could be verified: " + err.Error())}
	}
	v.Digest, v.At = digest, time.Now().UTC().Format(time.RFC3339)
	return v
}

func capVerdictReason(s string) string {
	if r := []rune(s); len(r) > maxVerdictReason {
		return string(r[:maxVerdictReason]) + "…"
	}
	return s
}
