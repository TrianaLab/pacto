package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/validation"
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
	in := publish.Input{Bundle: b, Revision: opts.Revision, Checks: []publish.Check{validationCheck(s.guaranteeStage(ctx, path, c))}}
	if in.Revision == "" {
		if abs, err := env.absPath(path); err == nil {
			in.Revision = gitRevision(abs)
		}
	}
	reg := env.publishRegistry()
	tools := newExecTools(path, env.run, reg)
	in.Checks = append(in.Checks, checkOf("references", rejectLocalDeps(c), rejectLocalRefs(c)),
		checkOf("lock", s.verifyLockIfPresent(ctx, path, b)), checkOf("build outputs", sbomsIgnored(c, path)))
	in.Preflight = preflight(ctx, env, tools, path, c, fsys, opts.Revision == "" && in.Revision != "", in.Revision)
	if opts.Plan {
		return &publish.Result{Plan: publish.MakePlan(ctx, reg, in)}, nil
	}
	res, err := publish.Apply(ctx, reg, tools, in)
	if errors.Is(err, publish.ErrNotReady) {
		res.Refusal = keepRefusal(ctx, env, tools, b, res.Plan)
	}
	return res, err
}

// preflight checks what the absent items need: the builder of each artifact
// still to build, and, when anything is written, the signer and a clean
// checkout (askGit says whether to ask git). A re-run of a version already
// published needs none of them, so `--propose` still works from a machine
// with no buildx, no OIDC token or an unrelated uncommitted file.
func preflight(ctx context.Context, env sysDeps, tools execTools, path string, c *contract.Contract, fsys fs.FS, askGit bool, rev string) func(publish.Plan) []publish.Check {
	return func(p publish.Plan) []publish.Check {
		absent := map[string]bool{}
		for _, it := range p.Items {
			absent[it.Name] = it.State == publish.Absent
		}
		sign := keyOutsideBundle(c, fsys)
		if p.Missing > 0 {
			sign = errors.Join(sign, tools.loadSigner(ctx, c))
		}
		checks := []publish.Check{checkOf("signing key", sign), checkOf("builders", builders(ctx, env, path, c, absent))}
		if askGit && p.Missing > 0 {
			checks = append(checks, checkOf("clean checkout", cleanCheckout(ctx, env.run, path, rev)))
		}
		return checks
	}
}

// validationCheck is the validation check after the guarantees stage, the one
// validation stage that reads other contracts. An unmet guarantee of a required
// dependency fails it; a warning, such as a provider it could not fetch
// (GUARANTEE_UNVERIFIED), passes it and is named in its message.
func validationCheck(st validation.Stage) publish.Check {
	if len(st.Errors) > 0 {
		msgs := make([]string, len(st.Errors))
		for i, e := range st.Errors {
			msgs[i] = e.Code + ": " + e.Message
		}
		return publish.Check{Name: "validation", Message: strings.Join(msgs, "; ")}
	}
	msg := "the contract is valid"
	for _, w := range st.Warnings {
		msg += "; warning " + w.Code + ": " + w.Message
	}
	return publish.Check{Name: "validation", Passed: true, Message: msg}
}

// checkOf is a passed check, or a failed one carrying the first error.
func checkOf(name string, errs ...error) publish.Check {
	if err := errors.Join(errs...); err != nil {
		return publish.Check{Name: name, Message: err.Error()}
	}
	return publish.Check{Name: name, Passed: true, Message: "ok"}
}

// preflightTimeout bounds each program the plan runs to predict a refusal.
const preflightTimeout = 30 * time.Second

// cleanCheckout refuses a git checkout with uncommitted changes to tracked
// files: the revision stamped and signed on every artifact would name a
// commit that does not hold what was built. Passing --revision skips it, as
// the caller then names the revision.
//
// ponytail: untracked files are not checked, since a build's own outputs
// (an SBOM, say) are often untracked and would refuse every resumed run.
func cleanCheckout(ctx context.Context, run runner, dir, rev string) error {
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	out, err := run(ctx, dir, nil, "git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Errorf("could not tell whether the checkout holds uncommitted changes (%w); commit them and run where git is installed, or pass --revision", err)
	}
	if changed := strings.TrimSpace(string(out)); changed != "" {
		return fmt.Errorf("the checkout has uncommitted changes, so revision %s does not hold what would be built; commit them, or pass --revision:\n%s", rev, changed)
	}
	return nil
}

// builders predicts a build that cannot start: docker buildx for a
// dockerfile build, and the program a command build runs, for each artifact
// absent names. It runs no build.
//
// ponytail: a builder on the plain docker driver passes, though it writes an
// OCI layout only with the containerd image store; the build says so.
func builders(ctx context.Context, env sysDeps, dir string, c *contract.Contract, absent map[string]bool) error {
	if c.Publish == nil {
		return nil
	}
	var errs []error
	buildx := false
	for _, a := range c.Publish.Artifacts {
		switch {
		case !absent[a.Name]:
		case a.Build.Dockerfile != nil && !buildx:
			buildx = true
			ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
			_, err := env.run(ctx, dir, nil, "docker", "buildx", "inspect")
			cancel()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s is built from a Dockerfile, but docker buildx has no usable builder: %w", a.Name, err))
			}
		case len(a.Build.Command) > 0:
			prog := a.Build.Command[0]
			if strings.ContainsRune(prog, '/') && !filepath.IsAbs(prog) {
				prog = filepath.Join(dir, prog) // the build runs in the contract directory
				if !strings.ContainsRune(prog, filepath.Separator) {
					// Join(".", "./render.sh") is "render.sh", which LookPath
					// would search $PATH for.
					prog = "." + string(filepath.Separator) + prog
				}
			}
			if _, err := env.lookPath(prog); err != nil {
				errs = append(errs, fmt.Errorf("the build of %s runs %s, which is not an executable here: %w", a.Name, a.Build.Command[0], err))
			}
		}
	}
	return errors.Join(errs...)
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

