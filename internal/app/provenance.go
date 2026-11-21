package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// provenanceRegistry is the registry access a provenance check needs.
type provenanceRegistry interface {
	publish.Registry
	ReferrerDescriptors(ctx context.Context, repo, digest string) ([]v1.Descriptor, error)
}

// How long a check is remembered, and how long one registry call may take.
const (
	provenanceTTL         = 5 * time.Minute
	provenanceFailTTL     = time.Minute
	provenanceCallTimeout = 10 * time.Second
	provenanceMaxReason   = 256
)

// sourceRelease is the BundleSignature source of a check against the Pacto
// release signer.
const sourceRelease = "release"

// Copy the dashboard shows when a check could not run.
const (
	reasonBudgetSpent   = "The registry checks for this refresh ran out of time; Pacto checks again on the next refresh."
	reasonNoDigest      = "This revision has no immutable digest, so nothing attached to it can be listed."
	reasonNoBundles     = "No Sigstore signature referrer is attached to this digest. Legacy cosign .sig tags are not read."
	reasonPartial       = "The registry paginates the referrers of this digest and Pacto read only the first page, so the list is incomplete."
	reasonWriteOnceTags = "Read from write-once pacto-sig tags: this registry serves no Referrers API."
)

type provenanceEntry struct {
	p     *fleet.BundleProvenance
	until time.Time
}

// ProvenanceChecker checks the signature and lists the OCI referrers of a
// revision's bundle digest, through the verification publish uses. Who signed
// is judged against a publisher rule, never against the signer the revision's
// own contract names in publish.sign. Every outcome is remembered for a while,
// so a refresh every 30 seconds does not ask the registry every time.
type ProvenanceChecker struct {
	reg provenanceRegistry
	now func() time.Time
	// mu guards memo and the root fields only; network calls run outside it.
	mu      sync.Mutex
	memo    map[string]provenanceEntry
	root    root.TrustedMaterial
	rootErr error
	rootAt  time.Time
	trust   *installTrust

	// Trust reads what the installation trusts (Delivery.InstallationTrust):
	// its publisher trust config and a reader of a service's pins. nil, or a
	// config it cannot read, judges against the rule inferred from the
	// revision's own registry, which never reads verified.
	Trust func(ctx context.Context) (Installation, error)
}

// NewProvenanceChecker returns a checker over the CLI's registry credentials
// and plain-HTTP hosts, as publish uses them.
func NewProvenanceChecker() *ProvenanceChecker {
	return newProvenanceChecker(publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries()...))
}

func newProvenanceChecker(reg provenanceRegistry) *ProvenanceChecker {
	return &ProvenanceChecker{reg: reg, now: time.Now, memo: map[string]provenanceEntry{}}
}

// signatureJudge decides what can be said about the signatures of
// repo@digest under the trusted root tm, starting from sig, and lists every
// identity whose signature verified (none when it read no signer).
type signatureJudge func(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature) (fleet.BundleSignature, []fleet.VerifiedSigner)

// deriveRule is the publisher rule inferred from where an artifact lives
// (delivery.DeriveRule); tests swap it for a registry host it knows.
var deriveRule = delivery.DeriveRule

// Check implements fleetsrc.ProvenanceFunc. It never fails: what it cannot
// read or check is unknown, with the reason. With an installation config
// (Trust) the signer is judged against the promoter's rule, verified policy
// and the service's pins; without one the rule is inferred from ref's own
// registry and organization, and an inferred accept is never verified.
func (p *ProvenanceChecker) Check(ctx context.Context, source, ref string, c *contract.Contract) *fleet.BundleProvenance {
	var service delivery.ServiceRef
	if c != nil {
		service.Name = c.Service.Name
	}
	return p.check(ctx, source, ref, func(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature) (fleet.BundleSignature, []fleet.VerifiedSigner) {
		return p.judgeSignature(ctx, repo, digest, tm, sig, service)
	})
}

// CheckSigner is Check against one fixed keyless identity and issuer, the
// Pacto release signer of the default external catalog: a match is verified
// with source release.
func (p *ProvenanceChecker) CheckSigner(ctx context.Context, source, ref, identity, issuer string) *fleet.BundleProvenance {
	return p.check(ctx, source, ref, func(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature) (fleet.BundleSignature, []fleet.VerifiedSigner) {
		return p.verifySigner(ctx, repo, digest, tm, sig, identity, issuer), nil
	})
}

