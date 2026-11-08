package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/plugin"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// InitFrom is the golden path a service is created from and what the caller
// gives it.
type InitFrom struct {
	// From is an oci:// reference or a directory.
	From string
	// Trust holds the template keys the path's signature is verified with.
	// Nil, or a config with no template key, leaves the signature unchecked.
	Trust     *delivery.TrustConfig
	Overrides override.Overrides
	// RunPlugins are the plugins the path names that the caller asked to run.
	RunPlugins []string
}

// localOrigin is the Unknown line of a service whose origin is a local path.
const localOrigin = "origin.ref is a local path; publish the golden path and use its oci:// reference so others can resolve it"

// publishedOrigin is the Unknown line of a service created from a local
// folder whose contract names where the path is published.
const publishedOrigin = "origin.ref is the coordinate the golden path's contract names, read from a local folder: Pacto did not check that the published revision holds these files, so origin.digest may match no published revision"

// InitFrom creates a service from a golden path: it reads the path from a
// directory or a registry, verifies its signature when the trust config names
// a template key, then plans the folder like Init, validating strictly with
// the resolver before anything is written.
func (s *Service) InitFrom(ctx context.Context, opts InitOptions, from InitFrom) (*InitResult, error) {
	opts.Template = &pactify.Template{}
	if err := pactify.TemplateOptions(opts); err != nil { // refused before anything is pulled
		return nil, err
	}
	dir := opts.Dir
	if dir == "" {
		dir = opts.Name
	}
	absDir, err := s.env().absPath(dir)
	if err != nil {
		return nil, err
	}
	t, err := s.resolveTemplate(ctx, from.From, absDir, from.Trust)
	if err != nil {
		return nil, err
	}
	t.Overrides = from.Overrides
	t.Validate = s.templateValidator(absDir)
	t.RunPlugins = from.RunPlugins
	t.TrustConfigured = from.Trust != nil && from.Trust.HasTemplateKeys()
	t.Find = plugin.Find
	if s.PluginRunner != nil {
		t.Run = s.PluginRunner.Run
	}
	opts.Template = t
	r, err := s.Init(ctx, opts)
	switch {
	case r == nil || strings.HasPrefix(from.From, contract.OCIScheme):
	case strings.HasPrefix(t.Ref, contract.OCIScheme):
		r.Unknown = append(r.Unknown, publishedOrigin)
	default:
		r.Unknown = append(r.Unknown, localOrigin)
	}
	return r, err
}

// resolveTemplate reads the golden path from, a directory or an oci://
// reference, and what service.origin records of it.
func (s *Service) resolveTemplate(ctx context.Context, from, absTarget string, trust *delivery.TrustConfig) (*pactify.Template, error) {
	if strings.HasPrefix(from, contract.OCIScheme) {
		return s.ociTemplate(ctx, from, trust)
	}
	absFrom, err := s.env().absPath(from)
	if err != nil {
		return nil, err
	}
	return dirTemplate(from, absFrom, absTarget)
}

// dirTemplate reads a golden path from a directory: the files a bundle of it
// would hold, their content hash as the origin digest, and as the origin ref
// its published coordinate or a path relative to the created contract, so no
// host path is recorded.
func dirTemplate(from, absFrom, absTarget string) (*pactify.Template, error) {
	fsys, err := readTemplateDir(absFrom)
	if err != nil {
		return nil, fmt.Errorf("reading the golden path %s: %w", from, err)
	}
	raw, err := fs.ReadFile(fsys, "pacto.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading the golden path %s: %w", from, err)
	}
	c, err := goldenPath(from, raw)
	if err != nil {
		return nil, err
	}
	digest, _ := lock.HashFS(fsys) // ponytail: a MapFS reads every file it holds
	ref := localRef(absFrom, absTarget)
	if c.Publish != nil && c.Publish.Bundle != "" {
		ref = c.Publish.Bundle + ":" + c.Service.Version
	}
	return &pactify.Template{FS: fsys, Ref: ref, Digest: digest,
		Signature: fleet.CreationStep{Step: fleet.StepSignature, Status: fleet.StepStatusUnknown, Detail: "a directory carries no signature"}}, nil
}

