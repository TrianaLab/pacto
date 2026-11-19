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

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
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
	// Image is a digest-pinned image (repo@sha256:...) a contract with no
	// publish section deploys: Pacto builds, signs and copies none.
	Image string
	// Registry is where a derived publish section publishes; empty lets the
	// CI context and the workload image decide.
	Registry string
	// SignKey signs a derived publish section with this cosign key file, a
	// relative path read from the working directory like any CLI file flag;
	// NoTransparencyLog keeps its signatures out of the transparency log.
	SignKey           string
	NoTransparencyLog bool
	// Getenv reads the CI context and SOURCE_DATE_EPOCH; nil is an empty
	// environment, so the CLI and the MCP adapter hand the runner's in.
	Getenv func(string) string
	// Bundle, when set, is the bundle read from Path that publishes, in place
	// of a fresh read of Path: pacto ci hands in the bundle it raised in
	// memory, so the bytes it judged are the bytes validated and pushed.
	Bundle *contract.Bundle
	// Index is the organization index repository the entry is written to:
	// "" derives it from the bundle repository, IndexNone writes none.
	Index string
	// unchanged is the bundle digest pacto ci found the declared version
	// holding with this content: Publish then writes nothing once that
	// bundle verifies as the publisher's and its deploy artifact is there.
	unchanged string
}

// flagErr refuses flag values that cannot work whatever the contract says.
func (o PublishOptions) flagErr() error {
	switch {
	case o.Image != "" && !strings.Contains(o.Image, "@sha256:"):
		return errors.New("--image needs a digest: a tag can move")
	case o.Image != "":
		return digestErr("--image", o.Image)
	case o.NoTransparencyLog && o.SignKey == "":
		return errors.New("--no-transparency-log needs --sign-key: keyless signing needs the transparency log")
	}
	return nil
}

// digestErr refuses an image ref whose digest is not a sha256 digest, so a
// truncated one is never signed into the images record as deployed.
func digestErr(what, ref string) error {
	_, digest, _ := strings.Cut(ref, "@")
	if h, err := v1.NewHash(digest); err != nil || h.Algorithm != "sha256" {
		return fmt.Errorf("%s %s: the digest is not a sha256 digest (sha256:<64 hex>)", what, ref)
	}
	return nil
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
	if ci.Detect(opts.Getenv).Base.Value != "" {
		return nil, errors.New("refused: " + RefusedPullRequest)
	}
	if err := opts.flagErr(); err != nil {
		return nil, err
	}
	if opts.SignKey != "" {
		// The derived section resolves a relative key against the contract
		// directory, which is right only for a key the contract declares.
		abs, err := s.env().absPath(opts.SignKey)
		if err != nil {
			return nil, fmt.Errorf("--sign-key %s: %w", opts.SignKey, err)
		}
		opts.SignKey = abs
	}
	// The record names the revision the manifests are stamped with.
	revision := s.localInputs(path, opts.Revision).Commit
	cat, err := s.completionCatalog(ctx)
	if err != nil {
		return nil, err
	}
	inputs := s.localInputs(path, revision)
	c, raw, fsys, err := publishedBundle(ctx, path, opts, s.BundleStore, inputs, cat)
	if err != nil {
		return nil, err
	}
	env := s.env()
	b, d, err := deriveSection(ctx, path, &contract.Bundle{Contract: c, RawYAML: raw, FS: fsys}, opts, env.run, inputs)
	if err != nil {
		return nil, err
	}
	in := publish.Input{Bundle: b, Revision: revision, Source: ciSource(os.Getenv)}
	pub := c.Publish
	if d != nil {
		pub, in.Publish, in.Builtin, in.Created, in.Images = d.pub, d.pub, d.builtins, d.created, d.images
	} else {
		d = &derived{}
	}
	reg := env.publishRegistry()
	tools := newExecTools(path, env.run, reg)
	vb, done, dv, err := versioned(ctx, env.run, reg, tools, path, b, pub, d, revision, inputs, opts.unchanged)
	if err != nil || done != nil {
		return s.alreadyPublished(ctx, tools, b, pub, done, err, opts)
	}
	b = vb
	in.Bundle, c, fsys = b, b.Contract, b.FS
	notes := append(d.notes, dv.Notes...)
	in.Checks = append(publishChecks(d), validationCheck(s.guaranteeStage(ctx, path, c)),
		checkOf("references", rejectLocalDeps(c), rejectLocalRefs(c)),
		checkOf("lock", s.verifyLockIfPresent(ctx, path, b)), checkOf("build outputs", sbomsIgnored(pub, path)))
	in.Preflight = preflight(ctx, env, tools, path, pub, d.programs, fsys, opts.Revision == "" && in.Revision != "", in.Revision)
	if opts.Plan {
		return &publish.Result{Plan: derivedFrom(publish.MakePlan(ctx, reg, in), dv), Notes: notes}, nil
	}
	res, err := publish.Apply(ctx, reg, tools, in)
	res.Plan = derivedFrom(res.Plan, dv)
	if errors.Is(err, publish.ErrNotReady) {
		res.Refusal = keepRefusal(ctx, env, tools, b, pub, res.Plan)
	}
	if err == nil {
		res.Signer = tools.signer(publish.SignOf(pub))
		err = s.readBackAndIndex(ctx, reg, tools, b, pub, res, opts.Index)
	}
	res.Notes = notes
	return res, err
}

