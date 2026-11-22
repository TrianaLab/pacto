package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
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
// parse (err). missing are the issuers of signers whose public-good root
// could not be fetched: their declarations are not checked, never read
// under another signer's root.
type rootsRead struct {
	roots   []signerRoot
	missing []string
	noRoot  bool
	err     error
}

// declarationTrust is what one run judges registry declarations with: the
// installation signer policy (nil when none was read, then public-good
// Sigstore's root and every person it vouches for), its signers' trusted
// roots, read once on the first declaration, and each judged bundle by tag
// and digest, so a stack of declarations is walked once.
type declarationTrust struct {
	policy *fleet.DeclarationSignerPolicy
	// rules are the installation's publisher rules, which judge the CI
	// publishes of a carried owner's history.
	rules      []delivery.PublisherRule
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
	t.rules = configRules(in.Config)
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
		} else {
			out.missing = append(out.missing, sg.Issuer)
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
	asker := rec.Declaration.Asker
	signers, why, err := t.signers(ctx, reg, repo, digest, asker.Issuer)
	if err != nil || why != "" {
		return nil, why, err
	}
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
// a declaration cannot be checked because no root is reachable, or none for
// the asker's issuer; a root that does not parse, or signatures that could
// not be read, are errors.
func (t *declarationTrust) signers(ctx context.Context, reg publish.Registry, repo, digest, issuer string) ([]fleet.VerifiedSigner, string, error) {
	if t.policy != nil && t.policy.Unread != "" {
		return nil, "the installation signer policy could not be read (" + t.policy.Unread + ")", nil
	}
	rr := t.roots()
	switch {
	case rr.err != nil:
		return nil, "", rr.err
	case rr.noRoot:
		return nil, "declaration " + repo + "@" + digest + " not checked: no trusted root reachable", nil
	case slices.Contains(rr.missing, issuer):
		return nil, "declaration " + repo + "@" + digest + " not checked: no trusted root reachable for " + issuer, nil
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

// acceptBelow accepts every release below a declaration: plan mode reads
// its baselines unverified and says so (planModeNote).
func acceptBelow(context.Context, string, string) error { return nil }

// declarationSource is the registry declaration base carries (Decision 3):
// base itself when it counts as a declaration of want, else the
// declaration base's record cites, read by digest only in repo (the CLI
// keychain never reaches a host a record names) and only while it still
// counts. A carry re-cites the original declaration, so this is one hop at
// any depth of history. A bundle with neither reads nothing; one whose
// record does not parse says so in a note. A cited declaration that is
// gone or no longer counts returns each field it carried as a dropped
// candidate (Decision 10); notes say why any other declaration found is not
// carried; a check that could not complete is an error naming the
// declaration it was checking.
func (t *declarationTrust) declarationSource(ctx context.Context, reg publish.Registry, want wantService, tags []string, base carryBase) (*registryDeclaration, []carryCandidate, []string, error) {
	ref := base.repo + "@" + base.digest
	d, dropped, notes, err := t.source(ctx, reg, want, tags, base, &ref)
	if err != nil {
		err = fmt.Errorf("registry declaration %s could not be checked: %w", ref, err)
	}
	return d, dropped, notes, err
}

// source is declarationSource, setting *ref to the declaration it reads.
func (t *declarationTrust) source(ctx context.Context, reg publish.Registry, want wantService, tags []string, base carryBase, ref *string) (*registryDeclaration, []carryCandidate, []string, error) {
	rec, err := contract.ReadInferredRecord(base.b.FS)
	if err != nil {
		return nil, nil, []string{"baseline " + *ref + " record unreadable: " + err.Error() + "; registry declarations not carried"}, nil
	}
	tag, digest := base.version, base.digest
	var cited *contract.CitedDeclaration
	switch {
	case rec == nil || rec.Declaration == nil && rec.Inputs.Declaration == nil:
		return nil, nil, nil, nil
	case rec.Declaration == nil:
		cited, *ref = rec.Inputs.Declaration, rec.Inputs.Declaration.Record
		repo, d, _ := strings.Cut(*ref, "@")
		if repo != base.repo {
			return nil, nil, []string{"registry declaration " + *ref + " not read: it is outside " + base.repo}, nil
		}
		h, err := reg.Head(ctx, *ref)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("reading %s: %w", *ref, err)
		}
		if h == nil {
			return nil, citedDropped(base.b.Contract, cited, "it is gone"), nil, nil
		}
		b, err := reg.Pull(ctx, *ref)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("reading %s: %w", *ref, err)
		}
		tag, digest = b.Contract.Service.Version, d
	}
	d, why, err := t.countedDeclaration(ctx, reg, want, base.repo, tags, tag, digest, acceptBelow)
	switch {
	case err != nil:
		return nil, nil, nil, err
	case d == nil && cited != nil:
		// A cited bundle with no declaration block at all is no declaration.
		return nil, citedDropped(base.b.Contract, cited, cmp.Or(why, "it is not a registry declaration")), nil, nil
	case d == nil:
		return nil, nil, []string{"registry declaration " + *ref + " not carried: " + why}, nil
	}
	return d, nil, nil, nil
}

// citedDropped are the fields cited says the baseline c carried from a
// registry declaration that no longer carries (why), each valued as c
// published it, so the plan and its JSON name every field that stops
// carrying.
func citedDropped(c *contract.Contract, cited *contract.CitedDeclaration, why string) []carryCandidate {
	var out []carryCandidate
	for _, f := range strings.Split(cited.Fields, ",") {
		cand := carryCandidate{Field: f, Kind: carriedKindDeclared, Record: cited.Record, Signer: cited.Signer, dropped: why}
		if f == "service.owner" {
			cand.Value = carriedOwner(c.Service.Owner)
		}
		name, _ := wholeEntry(f)
		if n, _, ok := dependencyField(f); ok {
			name = n
		}
		if i := slices.IndexFunc(c.Dependencies, func(d contract.Dependency) bool { return d.Name == name }); name != "" && i >= 0 {
			cand.Value = c.Dependencies[i].Compatibility
		}
		out = append(out, cand)
	}
	return out
}

// carryOwnerRevisionCap bounds the newest releases pacto ci reads to judge a
// carried owner.
const carryOwnerRevisionCap = 32

// ownerVerdict is what the owner rule (OwnerRights) says of a declaration's
// owner: whether it is effective, why not, and the effective owner after
// the history under the owner policy, which accepted ranges are matched
// against.
type ownerVerdict struct {
	Allowed bool
	Reason  string
	Owner   delivery.AcceptanceOwner
}

// ownerHistory is the owner verdict on d from repo's newest final releases
// (at most carryOwnerRevisionCap), each read as a revision and judged by
// OwnerRights under policy (from says why there is none) with the run's
// trusted roots and publisher rules. Only a CI publish or a judged
// declaration sets the owner: a release pushed with registry write alone
// never chooses the owner CI signs. A spent cap or no policy is never an
// allowed owner, unless a CI publish in the window already set the declared
// owner; a history that could not be read is an error.
func (t *declarationTrust) ownerHistory(ctx context.Context, reg publish.Registry, repo string, tags []string, d *registryDeclaration,
	policy *delivery.OwnerPolicy, from string, now time.Time) (ownerVerdict, error) {
	rr := t.roots()
	if rr.err != nil || rr.noRoot {
		return ownerVerdict{Reason: "no trusted root reads the owner history"}, nil
	}
	service, key := d.Bundle.Contract.Service.Name, fleet.RevisionKey(strings.TrimPrefix(d.Ref, repo+"@"))
	versions := slices.DeleteFunc(slices.Clone(semver.Filter(tags)), func(v string) bool { return strings.Contains(v, "-") })
	truncated := len(versions) > carryOwnerRevisionCap
	versions = versions[:min(len(versions), carryOwnerRevisionCap)]
	revs := make([]fleet.ContractRevision, 0, len(versions))
	for _, v := range versions {
		r, err := t.historyRevision(ctx, reg, rr.roots, repo, service, v)
		if err != nil {
			return ownerVerdict{}, fmt.Errorf("the owner history could not be read: %w", err)
		}
		if r != nil {
			revs = append(revs, *r)
		}
	}
	return judgeOwnerHistory(ctx, revs, truncated, d, key, repo, ciOwnerJudge(ownerJudge(service, policy, from, now)), policy, from), nil
}

// judgeOwnerHistory is the owner verdict on d (revision key) from revs,
// repo's newest releases (truncated when older ones were not read). A
// declaration outside the window is judged by the window when a CI publish
// in it already set the declared owner: every carry re-cites the original
// declaration, which ages out of any window.
func judgeOwnerHistory(ctx context.Context, revs []fleet.ContractRevision, truncated bool, d *registryDeclaration, key fleet.RevisionKey, repo string,
	judge OwnerJudge, policy *delivery.OwnerPolicy, from string) ownerVerdict {
	current, rights := OwnerRights(ctx, revs, judge)
	v := ownerVerdict{Allowed: true, Owner: delivery.AcceptanceOwner{Claim: current, Policy: policy}}
	inWindow := slices.ContainsFunc(revs, func(r fleet.ContractRevision) bool { return r.Key == key })
	ciSet := slices.ContainsFunc(revs, ciSetsOwner)
	switch {
	case !inWindow && !truncated:
		return ownerVerdict{Reason: "declaration " + d.Ref + " is not among the releases of " + repo}
	case !inWindow && (!ciSet || !current.Owner.Equal(d.Bundle.Contract.Service.Owner)), truncated && !ciSet:
		return ownerVerdict{Reason: fmt.Sprintf("owner history longer than %d releases", carryOwnerRevisionCap)}
	}
	if r, judged := rights[key]; judged {
		v.Allowed, v.Reason = r.Allowed, r.Message
	}
	if policy == nil {
		v.Allowed, v.Reason = false, strings.TrimSuffix("no owner policy readable from CI: "+from, ": ")
	}
	return v
}

// historyRevision is registryRevision under the first root, with the
// signers every other root verifies added to a declaration, so a
// declaration counted under any policy signer's root is judged by who
// signed it.
func (t *declarationTrust) historyRevision(ctx context.Context, reg publish.Registry, roots []signerRoot, repo, service, version string) (*fleet.ContractRevision, error) {
	r, err := registryRevision(ctx, reg, roots[0].tm, roots[0].tlog, repo, service, version, t.rules, nil)
	if err != nil || r == nil || !isDeclaration(r) {
		return r, err
	}
	for _, extra := range roots[1:] {
		ids, err := publish.SignersOf(ctx, reg, extra.tm, extra.tlog, repo, r.Digest)
		if err != nil && !errors.Is(err, publish.ErrNoSignature) && !errors.Is(err, publish.ErrSignatureInvalid) {
			return nil, fmt.Errorf("reading the signatures of %s@%s: %w", repo, r.Digest, err)
		}
		r.Provenance.Signers = verifiedSigners(append(identitiesOf(r.Provenance.Signers), ids...), nil)
	}
	return r, nil
}

// identitiesOf is signers as publisher identities.
func identitiesOf(signers []fleet.VerifiedSigner) []delivery.PublisherIdentity {
	out := make([]delivery.PublisherIdentity, 0, len(signers))
	for _, s := range signers {
		out = append(out, delivery.PublisherIdentity{Subject: s.Subject, Issuer: s.Issuer, Class: delivery.ActorClass(s.Class)})
	}
	return out
}

// ciOwnerJudge is judge for the owner CI carries: a release outside the
// owner rule sets the owner only when a CI identity published it. One
// pushed with registry write alone (unsigned, or signed by nobody a rule
// accepts) is unverified, so it never becomes the owner a declaration
// repeats unjudged or the owner an accepted range is matched against.
func ciOwnerJudge(judge OwnerJudge) OwnerJudge {
	return func(ctx context.Context, r *fleet.ContractRevision, current delivery.OwnerClaim) *delivery.OwnerRight {
		if v := judge(ctx, r, current); v != nil || fleet.CIPublished(r.Provenance) {
			return v
		}
		return &delivery.OwnerRight{Code: delivery.OwnerSignerUnverified,
			Message: "release " + r.Version + " of " + r.Service + " is not a CI publish: its owner is not counted"}
	}
}

// ciSetsOwner reports whether r is a CI publish that sets an owner as it is.
func ciSetsOwner(r fleet.ContractRevision) bool {
	return setsOwnerAsIs(r) && fleet.CIPublished(r.Provenance)
}

// declarationCandidates are the fields d decides (Decision 5): its owner
// block, dropped unless the owner rule finds it effective, and each range
// it accepted, dropped unless its signer holds the owner right against the
// effective owner. unknown and empty ranges are no decision.
func declarationCandidates(d *registryDeclaration, v ownerVerdict, now time.Time) []carryCandidate {
	svc := d.Bundle.Contract.Service
	base := carryCandidate{Kind: carriedKindDeclared, Record: d.Ref, Signer: citedSigner(d.Signer), AtVersion: d.Version}
	var out []carryCandidate
	if o := svc.Owner; !o.IsEmpty() {
		c := base
		c.Field, c.Value, c.owner = "service.owner", carriedOwner(o), &o
		if !v.Allowed {
			c.dropped = v.Reason
		}
		out = append(out, c)
	}
	signer := delivery.PublisherIdentity{Subject: d.Signer.Subject, Issuer: d.Signer.Issuer, Class: delivery.ActorClass(d.Signer.Class)}
	owns := delivery.HoldsOwnerRight(svc.Name, signer, v.Owner, now)
	for _, a := range d.Record.Declaration.Accepted {
		name, _, ok := dependencyField(a.Field)
		if !ok || a.Range == "" || a.Range == contract.CompatibilityUnknown {
			continue
		}
		c := base
		c.Field, c.Value, c.entry = a.Field, a.Range, &contract.Dependency{Name: name, Compatibility: a.Range}
		if i := slices.IndexFunc(d.Bundle.Contract.Dependencies, func(dep contract.Dependency) bool { return dep.Name == name }); i >= 0 {
			c.entry.Ref = d.Bundle.Contract.Dependencies[i].Ref
		}
		switch {
		case owns:
		case v.Owner.Policy == nil && v.Reason != "":
			// No owner policy, or no history judged: the owner is unknown, never someone else.
			c.dropped = "owner unverified: " + v.Reason
		default:
			c.dropped = "routed: " + d.Signer.Subject + " is not an owner of " + svc.Name
		}
		out = append(out, c)
	}
	return out
}
