package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// Copy the answers carry about the external catalogs.
const (
	reasonCatalogChecking  = "Pacto is checking the default catalog's signature on ghcr.io; a later refresh reports the result."
	reasonCatalogNoGHCR    = "The default catalog is checked only when a source already reads ghcr.io, so the embedded copy is used unchecked."
	reasonCatalogNoChecker = "No provenance checker is configured, so the embedded copy of the default catalog is used unchecked."
	reasonCatalogOneShot   = "A one-shot command does not check the default catalog's signature; the dashboard, pacto mcp and the TUI do."
	reasonCatalogVerified  = "Signed by the Pacto release workflow; the embedded copy in use has the digest of the signed registry artifact."
	reasonOrgCatalogSigner = "No trusted signer is configured for the organization catalog, so its signature is not read; the digest pin is what it is trusted by."
)

// catalogCheckTimeout bounds the whole background check of the default
// catalog, which makes a few registry calls of provenanceCallTimeout each.
const catalogCheckTimeout = 30 * time.Second

// externalCatalogSource names the catalogs in limitations and provenance checks.
const externalCatalogSource = "external-catalog"

// catalogLoader remembers the default catalog's signature check and the
// organization catalogs pulled, so a dashboard refreshing every 30 seconds
// asks the registry once per TTL.
type catalogLoader struct {
	mu       sync.Mutex
	def      *externalcatalog.Ref
	defUntil time.Time
	checking bool
	org      map[string]orgCatalogEntry
}

type orgCatalogEntry struct {
	data  []byte
	err   error
	until time.Time
}

// CheckExternalCatalogRef refuses an organization catalog reference that is
// not pinned by digest. An empty ref configures no organization catalog.
func CheckExternalCatalogRef(ref string) error {
	if ref == "" {
		return nil
	}
	repo, digest, ok := strings.Cut(contract.TrimOCIScheme(ref), "@")
	if _, err := v1.NewHash(digest); !ok || !strings.Contains(repo, "/") || !strings.HasPrefix(digest, "sha256:") || err != nil {
		return fmt.Errorf("external catalog %q: pin it by digest as oci://REPOSITORY@sha256:DIGEST; a tag can move, so it is refused", ref)
	}
	return nil
}

// externalCatalog assembles the catalog a build classifies external systems
// with: the embedded default, checked against ghcr.io in the background when a
// source already reads it, and the organization catalog opts names. A catalog
// that cannot be read is a limitation; only a malformed reference is an error.
func (s *Service) externalCatalog(ctx context.Context, opts FleetOptions) (*externalcatalog.Catalog, []fleet.Limitation, error) {
	if err := CheckExternalCatalogRef(opts.ExternalCatalogRef); err != nil {
		return nil, nil, err
	}
	def := s.defaultCatalogRef(opts)
	if opts.ExternalCatalogRef == "" {
		cat, _ := externalcatalog.Assemble(def, nil, externalcatalog.Ref{})
		return cat, nil, nil
	}
	ref := opts.ExternalCatalogRef
	repo, digest, _ := strings.Cut(contract.TrimOCIScheme(ref), "@")
	org := externalcatalog.Ref{
		Origin: externalcatalog.OriginOrganization, Repository: repo, Digest: digest,
		Copy: externalcatalog.CopyRegistry, Signature: fleet.SignatureUnknown, Reason: reasonOrgCatalogSigner,
	}
	data, err := s.orgCatalog(ctx, ref)
	what := "could not be read"
	var cat *externalcatalog.Catalog
	if err == nil {
		what = "is not a valid catalog"
		cat, err = externalcatalog.Assemble(def, data, org)
	}
	if err != nil {
		cat, _ = externalcatalog.Assemble(def, nil, externalcatalog.Ref{})
		return cat, []fleet.Limitation{{
			Code: fleet.LimitationExternalCatalogUnavailable, Source: externalCatalogSource,
			Message: capReason(fmt.Sprintf("The organization catalog %s %s, so only the default catalog classified external systems: %v", ref, what, err)),
		}}, nil
	}
	return cat, nil, nil
}

