package discover

import (
	"cmp"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// kustomization reads the kustomization p, whose root is k: a local resource
// is a manifest the walk already reads, so only one that cannot be read in
// the folder is a finding, as is a remote one and a helmCharts entry; images
// give the image; an overlays/<env>/ kustomization is that environment's.
func (r *reader) kustomization(p string, k *yaml.Node) {
	r.overlay(p)
	for _, key := range []string{"resources", "bases"} {
		for _, res := range items(contract.MapValue(k, key)) {
			r.kustomizeResource(p, res)
		}
	}
	r.configMapGenerator(p, k)
	if line := keyLine(k, "helmCharts"); line > 0 {
		r.find(CodeUnsupportedFile, at(p, line), unsupportedMessage(at(p, line), unsupportedHelmCharts))
	}
	var images []*yaml.Node
	var names []string
	for _, img := range items(contract.MapValue(k, "images")) {
		if name := scalar(contract.MapValue(img, "name")); name != "" {
			images, names = append(images, img), append(names, name)
		}
	}
	i, ok := r.pick(at(p, keyLine(k, "images")), "images", names)
	if !ok {
		return
	}
	value := cmp.Or(scalar(contract.MapValue(images[i], "newName")), names[i])
	if digest := scalar(contract.MapValue(images[i], "digest")); digest != "" {
		value += "@" + digest
	} else if tag := scalar(contract.MapValue(images[i], "newTag")); tag != "" {
		value += ":" + tag
	}
	r.addAt(p, images[i].Line, rankKustomize, "image", value, "")
}

// envsFile is one configMapGenerator envs entry: the file it names and the
// "<path>:<line>" that names it.
type envsFile struct{ path, source string }

// configMapGenerator hands each configMapGenerator literal of the
// kustomization p, whose root is k, to the Values visitor, and keeps its envs
// files for generatorEnvs. A secretGenerator is never read.
func (r *reader) configMapGenerator(p string, k *yaml.Node) {
	for _, g := range items(contract.MapValue(k, "configMapGenerator")) {
		for _, lit := range items(contract.MapValue(g, "literals")) {
			r.keyValue("kustomize", p, lit.Line, scalar(lit))
		}
		for _, e := range items(contract.MapValue(g, "envs")) {
			r.envs = append(r.envs, envsFile{path: path.Join(path.Dir(p), scalar(e)), source: at(p, e.Line)})
		}
	}
}

// generatorEnvs reads each envs file a configMapGenerator names, one
// KEY=VALUE per line, when the walk visited it as a regular file: one
// .pactoignore leaves out, under a dot-folder, outside the folder or a link
// is never opened, and is a finding.
func (r *reader) generatorEnvs() {
	for _, e := range r.envs {
		if !r.walked[e.path] {
			r.find(CodeUnresolved, e.source, unresolvedEnvsMessage(e.source))
			continue
		}
		if r.in.Values == nil { // nothing reads the values
			continue
		}
		data := r.read(e.path)
		for i, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				r.keyValue("kustomize", e.path, i+1, line)
			}
		}
	}
}

// unresolvedEnvsMessage says the kustomization line at source names an envs
// file Pacto does not read.
func unresolvedEnvsMessage(source string) string {
	return source + " names a configMapGenerator envs file that is missing from the folder, left out of it or not a regular file, so it is not read"
}

// kustomizeResource reads one resource entry of the kustomization p.
func (r *reader) kustomizeResource(p string, res *yaml.Node) {
	ref := scalar(res)
	local := path.Join(path.Dir(p), ref)
	if _, err := fs.Stat(r.in.Folder, local); fs.ValidPath(local) && err == nil {
		return
	}
	src := at(p, res.Line)
	if remoteResource(ref) {
		r.find(CodeUnsupportedFile, src, remoteResourceMessage(src, cleanURL(ref)))
		return
	}
	r.find(CodeUnresolved, src, unresolvedResourceMessage(src))
}

// remoteHostRE is a resource that starts with a host name: github.com/org/repo.
var remoteHostRE = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+/[^/]+/`)

// remoteResource reports whether the resource ref is a remote one Kustomize
// fetches: a URL, a git@ address, a ?ref= query, a //subdir or a host path.
func remoteResource(ref string) bool {
	return strings.Contains(ref, "://") || strings.HasPrefix(ref, "git@") || strings.ContainsAny(ref, "?") ||
		strings.Contains(ref, "//") || remoteHostRE.MatchString(ref)
}

// pick is the one of names, listed under what at src, the service is: the one
// whose last path segment is the service name, else the only one. With
// several and none so named, none is, and it is a finding.
func (r *reader) pick(src, what string, names []string) (int, bool) {
	for i, n := range names {
		if path.Base(n) == r.in.Name {
			return i, true
		}
	}
	if len(names) > 1 {
		r.find(CodeAmbiguous, src, ambiguousNamesMessage(src, what, names))
	}
	return 0, len(names) == 1
}

// keyLine is the line of the key in the mapping m, 0 when m has none.
func keyLine(m *yaml.Node, key string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return 0
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i].Line
		}
	}
	return 0
}

// at is the source "<p>:<line>".
func at(p string, line int) string { return fmt.Sprintf("%s:%d", p, line) }
