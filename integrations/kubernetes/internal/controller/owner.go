/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	Rules []core.PublisherRule
	// DeliveryPolicy reads the verified signed delivery policy
	// (--publishers-policy); nil when none is configured. Its publisher rules
	// replace Rules and its signer class rules classify signers, as the fleet
	// does. One that does not verify accepts no CI identity and allows no
	// owner change (unverified, judged again).
	DeliveryPolicy func(ctx context.Context) (*core.PublisherPolicy, string)
	Registry       func(auth *authn.AuthConfig) publish.Registry
	Now            func() time.Time

	// classes, source and unread are what resolved read from DeliveryPolicy.
	classes []core.SignerClassRule
	source  string
	unread  string
}

// resolved is oc with the delivery policy in force: its rules and signer
// classes, or, when it does not verify, no rule and why.
func (oc *OwnerCheck) resolved(ctx context.Context) *OwnerCheck {
	if oc.DeliveryPolicy == nil {
		return oc
	}
	c := *oc
	pol, why := oc.DeliveryPolicy(ctx)
	if pol == nil {
		c.Rules, c.unread = nil, cmp.Or(why, "the delivery policy could not be verified")
		return &c
	}
	c.Rules, c.classes, c.source = pol.Publishers, pol.Signers, core.PublisherSourcePolicy
	return &c
}

// ownerJudgedPrefix starts the tail of the OwnerVerified message that names
// the bundle judged, the revision it was judged against and the policy it
// was judged under, so the next reconcile keeps the verdict of an unchanged
// bundle and walks past a claim that did not count.
const ownerJudgedPrefix = " [bundle "

// verifyOwner sets OwnerVerified for a Pacto whose OCI bundle changes the
// owner of the revision the CR ran before (prev), and reports whether the
// bundle's owner may be shown. An unsigned bundle without declaration keys,
// or a CI identity's publish, is outside the rule (registry write is the
// trust boundary) and never reads its reference. The reference is never the
// baseline a record names: it is the previously run revision, or, when that
// one's owner claim was refused or unverified, the reference that one was
// judged against. With no previous revision, or one whose reference could
// not be read, the owner before the bundle is unknown: a refusal judged
// against it reads unverified. The condition is absent for every other
// Pacto.
//
// ponytail: a verdict is kept while the digest and the policy are unchanged,
// a transient unknown (a root or registry read that failed) is judged again
// on every reconcile. Memoise per digest in the loader cache if many Pactos
// share one bundle.
func (r *PactoReconciler) verifyOwner(ctx context.Context, pacto *pactov1alpha1.Pacto, ec *effectiveContract, prev *pactov1alpha1.PactoStatus) bool {
	oc, lr := r.Owners, ec.loaded
	if oc == nil || lr.ResolvedDigest == "" {
		meta.RemoveStatusCondition(&pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
		return true
	}
	oc = oc.resolved(ctx)
	fp := oc.fingerprint()
	ref, judge := ownerReference(prev, lr.ResolvedDigest, fp)
	if !judge {
		if c := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified); c != nil && judged(c).bundle == lr.ResolvedDigest {
			return c.Status == metav1.ConditionTrue
		}
		meta.RemoveStatusCondition(&pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
		return true
	}
	tm, rootErr := oc.Root()
	reg := oc.Registry(ec.auth)
	cur := oc.bundle(ctx, reg, tm, lr)
	if cur.outsideRule() {
		meta.RemoveStatusCondition(&pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
		return true
	}
	tail := ownerTail{bundle: lr.ResolvedDigest, ref: ref, policy: fp}
	current, err := r.referenceClaim(ctx, oc, reg, tm, ref, ec.auth)
	if err != nil {
		tail.unread = true
		r.setOwnerVerified(pacto, metav1.ConditionUnknown, pactov1alpha1.ReasonOwnerSignerUnverified,
			"the owner of the revision this one replaces is unknown: "+err.Error(), tail)
		return false
	}
	proposed, claims := cur.claim()
	if !ownerChange(current, proposed, claims, cur.declaration()) {
		meta.RemoveStatusCondition(&pacto.Status.Conditions, pactov1alpha1.ConditionOwnerVerified)
		return true
	}
	v, kept := oc.verdict(ctx, reg, tm, cur, ref, current, proposed)
	msg := v.Message
	if rootErr != nil {
		msg += " (the Sigstore trusted root could not be fetched: " + rootErr.Error() + ")"
	}
	status, reason := ownerVerdict(v)
	tail.kept = status != metav1.ConditionUnknown || kept
	r.setOwnerVerified(pacto, status, reason, msg, tail)
	return v.Allowed
}

// verdict judges cur's owner change over current from its certificates. A
// refusal judged against an unknown reference (ref empty), or a CI signer
// no installation rule judged, reads unverified. kept says an unknown
// verdict stays while the bundle and the policy do: only a transient one
// (a root or a signature read that failed) is judged again.
func (oc *OwnerCheck) verdict(ctx context.Context, reg publish.Registry, tm root.TrustedMaterial, cur ownerBundle, ref string, current, proposed core.OwnerClaim) (core.OwnerRight, bool) {
	service := cur.rev.Service
	v := publish.VerifyOwnerRight(ctx, publish.OwnerRightInput{Registry: reg, Root: tm, TLog: hasTLog(tm), Repo: cur.repo, Digest: cur.digest,
		Service: service, Proposed: proposed.Owner, Current: current, Policy: oc.Policy, PolicyFrom: oc.PolicyFrom, ClassRules: oc.classes, Now: oc.Now()})
	refused := v.Code == core.OwnerSignerNotPerson || v.Code == core.OwnerSignerNotAllowed
	switch {
	case v.Allowed && oc.unread != "":
		// Who is a person is unknown until the policy's signer classes are read.
		v.Allowed, v.Grant = false, ""
		v.Code, v.Message = core.OwnerSignerUnverified, "the owner this revision of "+service+" sets is unverified: "+oc.unread
		return v, false
	case v.Code == core.OwnerSignerNotPerson && fleet.CIUndecided(cur.rev.Provenance, len(oc.Rules) > 0):
		v.Code, v.Message = core.OwnerSignerUnverified, "the owner this revision of "+service+" sets is unverified: no installation publisher rule says whether its CI signer may publish it"
	case ref == "" && refused:
		v.Code, v.Message = core.OwnerSignerUnverified, "the owner this revision of "+service+" sets is unverified: the owner before it was not read"
	default:
		return v, v.Code == core.OwnerPolicyUnverified
	}
	return v, true
}

// fingerprint names what a verdict was judged under: the owner policy and
// the publisher rules. A helm upgrade that changes either judges a kept
// verdict again.
func (oc *OwnerCheck) fingerprint() string {
	b, _ := json.Marshal(struct {
		Policy  *core.OwnerPolicy
		Rules   []core.PublisherRule
		Classes []core.SignerClassRule `json:",omitempty"`
	}{oc.Policy, oc.Rules, oc.classes})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:6])
}