// localRef is absFrom relative to absTarget. When the two share no parent
// below the filesystem root or the home folder, that path would spell out
// host folders such as /Users/<name>, so only the path's folder name is kept.
func localRef(absFrom, absTarget string) string {
	ref, _ := filepath.Rel(absTarget, absFrom) // ponytail: two absolute paths always have a relative path
	common := absTarget
	for _, part := range strings.Split(ref, string(filepath.Separator)) {
		if part == ".." {
			common = filepath.Dir(common)
		}
	}
	home, _ := os.UserHomeDir()
	if filepath.Dir(common) == common || home != "" && common != home && strings.HasPrefix(home, common+string(filepath.Separator)) {
		return filepath.Base(absFrom)
	}
	return filepath.ToSlash(ref)
}

// readTemplateDir reads the files a bundle of the directory would hold into
// memory, within the limits a pulled bundle is read with. A symlink or any
// other entry that is not a regular file is refused: os.DirFS follows a link
// out of the folder, to a secret or to a device that never ends.
func readTemplateDir(abs string) (fstest.MapFS, error) {
	if info, err := os.Lstat(filepath.Join(abs, ignore.IgnoreFileName)); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", ignore.IgnoreFileName)
	}
	dirFS := os.DirFS(abs)
	matcher, err := ignore.Load(dirFS)
	if err != nil {
		return nil, err
	}
	out := fstest.MapFS{}
	var total int64
	err = fs.WalkDir(ignore.FS(dirFS, matcher), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file: a golden path holds only files and folders", name)
		}
		f, err := dirFS.Open(name)
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(f, oci.MaxFileSize+1))
			_ = f.Close()
		}
		if err != nil {
			return err
		}
		if total += int64(len(data)); len(data) > oci.MaxFileSize || total > oci.MaxTotalSize {
			return fmt.Errorf("%s is over the size a bundle may hold (%d bytes a file, %d in all)", name, oci.MaxFileSize, oci.MaxTotalSize)
		}
		out[name] = &fstest.MapFile{Data: data}
		return nil
	})
	return out, err
}

// ociTemplate pulls a golden path by the manifest digest its tag resolves to,
// so the bytes read are the bytes the signature check names. With template
// keys the pull reads past the disk cache, which serves a digest-pinned entry
// without rehashing it.
func (s *Service) ociTemplate(ctx context.Context, from string, trust *delivery.TrustConfig) (*pactify.Template, error) {
	if err := s.requireBundleStore(); err != nil {
		return nil, err
	}
	location, err := oci.ResolveRef(ctx, s.BundleStore, contract.TrimOCIScheme(from), "")
	if err != nil {
		return nil, err
	}
	repo, _, digest := contract.SplitOCIRef(location)
	if digest == "" {
		if digest, err = s.BundleStore.Resolve(ctx, location); err != nil {
			return nil, fmt.Errorf("resolving the golden path %s: %w", from, err)
		}
	}
	b, err := s.pullTemplate(ctx, repo, digest, trust != nil && trust.HasTemplateKeys())
	if err != nil {
		return nil, fmt.Errorf("pulling the golden path %s: %w", from, err)
	}
	raw, err := b.Raw()
	if err != nil {
		return nil, fmt.Errorf("reading the golden path %s: %w", from, err)
	}
	if _, err := goldenPath(from, raw); err != nil {
		return nil, err
	}
	sig, err := s.verifyTemplate(ctx, repo, digest, trust)
	if err != nil {
		return nil, err
	}
	hash, err := lock.HashFS(b.FS)
	if err != nil {
		return nil, fmt.Errorf("hashing the golden path %s: %w", from, err)
	}
	ref := from
	if !oci.HasExplicitTag(contract.TrimOCIScheme(from)) {
		ref = contract.OCIScheme + location // the tag it resolved to
	}
	return &pactify.Template{FS: b.FS, Ref: ref, Digest: hash, Signature: sig}, nil
}

