package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

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
// signers its provenance lists: a declaration, or a bundle signed by anyone
// but an accepted CI identity, is decided by DecideOwnerRight for each
// signer, the first allowed one winning, else the most decisive refusal. A
// declaration with no verified signer is unverified.
func ownerJudge(service string, policy *delivery.OwnerPolicy, from string, now time.Time) OwnerJudge {
	return func(_ context.Context, r *fleet.ContractRevision, current delivery.OwnerClaim) *delivery.OwnerRight {
		p := r.Provenance
		signed := p != nil && p.Signature.Status != fleet.SignatureUnsigned
		if !isDeclaration(r) && (!signed || ciAccepted(p)) {
			return nil
		}
		proposed, _ := revisionClaim(r)
		var best *delivery.OwnerRight
		for _, s := range signersOf(p) {
			id := delivery.PublisherIdentity{Subject: s.Subject, Issuer: s.Issuer, Class: delivery.ActorClass(s.Class)}
			v := delivery.DecideOwnerRight(service, id, current, proposed, policy, from, now)
			if v.Allowed {
				return &v
			}
			if best == nil || decisive(v.Code) > decisive(best.Code) {
				best = &v
			}
		}
		if best == nil {
			best = &delivery.OwnerRight{Code: delivery.OwnerSignerUnverified, WorkloadAccess: delivery.WorkloadAccess{Resource: "services", Name: service,
				Source: delivery.AccessByRegistryPolicy, Unverified: "no signature of this version verified"},
				Message: "the owner set by " + service + " " + r.Version + " is unverified: no signature of it verified"}
		}
		return best
	}
}

// signersOf is the verified signers p lists, none for no provenance.
func signersOf(p *fleet.BundleProvenance) []fleet.VerifiedSigner {
	if p == nil {
		return nil
	}
	return p.Signers
}

// ciAccepted reports whether p's verified signature is a CI identity's an
// installation rule, pin or policy accepted.
func ciAccepted(p *fleet.BundleProvenance) bool {
	s := p.Signature
	return s.Status == fleet.SignatureVerified && slices.ContainsFunc(p.Signers, func(v fleet.VerifiedSigner) bool {
		return v.Subject == s.Signer && v.Issuer == s.Issuer && v.Class == string(delivery.ClassCI)
	})
}

// decisive ranks refusal codes: a person outside the rule over a signer who
// is no person over an unknown policy over an unverified signer.
func decisive(code string) int {
	return 4 - slices.Index([]string{delivery.OwnerSignerNotAllowed, delivery.OwnerSignerNotPerson, delivery.OwnerPolicyUnverified, delivery.OwnerSignerUnverified}, code)
}

// maxOwnerHistory bounds the versions a declaration reads to find the
// service's effective owner.
const maxOwnerHistory = 100

// currentOwner is the service's effective owner: OwnerRights over every
// version findBaseline listed in its bundle repository, each read with its contract, record and
// the signature the installation's publisher rule judges. A repository with
// more versions than maxOwnerHistory is refused: an owner read from part of
// the history is unknown.
func (pl *declarePlanner) currentOwner(ctx context.Context) (delivery.OwnerClaim, error) {
	versions := semver.Filter(pl.tags)
	if len(versions) == 0 {
		return delivery.OwnerClaim{}, nil
	}
	if len(versions) > maxOwnerHistory {
		return delivery.OwnerClaim{}, &DeclareError{Code: codeRegistryUnreadable, Message: fmt.Sprintf("%s holds %d versions, more than the %d a declaration reads "+
			"to find the current owner: the owner is unknown", pl.repo(), len(versions), maxOwnerHistory)}
	}
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
	current, _ := OwnerRights(ctx, revs, func(ctx context.Context, r *fleet.ContractRevision, current delivery.OwnerClaim) *delivery.OwnerRight {
		policy, from := pl.ownerPolicy(ctx)
		return ownerJudge(pl.plan.Service, policy, from, pl.s.env().now())(ctx, r, current)
	})
	return current, nil
}

// ownerPolicy reads the owner policy once per plan, on first need.
func (pl *declarePlanner) ownerPolicy(ctx context.Context) (*delivery.OwnerPolicy, string) {
	if pl.policyFrom == nil {
		p, from := pl.s.OwnerPolicy(ctx, pl.repo(), "")
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
