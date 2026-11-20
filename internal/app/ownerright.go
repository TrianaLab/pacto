package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// OwnerJudge decides the owner change rev makes over the effective owner
// current. nil means rev is outside the owner rule (an unsigned bundle with
// no declaration, or one an accepted CI identity published): its claim
// counts as it is.
type OwnerJudge func(ctx context.Context, rev *fleet.ContractRevision, current delivery.OwnerClaim) *delivery.OwnerRight

// OwnerRights walks one service's revisions in fleet.LessRevisionChrono
// order and returns its effective owner after them, with the verdict on each
// revision judge judged. A revision changes the effective owner only when
// its claim (declared owner, else attested owner) differs and judge does not
// refuse it, so a refused or unverified claim never becomes the reference of
// the next one. A record's self-named baseline is never read.
func OwnerRights(ctx context.Context, revs []fleet.ContractRevision, judge OwnerJudge) (delivery.OwnerClaim, map[fleet.RevisionKey]delivery.OwnerRight) {
	ordered := make([]*fleet.ContractRevision, len(revs))
	for i := range revs {
		ordered[i] = &revs[i]
	}
	sort.SliceStable(ordered, func(i, j int) bool { return fleet.LessRevisionChrono(ordered[i], ordered[j]) })
	var current delivery.OwnerClaim
	rights := map[fleet.RevisionKey]delivery.OwnerRight{}
	for _, r := range ordered {
		proposed, claims := revisionClaim(r)
		if !ownerChange(current, proposed, claims, isDeclaration(r)) {
			if claims {
				current = proposed // the same owner, perhaps read from a stronger tier
			}
			continue
		}
		if v := judge(ctx, r, current); v != nil {
			rights[r.Key] = *v
			if !v.Allowed {
				continue
			}
		}
		current = proposed
	}
	return current, rights
}

// revisionClaim is the owner r claims: its pacto.yaml owner (declared), else
// its attested owner; false when it claims none.
func revisionClaim(r *fleet.ContractRevision) (delivery.OwnerClaim, bool) {
	if !r.Owner.IsEmpty() {
		return delivery.OwnerClaim{Owner: r.Owner, Provenance: fleet.OwnerProvenanceDeclared}, true
	}
	if o, citation, _, ok := fleet.AttestedOwner(r); ok {
		return delivery.OwnerClaim{Owner: o, Provenance: fleet.OwnerProvenanceAttested, Source: citation}, true
	}
	return delivery.OwnerClaim{}, false
}

// ownerChange reports whether a claim changes the effective owner current.
// A revision that claims no owner removes one only when it is a declaration
// and the current owner is declared: an attested owner is the repository's,
// and a declaration that names none leaves it.
func ownerChange(current, proposed delivery.OwnerClaim, claims, declaration bool) bool {
	if !claims {
		return declaration && current.Provenance == fleet.OwnerProvenanceDeclared
	}
	return !proposed.Owner.Equal(current.Owner)
}

// isDeclaration reports whether r's record carries a declaration.
func isDeclaration(r *fleet.ContractRevision) bool {
	return r.Record != nil && r.Record.Declaration != nil
}

// ownerJudge judges a published revision's owner change from the verified
// signers its provenance lists (delivery.JudgeOwnerSigners): a declaration,
// or a bundle signed by anyone but an accepted CI identity. A declaration
// with no verified signer is unverified.
func ownerJudge(service string, policy *delivery.OwnerPolicy, from string, now time.Time) OwnerJudge {
	return func(_ context.Context, r *fleet.ContractRevision, current delivery.OwnerClaim) *delivery.OwnerRight {
		if outsideOwnerRule(r) {
			return nil
		}
		proposed, _ := revisionClaim(r)
		var ids []delivery.PublisherIdentity
		for _, s := range signersOf(r.Provenance) {
			ids = append(ids, delivery.PublisherIdentity{Subject: s.Subject, Issuer: s.Issuer, Class: delivery.ActorClass(s.Class)})
		}
		v := delivery.JudgeOwnerSigners(service, ids, "", current, proposed, policy, from, now)
		return &v
	}
}

