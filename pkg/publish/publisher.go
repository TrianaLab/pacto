package publish

import (
	"context"
	"errors"
	"slices"
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
// under root, whoever it names, classified by the default rule: who signed is
// judged by the caller.
// ErrNoSignature means no signature is attached; any other error with
// no signer means none could be verified.
func SignersOf(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string) ([]delivery.PublisherIdentity, error) {
	signers, _, err := signersOf(ctx, reg, root, tlog, repo, digest)
	return signers, err
}

// signersOf is SignersOf with, beside each signer, the commit its
// certificate was issued for (signing.CommitOf).
func signersOf(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string) ([]delivery.PublisherIdentity, []string, error) {
	p := signing.Policy{AnyIdentity: true, Root: root, Tlog: tlog}
	var signers []delivery.PublisherIdentity
	var commits []string
	err := VerifyDigest(ctx, reg, func(b, d []byte) (string, error) {
		res, err := p.Verify(b, d)
		if err != nil {
			return "", err
		}
		if pt := res.Statement.GetPredicateType(); pt != SignPredicate {
			return pt, nil // VerifyDigest refuses an attestation that is not a signature
		}
		signers = append(signers, delivery.Classified(signing.IdentityOf(res), nil))
		commits = append(commits, signing.CommitOf(res))
		return "", errSignerRead
	}, repo, digest)
	if len(signers) > 0 {
		return signers, commits, nil
	}
	return nil, nil, err
}

// CheckPublisher judges who signed repo@digest against rules and pins: it is
// SignersOf then delivery.JudgeSigners, and returns the signers it read too,
// and the commit the certificate of the verdict's signer was issued for ("" when
// none or no signer). Unsigned and unreadable are never accepted.
func CheckPublisher(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string,
	service delivery.ServiceRef, source string, rules []delivery.PublisherRule, ruleSource string, pins []delivery.IdentityPin) (delivery.PublisherVerdict, []delivery.PublisherIdentity, string) {
	signers, commits, err := signersOf(ctx, reg, root, tlog, repo, digest)
	v := JudgeRead(digest, signers, err, service, source, rules, ruleSource, pins)
	commit := ""
	if v.Identity != nil {
		// JudgeSigners judges in order, so the first equal signer is the one judged.
		if i := slices.Index(signers, *v.Identity); i >= 0 {
			commit = commits[i]
		}
	}
	return v, signers, commit
}

// JudgeRead judges what SignersOf read of digest: its signers through
// delivery.JudgeSigners, ErrNoSignature as unsigned and any other error as
// unknown. The promoter's registry watch reads signers once per digest and
// judges them on every pass.
func JudgeRead(digest string, signers []delivery.PublisherIdentity, err error,
	service delivery.ServiceRef, source string, rules []delivery.PublisherRule, ruleSource string, pins []delivery.IdentityPin) delivery.PublisherVerdict {
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