// readBackAndIndex confirms what res published and then writes its
// organization index entry, from b with the effective publish section pub,
// claiming the policy that signed it. Only the read-back can fail.
func (s *Service) readBackAndIndex(ctx context.Context, reg publish.Registry, tools execTools, b *contract.Bundle, pub *contract.Publish, res *publish.Result, override string) error {
	if err := readBack(ctx, reg, res); err != nil {
		return err
	}
	s.indexVersion(ctx, tools, b, pub, res, override)
	return nil
}

// alreadyPublished is done, a version found already published, with its
// organization index entry written unless err or opts.Plan: re-running a
// publish backfills a missing entry.
func (s *Service) alreadyPublished(ctx context.Context, tools execTools, b *contract.Bundle, pub *contract.Publish, done *publish.Result, err error, opts PublishOptions) (*publish.Result, error) {
	if err == nil && !opts.Plan {
		s.indexVersion(ctx, tools, b, pub, done, opts.Index)
	}
	return done, err
}

// indexVersion writes the organization index entry of res's version into
// res.Index, from b at that version with the effective publish section pub,
// claiming the policy that signed it.
func (s *Service) indexVersion(ctx context.Context, tools execTools, b *contract.Bundle, pub *contract.Publish, res *publish.Result, override string) {
	c := *b.Contract
	c.Service.Version = res.Plan.Version // an unchanged derived version is not pinned in b
	_, policy, _ := tools.signing(ctx, publish.SignOf(pub))
	res.Index = s.keepIndexEntry(ctx, &contract.Bundle{Contract: &c}, publish.Repo(pub.Bundle)+":"+res.Plan.Version, res.Digests[publish.BundleItem], publisherOf(policy), override)
}

// publishedBundle is the bundle Publish publishes, validated and made
// publishable: opts.Bundle when set, else path read and completed with in
// and cat.
func publishedBundle(ctx context.Context, path string, opts PublishOptions, store oci.BundleStore, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.Contract, []byte, fs.FS, error) {
	if opts.Bundle != nil {
		return validatedFull(ctx, path, opts.Bundle, store, true)
	}
	return loadAndValidateFull(ctx, path, opts.Overrides, store, in, cat, true)
}

