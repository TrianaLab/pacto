package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// derivedImage is the name of the image artifact a derived publish section
// builds.
const derivedImage = "image"

// derived is the publish section Pacto derives, in memory, for a contract
// that declares none, and what carrying it out needs.
type derived struct {
	pub *contract.Publish
	// builtins build the derived artifacts; programs are what each image
	// builder runs, by artifact, so the plan checks each is there before
	// anything is built.
	builtins map[string]publish.Builtin
	programs map[string][]string
	// alt is the bundle repository the layout did not pick; deploy is the
	// repository of the deploy artifact.
	alt, deploy string
	// created is the commit time stamped on derived artifacts, "" unknown.
	created string
	// build says how the image is had, shown with the plan's checks.
	build string
	notes []string
	// refuse is why the image cannot be had (no tool, no supplied image):
	// it refuses a publish that has something to write.
	refuse error
	// images are the images deployed without being built: supplied
	// (--image) or pinned by digest in the workload.
	images []publish.ImageRecord
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
// are d.refuse. epoch is the commit time builders get as SOURCE_DATE_EPOCH.
func derivePublish(dir, name, registry string, bp discover.BuildPlan, findings []contract.InferredFinding, opts derivedOpts, run runner, epoch string) (*derived, error) {
	imageRepo := workloadRepo(bp)
	bundle, image, deploy := publish.Layout(registry, name, imageRepo)
	d := &derived{pub: &contract.Publish{Bundle: "oci://" + bundle}, alt: publish.AltLayout(registry, name, imageRepo), deploy: deploy, created: createdAt(epoch)}
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
		d.images = []publish.ImageRecord{suppliedRecord(opts.image, "flag:--image")}
	case bp.Pinned:
		if err := digestErr("workload image", bp.Image); err != nil {
			return nil, err
		}
		d.build = "image supplied, not built by Pacto: " + bp.Image
		d.images = []publish.ImageRecord{suppliedRecord(bp.Image, bp.ImageSource)}
	case bp.Tool != "":
		build, programs := imageBuilder(dir, bp, image, run, epoch)
		file, _, _ := strings.Cut(bp.ToolSource, ":")
		d.build = fmt.Sprintf("%s (%s), image %s", bp.Tool, file, image)
		d.pub.Artifacts = []contract.PublishArtifact{{Name: derivedImage, Type: contract.ArtifactImage, Ref: "oci://" + image}}
		d.builtins, d.programs = map[string]publish.Builtin{derivedImage: build}, map[string][]string{derivedImage: programs}
		if epoch == "" {
			d.notes = append(d.notes, "not reproducible: no commit time (set SOURCE_DATE_EPOCH)")
		}
	case len(findings) > 0:
		msgs := make([]string, len(findings))
		for i, f := range findings {
			msgs[i] = "finding: " + f.Message
		}
		d.refuse = fmt.Errorf("%s declares no publish section and Pacto cannot derive one: %s", name, strings.Join(msgs, "; "))
	default:
		d.build = "no image to build"
	}
	return d, nil
}

// workloadRepo is the repository of the image bp's workload runs, "" for none.
func workloadRepo(bp discover.BuildPlan) string {
	if bp.Image == "" {
		return ""
	}
	repo, _ := discover.ImageRepo(bp.Image)
	return repo
}

// derivedBundleRepo is the repository a derived section for the service
// name in dir publishes its bundle to under registry, as derivePublish
// names it. A folder it cannot read has no workload image.
func derivedBundleRepo(dir, registry, name string) string {
	var bp discover.BuildPlan
	if sc, err := discover.Dir(dir); err == nil {
		bp, _ = discover.Build(dir, sc)
	}
	bundle, _, _ := publish.Layout(registry, name, workloadRepo(bp))
	return bundle
}

