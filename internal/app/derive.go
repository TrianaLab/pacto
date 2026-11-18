package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// derivedImage is the name of the image artifact a derived publish section
// builds.
const derivedImage = "image"

// derived is the publish section Pacto derives, in memory, for a contract
// that declares none, and what carrying it out needs.
type derived struct {
	pub *contract.Publish
	// builtins build the derived artifacts; programs are what they run, so
	// the plan checks each is there before anything is built.
	builtins map[string]publish.Builtin
	programs []string
	// alt is the bundle repository the layout did not pick.
	alt string
	// created is the commit time stamped on derived artifacts, "" unknown.
	created string
	// build says how the image is had, shown with the plan's checks.
	build string
	notes []string
}

// derivedOpts are the flags that shape a derived publish section.
type derivedOpts struct {
	image, signKey string
	noTLog         bool
}

// derivePublish is the publish section for the service name in dir under
// registry: the bundle, and the image built with the one tool bp names,
// every program run directly, never through a shell. A supplied image
// (--image, or a workload image pinned by digest) is built, signed and
// copied by nobody. With no tool and no supplied image, Build's findings
// refuse it. epoch is the commit time builders get as SOURCE_DATE_EPOCH.
func derivePublish(dir, name, registry string, bp discover.BuildPlan, findings []contract.InferredFinding, opts derivedOpts, run runner, epoch string) (*derived, error) {
	imageRepo := ""
	if bp.Image != "" {
		imageRepo, _ = discover.ImageRepo(bp.Image)
	}
	bundle, image, _ := publish.Layout(registry, name, imageRepo)
	d := &derived{pub: &contract.Publish{Bundle: "oci://" + bundle}, alt: publish.AltLayout(registry, name, imageRepo), created: createdAt(epoch)}
	if opts.signKey != "" {
		d.pub.Sign = &contract.PublishSign{Key: opts.signKey}
		if opts.noTLog {
			off := false
			d.pub.Sign.TransparencyLog = &off
		}
	}
	switch {
	case opts.image != "":
		d.build = "image supplied, not built by Pacto: " + opts.image
	case bp.Pinned:
		d.build = "image supplied, not built by Pacto: " + bp.Image
	case bp.Tool != "":
		build, programs := imageBuilder(dir, bp, image, run, epoch)
		file, _, _ := strings.Cut(bp.ToolSource, ":")
		d.build = fmt.Sprintf("%s (%s), image %s", bp.Tool, file, image)
		d.pub.Artifacts = []contract.PublishArtifact{{Name: derivedImage, Type: contract.ArtifactImage, Ref: "oci://" + image}}
		d.builtins, d.programs = map[string]publish.Builtin{derivedImage: build}, programs
		if epoch == "" {
			d.notes = append(d.notes, "not reproducible: no commit time (set SOURCE_DATE_EPOCH)")
		}
	case len(findings) > 0:
		msgs := make([]string, len(findings))
		for i, f := range findings {
			msgs[i] = "finding: " + f.Message
		}
		return nil, fmt.Errorf("%s declares no publish section and Pacto cannot derive one: %s", name, strings.Join(msgs, "; "))
	default:
		d.build = "no image to build"
	}
	return d, nil
}

// imageBuilder builds the image with bp's tool into the image repository
// repo, and names the programs it runs. ponytail: one image per service; a
// second image is a finding asking for a publish section.
func imageBuilder(dir string, bp discover.BuildPlan, repo string, run runner, epoch string) (publish.Builtin, []string) {
	do := func(ctx context.Context, env []string, name string, args ...string) error {
		if epoch != "" {
			env = append(env, "SOURCE_DATE_EPOCH="+epoch)
		}
		_, err := run(ctx, dir, env, name, args...)
		return err
	}
	switch bp.Tool {
	case discover.ToolSkaffold:
		return func(ctx context.Context, out string, env []string) error {
			file := filepath.Join(out, "build.json")
			if err := do(ctx, env, "skaffold", "build", "--push=false", "--file-output="+file, "--tag=pacto-"+rev12(env)); err != nil {
				return err
			}
			built, err := skaffoldImage(file)
			if err != nil {
				return err
			}
			return do(ctx, env, "docker", "image", "save", "-o", filepath.Join(out, "image.tar"), built)
		}, []string{"skaffold", "docker"}
	case discover.ToolKo:
		return func(ctx context.Context, out string, env []string) error {
			return do(ctx, append(env, "KO_DOCKER_REPO="+repo), "ko", "build", "--push=false", "--bare", "--sbom=none", "--oci-layout-path="+out, bp.Main)
		}, []string{"ko"}
	case discover.ToolJib:
		args := []string{"-q", "compile", "jib:buildTar"}
		if strings.Contains(bp.Wrapper, "gradle") {
			args = []string{"jibBuildTar"}
		}
		return func(ctx context.Context, out string, env []string) error {
			return do(ctx, env, bp.Wrapper, append(args, "-Dimage="+repo+":"+envValue(env, "PACTO_VERSION"), "-Djib.outputPaths.tar="+filepath.Join(out, "image.tar"))...)
		}, []string{bp.Wrapper}
	case discover.ToolBuildpacks:
		return func(ctx context.Context, out string, env []string) error {
			tag := repo + ":pacto-" + rev12(env)
			if err := do(ctx, env, "pack", "build", tag, "--path", "."); err != nil {
				return err
			}
			return do(ctx, env, "docker", "image", "save", "-o", filepath.Join(out, "image.tar"), tag)
		}, []string{"pack", "docker"}
	}
	file := strings.TrimSuffix(bp.ToolSource, ":1")
	df := &contract.DockerfileBuild{Context: path.Dir(file)}
	if path.Base(file) != "Dockerfile" {
		df.File = file
	}
	return func(ctx context.Context, out string, env []string) error {
		return do(ctx, env, "docker", buildxArgs(df, out, env, epoch != "")...)
	}, []string{"docker"}
}