// versioned is b at the version it publishes as, or done when nothing is to
// be written: derived and pinned when service.version is undeclared, as
// declared otherwise. A derived section that cannot have its image (d.refuse)
// refuses only once something is to be published: an unchanged service
// whose image is supplied by --image needs none.
func versioned(ctx context.Context, run runner, reg publish.Registry, tools execTools, path string, b *contract.Bundle, pub *contract.Publish, d *derived, revision string, in contract.InferredInputs, unchanged string) (*contract.Bundle, *publish.Result, publish.Derived, error) {
	if b.Contract.Service.Version != "" {
		switch {
		case unchanged != "":
			res, err := publishedUnchanged(ctx, reg, tools, b, pub, revision, unchanged)
			return nil, res, publish.Derived{}, err
		case d.refuse != nil:
			return nil, nil, publish.Derived{}, d.refuse
		}
		return b, nil, publish.Derived{}, nil
	}
	pinned, done, dv, err := derivedBundle(ctx, run, reg, tools, path, b, pub, d.alt, revision, in)
	switch {
	case done != nil:
		return nil, done, dv, nil
	case d.refuse != nil:
		return nil, nil, dv, d.refuse
	}
	return pinned, nil, dv, err
}

// publishedUnchanged is the publish of a declared version pacto ci found
// holding this content at digest: nothing is written, once that bundle is
// signed by the publisher identity and the deploy artifact, the commit
// point, is there. A tag read without either is never "unchanged".
func publishedUnchanged(ctx context.Context, reg publish.Registry, tools execTools, b *contract.Bundle, pub *contract.Publish, revision, digest string) (*publish.Result, error) {
	repo, version := publish.Repo(pub.Bundle), b.Contract.Service.Version
	if err := tools.Verify(ctx, publish.SignOf(pub), repo+"@"+digest); err != nil {
		return nil, fmt.Errorf("%s:%s holds this content, but not signed by the publisher identity: %w", repo, version, err)
	}
	deploy, notes := unchangedDeploy(ctx, reg, tools, pub, version)
	if len(notes) > 0 {
		return nil, fmt.Errorf("%s: the publish of %s stopped before its commit point; re-run it from the commit that published it, or publish a higher service.version", notes[0], version)
	}
	res := unchangedResult(b, repo, revision, publish.Derived{Version: version, Baseline: version, BaselineDigest: digest})
	res.Deploy = deploy
	return res, nil
}

// derivedFrom is p with a derived version's baseline and classification:
// the verified baseline the version was measured from, never the newest tag
// below it the version rule reads, which may be one nobody verified.
func derivedFrom(p publish.Plan, dv publish.Derived) publish.Plan {
	if dv.Baseline != "" {
		p.Baseline, p.Classification = dv.Baseline, dv.Classification
	}
	return p
}

// RefusedPullRequest is why a pull or merge request pipeline never publishes.
const RefusedPullRequest = "pull request pipelines do not publish; publish runs on the merged commit"

// readBack reads every tag of res again once the deploy tag, the commit
// point, is written: a registry has no conditional tag write, so a write
// landing between this publish's read and now is seen and reported, never
// taken as published.
func readBack(ctx context.Context, reg publish.Registry, res *publish.Result) error {
	for _, it := range res.Plan.Items {
		now, err := reg.Head(ctx, it.Ref)
		if err != nil {
			return fmt.Errorf("reading %s back: %w", it.Ref, err)
		}
		if want := res.Digests[it.Name]; now == nil || now.Digest != want {
			return fmt.Errorf("%w: %s no longer holds %s once this publish finished; another publish wrote it meanwhile", publish.ErrTagRace, it.Ref, want)
		}
	}
	return nil
}

// publishChecks is how a derived section has its image, said with the
// plan's checks; none for a declared section.
func publishChecks(d *derived) []publish.Check {
	if d.build == "" {
		return nil
	}
	return []publish.Check{{Name: "build", Passed: true, Message: d.build}}
}

// ciSource is the URL of the repository CI builds from: GitHub Actions, else
// GitLab CI (the order contextDefault in internal/cli/runrecord.go reads), else
// empty when not in CI.
func ciSource(getenv func(string) string) string {
	if srv, repo := getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"); srv != "" && repo != "" {
		return srv + "/" + repo
	}
	return getenv("CI_PROJECT_URL")
}

