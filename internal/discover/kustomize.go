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
// give the image.
// ponytail: an overlays/<env>/ kustomization is an environment candidate; P7
// records it.
func (r *reader) kustomization(p string, k *yaml.Node) {
	for _, key := range []string{"resources", "bases"} {
		for _, res := range items(contract.MapValue(k, key)) {
			r.kustomizeResource(p, res)
		}
	}
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