// defaultCatalogRef reports what is known about the embedded default catalog.
// It starts the signature check in the background, at most once per TTL, and
// never waits for it.
func (s *Service) defaultCatalogRef(opts FleetOptions) externalcatalog.Ref {
	ref := externalcatalog.DefaultRef()
	switch {
	case !readsGHCR(opts):
		ref.Reason = reasonCatalogNoGHCR
		return ref
	case opts.Provenance == nil:
		ref.Reason = reasonCatalogNoChecker
		return ref
	case !opts.LongRunning:
		ref.Reason = reasonCatalogOneShot
		return ref
	}
	now := s.env().now
	l := &s.catalogs
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.checking && (l.def == nil || !now().Before(l.defUntil)) {
		l.checking = true
		go l.checkDefault(opts.Provenance, now)
	}
	if l.def == nil {
		ref.Reason = reasonCatalogChecking
		return ref
	}
	return *l.def
}

// checkDefault checks the default catalog's signature through p, on its own
// deadline: a build that started it may have finished long ago.
func (l *catalogLoader) checkDefault(p *ProvenanceChecker, now func() time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), catalogCheckTimeout)
	defer cancel()
	res := p.CheckSigner(ctx, externalCatalogSource, contract.OCIScheme+externalcatalog.DefaultRepository+"@"+externalcatalog.DefaultDigest(),
		externalcatalog.DefaultSigner, externalcatalog.DefaultIssuer)
	ref := externalcatalog.DefaultRef()
	ref.Signature, ref.Reason = res.Signature.Status, res.Signature.Reason
	if res.Signature.Status == fleet.SignatureVerified {
		ref.Copy, ref.Signer, ref.Reason = externalcatalog.CopyRegistry, res.Signature.Signer, reasonCatalogVerified
	}
	until := now().Add(ttlFor(ref.Signature != fleet.SignatureUnknown))
	l.mu.Lock()
	l.def, l.defUntil, l.checking = &ref, until, false
	l.mu.Unlock()
}

// orgCatalog returns the bytes of the organization catalog ref, pulled once
// per TTL (a failure is remembered for the shorter one). Pulling by digest
// makes the registry client refuse a manifest with any other digest.
func (s *Service) orgCatalog(ctx context.Context, ref string) ([]byte, error) {
	now := s.env().now()
	l := &s.catalogs
	l.mu.Lock()
	e, ok := l.org[ref]
	l.mu.Unlock()
	if ok && now.Before(e.until) {
		return e.data, e.err
	}
	cctx, cancel := context.WithTimeout(ctx, provenanceCallTimeout)
	defer cancel()
	client := oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.WithInsecureRegistries(oci.EnvInsecureRegistries()...))
	data, err := externalcatalog.Read(cctx, client, ref)
	if ctx.Err() != nil {
		// The caller gave up, not the registry: the next build pulls again.
		return data, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.org == nil {
		l.org = map[string]orgCatalogEntry{}
	}
	// ponytail: one memo entry per configured ref; a process configures one.
	l.org[ref] = orgCatalogEntry{data: data, err: err, until: now.Add(ttlFor(err == nil))}
	return data, err
}

// readsGHCR reports whether a configured source already reads ghcr.io, the
// only case in which the default catalog is checked against it.
func readsGHCR(opts FleetOptions) bool {
	refs := opts.OCIRefs
	for _, r := range opts.CatalogRoots {
		if strings.HasPrefix(r, contract.OCIScheme) {
			refs = append(refs[:len(refs):len(refs)], r)
		}
	}
	for _, r := range refs {
		host, _, ok := strings.Cut(contract.TrimOCIScheme(r), "/")
		if ok && strings.EqualFold(host, "ghcr.io") {
			return true
		}
	}
	for _, p := range opts.Discover {
		if host, _, _ := strings.Cut(contract.TrimOCIScheme(p), "/"); strings.EqualFold(host, "ghcr.io") {
			return true
		}
	}
	return false
}