// preflight checks what the absent items need: the builder of each artifact
// still to build, and, when anything is written, the signer and a clean
// checkout (askGit says whether to ask git). A re-run of a version already
// published needs none of them, so `--propose` still works from a machine
// with no buildx, no OIDC token or an unrelated uncommitted file.
//
// pub is the effective publish section and programs what its detected
// image builders run, by artifact.
func preflight(ctx context.Context, env sysDeps, tools execTools, path string, pub *contract.Publish, programs map[string][]string, fsys fs.FS, askGit bool, rev string) func(publish.Plan) []publish.Check {
	return func(p publish.Plan) []publish.Check {
		absent := map[string]bool{}
		for _, it := range p.Items {
			absent[it.Name] = it.State == publish.Absent
		}
		sign := keyOutsideBundle(pub, path, fsys)
		if p.Missing > 0 {
			sign = errors.Join(sign, tools.loadSigner(ctx, pub))
		}
		checks := []publish.Check{checkOf("signing key", sign), checkOf("builders", builders(ctx, env, path, pub, programs, absent))}
		if askGit && p.Missing > 0 {
			checks = append(checks, checkOf("clean checkout", cleanCheckout(ctx, env.run, path, rev)))
		}
		return checks
	}
}

// validationCheck is the validation check after the guarantees stage, the one
// validation stage that reads other contracts. An unmet guarantee of a required
// dependency fails it; a warning, such as a provider it could not fetch
// (GUARANTEE_UNVERIFIED), passes it and leads its message, so the row never
// reads as a clean pass.
func validationCheck(st validation.Stage) publish.Check {
	if len(st.Errors) > 0 {
		msgs := make([]string, len(st.Errors))
		for i, e := range st.Errors {
			msgs[i] = e.Code + ": " + e.Message
		}
		return publish.Check{Name: "validation", Message: strings.Join(msgs, "; ")}
	}
	if len(st.Warnings) == 0 {
		return publish.Check{Name: "validation", Passed: true, Message: "the contract is valid"}
	}
	msgs := make([]string, len(st.Warnings))
	for i, w := range st.Warnings {
		msgs[i] = "warning " + w.Code + ": " + w.Message
	}
	return publish.Check{Name: "validation", Passed: true, Message: strings.Join(msgs, "; ") + "; the contract is otherwise valid"}
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
// dockerfile build, the program a command build runs and every program a
// detected image builder runs (programs, by artifact), for each artifact
// absent names. It runs no build.
//
// ponytail: a builder on the plain docker driver passes, though it writes an
// OCI layout only with the containerd image store; the build says so.
func builders(ctx context.Context, env sysDeps, dir string, pub *contract.Publish, programs map[string][]string, absent map[string]bool) error {
	if pub == nil {
		return nil
	}
	var errs []error
	buildx := false
	for _, a := range pub.Artifacts {
		switch {
		case !absent[a.Name]:
		case len(programs[a.Name]) > 0:
			for _, prog := range programs[a.Name] {
				errs = append(errs, lookProgram(env, dir, a.Name, prog))
			}
		case a.Build.Dockerfile != nil && !buildx:
			buildx = true
			ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
			_, err := env.run(ctx, dir, nil, "docker", "buildx", "inspect")
			cancel()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s is built from a Dockerfile, but docker buildx has no usable builder: %w", a.Name, err))
			}
		case len(a.Build.Command) > 0:
			errs = append(errs, lookProgram(env, dir, a.Name, a.Build.Command[0]))
		}
	}
	return errors.Join(errs...)
}