func (p *ProvenanceChecker) check(ctx context.Context, source, ref string, judge signatureJudge) *fleet.BundleProvenance {
	if hit := p.recall(ref); hit != nil {
		return hit
	}
	if ctx.Err() != nil {
		res := p.result(source)
		res.Signature.Reason, res.ReferrersUnavailable = reasonBudgetSpent, reasonBudgetSpent
		return res // the next refresh checks again
	}
	res := p.compute(ctx, source, ref, judge)
	p.remember(ref, res)
	return cloneProvenance(res)
}

// result is an unknown outcome checked now by source.
func (p *ProvenanceChecker) result(source string) *fleet.BundleProvenance {
	return &fleet.BundleProvenance{
		Signature: fleet.BundleSignature{Status: fleet.SignatureUnknown},
		Referrers: fleet.NewReferrersPreview(nil),
		Source:    source, CheckedAt: p.now(),
	}
}

func (p *ProvenanceChecker) compute(ctx context.Context, source, ref string, judge signatureJudge) *fleet.BundleProvenance {
	res := p.result(source)
	repo, digest, ok := strings.Cut(contract.TrimOCIScheme(ref), "@")
	if _, err := v1.NewHash(digest); !ok || err != nil {
		res.Signature.Reason, res.ReferrersUnavailable = reasonNoDigest, reasonNoDigest
		return res
	}
	refs, tagged, err := p.listReferrers(ctx, repo, digest)
	partial := errors.Is(err, publish.ErrReferrersPartial)
	if err != nil && !partial {
		why := capReason("The registry could not list what is attached to this digest: " + err.Error())
		res.Signature.Reason, res.ReferrersUnavailable = why, why
		return res
	}
	res.Referrers = fleet.NewReferrersPreview(refs)
	bundles := 0
	for _, r := range refs {
		if r.ArtifactType == delivery.BundleMediaType {
			bundles++
		}
	}
	res.Signature, res.Signers = p.signature(ctx, repo, digest, bundles, judge)
	if tagged {
		// A fixed prefix: the capped reason after it keeps its own bound.
		res.Signature.Reason = reasonWriteOnceTags + " " + res.Signature.Reason
	}
	if partial {
		// A signature that verified on the first page stands; anything else may
		// change on a page Pacto did not read.
		res.Referrers.Truncated, res.ReferrersUnavailable = true, reasonPartial
		if res.Signature.Status != fleet.SignatureVerified {
			res.Signature = fleet.BundleSignature{Status: fleet.SignatureUnknown, Reason: reasonPartial}
		}
	}
	return res
}

// listReferrers lists every referrer of repo@digest and says whether they
// were read from write-once tags.
func (p *ProvenanceChecker) listReferrers(ctx context.Context, repo, digest string) ([]fleet.OCIReferrer, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, provenanceCallTimeout)
	defer cancel()
	descs, err := p.reg.ReferrerDescriptors(cctx, repo, digest)
	if err != nil && !errors.Is(err, publish.ErrReferrersPartial) {
		return nil, false, err
	}
	// The listing probed the registry already, so this is the remembered answer.
	mode, _ := p.reg.Mode(cctx, repo, digest)
	out := make([]fleet.OCIReferrer, 0, len(descs))
	for _, d := range descs {
		r := fleet.OCIReferrer{ArtifactType: d.ArtifactType, Digest: d.Digest.String()}
		if r.ArtifactType == "" {
			r.ArtifactType = string(d.MediaType)
		}
		if t, err := time.Parse(time.RFC3339, d.Annotations["org.opencontainers.image.created"]); err == nil {
			r.Created = &t
		}
		out = append(out, r)
	}
	return out, mode == capabilities.ReferrersWriteOnceTags, err
}

