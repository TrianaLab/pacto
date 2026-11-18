package discover

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/ignore"
)

// BuildTool is a tool that builds a service's image.
type BuildTool string

// The build tools Pacto detects, Skaffold first: it orchestrates the others.
const (
	ToolSkaffold   BuildTool = "skaffold"
	ToolKo         BuildTool = "ko"
	ToolJib        BuildTool = "jib"
	ToolBuildpacks BuildTool = "buildpacks"
	ToolDockerfile BuildTool = "dockerfile"
)

// The kinds of deploy source.
const (
	DeployKustomize = "kustomize"
	DeployChart     = "chart"
	DeployManifests = "manifests"
)

// DeploySource is the files that deploy the service: a kustomization, a
// chart folder or plain manifests, and the "<path>[:<line>]" that shows it.
type DeploySource struct {
	Kind   string
	Paths  []string
	Source string
}

// BuildPlan is how the folder builds and deploys its service. Tool is the one
// build tool, empty when the image is pinned or no single tool shows; Main is
// ko's main package, Wrapper the Jib program (./mvnw, mvn, ./gradlew or
// gradle) and ToolSource the "<path>:<line>" that names the tool. Image is
// the workload image as written, at ImageSource; Pinned marks one already
// pinned by digest, which needs no build. Deploy is nil when no single
// deploy source shows.
type BuildPlan struct {
	Tool                      BuildTool
	Main, Wrapper, ToolSource string
	Image, ImageSource        string
	Pinned                    bool
	Deploy                    *DeploySource
}

// Build reads which tool builds the image of the service in dir, scanned as
// sc by Dir, and which files deploy it. Every ambiguity is a finding and
// leaves that part of the plan empty: several tools, a skaffold.yaml with
// other than one artifact, no tool for an unpinned image, several deploy
// sources and a kustomization resource outside the folder. It reads only
// through the folder root, with .pactoignore applied, and runs nothing.
func Build(dir string, sc *Scan) (BuildPlan, []contract.InferredFinding) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return BuildPlan{}, nil
	}
	if err != nil {
		return BuildPlan{}, []contract.InferredFinding{{Code: CodeUnreadable, Source: ".", Message: unreadableMessage(".", unreadableFailed)}}
	}
	defer func() { _ = root.Close() }()
	b := &builder{sc: sc, fsys: ignore.FS(root.FS(), sc.ign)}
	if w := sc.Workload; w != nil && w.Image != "" {
		b.plan.Image, b.plan.ImageSource = w.Image, sc.source(w.File, 1, w.Image)
		b.plan.Pinned = strings.Contains(w.Image, "@sha256:")
	}
	if !b.plan.Pinned {
		b.tool()
	}
	b.deploy()
	return b.plan, b.findings
}

// builder collects what Build reads.
type builder struct {
	sc       *Scan
	fsys     fs.FS
	plan     BuildPlan
	findings []contract.InferredFinding
}

func (b *builder) find(code, source, message string) {
	b.findings = append(b.findings, contract.InferredFinding{Code: code, Source: source, Message: message})
}

func (b *builder) exists(p string) bool {
	_, err := fs.Stat(b.fsys, p)
	return err == nil
}