// pullTemplate pulls repo@digest. A pull whose signature is then checked
// reads past the disk cache and refuses bytes of another digest.
func (s *Service) pullTemplate(ctx context.Context, repo, digest string, verified bool) (*contract.Bundle, error) {
	pinned := repo + "@" + digest
	logging.LoggerFromContext(ctx).Debug("pulling golden path", "ref", pinned)
	if !verified {
		return s.BundleStore.Pull(ctx, pinned)
	}
	store := s.BundleStore
	if u, ok := store.(interface{ Uncached() oci.BundleStore }); ok {
		store = u.Uncached()
	}
	b, got, err := oci.PullPinned(ctx, store, pinned)
	if err == nil && got != digest {
		err = fmt.Errorf("the registry answered %s with %s", pinned, got)
	}
	return b, err
}

// goldenPath parses a golden path's contract and refuses one that declares no
// service.template.
func goldenPath(from string, raw []byte) (*contract.Contract, error) {
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("reading the golden path %s: %w", from, err)
	}
	if c.Service.Template == nil {
		return nil, fmt.Errorf("%s is not a golden path: its contract declares no service.template; to copy a service use pacto pull", from)
	}
	return c, nil
}

// noTemplateKey is the signature step when no template key is configured.
const noTemplateKey = "signature not checked: no trust config names a template key; pass --trust or add a role: template key to PACTO_DELIVERY_TRUST"

// verifyTemplate checks repo@digest's signature against the trust config's
// template keys for repo. With none configured the signature is unknown; with
// some, anything short of a signature one of them verifies refuses creation.
func (s *Service) verifyTemplate(ctx context.Context, repo, digest string, trust *delivery.TrustConfig) (fleet.CreationStep, error) {
	step := fleet.CreationStep{Step: fleet.StepSignature, Status: fleet.StepStatusUnknown, Detail: noTemplateKey}
	if trust == nil || !trust.HasTemplateKeys() {
		return step, nil
	}
	keys := trust.TemplateKeys(repo)
	if len(keys) == 0 {
		return step, fmt.Errorf("the golden path %s is refused: no template key in the trust config may sign golden paths in %s", repo, repo)
	}
	var signer string
	verify := func(b, d []byte) (string, error) {
		var errs []error
		for _, k := range keys {
			p, err := signing.PolicyOf(k)
			if err == nil {
				var pt string
				if pt, err = bundleVerifier(p)(b, d); err == nil {
					signer = keyName(k)
					return pt, nil
				}
			}
			errs = append(errs, fmt.Errorf("%s: %w", keyName(k), err))
		}
		return "", errors.Join(errs...)
	}
	if err := publish.VerifyDigest(ctx, s.env().publishRegistry(), verify, repo, digest); err != nil {
		return step, fmt.Errorf("the golden path %s@%s is refused: no template key the trust config names verifies its signature: %w", repo, digest, err)
	}
	step.Status, step.Detail = fleet.StepStatusDone, "signed by "+signer+", a template key of the trust config"
	return step, nil
}

// keyName names a trusted key for a person.
func keyName(k delivery.TrustedKey) string {
	switch {
	case k.Identity != "" && k.Name != "":
		return k.Name + " (" + k.Identity + " from " + k.Issuer + ")"
	case k.Identity != "":
		return k.Identity + " from " + k.Issuer
	case k.Name != "":
		return k.Name
	}
	return "the template key"
}

// templateValidator validates a created contract strictly, with the resolver
// rooted at the folder it is created in, so configuration and policy refs
// resolve. With no bundle store there is no resolver and a ref fails closed.
func (s *Service) templateValidator(dir string) func(context.Context, *contract.Contract, []byte, fs.FS) []validation.Stage {
	var resolver validation.BundleResolver
	if s.BundleStore != nil {
		resolver = s.PolicyResolver(dir)
	}
	return func(ctx context.Context, c *contract.Contract, raw []byte, fsys fs.FS) []validation.Stage {
		return validation.ValidateStagesWithResolverAt(ctx, c, raw, fsys, resolver, s.env().now())
	}
}