// signature is what can be said about the bundles attached to repo@digest:
// unsigned with none, unknown without Sigstore's trusted root, else judge's
// answer within one registry call's time.
func (p *ProvenanceChecker) signature(ctx context.Context, repo, digest string, bundles int, judge signatureJudge) (fleet.BundleSignature, []fleet.VerifiedSigner) {
	sig := fleet.BundleSignature{Status: fleet.SignatureUnknown, Source: delivery.PublisherSourceNone, Bundles: bundles}
	if bundles == 0 {
		sig.Status, sig.Reason = fleet.SignatureUnsigned, reasonNoBundles
		return sig, nil
	}
	tm, err := p.trustedRoot()
	if err != nil {
		sig.Reason = capReason("Sigstore's trusted root could not be fetched, so the signature could not be checked: " + err.Error())
		return sig, nil
	}
	cctx, cancel := context.WithTimeout(ctx, provenanceCallTimeout)
	defer cancel()
	return judge(cctx, repo, digest, tm, sig)
}

// judgeSignature judges who signed repo@digest: against the installation's
// rule, verified policy and the service's pins when Trust reads them, else
// against the rule inferred from repo's registry and organization, whose
// accept reads unknown, never verified. A part of the installation's trust
// that could not be applied is named in the reason. The signers it read are
// returned too, from the same registry read.
func (p *ProvenanceChecker) judgeSignature(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature, service delivery.ServiceRef) (fleet.BundleSignature, []fleet.VerifiedSigner) {
	t := p.installation(ctx)
	notes := []string{t.note}
	if t.inferred {
		rule, why := deriveRule(repo, "main")
		t = &installTrust{source: delivery.PublisherSourceInferred, expect: rule, operator: t.operator}
		if why == "" {
			t.rules = []delivery.PublisherRule{rule}
		}
	}
	service.Domain = contract.OCIDomain(repo)
	pins, why, cut := t.pinsOf(ctx, service)
	v, signers, commit := publish.CheckPublisher(ctx, p.reg, tm, rekorURL != "", repo, digest, service, "", t.rules, t.source, pins, t.operator)
	if len(signers) == 0 && v.Verdict == delivery.VerdictUnknown {
		// No keyless signer verified: the operator's key may be the only one.
		if id, ok := p.operatorKeySigned(ctx, t.operator, repo, digest); ok {
			v = delivery.OperatorRefusal(id)
		}
	}
	sig = signatureOf(v, t.expect, commit, sig)
	switch {
	case v.Source == delivery.PublisherSourcePin:
		sig.Reason = capReason(pinReason(v, service, pins))
	case cut && sig.Status == fleet.SignatureUnverified && sig.Source != fleet.SignatureSourceOperator:
		sig.Status = fleet.SignatureUnknown // the pin that covers it may be one not read
	}
	if t.unguarded != "" && sig.Status == fleet.SignatureVerified && sig.Source != delivery.PublisherSourceRule {
		sig.Status = fleet.SignatureUnknown // the promoter may have moved past this policy or pin
		sig.SignerRepository, sig.SignerCommit = "", ""
		notes = append(notes, t.unguarded)
	}
	if note := strings.Join(slices.DeleteFunc(append(notes, why), func(s string) bool { return s == "" }), "; "); note != "" {
		// Capped apart, so a long verdict never hides what was not applied.
		sig.Reason = strings.TrimSuffix(sig.Reason, ".") + ". " + capReason(upperFirst(note)+".")
	}
	return sig, verifiedSigners(signers, t.classes)
}

// operatorKeySigned reports whether the installation operator's key k
// signed repo@digest, naming it by its key hint. A keyless operator signer is
// read with the other signers (delivery.JudgeSigners), so only a key is
// checked here.
func (p *ProvenanceChecker) operatorKeySigned(ctx context.Context, k *delivery.TrustedKey, repo, digest string) (delivery.PublisherIdentity, bool) {
	if k == nil {
		return delivery.PublisherIdentity{}, false
	}
	pol, err := signing.PolicyOf(*k)
	if err != nil || pol.Key == nil {
		return delivery.PublisherIdentity{}, false
	}
	if publish.VerifyDigest(ctx, p.reg, publish.PolicyVerifier(pol), repo, digest) != nil {
		return delivery.PublisherIdentity{}, false
	}
	return delivery.PublisherIdentity{Subject: signing.Hint(pol.Key)}, true
}