// outsideOwnerRule reports whether r's owner claim counts as it is: no
// declaration, and unsigned or a CI identity's publish (fleet.CIPublished).
// Registry write is the trust boundary of an unsigned push.
func outsideOwnerRule(r *fleet.ContractRevision) bool {
	p := r.Provenance
	signed := p != nil && p.Signature.Status != fleet.SignatureUnsigned
	return !isDeclaration(r) && (!signed || fleet.CIPublished(p))
}

// signersOf is the verified signers p lists, none for no provenance.
func signersOf(p *fleet.BundleProvenance) []fleet.VerifiedSigner {
	if p == nil {
		return nil
	}
	return p.Signers
}

// OwnerPolicyFunc reads the owner policy for the bundle repository repo,
// and when there is none, why (Service.OwnerPolicy).
type OwnerPolicyFunc func(ctx context.Context, repo string) (*delivery.OwnerPolicy, string)

// fleetOwnerPolicy is Service.OwnerPolicy with the fleet's trust config
// path: a server's mounted policy, else the signed target snapshots.
func (s *Service) fleetOwnerPolicy(trust string) OwnerPolicyFunc {
	return func(ctx context.Context, repo string) (*delivery.OwnerPolicy, string) {
		return s.OwnerPolicy(ctx, repo, trust)
	}
}

// ownerRights is the fleet's owner pass (fleetsrc.OwnerRightsFunc): per
// service, OwnerRights over the revisions one registry source read, each
// owner change judged by publish.VerifyOwnerRight from the bundle's
// certificates, and the verdict stamped on the revision's provenance. Every
// declaration gets a stamp, allowed when its owner is unchanged. Nothing is
// stamped once the budget in ctx is spent: an unstamped declaration reads
// unverified in the fleet.
func (p *ProvenanceChecker) ownerRights(policy OwnerPolicyFunc) fleetsrc.OwnerRightsFunc {
	return func(ctx context.Context, _ string, revs []fleet.ContractRevision) {
		groups := map[fleet.ServiceKey][]fleet.ContractRevision{}
		for _, r := range revs {
			groups[r.ServiceKey] = append(groups[r.ServiceKey], r)
		}
		policies := map[string]*ownerPolicyRead{}
		for _, group := range groups {
			p.stampOwnerRights(ctx, group, func(repo string) *ownerPolicyRead {
				if policies[repo] == nil {
					pol, from := policy(ctx, repo)
					policies[repo] = &ownerPolicyRead{pol, from}
				}
				return policies[repo]
			})
		}
	}
}

// ownerPolicyRead is one read of an owner policy and why it is absent.
type ownerPolicyRead struct {
	policy *delivery.OwnerPolicy
	from   string
}

