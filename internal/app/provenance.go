package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
// repo@digest under the trusted root tm, starting from sig.
type signatureJudge func(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature) fleet.BundleSignature

// deriveRule is the publisher rule inferred from where an artifact lives
// (delivery.DeriveRule); tests swap it for a registry host it knows.
var deriveRule = delivery.DeriveRule

// Check implements fleetsrc.ProvenanceFunc. It never fails: what it cannot
// read or check is unknown, with the reason. No installation config is read
// yet, so the rule is inferred from ref's own registry and organization, and
// an inferred accept is never verified.
func (p *ProvenanceChecker) Check(ctx context.Context, source, ref string, c *contract.Contract) *fleet.BundleProvenance {
	var service delivery.ServiceRef
	if c != nil {
		service.Name = c.Service.Name
	}
	return p.check(ctx, source, ref, func(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature) fleet.BundleSignature {
		return p.judgeSignature(ctx, repo, digest, tm, sig, service)
	})
}

// CheckSigner is Check against one fixed keyless identity and issuer, the
// Pacto release signer of the default external catalog: a match is verified
// with source release.
func (p *ProvenanceChecker) CheckSigner(ctx context.Context, source, ref, identity, issuer string) *fleet.BundleProvenance {
	return p.check(ctx, source, ref, func(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature) fleet.BundleSignature {
		return p.verifySigner(ctx, repo, digest, tm, sig, identity, issuer)
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
	res.Signature = p.signature(ctx, repo, digest, bundles, judge)
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
func (p *ProvenanceChecker) signature(ctx context.Context, repo, digest string, bundles int, judge signatureJudge) fleet.BundleSignature {
	sig := fleet.BundleSignature{Status: fleet.SignatureUnknown, Source: delivery.PublisherSourceNone, Bundles: bundles}
	if bundles == 0 {
		sig.Status, sig.Reason = fleet.SignatureUnsigned, reasonNoBundles
		return sig
	}
	tm, err := p.trustedRoot()
	if err != nil {
		sig.Reason = capReason("Sigstore's trusted root could not be fetched, so the signature could not be checked: " + err.Error())
		return sig
	}
	cctx, cancel := context.WithTimeout(ctx, provenanceCallTimeout)
	defer cancel()
	return judge(cctx, repo, digest, tm, sig)
}

// judgeSignature judges who signed repo@digest against the rule inferred
// from repo's registry and organization. That rule comes from where the
// artifact lives, so its accept reads unknown, never verified.
func (p *ProvenanceChecker) judgeSignature(ctx context.Context, repo, digest string, tm root.TrustedMaterial, sig fleet.BundleSignature, service delivery.ServiceRef) fleet.BundleSignature {
	var rules []delivery.PublisherRule
	rule, why := deriveRule(repo, "main")
	if why == "" {
		rules = []delivery.PublisherRule{rule}
	}
	v := publish.CheckPublisher(ctx, p.reg, tm, rekorURL != "", repo, digest, service, "", rules, delivery.PublisherSourceInferred, nil)
	return signatureOf(v, rule, sig)
}

// signatureOf maps a publisher verdict onto the fleet's signature statuses:
// only a rule, pin or policy accept is verified; an inferred accept is
// unknown; a signer no rule covers is unverified.
func signatureOf(v delivery.PublisherVerdict, rule delivery.PublisherRule, sig fleet.BundleSignature) fleet.BundleSignature {
	if v.Identity != nil {
		sig.Signer, sig.Issuer = v.Identity.Subject, v.Identity.Issuer
	}
	switch v.Verdict {
	case delivery.VerdictAccepted:
		sig.Source = v.Source
		if v.Source == delivery.PublisherSourceInferred {
			sig.Reason = capReason(fmt.Sprintf("Signed by %s, a CI workflow the rule %s covers; no installation rule was read, so it is not verified.", sig.Signer, rule.Name))
			return sig
		}
		sig.Status = fleet.SignatureVerified
	case delivery.VerdictNewSigner, delivery.VerdictRefused:
		sig.Status, sig.ExpectedSigner, sig.ExpectedIssuer = fleet.SignatureUnverified, rule.Subject, rule.Issuer
	case delivery.VerdictUnsigned:
		sig.Status, v.Reason = fleet.SignatureUnsigned, noSignatureReason(sig.Bundles)
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
	err := publish.VerifyDigest(ctx, p.reg, bundleVerifier(signing.Policy{Identity: identity, Issuer: issuer, Root: tm, Tlog: rekorURL != ""}), repo, digest)
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