// verifiedSigners is ids as the fleet lists them: classified by the verified
// installation policy's signer rules (else the default rule), sorted by
// issuer, subject and class, without duplicates. The fleet bounds the list
// once it has matched a declaration's signer against all of it.
func verifiedSigners(ids []delivery.PublisherIdentity, classes []delivery.SignerClassRule) []fleet.VerifiedSigner {
	var out []fleet.VerifiedSigner
	for _, id := range ids {
		id = delivery.Classified(id, classes)
		out = append(out, fleet.VerifiedSigner{Subject: id.Subject, Issuer: id.Issuer, Class: string(id.Class)})
	}
	slices.SortFunc(out, func(a, b fleet.VerifiedSigner) int {
		return cmp.Or(strings.Compare(a.Issuer, b.Issuer), strings.Compare(a.Subject, b.Subject), strings.Compare(a.Class, b.Class))
	})
	return slices.Compact(out)
}

// installTrust is what a signature is judged against: rules from source,
// the rule a signer no rule covers is shown against (expect) and a reader of
// a service's pins. note names a part of the installation's trust that was
// not applied; inferred means no installation config was read.
type installTrust struct {
	rules []delivery.PublisherRule
	// classes are the verified policy's signer class rules, nil without one.
	classes []delivery.SignerClassRule
	source  string
	expect  delivery.PublisherRule
	pins    PinsFunc
	note    string
	// unguarded says why the promoter's high-water marks were not read, so
	// a policy or pin accept reads unknown.
	unguarded string
	// operator is the installation operator's signer, which never makes a
	// publisher; nil when no advertisement was read.
	operator *delivery.TrustedKey
	inferred bool
	failed   bool
	until    time.Time
	// declare is the installation signer policy declarations are judged
	// with, nil when no installation trust was read.
	declare *fleet.DeclarationSignerPolicy
}

// DeclarationSigners is the installation signer policy a declared
// revision's signer is judged with, read with the installation's trust and
// remembered with it; nil when no Trust reader is set.
func (p *ProvenanceChecker) DeclarationSigners(ctx context.Context) *fleet.DeclarationSignerPolicy {
	return p.installation(ctx).declare
}

// installation is what the installation trusts, read with Trust and
// remembered for provenanceTTL, or for provenanceFailTTL when a part of it
// could not be read.
func (p *ProvenanceChecker) installation(ctx context.Context) *installTrust {
	if p.Trust == nil {
		return &installTrust{inferred: true}
	}
	p.mu.Lock()
	t := p.trust
	p.mu.Unlock()
	if t != nil && p.now().Before(t.until) {
		return t
	}
	t = p.readInstallation(ctx)
	t.until = p.now().Add(ttlFor(!t.failed))
	p.mu.Lock()
	p.trust = t
	p.mu.Unlock()
	return t
}

// readInstallation reads the installation's trust as the promoter judges
// with it: a configured policy's rules when its tag's digest verifies with
// the policy signers, nothing but pins when it does not, else the rule
// derived from the configured registry.
func (p *ProvenanceChecker) readInstallation(ctx context.Context) *installTrust {
	in, err := p.Trust(ctx)
	if errors.Is(err, ErrNoInstallationTrust) {
		return &installTrust{inferred: true, operator: in.Config.InferredSigner}
	}
	if err != nil {
		return &installTrust{inferred: true, failed: true, operator: in.Config.InferredSigner,
			note:    "the installation's publisher trust config could not be read (" + err.Error() + "), so the rule was inferred from where the revision lives",
			declare: &fleet.DeclarationSignerPolicy{Unread: err.Error()}}
	}
	cfg := in.Config
	t := &installTrust{pins: in.Pins, source: delivery.PublisherSourceRule, unguarded: in.Unguarded, failed: in.Unguarded != "", operator: cfg.InferredSigner,
		declare: &fleet.DeclarationSignerPolicy{Unread: in.SignersUnread}}
	for _, s := range in.Signers {
		t.declare.Signers = append(t.declare.Signers, fleet.PolicySigner{Issuer: s.Issuer, Subjects: s.Subjects})
	}
	if cfg.Policy != "" {
		pol, err := verifiedPolicy(ctx, p.reg, cfg, "")
		if in.PolicyDigest != "" && (err != nil || rfcTime(in.PolicyAt).After(rfcTime(pol.At))) {
			// The promoter keeps the newest policy it verified: a tag moved
			// back, or one that no longer verifies, never replaces it.
			if kept, kerr := verifiedPolicy(ctx, p.reg, cfg, in.PolicyDigest); kerr == nil {
				pol, err = kept, nil
			}
		}
		if err != nil {
			t.failed, t.note = true, "the delivery policy "+cfg.Policy+" could not be verified ("+err.Error()+"), so only pinned signers are accepted"
			return t
		}
		t.rules, t.classes, t.source = pol.Publishers, pol.Signers, delivery.PublisherSourcePolicy
		return t
	}
	rule, why := cfg.Rule()
	if why != "" {
		t.note = why
		return t
	}
	t.rules, t.expect = []delivery.PublisherRule{rule}, rule
	return t
}