// skaffoldImage is the one image skaffold build wrote to its build file.
func skaffoldImage(file string) (string, error) {
	var out struct {
		Builds []struct {
			Tag string `json:"tag"`
		} `json:"builds"`
	}
	data, err := fs.ReadFile(os.DirFS(filepath.Dir(file)), filepath.Base(file))
	if err == nil {
		err = json.Unmarshal(data, &out)
	}
	if err != nil || len(out.Builds) != 1 || out.Builds[0].Tag == "" {
		return "", errors.Join(fmt.Errorf("skaffold build did not name exactly one image in %s", filepath.Base(file)), err)
	}
	return out.Builds[0].Tag, nil
}

// envValue is the value of key in env.
func envValue(env []string, key string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v
		}
	}
	return ""
}

// rev12 is the first twelve characters of the source revision in env.
func rev12(env []string) string {
	rev := envValue(env, "PACTO_REVISION")
	return rev[:min(12, len(rev))]
}

// commitEpoch is the commit time builders get as SOURCE_DATE_EPOCH: the
// environment's value, else HEAD's commit time, else "" (unknown). It only
// reads the repository.
func commitEpoch(ctx context.Context, getenv func(string) string, run runner, dir string) string {
	if getenv != nil && getenv("SOURCE_DATE_EPOCH") != "" {
		return getenv("SOURCE_DATE_EPOCH")
	}
	ctx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	out, err := run(ctx, dir, nil, "git", "show", "-s", "--format=%ct", "HEAD")
	sec := strings.TrimSpace(string(out))
	if _, perr := strconv.ParseInt(sec, 10, 64); err != nil || perr != nil {
		return ""
	}
	return sec
}

// createdAt is epoch (seconds) as the RFC 3339 created annotation, "" when
// it is not a number.
func createdAt(epoch string) string {
	sec, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format(time.RFC3339)
}

// deriveSection is the publish section Publish carries out for b, the
// bundle with a supplied image recorded, and nil for a declared section. The
// flags that shape a derived section refuse a declared one.
func deriveSection(ctx context.Context, dir string, b *contract.Bundle, opts PublishOptions, run runner, in contract.InferredInputs) (*contract.Bundle, *derived, error) {
	c := b.Contract
	if c.Publish != nil {
		if opts.Image != "" || opts.Registry != "" || opts.SignKey != "" {
			return nil, nil, errors.New("--image, --registry and --sign-key apply only when the contract declares no publish section")
		}
		return b, nil, nil
	}
	if cur, behind := contract.Behind(c.PactoVersion); behind {
		return nil, nil, fmt.Errorf("%s declares no publish section; Pacto derives one for pactoVersion %s: set pactoVersion: %q", c.Service.Name, cur, cur)
	}
	sc, err := discover.Dir(dir)
	if err != nil {
		return nil, nil, err
	}
	bp, findings := discover.Build(dir, sc)
	registry, err := deriveRegistry(opts, bp)
	if err != nil {
		return nil, nil, err
	}
	d, err := derivePublish(dir, c.Service.Name, registry, bp, findings,
		derivedOpts{image: opts.Image, signKey: opts.SignKey, noTLog: opts.NoTransparencyLog}, run, commitEpoch(ctx, opts.Getenv, run, dir))
	if err == nil && opts.Image != "" {
		b, err = suppliedImage(b, opts.Image, in)
	}
	return b, d, err
}

// deriveRegistry is the registry a derived section publishes to: --registry,
// else what the CI context and the workload image say.
func deriveRegistry(opts PublishOptions, bp discover.BuildPlan) (string, error) {
	flag, err := ci.NormalizeRegistry(opts.Registry)
	if err != nil {
		return "", err
	}
	var images []ci.ImageRef
	if bp.Image != "" {
		images = []ci.ImageRef{{Image: bp.Image, Source: bp.ImageSource}}
	}
	v, notes := ci.ResolveRegistry(flag, ci.Detect(opts.Getenv), images)
	if v.Value == "" {
		return "", fmt.Errorf("no registry to publish to%s: pass --registry", strings.Join(append([]string{""}, notes...), "; "))
	}
	return v.Value, nil
}

// suppliedImage is b with its record saying the image is supplied by
// --image, not built by Pacto.
func suppliedImage(b *contract.Bundle, ref string, in contract.InferredInputs) (*contract.Bundle, error) {
	rec, err := recordOf(b.FS, in)
	if err != nil {
		return nil, err
	}
	rec.Fields = slices.DeleteFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "publish.build" })
	rec.Fields = append(rec.Fields, contract.Inferred{Field: "publish.build", Value: "image supplied, not built by Pacto: " + ref,
		Source: "flag:--image", Confidence: contract.ConfidenceLikely})
	return &contract.Bundle{Contract: b.Contract, RawYAML: b.RawYAML, FS: contract.WithInferredRecord(b.FS, rec.Marshal())}, nil
}
