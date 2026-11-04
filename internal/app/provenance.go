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

// Copy the dashboard shows when a check could not run.
const (
	reasonBudgetSpent = "The registry checks for this refresh ran out of time; Pacto checks again on the next refresh."
	reasonNoDigest    = "This revision has no immutable digest, so nothing attached to it can be listed."
	reasonNoBundles   = "No Sigstore signature referrer is attached to this digest. Legacy cosign .sig tags are not read."
	reasonKeySigner   = "The contract signs with a key file or a KMS key. The bundle carries no public key and the dashboard does not call a contract's KMS, so the signature could not be checked."
	reasonNoIdentity  = "The contract signs keyless but names no identity and issuer to check against."
)

type provenanceEntry struct {
	p     *fleet.BundleProvenance
	until time.Time
}

// ProvenanceChecker checks the signature and lists the OCI referrers of a
// revision's bundle digest, through the verification publish uses. The signer
// it checks against is the one the revision's own contract declares in
// publish.sign. Every outcome is remembered for a while, so a refresh every 30
// seconds does not ask the registry every time.
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

// Check implements fleetsrc.ProvenanceFunc. It never fails: what it cannot
// read or check is unknown, with the reason.
func (p *ProvenanceChecker) Check(ctx context.Context, source, ref string, c *contract.Contract) *fleet.BundleProvenance {
	if hit := p.recall(ref); hit != nil {
		return hit
	}
	if ctx.Err() != nil {
		res := p.result(source)
		res.Signature.Reason, res.ReferrersUnavailable = reasonBudgetSpent, reasonBudgetSpent
		return res // the next refresh checks again
	}
	res := p.compute(ctx, source, ref, c)
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

func (p *ProvenanceChecker) compute(ctx context.Context, source, ref string, c *contract.Contract) *fleet.BundleProvenance {
	res := p.result(source)
	repo, digest, ok := strings.Cut(contract.TrimOCIScheme(ref), "@")
	if _, err := v1.NewHash(digest); !ok || err != nil {
		res.Signature.Reason, res.ReferrersUnavailable = reasonNoDigest, reasonNoDigest
		return res
	}
	refs, err := p.listReferrers(ctx, repo, digest)
	if err != nil {
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
	res.Signature = p.classifySignature(ctx, repo, digest, bundles, c)
	return res
}

// listReferrers lists every referrer of repo@digest.
func (p *ProvenanceChecker) listReferrers(ctx context.Context, repo, digest string) ([]fleet.OCIReferrer, error) {
	cctx, cancel := context.WithTimeout(ctx, provenanceCallTimeout)
	defer cancel()
	descs, err := p.reg.ReferrerDescriptors(cctx, repo, digest)
	if err != nil {
		return nil, err
	}
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
	return out, nil
}

// classifySignature decides what can be said about the bundles attached to
// the digest against the signer c declares.
func (p *ProvenanceChecker) classifySignature(ctx context.Context, repo, digest string, bundles int, c *contract.Contract) fleet.BundleSignature {
	sig := fleet.BundleSignature{Status: fleet.SignatureUnknown, Bundles: bundles}
	switch {
	case bundles == 0:
		sig.Status, sig.Reason = fleet.SignatureUnsigned, reasonNoBundles
	case c == nil || c.Publish == nil || c.Publish.Sign == nil:
		sig.Reason = fmt.Sprintf("%d Sigstore bundles are attached, but the contract names no publish.sign signer to check them against.", bundles)
	case c.Publish.Sign.Key != "" || c.Publish.Sign.KMS != "":
		sig.Reason = reasonKeySigner
	case c.Publish.Sign.Identity == "" || c.Publish.Sign.Issuer == "":
		sig.Reason = reasonNoIdentity
	default:
		p.verifyKeyless(ctx, repo, digest, *c.Publish.Sign, &sig)
	}
	return sig
}

// verifyKeyless verifies the bundles against the keyless identity and issuer
// the contract declares, under public-good Sigstore.
func (p *ProvenanceChecker) verifyKeyless(ctx context.Context, repo, digest string, sign contract.PublishSign, sig *fleet.BundleSignature) {
	tm, err := p.trustedRoot()
	if err != nil {
		sig.Reason = capReason("Sigstore's trusted root could not be fetched, so the signature could not be checked: " + err.Error())
		return
	}
	cctx, cancel := context.WithTimeout(ctx, provenanceCallTimeout)
	defer cancel()
	err = publish.VerifyDigest(cctx, p.reg, bundleVerifier(signing.Policy{Identity: sign.Identity, Issuer: sign.Issuer, Root: tm, Tlog: rekorURL != ""}), repo, digest)
	switch {
	case err == nil:
		sig.Status, sig.Signer, sig.Issuer = fleet.SignatureVerified, sign.Identity, sign.Issuer
		sig.Reason = capReason(fmt.Sprintf("Signed by %s (issuer %s), the signer this contract's own publish.sign declares. Pacto checks the signature, not who may declare that signer.", sign.Identity, sign.Issuer))
	case errors.Is(err, publish.ErrNoSignature):
		sig.Status = fleet.SignatureUnsigned
		sig.Reason = fmt.Sprintf("%d Sigstore bundles are attached; none of them is a signature.", sig.Bundles)
	case errors.Is(err, publish.ErrSignatureInvalid):
		sig.Status, sig.Signer, sig.Issuer = fleet.SignatureUnverified, sign.Identity, sign.Issuer
		sig.Reason = capReason(fmt.Sprintf("A signature is attached but it does not verify against %s (issuer %s): %v", sign.Identity, sign.Issuer, err))
	default:
		sig.Reason = capReason("The signature could not be checked: " + err.Error())
	}
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