// stampOwnerRights judges one service's revisions and stamps the verdicts.
// The fleet reads only the refs its sources name, so the owner before a
// revision may be unread: then a refusal, a grant to a member of the owner
// read and an unchanged declaration are unconfirmed (OWNER_SIGNER_UNVERIFIED);
// only a default approver's grant holds whatever the owner was. A check the
// budget or the trusted root cut short is unverified too.
func (p *ProvenanceChecker) stampOwnerRights(ctx context.Context, revs []fleet.ContractRevision, policy func(repo string) *ownerPolicyRead) {
	spent := map[fleet.RevisionKey]bool{}
	hist := &ownerHistory{reg: p.reg, revs: revs, tags: map[string][]string{}}
	_, rights := OwnerRights(ctx, revs, func(ctx context.Context, r *fleet.ContractRevision, current delivery.OwnerClaim) *delivery.OwnerRight {
		if outsideOwnerRule(r) {
			return nil
		}
		repo, digest, _ := strings.Cut(contract.TrimOCIScheme(r.ResolvedRef), "@")
		tm, err := p.trustedRoot()
		if ctx.Err() != nil || err != nil {
			spent[r.Key] = true // unknown, and never the next reference
			return &delivery.OwnerRight{Code: delivery.OwnerSignerUnverified}
		}
		pol := policy(repo)
		proposed, _ := revisionClaim(r)
		v := publish.VerifyOwnerRight(ctx, publish.OwnerRightInput{Registry: p.reg, Root: tm, TLog: rekorURL != "", Repo: repo, Digest: cmp.Or(digest, r.Digest),
			Service: r.Service, Proposed: proposed.Owner, Current: current, Policy: pol.policy, PolicyFrom: pol.from,
			ClassRules: p.installation(ctx).classes, Now: p.now()})
		spent[r.Key] = ctx.Err() != nil
		if historyMatters(v) && hist.unknownBefore(ctx, r) {
			v = unreadHistory(r)
		}
		return &v
	})
	for i := range revs {
		r := &revs[i]
		v, judged := rights[r.Key]
		switch {
		case r.Provenance == nil:
		case spent[r.Key]:
			r.Provenance.OwnerRight = &fleet.OwnerRightCheck{Code: delivery.OwnerSignerUnverified,
				Message: "the owner this version of " + r.Service + " sets is unverified: the owner check did not finish"}
		case judged:
			r.Provenance.OwnerRight = &fleet.OwnerRightCheck{Allowed: v.Allowed, Code: v.Code, Message: v.Message}
		case isDeclaration(r) && hist.unknownBefore(ctx, r):
			v := unreadHistory(r)
			r.Provenance.OwnerRight = &fleet.OwnerRightCheck{Code: v.Code, Message: v.Message}
		case isDeclaration(r):
			r.Provenance.OwnerRight = &fleet.OwnerRightCheck{Allowed: true, Message: "the owner is unchanged"}
		}
	}
}

// historyMatters reports whether v depends on the owner before the
// revision: every verdict but a default approver's grant and an
// unverified one.
func historyMatters(v delivery.OwnerRight) bool {
	if v.Allowed {
		return !strings.HasPrefix(v.Grant, "default-approver:")
	}
	return v.Code == delivery.OwnerSignerNotPerson || v.Code == delivery.OwnerSignerNotAllowed
}

// unreadHistory is the verdict on r when the owner before it was not read.
func unreadHistory(r *fleet.ContractRevision) delivery.OwnerRight {
	return delivery.OwnerRight{WorkloadAccess: delivery.WorkloadAccess{Unverified: "the owner before this version was not read"}, Code: delivery.OwnerSignerUnverified,
		Message: "the owner this version of " + r.Service + " sets is unverified: the owner before it was not read"}
}

// ownerHistory tells which of one service's revisions follow a version of
// their repository the fleet did not read.
type ownerHistory struct {
	reg  publish.Registry
	revs []fleet.ContractRevision
	// tags holds each repository's semver tags once listed; nil for one
	// that could not be listed.
	tags map[string][]string
}

// unknownBefore reports whether the owner before r is unknown: a version of
// r's repository below r was not read since the newest read revision before
// r that sets the owner as it is (outsideOwnerRule), or the repository's
// versions could not be listed.
//
// ponytail: one tag listing per repository and pass; the walk is quadratic
// in the revisions one source read of one service.
func (h *ownerHistory) unknownBefore(ctx context.Context, r *fleet.ContractRevision) bool {
	from, read := "", map[string]bool{}
	for i := range h.revs {
		o := &h.revs[i]
		read[o.Version] = true
		if _, claims := revisionClaim(o); claims && outsideOwnerRule(o) && fleet.LessRevisionChrono(o, r) && (from == "" || semver.Compare(o.Version, from) > 0) {
			from = o.Version
		}
	}
	repo, _, _ := strings.Cut(contract.TrimOCIScheme(r.ResolvedRef), "@")
	tags, listed := h.tags[repo]
	if !listed {
		all, err := h.reg.Tags(ctx, repo)
		if tags = append([]string{}, semver.Filter(all)...); err != nil {
			tags = nil // a repository that could not be listed has unread versions
		}
		h.tags[repo] = tags
	}
	if tags == nil {
		return true
	}
	return slices.ContainsFunc(tags, func(t string) bool {
		return !read[t] && semver.Compare(t, r.Version) < 0 && (from == "" || semver.Compare(t, from) > 0)
	})
}

// maxOwnerHistory bounds the versions a declaration reads to find the
// service's effective owner.
const maxOwnerHistory = 100