// suppliedRecord is the images-record entry of ref (repo@digest), an image
// this publish deploys without building it, had from source.
func suppliedRecord(ref, source string) publish.ImageRecord {
	repo, _ := discover.ImageRepo(ref)
	_, digest, _ := strings.Cut(ref, "@")
	return publish.ImageRecord{Name: derivedImage, Repository: repo, Digest: digest, Source: source}
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
// bundle with a supplied image recorded. A declared section is nil, or
// itself with the builds detection gives its artifacts that declare none.
// The flags that shape a derived section refuse a declared one.
func deriveSection(ctx context.Context, dir string, b *contract.Bundle, opts PublishOptions, run runner, in contract.InferredInputs) (*contract.Bundle, *derived, error) {
	c := b.Contract
	if c.Publish != nil {
		if opts.Image != "" || opts.Registry != "" || opts.SignKey != "" {
			return nil, nil, errors.New("--image, --registry and --sign-key apply only when the contract declares no publish section")
		}
		d, err := detectBuilds(ctx, dir, c.Publish, opts.Getenv, run)
		return b, d, err
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
	if err == nil {
		err = d.addDeploy(dir, sc, bp, findings, opts.Image)
	}
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
	v, notes := ci.ResolveRegistry(flag, ci.Value{}, ci.Detect(opts.Getenv), images)
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

// addDeploy adds the deploy artifact the folder's one deploy source gives:
// its manifests or its kustomization as a manifests artifact, or its chart,
// which is its own deploy artifact. Pacto writes no applier object: any
// applier that takes plain manifests or a Helm OCI chart deploys it. The
// image the workload names is pinned to the one this publish builds, or to
// supplied (--image). Several deploy sources, or a kustomization that leaves
// the folder, refuse it with Build's finding.
func (d *derived) addDeploy(dir string, sc *discover.Scan, bp discover.BuildPlan, findings []contract.InferredFinding, supplied string) error {
	if bp.Deploy == nil {
		var msgs []string
		for _, f := range findings {
			// ponytail: every build-tool finding asks for --image; the rest are the deploy's.
			if !strings.Contains(f.Message, "pass --image") {
				msgs = append(msgs, "finding: "+f.Message)
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("the deploy artifact cannot be assembled: %s", strings.Join(msgs, "; "))
		}
		return nil
	}
	// built: this publish builds the image, or --image supplies it; either
	// way the deploy pins it. A workload image pinned by digest is kept.
	bi, notes, err := deployBuiltin(dir, sc, bp, supplied != "" || bp.Tool != "", derivedImage, supplied)
	if err != nil {
		return err
	}
	name := deployType(*bp.Deploy)
	d.pub.Deploy = name
	d.pub.Artifacts = append(d.pub.Artifacts, contract.PublishArtifact{Name: name, Type: name, Ref: "oci://" + d.deploy})
	if d.builtins == nil {
		d.builtins = map[string]publish.Builtin{}
	}
	d.builtins[name] = bi
	d.notes = append(d.notes, notes...)
	return nil
}

// deployType is the artifact type the deploy source src gives.
func deployType(src discover.DeploySource) string {
	if src.Kind == discover.DeployChart {
		return contract.ArtifactChart
	}
	return contract.ArtifactManifests
}

// deployBuiltin assembles the deploy artifact from bp's deploy source, and
// says what it leaves out. built says an image is pinned in it: supplied
// (--image), else the one the image artifact img builds.
func deployBuiltin(dir string, sc *discover.Scan, bp discover.BuildPlan, built bool, img, supplied string) (publish.Builtin, []string, error) {
	set, err := readDeploy(dir, sc, *bp.Deploy)
	if err != nil {
		return nil, nil, err
	}
	from, where := "", ""
	if built && bp.Image != "" {
		from, _ = discover.ImageRepo(bp.Image)
		where, _, _ = strings.Cut(bp.ImageSource, ":")
		set.written, _, _ = strings.Cut(bp.Image, "@")
		if i := strings.LastIndex(set.written, ":"); i > strings.LastIndex(set.written, "/") {
			set.written = set.written[:i]
		}
	}
	if set.kind == discover.DeployChart {
		if _, values := firstMapping(set.files["values.yaml"]); built && contract.MapValue(values, "image") == nil {
			return nil, nil, fmt.Errorf("deploy: %s/values.yaml has no image: to pin the image this publish deploys in: declare publish", set.chartDir)
		}
	}
	return set.builtin(built, from, where, img, supplied), set.notes, nil
}

// detectBuilds is what builds each artifact of the declared section pub that
// declares no build (pactoVersion 2.5): the folder's one build tool for an
// image, Pacto's own assembly for manifests or a chart, pinning the image
// the section's one image artifact builds. It never adds an artifact, so a
// section with none stays a contract-only publish; nil when every artifact
// declares its build.
func detectBuilds(ctx context.Context, dir string, pub *contract.Publish, getenv func(string) string, run runner) (*derived, error) {
	var bare []int
	var images []string
	for i, a := range pub.Artifacts {
		if a.Build.Dockerfile == nil && len(a.Build.Command) == 0 {
			bare = append(bare, i)
		}
		if a.Type == contract.ArtifactImage {
			images = append(images, a.Name)
		}
	}
	if len(bare) == 0 {
		return nil, nil
	}
	sc, err := discover.Dir(dir)
	if err != nil {
		return nil, err
	}
	bp, findings := discover.Build(dir, sc)
	d := &derived{pub: pub, builtins: map[string]publish.Builtin{}, programs: map[string][]string{}}
	for _, i := range bare {
		a := pub.Artifacts[i]
		if a.Type == contract.ArtifactImage {
			err = d.detectImage(ctx, dir, a, images, bp, findings, getenv, run)
		} else {
			err = d.detectDeploy(dir, sc, a, pub.Artifacts[:i], images, bp, findings)
		}
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

// detectImage builds the image artifact a with the folder's one build tool.
// ponytail: one image per folder, as derived sections do; a second image
// artifact declares its build.
func (d *derived) detectImage(ctx context.Context, dir string, a contract.PublishArtifact, images []string, bp discover.BuildPlan, findings []contract.InferredFinding, getenv func(string) string, run runner) error {
	switch {
	case len(images) > 1:
		return fmt.Errorf("artifact %s declares no build, and Pacto detects the build of one image per folder: declare build on each of %s", a.Name, strings.Join(images, ", "))
	case bp.Tool == "":
		return noDetection(a, findings, "pass --image", "the folder shows no build tool")
	}
	bi, programs := imageBuilder(dir, bp, strings.TrimPrefix(a.Ref, "oci://"), run, commitEpoch(ctx, getenv, run, dir))
	d.builtins[a.Name], d.programs[a.Name] = bi, programs
	return nil
}

// detectDeploy assembles the manifests or chart artifact a from the
// folder's one deploy source, pinning the image of the section's one image
// artifact, which before (the artifacts declared ahead of a) must hold, so
// its digest is known when a is built.
func (d *derived) detectDeploy(dir string, sc *discover.Scan, a contract.PublishArtifact, before []contract.PublishArtifact, images []string, bp discover.BuildPlan, findings []contract.InferredFinding) error {
	if bp.Deploy == nil {
		return noDetection(a, findings, "", "the folder holds no manifests, kustomization or chart")
	}
	if got := deployType(*bp.Deploy); got != a.Type {
		return fmt.Errorf("artifact %s declares no build, and is a %s, but the folder deploys with a %s: declare build", a.Name, a.Type, got)
	}
	img := ""
	if len(images) == 1 {
		img = images[0]
		if !slices.ContainsFunc(before, func(b contract.PublishArtifact) bool { return b.Name == img }) {
			return fmt.Errorf("artifact %s pins the image %s, so declare %s before it", a.Name, img, img)
		}
	}
	bi, notes, err := deployBuiltin(dir, sc, bp, img != "", img, "")
	if err != nil {
		return err
	}
	d.builtins[a.Name] = bi
	d.notes = append(d.notes, notes...)
	return nil
}

// noDetection refuses the artifact a, which declares no build, with the
// findings that leave its build undetected (only those holding keep, when
// set: the rest are another part's), else with none.
func noDetection(a contract.PublishArtifact, findings []contract.InferredFinding, keep, none string) error {
	var msgs []string
	for _, f := range findings {
		if keep == "" || strings.Contains(f.Message, keep) {
			msgs = append(msgs, "finding: "+f.Message)
		}
	}
	if len(msgs) == 0 {
		msgs = []string{none}
	}
	return fmt.Errorf("artifact %s declares no build, and Pacto cannot detect one: %s", a.Name, strings.Join(msgs, "; "))
}

// deployFiles is what a derived deploy artifact ships, read once when the
// section is derived: the files by output path (for a chart, by path in the
// chart), the chart's folder and name, the Pacto resource's path and what
// was left out.
type deployFiles struct {
	kind, chartDir, chartName, resource string
	// written is the workload image's name as the manifests write it, with
	// no tag or digest: kustomize matches an images entry by it literally.
	written string
	files   map[string][]byte
	notes   []string
}

// folder reads the service folder through its root, so no read leaves it:
// fsys with .pactoignore applied, root as written.
type folder struct {
	root *os.Root
	fsys fs.FS
}

func openFolder(dir string) (*folder, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	ign, err := ignore.Load(root.FS())
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return &folder{root: root, fsys: ignore.FS(root.FS(), ign)}, nil
}

// read is the regular file p of fsys; a symlink is refused.
func (f *folder) read(fsys fs.FS, p string) ([]byte, error) {
	if info, err := f.root.Lstat(filepath.FromSlash(p)); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("deploy: %s is a symlink; Pacto copies regular files only", p)
	}
	raw, err := fs.ReadFile(fsys, p)
	if errors.Is(err, fs.ErrNotExist) {
		err = fmt.Errorf("deploy: %s is missing from the folder or left out by .pactoignore: %w", p, fs.ErrNotExist)
	}
	return raw, err
}

// readDeploy reads the files the deploy source src ships, leaves out every
// file holding a Secret, and refuses a file that sets a sensitive literal.
func readDeploy(dir string, sc *discover.Scan, src discover.DeploySource) (*deployFiles, error) {
	f, err := openFolder(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.root.Close() }()
	set := &deployFiles{kind: src.Kind, files: map[string][]byte{}, resource: "pacto-resource.yaml"}
	for _, s := range sc.Secrets {
		set.leftOut(s)
	}
	switch src.Kind {
	case discover.DeployChart:
		set.resource = "templates/pacto-resource.yaml"
		err = set.chart(f, src.Paths[0])
	case discover.DeployKustomize:
		set.resource = path.Join(path.Dir(src.Paths[0]), set.resource)
		err = set.kustomize(f, sc.Secrets, src.Paths[0], map[string]bool{})
	default:
		for _, p := range src.Paths {
			if err = set.copy(f, p); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = set.pactoResource(f, src)
	}
	if err == nil {
		err = scanOutput(set.chartDir, set.files)
	}
	return set, err
}

// leftOut notes that the file p holds a Secret and does not ship.
func (s *deployFiles) leftOut(p string) {
	note := p + " holds a Secret's values, so it is left out of the deploy artifact, which anyone who can pull it reads; " +
		"supply it another way (a SealedSecret, an ExternalSecret or SOPS)"
	if !slices.Contains(s.notes, note) {
		s.notes = append(s.notes, note)
	}
}

// copy ships the file p, unless it holds a Secret.
func (s *deployFiles) copy(f *folder, p string) error {
	raw, err := f.read(f.fsys, p)
	if err != nil {
		return err
	}
	if secretFile(raw) {
		s.leftOut(p)
		return nil
	}
	s.files[p] = raw
	return nil
}

// kustomization is what a kustomization.yaml names that a deploy needs.
// ponytail: crds, replacements and openapi paths are not copied; add them
// when a folder needs them.
type kustomization struct {
	Resources             []string `yaml:"resources"`
	Bases                 []string `yaml:"bases"`
	Components            []string `yaml:"components"`
	PatchesStrategicMerge []string `yaml:"patchesStrategicMerge"`
	Patches               []struct {
		Path string `yaml:"path"`
	} `yaml:"patches"`
	ConfigMapGenerator []struct {
		Files []string `yaml:"files"`
		Envs  []string `yaml:"envs"`
		Env   string   `yaml:"env"`
	} `yaml:"configMapGenerator"`
}

// refs are the local paths k names, inline patches left out.
func (k kustomization) refs() []string {
	refs := slices.Concat(k.Resources, k.Bases, k.Components)
	for _, p := range k.PatchesStrategicMerge {
		if !strings.Contains(p, "\n") {
			refs = append(refs, p)
		}
	}
	for _, p := range k.Patches {
		refs = append(refs, p.Path)
	}
	for _, g := range k.ConfigMapGenerator {
		for _, file := range g.Files {
			if _, src, ok := strings.Cut(file, "="); ok {
				file = src // key=path
			}
			refs = append(refs, file)
		}
		refs = append(append(refs, g.Envs...), g.Env)
	}
	return slices.DeleteFunc(refs, func(r string) bool { return r == "" || discover.RemoteResource(r) })
}

// kustomize ships the kustomization p and the files it names, through nested
// kustomizations. A kustomization holding a secretGenerator is left out, with
// every file it names.
func (s *deployFiles) kustomize(f *folder, secrets []string, p string, seen map[string]bool) error {
	if seen[p] || slices.Contains(secrets, p) {
		return nil
	}
	seen[p] = true
	raw, err := f.read(f.fsys, p)
	if err != nil {
		return err
	}
	s.files[p] = raw
	var k kustomization
	_ = yaml.Unmarshal(raw, &k) // ponytail: what does not decode names nothing; kustomize says so on the cluster
	for _, ref := range k.refs() {
		if err := s.kustomizeRef(f, secrets, p, ref, seen); err != nil {
			return err
		}
	}
	return nil
}

// kustomizeRef ships what the kustomization p names as ref: a folder's
// kustomization, else the file.
func (s *deployFiles) kustomizeRef(f *folder, secrets []string, p, ref string, seen map[string]bool) error {
	local := path.Join(path.Dir(p), ref)
	if !fs.ValidPath(local) {
		return fmt.Errorf("deploy: %s names %s, outside the folder; Pacto deploys only the folder's files: declare publish", p, ref)
	}
	if info, err := fs.Stat(f.fsys, local); err == nil && info.IsDir() {
		return s.kustomize(f, secrets, path.Join(local, "kustomization.yaml"), seen)
	}
	return s.copy(f, local)
}

// chart ships the chart folder dir, less what its .helmignore names, its
// per-environment values files and every file holding a Secret.
func (s *deployFiles) chart(f *folder, dir string) error {
	s.chartDir = dir
	raw, err := f.read(f.fsys, path.Join(dir, ".helmignore"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	helmignore := ignore.New(strings.Split(string(raw), "\n"))
	err = fs.WalkDir(f.fsys, dir, func(p string, e fs.DirEntry, err error) error {
		rel := strings.TrimPrefix(p, dir+"/")
		switch {
		case err != nil:
			return err
		case p == dir:
		case helmignore.Ignored(rel, e.IsDir()) && e.IsDir():
			return fs.SkipDir
		case helmignore.Ignored(rel, e.IsDir()) || e.IsDir():
		case !strings.Contains(rel, "/") && strings.HasPrefix(rel, "values-"):
			s.notes = append(s.notes, p+" holds per-environment values, so it is left out of the chart")
		default:
			raw, err := f.read(f.fsys, p)
			if err == nil && secretFile(raw) {
				s.leftOut(p)
				return nil
			}
			s.files[rel] = raw
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, meta := firstMapping(s.files["Chart.yaml"])
	if s.chartName = scalarOf(contract.MapValue(meta, "name")); s.chartName == "" {
		return fmt.Errorf("deploy: %s names no chart", path.Join(dir, "Chart.yaml"))
	}
	return nil
}

// pactoResource ships delivery/pacto.yaml, the Pacto resource pacto init
// --delivery writes, as s.resource; a kustomization lists it.
func (s *deployFiles) pactoResource(f *folder, src discover.DeploySource) error {
	raw, err := f.read(f.root.FS(), "delivery/pacto.yaml") // .pactoignore keeps delivery/ out of the bundle, not out of the deploy
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	s.files[s.resource] = raw
	if src.Kind == discover.DeployKustomize {
		editYAML(s.files, src.Paths[0], func(k *yaml.Node) {
			seq := seqOf(k, "resources")
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: path.Base(s.resource)})
		})
	}
	return nil
}

// builtin writes the deploy artifact: the files and, when the image is
// built by the artifact img (or supplied), the workload's image (from, at
// where; a chart's values.yaml image) pinned to it by digest.
func (s *deployFiles) builtin(built bool, from, where, img, supplied string) publish.Builtin {
	key := strings.ToUpper(strings.ReplaceAll(img, "-", "_")) // as publish names it
	return func(_ context.Context, out string, env []string) error {
		version := envValue(env, "PACTO_VERSION")
		ref := supplied
		if ref == "" {
			ref = envValue(env, "PACTO_REF_"+key) + "@" + envValue(env, "PACTO_DIGEST_"+key)
		}
		files := maps.Clone(s.files)
		if raw, ok := files[s.resource]; ok {
			files[s.resource] = bytes.ReplaceAll(raw, []byte("@PACTO_VERSION@"), []byte(version))
		}
		switch {
		case s.kind == discover.DeployChart:
			return writeChart(out, s.chartName, files, built, ref, version)
		case from == "":
		case s.kind == discover.DeployKustomize:
			pinKustomizations(files, s.written, ref)
		default:
			if pinManifests(files, from, ref) == 0 {
				return fmt.Errorf("deploy: no image: %s line to pin in %s", from, where)
			}
		}
		return writeDeployTree(out, files)
	}
}

// pinManifests sets every image of the repository repo in files to ref and
// returns how many it set.
func pinManifests(files map[string][]byte, repo, ref string) int {
	n := 0
	for p, raw := range files {
		docs, ok := yamlDocs(raw)
		k := 0
		for _, d := range docs {
			k += pinImages(d, repo, ref)
		}
		if ok && k > 0 {
			files[p], n = encodeDocs(docs), n+k
		}
	}
	return n
}

// pinImages sets every image: value under n whose repository is repo to ref,
// and returns how many it set.
func pinImages(n *yaml.Node, repo, ref string) int {
	set := 0
	for i := 0; n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
		if v := n.Content[i+1]; n.Content[i].Value == "image" && v.Kind == yaml.ScalarNode {
			if r, _ := discover.ImageRepo(v.Value); r == repo {
				v.Value, set = ref, set+1
			}
		}
	}
	for _, c := range n.Content {
		set += pinImages(c, repo, ref)
	}
	return set
}

// pinKustomizations gives every kustomization.yaml in files one images entry
// that pins name to ref, replacing an entry of that name.
func pinKustomizations(files map[string][]byte, name, ref string) {
	repo, digest, _ := strings.Cut(ref, "@")
	entry := []string{"name", name}
	if repo != name {
		entry = append(entry, "newName", repo)
	}
	entry = append(entry, "digest", digest)
	for p := range files {
		if path.Base(p) != "kustomization.yaml" {
			continue
		}
		editYAML(files, p, func(k *yaml.Node) {
			seq := seqOf(k, "images")
			pin := mapping(entry...)
			i := slices.IndexFunc(seq.Content, func(it *yaml.Node) bool { return scalarOf(contract.MapValue(it, "name")) == name })
			if i < 0 {
				seq.Content = append(seq.Content, pin)
				return
			}
			seq.Content[i] = pin
		})
	}
}

// writeChart packages the chart files as a deterministic <name>-<version>.tgz
// in out: Chart.yaml's version and appVersion set to the service version
// (Helm OCI wants the chart version to be the tag) and, when pin,
// values.yaml's image set to ref.
func writeChart(out, name string, files map[string][]byte, pin bool, ref, version string) error {
	editYAML(files, "Chart.yaml", func(m *yaml.Node) {
		setKey(m, "version", version)
		setKey(m, "appVersion", version)
	})
	if pin {
		repo, digest, _ := strings.Cut(ref, "@")
		editYAML(files, "values.yaml", func(m *yaml.Node) {
			img := contract.MapValue(m, "image")
			if contract.MapValue(img, "repository") != nil {
				setKey(img, "repository", repo)
			}
			if contract.MapValue(img, "digest") != nil {
				setKey(img, "digest", digest)
				return
			}
			setKey(img, "tag", version+"@"+digest)
		})
	}
	stage := filepath.Join(out, "chart")
	err := writeDeployTree(filepath.Join(stage, name), files)
	var tgz []byte
	if err == nil {
		tgz, err = oci.BundleToTarGz(os.DirFS(stage))
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(out, name+"-"+version+".tgz"), tgz, 0o644)
	}
	return err
}

// writeDeployTree writes files under out, each at its slash path.
func writeDeployTree(out string, files map[string][]byte) error {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		full := filepath.Join(out, filepath.FromSlash(p))
		err := os.MkdirAll(filepath.Dir(full), 0o755)
		if err == nil {
			err = os.WriteFile(full, files[p], 0o644)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// secretFile reports whether a document of raw is a Secret or a
// kustomization's secretGenerator.
func secretFile(raw []byte) bool {
	docs, ok := yamlDocs(raw)
	if !ok && secretKind.Match(raw) {
		return true // a Helm template does not decode as YAML; its kind line still says Secret
	}
	return slices.ContainsFunc(docs, func(d *yaml.Node) bool {
		m := d.Content[0]
		return scalarOf(contract.MapValue(m, "kind")) == "Secret" || contract.MapValue(m, "secretGenerator") != nil
	})
}

// secretKind is a top-level kind: Secret line, read when a file does not
// decode.
var secretKind = regexp.MustCompile(`(?m)^kind:[ \t]*["']?Secret["']?[ \t]*(#.*)?\r?$`)

// scanOutput refuses a deploy file that sets a sensitive literal: an env
// entry, a ConfigMap's data or a values file key named like a secret, an
// env file line, or any URL that carries a credential.
// Paths are named under dir, the chart folder files are relative to.
func scanOutput(dir string, files map[string][]byte) error {
	for _, p := range slices.Sorted(maps.Keys(files)) {
		if sensitiveFile(p, files[p]) {
			return fmt.Errorf("deploy: %s sets a sensitive literal value, and the deploy artifact publishes it to anyone who can pull it: read it from a Secret instead", path.Join(dir, p))
		}
	}
	return nil
}

func sensitiveFile(p string, raw []byte) bool {
	base := path.Base(p)
	if !strings.HasSuffix(base, ".yaml") && !strings.HasSuffix(base, ".yml") {
		for _, line := range strings.Split(string(raw), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && !strings.HasPrefix(k, "#") && discover.SensitiveLiteral(k, strings.Trim(v, `"'`)) {
				return true
			}
		}
		return false
	}
	docs, _ := yamlDocs(raw)
	values := strings.HasPrefix(base, "values")
	return slices.ContainsFunc(docs, func(d *yaml.Node) bool {
		m := d.Content[0]
		if scalarOf(contract.MapValue(m, "kind")) == "ConfigMap" {
			return sensitiveNode(contract.MapValue(m, "data"), true) || sensitiveNode(m, false)
		}
		return sensitiveNode(m, values)
	})
}

// sensitiveNode reports whether n holds a sensitive literal: a {name, value}
// entry, any key: value pair when keys counts, or a URL with a credential.
func sensitiveNode(n *yaml.Node, keys bool) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode {
		return schemax.SensitiveValue(n.Value)
	}
	if name, value := contract.MapValue(n, "name"), contract.MapValue(n, "value"); scalarOf(value) != "" &&
		discover.SensitiveLiteral(scalarOf(name), scalarOf(value)) {
		return true
	}
	for i := 0; keys && n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
		if discover.SensitiveLiteral(n.Content[i].Value, scalarOf(n.Content[i+1])) {
			return true
		}
	}
	return slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return sensitiveNode(c, keys) })
}

// yamlDocs are the documents of raw, and whether all of it decoded.
func yamlDocs(raw []byte) ([]*yaml.Node, bool) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []*yaml.Node
	for {
		var d yaml.Node
		switch err := dec.Decode(&d); {
		case errors.Is(err, io.EOF):
			return docs, true
		case err != nil:
			return docs, false
		}
		docs = append(docs, &d)
	}
}

// firstMapping is raw's documents and its first one's mapping, nil when raw
// does not decode or starts with something else.
func firstMapping(raw []byte) ([]*yaml.Node, *yaml.Node) {
	docs, ok := yamlDocs(raw)
	if !ok || len(docs) == 0 || docs[0].Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	return docs, docs[0].Content[0]
}

// editYAML applies edit to the first mapping of files[p] and writes it back;
// a file that is missing or holds no mapping is left as it is.
func editYAML(files map[string][]byte, p string, edit func(*yaml.Node)) {
	docs, m := firstMapping(files[p])
	if m == nil {
		return
	}
	edit(m)
	files[p] = encodeDocs(docs)
}

func encodeDocs(docs []*yaml.Node) []byte {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	for _, d := range docs {
		_ = enc.Encode(d) // ponytail: a node that decoded always encodes
	}
	_ = enc.Close()
	return b.Bytes()
}

// seqOf is the sequence under key in the mapping m, made one when it is
// missing or holds something else.
func seqOf(m *yaml.Node, key string) *yaml.Node {
	v := contract.MapValue(m, key)
	if v == nil {
		v = &yaml.Node{}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	}
	if v.Kind != yaml.SequenceNode {
		*v = yaml.Node{Kind: yaml.SequenceNode}
	}
	return v
}

// setKey sets key in the mapping m to the string value.
func setKey(m *yaml.Node, key, value string) {
	v := contract.MapValue(m, key)
	if v == nil {
		v = &yaml.Node{}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	}
	*v = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// mapping is a mapping of the key, value pairs kv.
func mapping(kv ...string) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for _, s := range kv {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s})
	}
	return m
}

func scalarOf(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}
