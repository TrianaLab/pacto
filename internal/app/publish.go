package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// PublishOptions holds options for `pacto publish`.
type PublishOptions struct {
	Path      string
	Overrides override.Overrides
	// Revision is the source revision stamped on every manifest; empty reads
	// the git commit of Path.
	Revision string
	// Plan decides and writes nothing.
	Plan bool
}

// runner runs a program in dir with env added to the process environment and
// returns its standard output; a failure carries its standard error.
type runner func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)

func execRun(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, tail(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// tail keeps the end of a command's error output, where the reason is.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		return "…" + s[len(s)-2000:]
	}
	return s
}

// Publish plans and, unless opts.Plan, publishes what the contract's publish
// section declares. Pacto's own checks (validation, local references, the
// lock, signing, the source revision and the version rule) refuse it; Pacto
// runs no tests.
func (s *Service) Publish(ctx context.Context, opts PublishOptions) (*publish.Result, error) {
	path := defaultPath(opts.Path)
	c, raw, fsys, err := loadAndValidateFull(ctx, path, opts.Overrides, s.BundleStore)
	if err != nil {
		return nil, err
	}
	b := &contract.Bundle{Contract: c, RawYAML: raw, FS: fsys}
	env := s.env()
	in := publish.Input{Bundle: b, Revision: opts.Revision, Checks: []publish.Check{{Name: "validation", Passed: true, Message: "the contract is valid"}}}
	if in.Revision == "" {
		in.Revision = gitRevision(ctx, env.run, path)
	}
	in.Checks = append(in.Checks, checkOf("references", rejectLocalDeps(c), rejectLocalRefs(c)),
		checkOf("lock", s.verifyLockIfPresent(ctx, path, b)), checkOf("signing key", keyOutsideBundle(c, fsys)))
	reg := env.publishRegistry()
	if opts.Plan {
		return &publish.Result{Plan: publish.MakePlan(ctx, reg, in)}, nil
	}
	return publish.Apply(ctx, reg, newExecTools(path, env.run, reg), in)
}

// checkOf is a passed check, or a failed one carrying the first error.
func checkOf(name string, errs ...error) publish.Check {
	if err := errors.Join(errs...); err != nil {
		return publish.Check{Name: name, Message: err.Error()}
	}
	return publish.Check{Name: name, Passed: true, Message: "ok"}
}

// keyOutsideBundle refuses a signing key file that would be packed into the
// published bundle: a private key in a public artifact.
func keyOutsideBundle(c *contract.Contract, fsys fs.FS) error {
	if c.Publish == nil || c.Publish.Sign == nil || c.Publish.Sign.Key == "" || filepath.IsAbs(c.Publish.Sign.Key) {
		return nil
	}
	if _, err := fs.Stat(fsys, filepath.ToSlash(filepath.Clean(c.Publish.Sign.Key))); err == nil {
		return fmt.Errorf("the signing key %s would be published inside the bundle; add it to .pactoignore or keep it outside the contract directory", c.Publish.Sign.Key)
	}
	return nil
}