// yamlRoot is the first document of the YAML file p, nil when it is missing
// or does not parse.
func (b *builder) yamlRoot(p string) *yaml.Node {
	data, err := fs.ReadFile(b.fsys, p)
	var doc yaml.Node
	if err != nil || yaml.Unmarshal(data, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

// candidate is one build tool the folder shows, the file that shows it and
// its label in a finding.
type candidate struct {
	plan  BuildPlan
	label string
}

// tool picks the build tool: Skaffold when skaffold.yaml is there, else
// exactly one of ko, Jib, buildpacks and a Dockerfile.
func (b *builder) tool() {
	if b.exists("skaffold.yaml") {
		b.skaffold()
		return
	}
	var cs []candidate
	if b.exists(".ko.yaml") {
		main := scalar(contract.MapValue(firstItem(contract.MapValue(b.yamlRoot(".ko.yaml"), "builds")), "main"))
		if main == "" {
			main = "."
		}
		cs = append(cs, candidate{BuildPlan{Tool: ToolKo, Main: main, ToolSource: ".ko.yaml:1"}, "ko (.ko.yaml)"})
	}
	if c, ok := b.jib(); ok {
		cs = append(cs, c)
	}
	if b.exists("project.toml") {
		cs = append(cs, candidate{BuildPlan{Tool: ToolBuildpacks, ToolSource: "project.toml:1"}, "buildpacks (project.toml)"})
	}
	if df := b.sc.Dockerfile; df != "" {
		cs = append(cs, candidate{BuildPlan{Tool: ToolDockerfile, ToolSource: df + ":1"}, df})
	}
	switch {
	case len(cs) == 1:
		c := cs[0].plan
		b.plan.Tool, b.plan.Main, b.plan.Wrapper, b.plan.ToolSource = c.Tool, c.Main, c.Wrapper, c.ToolSource
	case len(cs) > 1:
		labels := make([]string, len(cs))
		for i, c := range cs {
			labels[i] = c.label
		}
		file, _ := splitLine(cs[0].plan.ToolSource)
		b.find(CodeAmbiguous, file, severalToolsMessage(labels))
	case b.plan.Image != "":
		b.find(CodeUnresolved, b.plan.ImageSource, noBuildToolMessage(b.plan.ImageSource))
	}
}

// skaffold reads skaffold.yaml, which must build exactly one artifact.
func (b *builder) skaffold() {
	build := contract.MapValue(b.yamlRoot("skaffold.yaml"), "build")
	arts := items(contract.MapValue(build, "artifacts"))
	if len(arts) != 1 {
		src := "skaffold.yaml"
		if line := keyLine(build, "artifacts"); line > 0 {
			src = at(src, line)
		}
		b.find(CodeAmbiguous, src, skaffoldArtifactsMessage(src, len(arts)))
		return
	}
	b.plan.Tool, b.plan.ToolSource = ToolSkaffold, at("skaffold.yaml", arts[0].Line)
}

// jibFiles are the build files that may name the Jib plugin, each with its
// wrapper and the program to run without one.
var jibFiles = []struct{ file, wrapper, program string }{
	{"pom.xml", "mvnw", "mvn"},
	{"build.gradle", "gradlew", "gradle"},
	{"build.gradle.kts", "gradlew", "gradle"},
}

// jib is the first build file naming the Jib plugin, with the wrapper next
// to it preferred over the program on the path.
func (b *builder) jib() (candidate, bool) {
	for _, j := range jibFiles {
		data, err := fs.ReadFile(b.fsys, j.file)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(strings.ToLower(line), "jib") {
				continue
			}
			wrapper := j.program
			if b.exists(j.wrapper) {
				wrapper = "./" + j.wrapper
			}
			return candidate{BuildPlan{Tool: ToolJib, Wrapper: wrapper, ToolSource: at(j.file, i+1)}, "Jib (" + j.file + ")"}, true
		}
	}
	return candidate{}, false
}

// deploy picks the one deploy source: a kustomization (the folder's own
// kustomization.yaml, else the only one), a chart (the folder itself, else
// the only one) or, with neither, the plain manifests.
func (b *builder) deploy() {
	var srcs []DeploySource
	if k, ok := b.pickOne(kustomizations(b.sc.Kustomize), "kustomization.yaml", DeployKustomize, &srcs); ok {
		srcs = append(srcs, DeploySource{Kind: DeployKustomize, Paths: []string{k}, Source: k})
	}
	if c, ok := b.pickOne(b.sc.Charts, ".", DeployChart, &srcs); ok {
		srcs = append(srcs, DeploySource{Kind: DeployChart, Paths: []string{c}, Source: path.Join(c, "Chart.yaml")})
	}
	if len(kustomizations(b.sc.Kustomize)) == 0 && len(b.sc.Manifests) > 0 {
		src := b.sc.Manifests[0]
		if file, _ := splitLine(b.plan.ImageSource); slices.Contains(b.sc.Manifests, file) {
			src = b.plan.ImageSource
		}
		srcs = append(srcs, DeploySource{Kind: DeployManifests, Paths: b.sc.Manifests, Source: src})
	}
	switch {
	case len(srcs) > 1:
		labels := make([]string, len(srcs))
		for i, s := range srcs {
			labels[i] = s.Kind + " (" + strings.Join(s.Paths, ", ") + ")"
		}
		file, _ := splitLine(srcs[0].Source)
		b.find(CodeAmbiguous, file, severalDeployMessage(labels))
	case len(srcs) == 1 && (srcs[0].Kind != DeployKustomize || b.kustomizeInside(srcs[0].Paths[0], map[string]bool{})):
		b.plan.Deploy = &srcs[0]
	}
}

// pickOne is the one of paths: preferred when listed, else the only one.
// With several and no preferred one each is a source of kind, appended to
// srcs, so deploy names them all.
func (b *builder) pickOne(paths []string, preferred, kind string, srcs *[]DeploySource) (string, bool) {
	if slices.Contains(paths, preferred) {
		return preferred, true
	}
	if len(paths) == 1 {
		return paths[0], true
	}
	for _, p := range paths {
		*srcs = append(*srcs, DeploySource{Kind: kind, Paths: []string{p}, Source: p})
	}
	return "", false
}

// kustomizations are the kustomization.yaml files among the manifests, a
// Flux Kustomization object left out.
func kustomizations(files []string) []string {
	return slices.DeleteFunc(slices.Clone(files), func(p string) bool { return path.Base(p) != "kustomization.yaml" })
}

// kustomizeInside reports whether every local resource, base and component
// the kustomization p names, through nested kustomizations, stays in the
// folder; each that leaves it is a finding. seen guards against a loop.
func (b *builder) kustomizeInside(p string, seen map[string]bool) bool {
	seen[p] = true
	k, inside := b.yamlRoot(p), true
	for _, key := range []string{"resources", "bases", "components"} {
		for _, res := range items(contract.MapValue(k, key)) {
			ref := scalar(res)
			if RemoteResource(ref) {
				continue
			}
			local := path.Join(path.Dir(p), ref)
			if !fs.ValidPath(local) {
				src := at(p, res.Line)
				b.find(CodeUnresolved, src, deployOutsideMessage(src))
				inside = false
				continue
			}
			if nested := path.Join(local, "kustomization.yaml"); b.exists(nested) && !seen[nested] {
				inside = b.kustomizeInside(nested, seen) && inside
			}
		}
	}
	return inside
}

// firstItem is the first entry of the sequence n, nil when it has none.
func firstItem(n *yaml.Node) *yaml.Node {
	if s := items(n); len(s) > 0 {
		return s[0]
	}
	return nil
}

// publish adds the publish.build and publish.deploy fields Build reads from
// the folder dir for the service name, when the contract declares no publish
// section, and returns Build's findings.
func (c *completion) publish(dir, name string) []contract.InferredFinding {
	if c.d.Publish != nil {
		return nil
	}
	p, findings := Build(dir, c.sc)
	declare := publishYAML(p, name)
	// A source a record cannot name (a path with a ':') gives no field and no finding.
	add := func(field, value, source string) {
		if file, _ := splitLine(source); recordable(file) {
			c.add(field, value, file, source, declare)
		}
	}
	switch {
	case p.Pinned:
		add("publish.build", "image supplied, not built by Pacto: "+p.Image, p.ImageSource)
	case p.Tool != "":
		file, _ := splitLine(p.ToolSource)
		value := fmt.Sprintf("%s (%s)", p.Tool, file)
		if p.Image != "" {
			repo, _ := ImageRepo(p.Image)
			value += ", image " + repo
		}
		add("publish.build", value, p.ToolSource)
	}
	if d := p.Deploy; d != nil {
		value := d.Kind + ": " + strings.Join(d.Paths, ", ")
		if p.Tool != "" || p.Pinned {
			value += "; image pinned by digest"
		}
		add("publish.deploy", value, d.Source)
	}
	return slices.DeleteFunc(findings, func(f contract.InferredFinding) bool {
		file, _ := splitLine(f.Source)
		return !recordable(file)
	})
}

// publishYAML is a publish section equivalent to the plan p for the service
// name, without a build command: pacto ci --publish detects the build.
// <registry> stands for the registry the contract publishes to.
func publishYAML(p BuildPlan, name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "publish:\n  bundle: oci://<registry>/%s\n", name)
	art := ""
	if p.Deploy != nil {
		art = DeployManifests
		if p.Deploy.Kind == DeployChart {
			art = DeployChart
		}
		fmt.Fprintf(&b, "  deploy: %s\n", art)
	}
	if p.Tool == "" && art == "" {
		return b.String()
	}
	b.WriteString("  artifacts:\n")
	if p.Tool != "" {
		ref := "<registry>/images/" + name
		if p.Image != "" {
			ref, _ = ImageRepo(p.Image)
		}
		fmt.Fprintf(&b, "    - name: image\n      type: image\n      ref: oci://%s\n", ref)
	}
	if art != "" {
		fmt.Fprintf(&b, "    - name: %s\n      type: %s\n      ref: oci://<registry>/deploy/%s\n", art, art, name)
	}
	return b.String()
}