// lookProgram refuses a program the build of artifact name runs that is not
// an executable here. A relative path is in dir, where the build runs.
func lookProgram(env sysDeps, dir, name, program string) error {
	prog := program
	if strings.ContainsRune(prog, '/') && !filepath.IsAbs(prog) {
		prog = filepath.Join(dir, prog) // the build runs in the contract directory
		if !strings.ContainsRune(prog, filepath.Separator) {
			// Join(".", "./render.sh") is "render.sh", which LookPath
			// would search $PATH for.
			prog = "." + string(filepath.Separator) + prog
		}
	}
	if _, err := env.lookPath(prog); err != nil {
		return fmt.Errorf("the build of %s runs %s, which is not an executable here: %w", name, program, err)
	}
	return nil
}

// keyOutsideBundle refuses a signing key file that would be packed into the
// published bundle: a private key in a public artifact.
func keyOutsideBundle(pub *contract.Publish, dir string, fsys fs.FS) error {
	key := publish.SignOf(pub).Key
	rel := key
	if filepath.IsAbs(key) {
		base, _ := filepath.Abs(dir) // ponytail: fails only without a working directory, then Rel fails and the key counts as outside
		rel, _ = filepath.Rel(base, key)
	}
	if key == "" || !filepath.IsLocal(rel) {
		return nil
	}
	if _, err := fs.Stat(fsys, filepath.ToSlash(filepath.Clean(rel))); err == nil {
		return fmt.Errorf("the signing key %s would be published inside the bundle; add it to .pactoignore or keep it outside the contract directory", key)
	}
	return nil
}

// sbomsIgnored refuses an SBOM path inside the contract directory that
// .pactoignore does not leave out: the build writes it after the plan packed
// the bundle, so the bundle would change under the publish after every image
// was pushed.
func sbomsIgnored(pub *contract.Publish, path string) error {
	if pub == nil {
		return nil
	}
	_, dir, _ := resolveLocalPath(path) // ponytail: the bundle was just loaded from it
	m, err := ignore.Load(os.DirFS(dir))
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range pub.Artifacts {
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
		_, err := t.run(ctx, t.dir, env, "docker", buildxArgs(d, out, env, false)...)
		return err
	}
	_, err := t.run(ctx, t.dir, env, a.Build.Command[0], a.Build.Command[1:]...)
	return err
}

// buildxArgs are the docker arguments that build d into an OCI layout at
// out, labelled with the version and revision in env. rewrite stamps every
// layer with SOURCE_DATE_EPOCH, so one commit builds one image.
func buildxArgs(d *contract.DockerfileBuild, out string, env []string, rewrite bool) []string {
	output := "type=oci,dest=" + out + ",tar=false"
	if rewrite {
		output += ",rewrite-timestamp=true"
	}
	args := []string{"buildx", "build", "--output", output}
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
	return append(args, d.Context)
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
func (t execTools) loadSigner(ctx context.Context, pub *contract.Publish) error {
	sign := publish.SignOf(pub)
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
		identity, issuer := sign.Identity, sign.Issuer
		if identity == "" {
			if identity, issuer, err = tokenIdentity(ctx); err != nil {
				return signing.Signer{}, signing.Policy{}, err
			}
		}
		s, err := signing.Keyless(fulcioURL, tsaURL, rekorURL, signing.IDToken(""), root)
		return s, signing.Policy{Identity: identity, Issuer: issuer, Root: root, Tlog: rekorURL != ""}, err
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

// signer is who signed for sign, as a row prints it: keyless (<identity>),
// key <path> or kms <uri>; "" when the signer never loaded.
func (t execTools) signer(sign contract.PublishSign) string {
	l, ok := t.signers[sign]
	switch {
	case !ok || l.err != nil:
		return ""
	case sign.Key != "":
		return "key " + sign.Key
	case sign.KMS != "":
		return "kms " + sign.KMS
	}
	return "keyless (" + l.p.Identity + ")"
}

// tokenIdentity is the identity and issuer the CI's OIDC token names, which a
// signer with no declared identity signs as and its signatures verify against.
func tokenIdentity(ctx context.Context) (string, string, error) {
	tok, err := signing.IDToken("")(ctx)
	if err != nil {
		return "", "", err
	}
	return signing.TokenIdentity(tok)
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
			ref, p.Identity, p.Issuer, err)
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