// policyReader reads a delivery policy from a registry.
type policyReader interface {
	Head(ctx context.Context, ref string) (*publish.Remote, error)
	Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error)
}

// verifiedPolicy reads the delivery policy cfg names, or its digest when one
// is given, from reg and verifies it with cfg's policy signers.
func verifiedPolicy(ctx context.Context, reg policyReader, cfg delivery.PublisherConfig, digest string) (delivery.PublisherPolicy, error) {
	verify, err := delivery.PolicyVerifier([]byte(cfg.PolicySigners), signing.OpenKey)
	if err != nil {
		return delivery.PublisherPolicy{}, err
	}
	ref, err := name.ParseReference(cfg.Policy)
	if err != nil {
		return delivery.PublisherPolicy{}, err
	}
	if digest == "" {
		h, err := reg.Head(ctx, cfg.Policy)
		if err == nil && h == nil {
			err = errors.New("it does not exist")
		}
		if err != nil {
			return delivery.PublisherPolicy{}, err
		}
		digest = h.Digest
	}
	bundle, _, err := reg.Layer(ctx, ref.Context().String(), digest)
	if err != nil {
		return delivery.PublisherPolicy{}, err
	}
	return delivery.OpenPolicy(bundle, verify)
}

// pinsOf is the pins of service, and of the same-named service of a Stage
// that names no domain, with what of them could not be read (cut says only
// part of a listing was read, so a signer no pin read covers is unknown).
func (t *installTrust) pinsOf(ctx context.Context, service delivery.ServiceRef) (out []delivery.IdentityPin, why string, cut bool) {
	if t.pins == nil {
		return nil, "", false
	}
	services := []delivery.ServiceRef{service}
	if service.Domain != "" {
		services = append(services, delivery.ServiceRef{Name: service.Name})
	}
	var notes []string
	for _, s := range services {
		pins, cutNote, dropped, err := t.pins(ctx, s)
		if err != nil {
			return nil, "the pins of " + service.Name + " could not be read (" + err.Error() + "), so no pinned signer is accepted", false
		}
		if cutNote != "" {
			notes, cut = append(notes, cutNote+", so a signer no pin read covers is unknown"), true
		}
		if dropped != "" {
			notes = append(notes, dropped)
		}
		for _, pin := range pins {
			pin.Service = service // an unscoped Stage's pin covers every same-named service
			out = append(out, pin)
		}
	}
	return out, strings.Join(notes, "; "), cut
}

// pinReason is why a signer a pin accepted is verified, naming no person:
// who confirmed it is shown only to those who may list the service's Stages
// (the delivery trust view).
func pinReason(v delivery.PublisherVerdict, service delivery.ServiceRef, pins []delivery.IdentityPin) string {
	i := slices.IndexFunc(pins, func(p delivery.IdentityPin) bool {
		return p.Issuer == v.Identity.Issuer && p.Subject == v.Identity.Subject
	})
	return fmt.Sprintf("Signed by %s (%s), an identity a person pinned for %s in namespace %s.", v.Identity.Subject, v.Identity.Issuer, service.Name, pins[i].Namespace)
}

// signatureOf maps a publisher verdict onto the fleet's signature statuses:
// only a rule, pin or policy accept is verified; an inferred accept is
// unknown; a signer no rule covers is unverified.
func signatureOf(v delivery.PublisherVerdict, rule delivery.PublisherRule, commit string, base fleet.BundleSignature) fleet.BundleSignature {
	sig := signing.BundleSignatureFor(v, commit)
	sig.Bundles = base.Bundles
	switch v.Verdict {
	case delivery.VerdictAccepted:
		if v.Source == delivery.PublisherSourceInferred {
			sig.Reason = capReason(fmt.Sprintf("Signed by %s, a CI workflow the rule %s covers; no installation rule was read, so it is not verified.", sig.Signer, rule.Name))
			return sig
		}
	case delivery.VerdictNewSigner, delivery.VerdictRefused:
		sig.ExpectedSigner, sig.ExpectedIssuer = rule.Subject, rule.Issuer
	case delivery.VerdictUnsigned:
		v.Reason = noSignatureReason(sig.Bundles)
	}
	sig.Reason = capReason(upperFirst(v.Reason))
	return sig
}

