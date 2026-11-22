package publish

import (
	"context"
	"errors"
	"fmt"
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

// PolicyVerifier verifies a bundle under p and returns the predicate type of
// the statement its signature covers.
func PolicyVerifier(p signing.Policy) BundleVerifier {
	return func(b, d []byte) (string, error) {
		res, err := p.Verify(b, d)
		if err != nil {
			return "", err
		}
		return res.Statement.GetPredicateType(), nil
	}
}

// SignersOf is every identity whose keyless signature of repo@digest verifies
// under root, whoever it names, classified by the default rule: who signed is
// judged by the caller.
// ErrNoSignature means no signature is attached; any other error with
// no signer means none could be verified.
func SignersOf(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string) ([]delivery.PublisherIdentity, error) {
	signers, _, err := signersOf(ctx, reg, root, tlog, repo, digest, isSignPredicate)
	return signers, err
}

// ProvenancePredicates are the statements whose keyless signer names where
// an artifact was built: Pacto's signature and a signed SLSA provenance
// attestation (as GitHub's attest-build-provenance pushes).
var ProvenancePredicates = []string{SignPredicate, "https://slsa.dev/provenance/v1", "https://slsa.dev/provenance/v0.2"}

// ProvenanceSigners is SignersOf over signatures and signed SLSA provenance
// attestations alike: every identity whose keyless signature of either kind
// verifies. The certificate names the source repository in both cases.
// Signers read from a walk that stopped at a bound or skipped an unreadable
// referrer come back with an error wrapping ErrSignersPartial: an unread
// signature may name another repository.
// ponytail: the SLSA predicate body is not parsed; for GitHub and GitLab
// builders its repository is the certificate's SourceRepositoryURI.
func ProvenanceSigners(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string) ([]delivery.PublisherIdentity, error) {
	signers, _, err := readSigners(ctx, reg, root, tlog, repo, digest, func(p string) bool { return slices.Contains(ProvenancePredicates, p) })
	switch {
	case len(signers) == 0:
		return nil, err
	case err != nil && !errors.Is(err, ErrSignatureInvalid):
		return signers, fmt.Errorf("%w: %v", ErrSignersPartial, err)
	}
	return signers, nil
}

// ErrSignersPartial is a signer read that did not read every signature.
var ErrSignersPartial = errors.New("signatures read partially")

// signersOf is SignersOf over the statements accept takes, with, beside
// each signer, the commit its certificate was issued for (signing.CommitOf).
func signersOf(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string, accept func(string) bool) ([]delivery.PublisherIdentity, []string, error) {
	signers, commits, err := readSigners(ctx, reg, root, tlog, repo, digest, accept)
	if len(signers) > 0 {
		return signers, commits, nil
	}
	return nil, nil, err
}

// readSigners is signersOf with the walk's own error beside the signers it
// read: ErrSignatureInvalid once every referrer was read, another error
// when one was not.
func readSigners(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string, accept func(string) bool) ([]delivery.PublisherIdentity, []string, error) {
	p := signing.Policy{AnyIdentity: true, Root: root, Tlog: tlog}
	var signers []delivery.PublisherIdentity
	var commits []string
	err := verifyReferrers(ctx, reg, func(b, d []byte) (string, error) {
		res, err := p.Verify(b, d)
		if err != nil {
			return "", err
		}
		if pt := res.Statement.GetPredicateType(); !accept(pt) {
			return pt, nil // verifyReferrers refuses a statement accept does not take
		}
		signers = append(signers, delivery.Classified(signing.IdentityOf(res), nil))
		commits = append(commits, signing.CommitOf(res))
		return "", errSignerRead
	}, repo, digest, accept)
	return signers, commits, err
}

// CheckPublisher judges who signed repo@digest against rules and pins: it is
// SignersOf then delivery.JudgeSigners, and returns the signers it read too,
// and the commit the certificate of the verdict's signer was issued for ("" when
// none or no signer). Unsigned and unreadable are never accepted; neither is
// inferred, the installation operator's signer (nil when none is known).
func CheckPublisher(ctx context.Context, reg Registry, root root.TrustedMaterial, tlog bool, repo, digest string,
	service delivery.ServiceRef, source string, rules []delivery.PublisherRule, ruleSource string, pins []delivery.IdentityPin, inferred *delivery.TrustedKey) (delivery.PublisherVerdict, []delivery.PublisherIdentity, string) {
	signers, commits, err := signersOf(ctx, reg, root, tlog, repo, digest, isSignPredicate)
	v := JudgeRead(digest, signers, err, service, source, rules, ruleSource, pins, inferred)
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
	service delivery.ServiceRef, source string, rules []delivery.PublisherRule, ruleSource string, pins []delivery.IdentityPin, inferred *delivery.TrustedKey) delivery.PublisherVerdict {
	var v delivery.PublisherVerdict
	switch {
	case err == nil:
		v = delivery.JudgeSigners(service, signers, source, rules, ruleSource, pins, inferred)
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