// gitRevision is the commit of the checkout dir is in, or "".
func gitRevision(ctx context.Context, run runner, dir string) string {
	out, err := run(ctx, dir, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// defaultPublishRegistry is the registry publish writes through: the CLI's
// credentials and plain-HTTP hosts.
func defaultPublishRegistry() publish.Registry {
	return publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries()...)
}

// execTools implements publish.Tools with docker buildx, the declared command
// and syft, which fail closed when their program is missing, and signs with
// sigstore-go through reg.
type execTools struct {
	dir string
	run runner
	reg publish.Registry
	// signers holds each sign config's loaded signer, so a run loads a key,
	// signs a KMS probe or fetches the trusted root once, not per reference.
	signers map[contract.PublishSign]loadedSigner
}

type loadedSigner struct {
	s   signing.Signer
	p   signing.Policy
	err error
}

func newExecTools(dir string, run runner, reg publish.Registry) execTools {
	return execTools{dir: dir, run: run, reg: reg, signers: map[contract.PublishSign]loadedSigner{}}
}

// Build implements publish.Tools.
func (t execTools) Build(ctx context.Context, a contract.PublishArtifact, out string, env []string) error {
	if d := a.Build.Dockerfile; d != nil {
		args := []string{"buildx", "build", "--output", "type=oci,dest=" + out + ",tar=false"}
		for _, e := range env {
			if v, ok := strings.CutPrefix(e, "PACTO_VERSION="); ok {
				args = append(args, "--label", publish.AnnotationVersion+"="+v)
			}
			if v, ok := strings.CutPrefix(e, "PACTO_REVISION="); ok {
				args = append(args, "--label", publish.AnnotationRevision+"="+v)
			}
		}
		if d.File != "" {
			args = append(args, "--file", d.File)
		}
		if len(d.Platforms) > 0 {
			args = append(args, "--platform", strings.Join(d.Platforms, ","))
		}
		_, err := t.run(ctx, t.dir, env, "docker", append(args, d.Context)...)
		return err
	}
	_, err := t.run(ctx, t.dir, env, a.Build.Command[0], a.Build.Command[1:]...)
	return err
}

// insecureHost reports whether ref's registry is reached over plain HTTP.
func insecureHost(ref string) bool {
	host, _, _ := strings.Cut(ref, "/")
	name, _, _ := strings.Cut(host, ":")
	return name == "localhost" || name == "127.0.0.1" || slices.Contains(oci.EnvInsecureRegistries(), host)
}

// Public-good Sigstore, which keyless publishing signs with and a key's
// signature is logged to unless transparencyLog is false. Tests swap them.
var (
	publicGoodRoot              = signing.PublicGoodRoot
	fulcioURL, rekorURL, tsaURL = signing.PublicFulcio, signing.PublicRekor, ""
)

// signing is how publish signs, and the policy a signature must verify
// under: the configured key file, KMS key or keyless identity and issuer.
func (t execTools) signing(ctx context.Context, sign contract.PublishSign) (signing.Signer, signing.Policy, error) {
	l, ok := t.signers[sign]
	if !ok {
		l.s, l.p, l.err = t.load(ctx, sign)
		t.signers[sign] = l
	}
	return l.s, l.p, l.err
}

// load builds what signing caches.
func (t execTools) load(ctx context.Context, sign contract.PublishSign) (signing.Signer, signing.Policy, error) {
	tlog := sign.TransparencyLog == nil || *sign.TransparencyLog
	var (
		kp  *signing.Keypair
		err error
	)
	switch {
	case sign.Key != "":
		key := sign.Key
		if !filepath.IsAbs(key) {
			key = filepath.Join(t.dir, key)
		}
		kp, err = signing.LoadKey(key)
	case sign.KMS != "":
		kp, err = signing.KMS(ctx, sign.KMS)
	default:
		// The sign check refused keyless without the transparency log.
		root, err := publicGoodRoot(signing.PublicTUF)
		if err != nil {
			return signing.Signer{}, signing.Policy{}, fmt.Errorf("public-good Sigstore's trusted root: %w", err)
		}
		s, err := signing.Keyless(fulcioURL, tsaURL, rekorURL, signing.IDToken(""), root)
		return s, signing.Policy{Identity: sign.Identity, Issuer: sign.Issuer, Root: root, Tlog: rekorURL != ""}, err
	}
	if err != nil {
		return signing.Signer{}, signing.Policy{}, err
	}
	log := ""
	if tlog {
		log = rekorURL
	}
	return signing.Keyed(kp, log), signing.Policy{Key: kp.GetPublicKey()}, nil
}

// Verify implements publish.Tools: a signature referrer of ref that verifies
// under the configured key, KMS key or keyless identity and issuer.
func (t execTools) Verify(ctx context.Context, sign contract.PublishSign, ref string) error {
	_, p, err := t.signing(ctx, sign)
	if err != nil {
		return err
	}
	return t.verify(ctx, p, ref)
}

func (t execTools) verify(ctx context.Context, p signing.Policy, ref string) error {
	repo, digest, _ := strings.Cut(ref, "@")
	return publish.VerifyDigest(ctx, t.reg, bundleVerifier(p), repo, digest)
}

// bundleVerifier verifies a bundle under p and returns the predicate type of
// the statement its signature covers.
func bundleVerifier(p signing.Policy) publish.BundleVerifier {
	return func(b, d []byte) (string, error) {
		res, err := p.Verify(b, d)
		if err != nil {
			return "", err
		}
		return res.Statement.GetPredicateType(), nil
	}
}

// Sign implements publish.Tools: a valid signature by the configured identity
// is kept, otherwise Pacto signs and the new signature must then verify.
func (t execTools) Sign(ctx context.Context, sign contract.PublishSign, ref string) (bool, error) {
	s, p, err := t.signing(ctx, sign)
	if err != nil {
		return false, err
	}
	if t.verify(ctx, p, ref) == nil {
		return false, nil
	}
	repo, digest, _ := strings.Cut(ref, "@")
	if err := publish.SignDigest(ctx, t.reg, s, repo, digest); err != nil {
		return false, err
	}
	// A keyless signature carries whatever identity the CI token had. One the
	// pinned verify rejects would never reach a Stage, so it fails here, before
	// any tag is written.
	if err := t.verify(ctx, p, ref); err != nil {
		return false, fmt.Errorf("the signature Pacto wrote for %s does not verify against the configured identity (identity %q, issuer %q); check that the CI identity matches publish.sign: %w",
			ref, sign.Identity, sign.Issuer, err)
	}
	return true, nil
}

// SBOM implements publish.Tools.
func (t execTools) SBOM(ctx context.Context, ref string) ([]byte, error) {
	var env []string
	if insecureHost(ref) {
		env = append(env, "SYFT_REGISTRY_INSECURE_USE_HTTP=true")
	}
	return t.run(ctx, t.dir, env, "syft", "scan", "registry:"+ref, "-o", "spdx-json", "-q")
}
