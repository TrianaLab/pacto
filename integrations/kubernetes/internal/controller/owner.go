/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/sigstore/sigstore-go/pkg/root"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// OwnerCheck is what the OwnerVerified condition judges a running bundle's
// owner change with: the manager's owner policy (the chart's declare block),
// the Sigstore root signatures chain to, the installation's publisher rules
// (an attested owner rests on a CI identity they accept) and a registry
// client that reads with a Pacto's own pull credentials.
type OwnerCheck struct {
	// Policy is the mounted owner policy; nil reads every owner change that
	// no member of the owner signed as unverified, PolicyFrom saying why.
	Policy     *core.OwnerPolicy
	PolicyFrom string
	// Root fetches the trusted root. A root that cannot be fetched verifies
	// no signature: the owner change is unverified, never allowed.
	Root func() (root.TrustedMaterial, error)
	// Rules are the installation's publisher rules; none means no owner is
	// ever attested here (fail closed).
	Rules    []core.PublisherRule
	Registry func(auth *authn.AuthConfig) publish.Registry
	Now      func() time.Time
}

// ownerJudgedPrefix starts the tail of the OwnerVerified message that names
// the bundle judged and the revision it was judged against, so the next
// reconcile keeps the verdict of an unchanged bundle and walks past a claim
// that did not count.
const ownerJudgedPrefix = " [bundle "

