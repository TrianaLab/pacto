package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// declarationsPublicGoodNote says a registry declaration was judged with no
// installation signer policy.
const declarationsPublicGoodNote = "no installation signer policy was read; declarations judged with public-good issuers"

// registryDeclaration is a counted registry declaration: a person's signed
// declaration bundle, pinned by digest (Ref is repo@digest).
type registryDeclaration struct {
	Ref, Version string
	Signer       fleet.VerifiedSigner
	Bundle       *contract.Bundle
	Record       *contract.InferredRecord
}

// signerRoot is one trusted root a declaration's signatures are read under,
// and whether it requires a transparency log entry.
type signerRoot struct {
	tm   root.TrustedMaterial
	tlog bool
}

// rootsRead is what a run's declaration roots resolved to: the roots, none
// reachable (noRoot, public-good only), or a configured root that does not
// parse (err).
type rootsRead struct {
	roots  []signerRoot
	noRoot bool
	err    error
}

// declarationTrust is what one run judges registry declarations with: the
// installation signer policy (nil when none was read, then public-good
// Sigstore's root and every person it vouches for), its signers' trusted
// roots, read once on the first declaration, and each judged bundle by tag
// and digest, so a stack of declarations is walked once.
type declarationTrust struct {
	policy     *fleet.DeclarationSignerPolicy
	roots      func() rootsRead
	publicGood bool
	memo       map[string]countedAnswer
}

// countedAnswer is one remembered countedDeclaration answer.
type countedAnswer struct {
	d   *registryDeclaration
	why string
	err error
}

// wantService is the local contract a declaration must declare.
type wantService struct{ Name, Domain string }

// declarationTrustFrom builds a run's declaration trust from what trust
// reads (nil reads nothing): the installation signer policy and each of its
// signers' trusted roots (public-good Sigstore's for a signer naming none).
// With no policy read, public-good Sigstore's root alone. A trust config
// that could not be read leaves an unread policy, which counts nobody.
func declarationTrustFrom(ctx context.Context, trust func(context.Context) (Installation, error)) *declarationTrust {
	t := &declarationTrust{memo: map[string]countedAnswer{}}
	var in Installation
	var err error
	if trust != nil {
		in, err = trust(ctx)
	}
	switch {
	case err != nil && !errors.Is(err, ErrNoInstallationTrust):
		t.policy = &fleet.DeclarationSignerPolicy{Unread: err.Error()}
	case len(in.Signers) > 0 || in.SignersUnread != "":
		t.policy = declarationPolicy(in.Signers, in.SignersUnread)
	default:
		t.publicGood = true
	}
	t.roots = sync.OnceValue(func() rootsRead { return signerRoots(in.Signers) })
	return t
}

// signerRoots parses each signer's trusted root; a signer naming none, or no
// signer at all, uses public-good Sigstore's, fetched once.
func signerRoots(signers []delivery.PersonSigner) rootsRead {
	var out rootsRead
	publicGood := sync.OnceValues(func() (root.TrustedMaterial, error) { return publicGoodRoot(signing.PublicTUF) })
	if len(signers) == 0 {
		signers = []delivery.PersonSigner{{}}
	}
	for _, sg := range signers {
		if sg.TrustedRoot != "" {
			tr, err := root.NewTrustedRootFromJSON([]byte(sg.TrustedRoot))
			if err != nil {
				return rootsRead{err: fmt.Errorf("the trusted root of the installation signer %s: %w", sg.Issuer, err)}
			}
			out.roots = append(out.roots, signerRoot{tr, len(tr.RekorLogs()) > 0})
			continue
		}
		if tm, err := publicGood(); err == nil {
			out.roots = append(out.roots, signerRoot{tm, len(tm.RekorLogs()) > 0})
		}
	}
	out.noRoot = len(out.roots) == 0
	return out
}

// declarationPolicy is the installation signer policy declarations are
// judged with: signers' issuers and subjects, unread when they could not be
// read.
func declarationPolicy(signers []delivery.PersonSigner, unread string) *fleet.DeclarationSignerPolicy {
	p := &fleet.DeclarationSignerPolicy{Unread: unread}
	for _, s := range signers {
		p.Signers = append(p.Signers, fleet.PolicySigner{Issuer: s.Issuer, Subjects: s.Subjects})
	}
	return p
}

// policyFor is the policy asker is judged with: the installation's, else
// (none read) one that allows asker, a person public-good Sigstore vouches
// for, as pacto declare lets them declare.
func (t *declarationTrust) policyFor(asker contract.InferredSigner) *fleet.DeclarationSignerPolicy {
	if t.policy != nil {
		return t.policy
	}
	return &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: asker.Issuer, Subjects: regexp.QuoteMeta(asker.Subject)}}}
}