// noSignatureReason is why bundles attached bundles hold no signature.
func noSignatureReason(bundles int) string {
	return fmt.Sprintf("%d Sigstore bundles are attached; none of them is a signature.", bundles)
}

// upperFirst starts s with a capital letter, as the dashboard's reasons do.
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// verifySigner verifies the bundles against one fixed keyless identity and
// issuer, under public-good Sigstore.
func (p *ProvenanceChecker) verifySigner(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature, identity, issuer string) fleet.BundleSignature {
	err := publish.VerifyDigest(ctx, p.reg, publish.PolicyVerifier(signing.Policy{Identity: identity, Issuer: issuer, Root: tm, Tlog: rekorURL != ""}), repo, digest)
	switch {
	case err == nil:
		sig.Status, sig.Source, sig.Signer, sig.Issuer = fleet.SignatureVerified, sourceRelease, identity, issuer
		sig.Reason = capReason(fmt.Sprintf("Signed by %s (issuer %s), the Pacto release signer.", identity, issuer))
	case errors.Is(err, publish.ErrNoSignature):
		sig.Status, sig.Reason = fleet.SignatureUnsigned, noSignatureReason(sig.Bundles)
	case errors.Is(err, publish.ErrSignatureInvalid):
		// Who signed it is not known: the fixed identity is only what was expected.
		sig.Status, sig.ExpectedSigner, sig.ExpectedIssuer = fleet.SignatureUnverified, identity, issuer
		sig.Reason = capReason(fmt.Sprintf("A signature is attached but it does not verify against %s (issuer %s): %v", identity, issuer, err))
	default:
		sig.Reason = capReason("The signature could not be checked: " + err.Error())
	}
	return sig
}

// trustedRoot is public-good Sigstore's trusted root, remembered for
// provenanceTTL, or a failure to fetch it for provenanceFailTTL.
func (p *ProvenanceChecker) trustedRoot() (root.TrustedMaterial, error) {
	p.mu.Lock()
	tm, err, at := p.root, p.rootErr, p.rootAt
	p.mu.Unlock()
	if !at.IsZero() && p.now().Before(at.Add(ttlFor(err == nil))) {
		return tm, err
	}
	tm, err = publicGoodRoot(signing.PublicTUF)
	p.mu.Lock()
	p.root, p.rootErr, p.rootAt = tm, err, p.now()
	p.mu.Unlock()
	return tm, err
}

// recall returns a clone of the remembered check of ref, or nil.
func (p *ProvenanceChecker) recall(ref string) *fleet.BundleProvenance {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.memo[ref]
	if !ok || !p.now().Before(e.until) {
		return nil
	}
	return cloneProvenance(e.p)
}

// remember keeps res for ref and drops expired checks, so the memo holds
// at most the refs checked within the last provenanceTTL.
func (p *ProvenanceChecker) remember(ref string, res *fleet.BundleProvenance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for k, e := range p.memo {
		if !now.Before(e.until) {
			delete(p.memo, k)
		}
	}
	p.memo[ref] = provenanceEntry{p: cloneProvenance(res), until: now.Add(ttlFor(res.Signature.Status != fleet.SignatureUnknown))}
}

func ttlFor(ok bool) time.Duration {
	if ok {
		return provenanceTTL
	}
	return provenanceFailTTL
}

// cloneProvenance copies p, its referrer list and their times.
func cloneProvenance(p *fleet.BundleProvenance) *fleet.BundleProvenance {
	c := *p
	c.Signers = slices.Clone(p.Signers)
	c.Referrers = fleet.NewReferrersPreview(p.Referrers.Items)
	c.Referrers.Total, c.Referrers.Truncated = p.Referrers.Total, p.Referrers.Truncated
	return &c
}

// capReason bounds copy that embeds a registry or verifier error.
func capReason(s string) string {
	if r := []rune(s); len(r) > provenanceMaxReason {
		return string(r[:provenanceMaxReason]) + "…"
	}
	return s
}