// verifyOwner sets OwnerVerified for a Pacto whose OCI bundle changes the
// owner of the revision the CR ran before (prev), and reports whether the
// bundle's owner may be shown. The reference is never the baseline a record
// names: it is the previously run revision, or, when that one's owner claim
// was refused or unverified, the reference that one was judged against. An
// unsigned bundle without declaration keys, or one an accepted CI identity
// published, is outside the rule (registry write is the trust boundary).
// The condition is absent for every other Pacto.
//
// ponytail: one registry read per new digest (signatures of the bundle and
// its reference); a verdict is kept while the digest is unchanged. Memoise
// per digest in the loader cache if many Pactos share one bundle.
func (r *PactoReconciler) verifyOwner(ctx context.Context, pacto *pactov1alpha1.Pacto, ec *effectiveContract, prev *pactov1alpha1.PactoStatus) bool {
	oc, lr := r.Owners, ec.loaded
	if oc == nil || lr.ResolvedDigest == "" {
		meta.RemoveStatusCondition(&pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
		return true
	}
	ref, judge := ownerReference(prev, lr.ResolvedDigest)
	if !judge {
		if c := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified); c != nil && judgedBundle(c) == lr.ResolvedDigest {
			return c.Status == metav1.ConditionTrue
		}
		meta.RemoveStatusCondition(&pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
		return true
	}
	tm, rootErr := oc.Root()
	reg := oc.Registry(ec.auth)
	cur := oc.bundle(ctx, reg, tm, lr)
	current, err := r.referenceClaim(ctx, oc, reg, tm, ref, ec.auth)
	if err != nil {
		r.setOwnerVerified(pacto, metav1.ConditionUnknown, pactov1alpha1.ReasonOwnerSignerUnverified,
			"the owner of the revision this one replaces is unknown: "+err.Error(), lr.ResolvedDigest, ref)
		return false
	}
	proposed, claims := cur.claim()
	if !ownerChange(current, proposed, claims, cur.declaration()) || cur.outsideRule() {
		meta.RemoveStatusCondition(&pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
		return true
	}
	v := publish.VerifyOwnerRight(ctx, publish.OwnerRightInput{Registry: reg, Root: tm, TLog: hasTLog(tm), Repo: cur.repo, Digest: cur.digest,
		Service: lr.Contract.Service.Name, Proposed: proposed.Owner, Current: current, Policy: oc.Policy, PolicyFrom: oc.PolicyFrom, Now: oc.Now()})
	msg := v.Message
	if rootErr != nil {
		msg += " (the Sigstore trusted root could not be fetched: " + rootErr.Error() + ")"
	}
	status, reason := ownerVerdict(v)
	r.setOwnerVerified(pacto, status, reason, msg, lr.ResolvedDigest, ref)
	return v.Allowed
}

// ownerReference is the revision (repository@digest) the bundle digest is
// judged against, empty for none, and false when there is nothing to judge:
// digest kept its verdict (true or false) from the last reconcile, or ran
// before without one.
func ownerReference(prev *pactov1alpha1.PactoStatus, digest string) (string, bool) {
	c := meta.FindStatusCondition(prev.Conditions, pactov1alpha1.ConditionOwnerVerified)
	if c != nil && judgedBundle(c) == digest {
		return judgedReference(c), c.Status == metav1.ConditionUnknown
	}
	ran := ""
	if prev.Contract != nil && prev.EvaluatedRevision != nil {
		ran = prev.EvaluatedRevision.ContractDigest
	}
	switch {
	case ran == digest:
		return "", false
	case ran == "":
		return "", true
	case c != nil && judgedBundle(c) == ran && c.Status != metav1.ConditionTrue:
		return judgedReference(c), true // the claim before did not count: its own reference stays
	}
	return repoOf(prev.Contract.ResolvedRef) + "@" + ran, true
}

// judgedBundle and judgedReference read the tail setOwnerVerified writes;
// a condition without one names neither.
func judgedBundle(c *metav1.Condition) string {
	b, _ := judged(c)
	return b
}

func judgedReference(c *metav1.Condition) string {
	_, ref := judged(c)
	return ref
}

func judged(c *metav1.Condition) (bundle, ref string) {
	i := strings.LastIndex(c.Message, ownerJudgedPrefix)
	if i < 0 {
		return "", ""
	}
	tail := strings.TrimSuffix(c.Message[i+len(ownerJudgedPrefix):], "]")
	bundle, ref, _ = strings.Cut(tail, ", judged against ")
	if ref == "none" {
		ref = ""
	}
	return bundle, ref
}

func (r *PactoReconciler) setOwnerVerified(pacto *pactov1alpha1.Pacto, status metav1.ConditionStatus, reason, msg, digest, ref string) {
	if ref == "" {
		ref = "none"
	}
	r.setCondition(pacto, pactov1alpha1.ConditionOwnerVerified, status, reason, msg+ownerJudgedPrefix+digest+", judged against "+ref+"]")
}

// ownerVerdict maps an owner right onto the condition's status and reason.
func ownerVerdict(v core.OwnerRight) (metav1.ConditionStatus, string) {
	switch {
	case v.Allowed:
		return metav1.ConditionTrue, pactov1alpha1.ReasonOwnerSignerAllowed
	case v.Code == core.OwnerSignerNotPerson:
		return metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotPerson
	case v.Code == core.OwnerSignerNotAllowed:
		return metav1.ConditionFalse, pactov1alpha1.ReasonOwnerSignerNotAllowed
	}
	return metav1.ConditionUnknown, pactov1alpha1.ReasonOwnerSignerUnverified
}

// referenceClaim is the owner the revision ref claims (declared, else
// attested), empty for none.
func (r *PactoReconciler) referenceClaim(ctx context.Context, oc *OwnerCheck, reg publish.Registry, tm root.TrustedMaterial, ref string, auth *authn.AuthConfig) (core.OwnerClaim, error) {
	if ref == "" {
		return core.OwnerClaim{}, nil
	}
	lr, err := r.Loader.Load(ctx, ref, "", auth)
	if err != nil {
		return core.OwnerClaim{}, err
	}
	_, digest, _ := strings.Cut(ref, "@")
	b := *lr // the loader caches lr: a copy names the digest
	b.ResolvedRef, b.ResolvedDigest = ref, digest
	c, _ := oc.bundle(ctx, reg, tm, &b).claim()
	return c, nil
}

// ownerBundle is one bundle as the owner rule reads it: its pacto.yaml
// owner, its inferred record and who signed it, judged against the
// installation's publisher rules.
type ownerBundle struct {
	repo, digest string
	rev          fleet.ContractRevision
}

// bundle reads lr's record and signature. An unreadable record holds no
// declaration and no attested owner.
func (oc *OwnerCheck) bundle(ctx context.Context, reg publish.Registry, tm root.TrustedMaterial, lr *loader.LoadResult) ownerBundle {
	repo := repoOf(lr.ResolvedRef)
	rec, _ := contract.ReadInferredRecord(lr.BundleFS)
	ruleSource := core.PublisherSourceRule
	if len(oc.Rules) == 0 {
		ruleSource = core.PublisherSourceInferred // no rule: never an accepted signer, never attested
	}
	v, signers, commit := publish.CheckPublisher(ctx, reg, tm, hasTLog(tm), repo, lr.ResolvedDigest,
		core.ServiceRef{Name: lr.Contract.Service.Name}, "", oc.Rules, ruleSource, nil)
	ids := make([]fleet.VerifiedSigner, 0, len(signers))
	for _, s := range signers {
		ids = append(ids, fleet.VerifiedSigner{Subject: s.Subject, Issuer: s.Issuer, Class: string(s.Class)})
	}
	return ownerBundle{repo: repo, digest: lr.ResolvedDigest, rev: fleet.ContractRevision{Owner: lr.Contract.Service.Owner, Record: rec,
		Provenance: &fleet.BundleProvenance{Signature: signing.BundleSignatureFor(v, commit), Signers: ids}}}
}

// claim is the owner b claims: its pacto.yaml owner (declared), else its
// attested owner; false when it claims none.
func (b ownerBundle) claim() (core.OwnerClaim, bool) {
	if !b.rev.Owner.IsEmpty() {
		return core.OwnerClaim{Owner: b.rev.Owner, Provenance: fleet.OwnerProvenanceDeclared}, true
	}
	if o, citation, _, ok := fleet.AttestedOwner(&b.rev); ok {
		return core.OwnerClaim{Owner: o, Provenance: fleet.OwnerProvenanceAttested, Source: citation}, true
	}
	return core.OwnerClaim{}, false
}

func (b ownerBundle) declaration() bool {
	return b.rev.Record != nil && b.rev.Record.Declaration != nil
}

// outsideRule reports whether b's owner claim counts as it is: no
// declaration, and unsigned or signed by a CI identity a rule accepted.
func (b ownerBundle) outsideRule() bool {
	p := b.rev.Provenance
	s := p.Signature
	ci := s.Status == fleet.SignatureVerified && slices.ContainsFunc(p.Signers, func(v fleet.VerifiedSigner) bool {
		return v.Subject == s.Signer && v.Issuer == s.Issuer && v.Class == string(core.ClassCI)
	})
	return !b.declaration() && (s.Status == fleet.SignatureUnsigned || ci)
}

// ownerChange reports whether a claim changes the reference owner current.
// A bundle that claims no owner removes one only when it is a declaration
// and the current owner is declared.
func ownerChange(current, proposed core.OwnerClaim, claims, declaration bool) bool {
	if !claims {
		return declaration && current.Provenance == fleet.OwnerProvenanceDeclared
	}
	return !proposed.Owner.Equal(current.Owner)
}

// hasTLog reports whether tm names a transparency log to check.
func hasTLog(tm root.TrustedMaterial) bool {
	return tm != nil && len(tm.RekorLogs()) > 0
}

// repoOf is ref without its tag or digest.
func repoOf(ref string) string {
	ref = contract.TrimOCIScheme(ref)
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}