// countedDeclaration is repo@digest, read under tag, when it counts as a
// registry declaration of want: its record is a declaration (not a draft),
// it declares want at version tag, it follows the repository's history
// (publish.FollowsBaseline, with verify judging the releases below it) and
// its asker signed it as a person the policy allows (fleet.DeclarationAccepted).
// Cheap checks come first: no signature is read before the history holds.
// When it does not count, why says so ("" for a bundle with no declaration
// at all); a check that could not complete is an error. Answers are
// remembered for the run.
func (t *declarationTrust) countedDeclaration(ctx context.Context, reg publish.Registry, want wantService, repo string, tags []string, tag, digest string,
	verify func(ctx context.Context, tag, ref string) error) (*registryDeclaration, string, error) {
	key := tag + " " + repo + "@" + digest
	if a, ok := t.memo[key]; ok {
		return a.d, a.why, a.err
	}
	d, why, err := t.count(ctx, reg, want, repo, tags, tag, digest, verify)
	t.memo[key] = countedAnswer{d, why, err}
	return d, why, err
}

// count is countedDeclaration without the memo.
func (t *declarationTrust) count(ctx context.Context, reg publish.Registry, want wantService, repo string, tags []string, tag, digest string,
	verify func(ctx context.Context, tag, ref string) error) (*registryDeclaration, string, error) {
	ref := repo + "@" + digest
	b, err := reg.Pull(ctx, ref)
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", ref, err)
	}
	rec, why := declarationRecord(b, want, tag)
	if rec == nil {
		return nil, why, nil
	}
	ok, why, err := publish.FollowsBaseline(ctx, reg, repo, tags, tag, b, verify, firstDeclaration)
	if err != nil || !ok {
		return nil, why, err
	}
	signers, why, err := t.signers(ctx, reg, repo, digest)
	if err != nil || why != "" {
		return nil, why, err
	}
	asker := rec.Declaration.Asker
	signer, lim, ok := fleet.DeclarationAccepted(&fleet.BundleProvenance{Signers: signers}, rec, t.policyFor(asker))
	if !ok {
		return nil, notAccepted(asker, signers, lim), nil
	}
	return &registryDeclaration{Ref: ref, Version: tag, Signer: signer, Bundle: b, Record: rec}, "", nil
}

// declarationRecord is b's record when b is a declared (never draft)
// declaration of want at version tag; otherwise nil and why ("" when b
// carries no declaration at all).
func declarationRecord(b *contract.Bundle, want wantService, tag string) (*contract.InferredRecord, string) {
	rec, err := contract.ReadInferredRecord(b.FS)
	svc := b.Contract.Service
	switch {
	case err != nil:
		return nil, "its pacto.inferred.yaml does not parse: " + err.Error()
	case rec == nil || rec.Declaration == nil:
		return nil, ""
	case rec.Declaration.State != contract.DeclarationDeclared:
		return nil, "a declaration draft is not a version"
	case svc.Name != want.Name || svc.Domain != want.Domain:
		return nil, "declares " + serviceText(svc.Name, svc.Domain) + ", not " + serviceText(want.Name, want.Domain)
	case svc.Version != tag:
		return nil, "declares version " + svc.Version + ", read under tag " + tag
	}
	return rec, ""
}

// serviceText names a service with its domain, when it has one.
func serviceText(name, domain string) string {
	if domain == "" {
		return name
	}
	return name + " (domain " + domain + ")"
}

// signers are the signers of repo@digest every run root verified. why says
// a declaration cannot be checked because no root is reachable; a root that
// does not parse, or signatures that could not be read, are errors.
func (t *declarationTrust) signers(ctx context.Context, reg publish.Registry, repo, digest string) ([]fleet.VerifiedSigner, string, error) {
	if t.policy != nil && t.policy.Unread != "" {
		return nil, "the installation signer policy could not be read (" + t.policy.Unread + ")", nil
	}
	rr := t.roots()
	switch {
	case rr.err != nil:
		return nil, "", rr.err
	case rr.noRoot:
		return nil, "declaration " + repo + "@" + digest + " not checked: no trusted root reachable", nil
	}
	var ids []delivery.PublisherIdentity
	for _, r := range rr.roots {
		got, err := publish.SignersOf(ctx, reg, r.tm, r.tlog, repo, digest)
		if err != nil && !errors.Is(err, publish.ErrNoSignature) && !errors.Is(err, publish.ErrSignatureInvalid) {
			return nil, "", fmt.Errorf("reading the signatures of %s@%s: %w", repo, digest, err)
		}
		ids = append(ids, got...)
	}
	return verifiedSigners(ids, nil), "", nil
}

// notAccepted is why a declaration whose asker DeclarationAccepted refused
// does not count.
func notAccepted(asker contract.InferredSigner, signers []fleet.VerifiedSigner, lim fleet.Limitation) string {
	if lim.Code != fleet.LimitationDeclarationSignerUnverified {
		return "declaration by " + asker.Subject + " not counted: " + lim.Message
	}
	if len(signers) == 0 {
		return "declaration unsigned: no signature names the asker " + asker.Subject
	}
	var by []string
	for _, s := range signers {
		if s.Class == contract.SignerClassHuman {
			by = append(by, s.Subject)
		} else {
			by = append(by, s.Subject+" ("+s.Class+")")
		}
	}
	return "declaration signed by " + strings.Join(by, ", ") + ", not the asker " + asker.Subject
}
