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
	return publish.Apply(ctx, reg, execTools{dir: path, run: env.run}, in)
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

// execTools implements publish.Tools with docker buildx, the declared command,
// cosign and syft. Each fails closed when its program is missing.
type execTools struct {
	dir string
	run runner
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

// Sign implements publish.Tools: a valid signature by the configured identity
// is kept, otherwise cosign signs.
func (t execTools) Sign(ctx context.Context, sign contract.PublishSign, ref string) (bool, error) {
	verify := []string{"verify"}
	signArgs := []string{"sign", "--yes"}
	if insecureHost(ref) {
		verify = append(verify, "--allow-http-registry")
		signArgs = append(signArgs, "--allow-http-registry")
	}
	if sign.TransparencyLog != nil && !*sign.TransparencyLog {
		verify = append(verify, "--insecure-ignore-tlog")
		signArgs = append(signArgs, "--tlog-upload=false", "--use-signing-config=false")
	}
	switch {
	case sign.Key != "":
		key := sign.Key
		if !filepath.IsAbs(key) {
			key = filepath.Join(t.dir, key)
		}
		pub, err := t.publicKey(ctx, key)
		if err != nil {
			return false, err
		}
		defer func() { _ = os.Remove(pub) }()
		verify = append(verify, "--key", pub)
		signArgs = append(signArgs, "--key", key)
	case sign.KMS != "":
		verify = append(verify, "--key", sign.KMS)
		signArgs = append(signArgs, "--key", sign.KMS)
	default:
		// ponytail: keyless declares no identity, so any valid keyless signature
		// counts as already signed; declare the identity if that is too loose.
		verify = append(verify, "--certificate-identity-regexp", ".*", "--certificate-oidc-issuer-regexp", ".*")
	}
	if _, err := t.run(ctx, t.dir, nil, "cosign", append(verify, ref)...); err == nil {
		return false, nil
	}
	if _, err := t.run(ctx, t.dir, nil, "cosign", append(signArgs, ref)...); err != nil {
		return false, err
	}
	return true, nil
}

// publicKey writes the public half of a cosign key file to a temporary file.
func (t execTools) publicKey(ctx context.Context, key string) (string, error) {
	out, err := t.run(ctx, t.dir, nil, "cosign", "public-key", "--key", key)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "pacto-cosign-*.pub")
	if err != nil {
		return "", err
	}
	_, err = f.Write(out)
	return f.Name(), errors.Join(err, f.Close())
}

// SBOM implements publish.Tools.
func (t execTools) SBOM(ctx context.Context, ref string) ([]byte, error) {
	var env []string
	if insecureHost(ref) {
		env = append(env, "SYFT_REGISTRY_INSECURE_USE_HTTP=true")
	}
	return t.run(ctx, t.dir, env, "syft", "scan", "registry:"+ref, "-o", "spdx-json", "-q")
}