// sbomsIgnored refuses an SBOM path inside the contract directory that
// .pactoignore does not leave out: the build writes it after the plan packed
// the bundle, so the bundle would change under the publish after every image
// was pushed.
func sbomsIgnored(c *contract.Contract, path string) error {
	if c.Publish == nil {
		return nil
	}
	_, dir, _ := resolveLocalPath(path) // ponytail: the bundle was just loaded from it
	m, err := ignore.Load(os.DirFS(dir))
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range c.Publish.Artifacts {
		rel := a.SBOM
		if filepath.IsAbs(rel) {
			base, _ := filepath.Abs(dir) // ponytail: fails only without a working directory, then Rel fails and rel is skipped
			rel, _ = filepath.Rel(base, rel)
		}
		if a.SBOM == "" || !filepath.IsLocal(rel) || ignoredIn(m, filepath.ToSlash(filepath.Clean(rel))) {
			continue
		}
		errs = append(errs, fmt.Errorf("the build of %s writes %s inside the contract directory, which changes the bundle after the plan packed it; add %s to .pactoignore", a.Name, a.SBOM, filepath.ToSlash(filepath.Clean(rel))))
	}
	return errors.Join(errs...)
}

// ignoredIn says whether .pactoignore leaves rel out, itself or by a parent
// folder, as packing the bundle does.
func ignoredIn(m *ignore.Matcher, rel string) bool {
	for p, dir := rel, false; p != "."; p, dir = path.Dir(p), true {
		if m.Ignored(p, dir) {
			return true
		}
	}
	return false
}

// defaultPublishRegistry is the registry publish writes through: the CLI's
// credentials and plain-HTTP hosts.
func defaultPublishRegistry() publish.Registry {
	return publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries()...)
}

// execTools implements publish.Tools with docker buildx or the declared
// command, which fail closed when their program is missing, and signs with
// sigstore-go through reg.
type execTools struct {
	dir string
	run runner
	reg publish.Registry
	// signers holds each sign config's loaded signer, so a run loads a key,
	// signs a KMS probe or fetches the trusted root once, not per reference.
	signers map[contract.PublishSign]loadedSigner
	// built holds, per SBOM path, when the build that must write it started.
	built map[string]time.Time
}

type loadedSigner struct {
	s   signing.Signer
	p   signing.Policy
	err error
}

func newExecTools(dir string, run runner, reg publish.Registry) execTools {
	return execTools{dir: dir, run: run, reg: reg, signers: map[contract.PublishSign]loadedSigner{}, built: map[string]time.Time{}}
}

// Build implements publish.Tools.
func (t execTools) Build(ctx context.Context, a contract.PublishArtifact, out string, env []string) error {
	if a.SBOM != "" {
		// ponytail: second precision, as some file systems keep no finer
		// mtime; a stale file written in the same second still passes.
		t.built[t.abs(a.SBOM)] = time.Now().Truncate(time.Second)
	}
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

// loadSigner opens the configured signer before anything is built: a missing
// key file, a wrong COSIGN_PASSWORD, an unreachable KMS or keyless signing
// with no OIDC token refuses the plan, not a publish that already pushed. The
// signer is kept for the run.
func (t execTools) loadSigner(ctx context.Context, c *contract.Contract) error {
	if c.Publish == nil || c.Publish.Sign == nil {
		return nil // the sign check refuses it
	}
	sign := *c.Publish.Sign
	if sign.Key == "" && sign.KMS == "" {
		// The token is read only when signing, so check it is there.
		if err := signing.HasIDToken(); err != nil {
			return err
		}
	}
	_, _, err := t.signing(ctx, sign)
	return err
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

// SBOM implements publish.Tools. It reads the file only when this publish
// built the image and the build wrote the file since it started: anything
// older is not this build's SBOM and is never attached or signed as one.
func (t execTools) SBOM(path string) ([]byte, error) {
	abs := t.abs(path)
	start, ok := t.built[abs]
	if !ok {
		return nil, fmt.Errorf("%s: this publish did not build the image, so nothing ties the file to it", path)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.ModTime().Before(start) {
		return nil, fmt.Errorf("%s was last written at %s, before the build started at %s: the build must write its SBOM", path, st.ModTime().Format(time.RFC3339), start.Format(time.RFC3339))
	}
	return os.ReadFile(abs)
}

// abs is path relative to the contract directory.
func (t execTools) abs(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.dir, path)
}