// ownerReference is the revision (repository@digest) the bundle digest is
// judged against, empty when the owner before it is unknown, and false when
// there is nothing to judge: digest kept its verdict under the policy fp, or
// ran before without one.
func ownerReference(prev *pactov1alpha1.PactoStatus, digest, fp string) (string, bool) {
	c := meta.FindStatusCondition(prev.Conditions, pactov1alpha1.ConditionOwnerVerified)
	var t ownerTail
	if c != nil {
		t = judged(c)
	}
	if t.bundle == digest {
		return t.ref, !t.kept || t.policy != fp
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
	case t.bundle == ran && c.Status != metav1.ConditionTrue && t.unread:
		return "", true // the reference before could not be read: unknown, never carried
	case t.bundle == ran && c.Status != metav1.ConditionTrue:
		return t.ref, true // the claim before did not count: its own reference stays
	}
	return repoOf(prev.Contract.ResolvedRef) + "@" + ran, true
}

// ownerTail is what the OwnerVerified message's tail records: the bundle
// judged, the reference ("" for unknown), the policy fingerprint, whether
// the verdict is kept while both are unchanged and whether the reference
// could not be read.
type ownerTail struct {
	bundle, ref, policy string
	kept, unread        bool
}

// judged reads the tail setOwnerVerified writes; a condition without one
// names no bundle.
func judged(c *metav1.Condition) ownerTail {
	i := strings.LastIndex(c.Message, ownerJudgedPrefix)
	if i < 0 {
		return ownerTail{}
	}
	parts := strings.Split(strings.TrimSuffix(c.Message[i+len(ownerJudgedPrefix):], "]"), ", ")
	t := ownerTail{bundle: parts[0]}
	for _, p := range parts[1:] {
		switch {
		case strings.HasPrefix(p, "judged against "):
			t.ref = strings.TrimPrefix(p, "judged against ")
		case strings.HasPrefix(p, "policy "):
			t.policy = strings.TrimPrefix(p, "policy ")
		case p == "kept":
			t.kept = true
		case p == "unread":
			t.unread = true
		}
	}
	if t.ref == "unknown" {
		t.ref = ""
	}
	return t
}

func (r *PactoReconciler) setOwnerVerified(pacto *pactov1alpha1.Pacto, status metav1.ConditionStatus, reason, msg string, t ownerTail) {
	tail := ownerJudgedPrefix + t.bundle + ", judged against " + cmp.Or(t.ref, "unknown") + ", policy " + t.policy
	if t.kept {
		tail += ", kept"
	}
	if t.unread {
		tail += ", unread"
	}
	r.setCondition(pacto, pactov1alpha1.ConditionOwnerVerified, status, reason, msg+tail+"]")
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
	ruleSource := cmp.Or(oc.source, core.PublisherSourceRule)
	if len(oc.Rules) == 0 {
		ruleSource = core.PublisherSourceInferred // no rule: never an accepted signer, never attested
	}
	v, signers, commit := publish.CheckPublisher(ctx, reg, tm, hasTLog(tm), repo, lr.ResolvedDigest,
		core.ServiceRef{Name: lr.Contract.Service.Name}, "", oc.Rules, ruleSource, nil, nil)
	ids := make([]fleet.VerifiedSigner, 0, len(signers))
	for _, s := range signers {
		s = core.Classified(s, oc.classes)
		ids = append(ids, fleet.VerifiedSigner{Subject: s.Subject, Issuer: s.Issuer, Class: string(s.Class)})
	}
	return ownerBundle{repo: repo, digest: lr.ResolvedDigest, rev: fleet.ContractRevision{Service: lr.Contract.Service.Name, Owner: lr.Contract.Service.Owner, Record: rec,
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
// declaration, and unsigned or a CI identity's publish (fleet.CIPublished).
func (b ownerBundle) outsideRule() bool {
	return !b.declaration() && (b.rev.Provenance.Signature.Status == fleet.SignatureUnsigned || fleet.CIPublished(b.rev.Provenance))
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