// currentOwner is the service's effective owner: OwnerRights over every
// version findBaseline listed in its bundle repository, each read with its
// contract, record and the signature the installation's publisher rule
// judges. A repository with more versions than maxOwnerHistory is read from
// its newest ones: a version among them that sets the owner as it is (CI or
// an unsigned push, outsideOwnerRule) makes every older one moot. With none,
// the owner before them is unknown and the plan is refused as unverified.
func (pl *declarePlanner) currentOwner(ctx context.Context) (delivery.OwnerClaim, error) {
	versions := semver.Filter(pl.tags)
	truncated := len(versions) > maxOwnerHistory
	versions = versions[:min(len(versions), maxOwnerHistory)]
	revs := make([]fleet.ContractRevision, 0, len(versions))
	for _, v := range versions {
		r, err := pl.historyRevision(ctx, pl.root, v)
		if err != nil {
			return delivery.OwnerClaim{}, &DeclareError{Code: codeRegistryUnreadable, Message: "reading " + pl.repo() + ":" + v + " for its owner: " + err.Error()}
		}
		if r != nil {
			revs = append(revs, *r)
		}
	}
	if truncated && !slices.ContainsFunc(revs, setsOwnerAsIs) {
		return delivery.OwnerClaim{}, &DeclareError{Code: delivery.OwnerSignerUnverified, Message: fmt.Sprintf("%s holds %d versions; a declaration reads the newest %d, "+
			"and none of them sets the owner as a CI publish or an unsigned push does, so the owner before them is unknown", pl.repo(), len(semver.Filter(pl.tags)), maxOwnerHistory)}
	}
	current, _ := OwnerRights(ctx, revs, func(ctx context.Context, r *fleet.ContractRevision, current delivery.OwnerClaim) *delivery.OwnerRight {
		policy, from := pl.ownerPolicy(ctx)
		return ownerJudge(pl.plan.Service, policy, from, pl.s.env().now())(ctx, r, current)
	})
	return current, nil
}

// setsOwnerAsIs reports whether r sets the effective owner whatever it was
// before: it claims one and is outside the owner rule.
func setsOwnerAsIs(r fleet.ContractRevision) bool {
	_, claims := revisionClaim(&r)
	return claims && outsideOwnerRule(&r)
}

// ownerPolicy reads the owner policy once per plan, on first need.
func (pl *declarePlanner) ownerPolicy(ctx context.Context) (*delivery.OwnerPolicy, string) {
	if pl.policyFrom == nil {
		p, from := pl.s.OwnerPolicy(ctx, pl.repo(), pl.req.DeliveryTrust)
		pl.ownerPol, pl.policyFrom = p, &from
	}
	return pl.ownerPol, *pl.policyFrom
}

// historyRevision is version as a revision: its owner, record and
// provenance; nil when the tag went away since the listing.
func (pl *declarePlanner) historyRevision(ctx context.Context, tm root.TrustedMaterial, version string) (*fleet.ContractRevision, error) {
	h, err := pl.reg.Head(ctx, pl.repo()+":"+version)
	if err != nil || h == nil {
		return nil, err
	}
	b, err := pl.reg.Pull(ctx, pl.repo()+"@"+h.Digest)
	if err != nil {
		return nil, err
	}
	rec, _ := contract.ReadInferredRecord(b.FS) // ponytail: an unreadable record holds no declaration and no attested owner
	v, signers, commit := publish.CheckPublisher(ctx, pl.reg, tm, pl.req.Signing.RekorURL != "", pl.repo(), h.Digest,
		delivery.ServiceRef{Name: pl.plan.Service}, "", pl.rules, delivery.PublisherSourceRule, nil)
	return &fleet.ContractRevision{Key: fleet.RevisionKey(h.Digest), Service: pl.plan.Service, Version: version, Digest: h.Digest,
		Owner: b.Contract.Service.Owner, Record: rec, Provenance: &fleet.BundleProvenance{Signature: signing.BundleSignatureFor(v, commit),
			Signers: verifiedSigners(signers, pl.req.Signing.Rules)}}, nil
}
