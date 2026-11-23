package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// ErrArtifactAlreadyExists is returned when a push is attempted for a
// reference that already exists in the registry and --force was not specified.
var ErrArtifactAlreadyExists = errors.New("artifact already exists")

// hasTagOrDigest reports whether an OCI reference includes a tag or digest.
func hasTagOrDigest(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	afterSlash := ref
	if i := strings.LastIndex(ref, "/"); i != -1 {
		afterSlash = ref[i+1:]
	}
	return strings.Contains(afterSlash, ":")
}

// PushOptions holds options for the push command.
type PushOptions struct {
	Ref       string
	Path      string
	Force     bool
	Overrides override.Overrides
	// Index is the organization index repository the entry is written to:
	// "" derives it from the bundle repository, IndexNone writes none.
	Index string
	// SignKeyPath, when set, signs the pushed digest with Signer and checks
	// the signature with Verify. The key file is refused inside the pushed
	// directory.
	SignKeyPath string
	Signer      publish.Signer
	Verify      publish.BundleVerifier
}

// PushResult holds the result of the push command.
type PushResult struct {
	Ref     string `json:"ref"`
	Digest  string `json:"digest"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Unchanged is true when the registry already held exactly this bundle.
	Unchanged bool `json:"unchanged,omitempty"`
	// Warnings are the guarantees check's warnings, such as a provider it
	// could not fetch (GUARANTEE_UNVERIFIED).
	Warnings []contract.ValidationWarning `json:"warnings,omitempty"`
	// Index says where this version's organization index entry was written,
	// or why it was not.
	Index string `json:"index,omitempty"`
}

// Push validates a contract bundle, builds an OCI artifact, and pushes it to a registry.
func (s *Service) Push(ctx context.Context, opts PushOptions) (*PushResult, error) {
	if err := s.requireBundleStore(); err != nil {
		return nil, err
	}

	parsed := graph.ParseDependencyRef(opts.Ref)
	if !parsed.IsOCI() {
		return nil, fmt.Errorf("push requires an OCI reference (oci://...): got %q", opts.Ref)
	}

	_, contribution := contract.ContributionRef(parsed.Location)
	if contribution {
		if opts.Index != "" && opts.Index != IndexNone {
			return nil, fmt.Errorf("--index %s: %s", opts.Index, contributionNotIndexed)
		}
		opts.Index = IndexNone
	}
	path := defaultPath(opts.Path)

	c, bundleFS, err := s.pushable(ctx, path, opts.Overrides, contribution)
	if err == nil {
		err = s.signKeyOutside(opts.SignKeyPath, path, bundleFS)
	}
	if err != nil {
		return nil, err
	}

	if err := s.verifyLockIfPresent(ctx, path, &contract.Bundle{Contract: c, FS: bundleFS}); err != nil {
		return nil, err
	}

	// Push resolves remote references, so it checks guarantees as publish does.
	guarantees := s.guaranteeStage(ctx, path, c)
	if len(guarantees.Errors) > 0 {
		return nil, fmt.Errorf("contract validation failed: %s", validationCheck(guarantees).Message)
	}

	ref, err := pushRef(parsed.Location, c, contribution)
	if err != nil {
		return nil, err
	}

	bundle := &contract.Bundle{Contract: c, FS: bundleFS}
	result := &PushResult{Ref: ref, Name: c.Service.Name, Version: c.Service.Version, Warnings: guarantees.Warnings}
	push := s.BundleStore.Push
	if contribution {
		result.Index, push = contributionNotIndexed, s.pushContribution
	}

	// The same content again is a successful no-op; different content under
	// an existing reference is refused unless --force.
	unchanged, err := s.alreadyPushed(ctx, opts.Force, ref, bundle, contribution)
	if err != nil {
		return nil, err
	}
	if unchanged != "" {
		result.Digest, result.Unchanged = unchanged, true
		// An already published version still gets its missing entry
		// and, when a key is given, its missing signature.
		result.Index = cmp.Or(s.keepIndexEntry(ctx, bundle, ref, result.Digest, orgindex.Publisher{}, opts.Index), result.Index)
		return result, s.signPushed(ctx, opts, ref, result.Digest)
	}

	logging.LoggerFromContext(ctx).Debug("pushing artifact", "ref", ref)
	digest, err := push(ctx, ref, bundle)
	if err != nil {
		return nil, err
	}

	result.Digest = digest
	// The entry claims no publisher: a key that signs a push names no identity.
	result.Index = cmp.Or(s.keepIndexEntry(ctx, bundle, ref, digest, orgindex.Publisher{}, opts.Index), result.Index)
	return result, s.signPushed(ctx, opts, ref, digest)
}

// alreadyPushed is the digest ref holds when it already holds bundle's
// content, and "" when ref is absent or force skips the check. Different
// content is ErrArtifactAlreadyExists. A contribution is unchanged only when
// its files are (its record is what the producer says), and reads absent
// once its record is aging (see contributionAging).
func (s *Service) alreadyPushed(ctx context.Context, force bool, ref string, bundle *contract.Bundle, contribution bool) (string, error) {
	if force {
		logging.LoggerFromContext(ctx).Debug("force flag set, skipping existence check", "ref", ref)
		return "", nil
	}
	remote, err := s.BundleStore.Resolve(ctx, ref)
	switch {
	case isNotFound(err):
		logging.LoggerFromContext(ctx).Debug("artifact not found, proceeding with push", "ref", ref)
		return "", nil
	case err != nil:
		return "", err
	}
	// A bundle that cannot be packed has no digest and reads as a conflict
	// below: refused, never overwritten.
	local, _ := oci.BundleDigest(bundle)
	if remote == local || s.onlyRecordDiffers(ctx, ref, remote, local, bundle, contribution) {
		if contribution && s.contributionAging(ctx, ref) {
			return "", nil // pushed again, with a fresh created time
		}
		logging.LoggerFromContext(ctx).Debug("identical artifact already published", "ref", ref, "digest", remote)
		return remote, nil
	}
	return "", fmt.Errorf("%w: %s holds %s, not this bundle's %s (use --force to overwrite)", ErrArtifactAlreadyExists, ref, remote, local)
}

// signKeyOutside refuses a --sign-key file inside the pushed directory dir,
// whose files are fsys. A relative key path is from the working directory.
func (s *Service) signKeyOutside(keyPath, dir string, fsys fs.FS) error {
	if keyPath == "" {
		return nil
	}
	key, _ := s.env().absPath(keyPath) // ponytail: fails only without a working directory, then the key counts as outside
	return keyOutsideBundle(key, dir, fsys)
}

// contributionNotIndexed is why a push to a contribution repository writes no
// organization index entry: a contribution is an inferred record, never a
// declared consumer.
const contributionNotIndexed = "contributions are never indexed"

// defaultPushTag is the tag a push with none uses: the version, or <name>_<version>
// in a contribution repository, which holds every service of its producer.
func defaultPushTag(c *contract.Contract, contribution bool) string {
	if contribution {
		return c.Service.Name + "_" + c.Service.Version
	}
	return c.Service.Version
}

// pushRef is the ref location is pushed as: tagged by defaultPushTag when it
// names no tag or digest, and refused when a contribution reader would never
// take it (see contributionTag).
func pushRef(location string, c *contract.Contract, contribution bool) (string, error) {
	ref := location
	if !hasTagOrDigest(ref) {
		ref += ":" + defaultPushTag(c, contribution)
	}
	if contribution {
		return ref, contributionTag(ref, c)
	}
	return ref, nil
}

// contributionTag refuses a contribution ref no reader takes: one with no
// tag or a tag other than <name>_<version>, and a version a reader skips
// (see contributionVersion).
func contributionTag(ref string, c *contract.Contract) error {
	repo, tag, _ := contract.SplitOCIRef(ref)
	want := defaultPushTag(c, true)
	if _, ok := contributionVersion(c.Service.Version); !ok {
		return fmt.Errorf("%s: version %s is no contribution version: a contribution version is MAJOR.MINOR.PATCH semver with no build metadata ('+')", ref, c.Service.Version)
	}
	if tag != want {
		return fmt.Errorf("%s: a contribution is pushed by tag, as %s", ref, repo+":"+want)
	}
	return nil
}

// pushContribution pushes b to ref, a tag of a contribution repository,
// annotated with when it was pushed: the time a reader shows the
// contribution was made, covered by its signed digest.
func (s *Service) pushContribution(ctx context.Context, ref string, b *contract.Bundle) (string, error) {
	repo, tag, _ := contract.SplitOCIRef(ref)
	env := s.env()
	created := env.now().UTC().Format(time.RFC3339)
	annotated := func() v1.Image {
		img, _ := oci.BundleImage(b) // pushable returned a contract and an FS
		return mutate.Annotations(img, map[string]string{publish.AnnotationCreated: created}).(v1.Image)
	}
	// The layer is a stream that reads once: one image is read through for
	// its digest and a second is written under it. ponytail: reading can only
	// fail when the bundle's files do, and then the write below refuses a
	// zero digest.
	img := annotated()
	layers, _ := img.Layers()
	rc, _ := layers[0].Compressed()
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	h, _ := img.Digest()
	reg := env.publishRegistry()
	if _, err := reg.Write(ctx, repo, digestedImage{annotated(), h}); err != nil {
		return "", err
	}
	if err := reg.Tag(ctx, repo, h.String(), tag); err != nil {
		return "", err
	}
	return h.String(), nil
}

// contributionAging reports whether the record ref holds was made more than
// half the freshness window ago, or says not when: an unchanged push renews
// it, so a producer that keeps pushing never reads stale. ponytail: half of
// RecentEvidenceWindow; a reader with a shorter --freshness sees records
// stale between renewals.
func (s *Service) contributionAging(ctx context.Context, ref string) bool {
	env := s.env()
	head, err := env.publishRegistry().Head(ctx, ref)
	if err != nil || head == nil {
		return false
	}
	created, err := time.Parse(time.RFC3339, head.Annotations[publish.AnnotationCreated])
	return err != nil || env.now().Sub(created) > fleet.RecentEvidenceWindow/2
}

// digestedImage is an image whose streamed layer was read, with the digest
// that read computed.
type digestedImage struct {
	v1.Image
	digest v1.Hash
}

func (d digestedImage) Digest() (v1.Hash, error) { return d.digest, nil }

// signPushed signs ref's repository at digest with the push's key, unless one
// of its signatures already verifies with that key, and checks the signature
// it wrote. A push with no key signs nothing.
func (s *Service) signPushed(ctx context.Context, opts PushOptions, ref, digest string) error {
	if opts.SignKeyPath == "" {
		return nil
	}
	reg, repo := s.env().publishRegistry(), refRepo(ref)
	if publish.VerifyDigest(ctx, reg, opts.Verify, repo, digest) == nil {
		return nil
	}
	err := publish.SignDigest(ctx, reg, opts.Signer, repo, digest)
	if err == nil {
		err = publish.VerifyDigest(ctx, reg, opts.Verify, repo, digest)
	}
	if err != nil {
		return fmt.Errorf("pushed %s@%s, but signing it failed: %w; run the push again to sign it", repo, digest, err)
	}
	return nil
}

// onlyRecordDiffers reports whether the bundle at ref, pinned to the digest
// remote, holds local's content: the same files under other manifest
// annotations, such as a contribution's push time, or files that differ only
// in an inferred record with the same cited inputs (a push from another
// commit or another Pacto of the same content). localDigest is local's
// digest without annotations. For a contribution only the same files count:
// its record's fields are the producer's content, not a by-product.
func (s *Service) onlyRecordDiffers(ctx context.Context, ref, remote, localDigest string, local *contract.Bundle, contribution bool) bool {
	prev, err := s.BundleStore.Pull(ctx, refRepo(ref)+"@"+remote)
	if err != nil {
		return false
	}
	if d, err := oci.BundleDigest(prev); err == nil && d == localDigest {
		return true
	}
	if contribution {
		return false
	}
	unchanged, _ := contract.InferredUnchanged(prev, local, oci.BundleDigest)
	return unchanged
}

// refRepo is ref without its tag or digest.
func refRepo(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i]
	}
	return ref
}

// isNotFound reports whether err indicates the artifact was not found.
func isNotFound(err error) bool {
	var notFound *oci.ArtifactNotFoundError
	return errors.As(err, &notFound)
}

// rejectLocalDeps returns an error if any dependency uses a local reference.
func rejectLocalDeps(c *contract.Contract) error {
	for _, dep := range c.Dependencies {
		if dep.Ref != "" && graph.ParseDependencyRef(dep.Ref).IsLocal() {
			return fmt.Errorf("local dependency detected: %s\nLocal dependencies are not allowed when publishing. All dependencies must use OCI references (oci://...)", dep.Ref)
		}
	}
	return nil
}

// rejectLocalRefs returns an error if any config or policy ref uses a local reference.
func rejectLocalRefs(c *contract.Contract) error {
	for _, cfg := range c.Configurations {
		if cfg.Ref != "" && graph.ParseDependencyRef(cfg.Ref).IsLocal() {
			return fmt.Errorf("local config ref detected: %s (configurations[%q])\nLocal references are not allowed when publishing. Use an OCI reference (oci://...)", cfg.Ref, cfg.Name)
		}
	}
	for i, pol := range c.Policies {
		if pol.Ref != "" && graph.ParseDependencyRef(pol.Ref).IsLocal() {
			return fmt.Errorf("local policy ref detected: %s (policies[%d])\nLocal references are not allowed when publishing. Use an OCI reference (oci://...)", pol.Ref, i)
		}
	}
	return nil
}

// pushable completes and validates the contract at path for a push: valid,
// with remote references resolved, and with no local dependency or
// reference a registry could not follow. A contribution is pushed as the
// producer wrote it: its pacto.inferred.yaml is the record, never replaced
// by what Pacto would infer from the folder.
func (s *Service) pushable(ctx context.Context, path string, overrides override.Overrides, contribution bool) (*contract.Contract, fs.FS, error) {
	var c *contract.Contract
	var bundleFS fs.FS
	var err error
	if contribution {
		c, bundleFS, err = loadContribution(ctx, path, overrides, s.BundleStore)
	} else {
		c, bundleFS, err = s.loadCompleted(ctx, path, overrides)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := rejectLocalDeps(c); err != nil {
		return nil, nil, err
	}
	if err := rejectLocalRefs(c); err != nil {
		return nil, nil, err
	}
	return c, bundleFS, nil
}

// loadCompleted is the bundle at path completed with what Pacto infers from
// its folder, then validated.
func (s *Service) loadCompleted(ctx context.Context, path string, overrides override.Overrides) (*contract.Contract, fs.FS, error) {
	cat, err := s.completionCatalog(ctx)
	if err != nil {
		return nil, nil, err
	}
	c, _, bundleFS, err := loadAndValidateFull(ctx, path, overrides, s.BundleStore, s.localInputs(path, ""), cat, false)
	return c, bundleFS, err
}

// loadContribution is the bundle at path as read, validated: a contribution
// carries the record its producer wrote.
func loadContribution(ctx context.Context, path string, overrides override.Overrides, store oci.BundleStore) (*contract.Contract, fs.FS, error) {
	b, err := readLocalBundle(path)
	if err == nil {
		b, err = applyOverrides(b, overrides)
	}
	if err != nil {
		return nil, nil, err
	}
	c, raw, bundleFS, err := validatedFull(ctx, path, b, store, false)
	if err != nil {
		return nil, nil, err
	}
	// ponytail: an unreadable record is no declaration; validation already
	// read it, and a reader refuses it on its own.
	rec, _ := contract.ReadInferredRecord(bundleFS)
	if why := contract.ContributionRefusal(&contract.Bundle{RawYAML: raw, FS: bundleFS}, rec); why != "" {
		return nil, nil, fmt.Errorf("%s: %s", path, why)
	}
	return c, bundleFS, nil
}
