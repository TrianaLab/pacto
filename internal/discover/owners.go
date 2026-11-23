package discover

import (
	"io/fs"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/ignore"
)

// codeownersPaths are where GitHub reads CODEOWNERS, in its order: the first
// found is the only one read.
var codeownersPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// ownerField adds the service.owner entry the owner file p gives at line: a
// team, or a person when dri. It is marked as read from an owner file, which
// no one verified.
func (r *reader) ownerField(p, file string, line, rank int, name string, dri bool) {
	value, declare := ownerYAML(name, dri)
	r.add(entry{Inferred: contract.Inferred{Field: "service.owner", Value: value, Source: at(p, line), Confidence: contract.ConfidenceLikely,
		Class: contract.InferredClassOwnerFile, Declare: declare}, rank: rank, file: file})
}

// ownerYAML is the value and the declaring YAML of the owner name: a team,
// or a person when dri.
func ownerYAML(name string, dri bool) (value, declare string) {
	if dri {
		return name + " (dri)", "service:\n  owner:\n    dri: " + Quote(name) + "\n"
	}
	return name, "service:\n  owner:\n    team: " + Quote(name) + "\n"
}

// codeowners reads the owner of the folder's pacto.yaml from the first
// CODEOWNERS GitHub reads in the repository: the last rule that matches it,
// as GitHub decides. A rule with no owners leaves the folder unowned.
func (r *reader) codeowners() {
	repo, prefix := r.in.Repo, "repository:"
	if repo == nil {
		repo, prefix = r.in.Folder, ""
	}
	for _, p := range codeownersPaths {
		if data, found := r.readOwnerFile(repo, p, prefix+p); found {
			if data != nil {
				r.codeownersRule(prefix+p, data)
			}
			return
		}
	}
}

// readOwnerFile is the bytes of the file p in fsys, cited as source. found is
// false when there is no such file; data is nil, with a finding, when there
// is one Pacto does not read: a link, a folder, a device or a file over 1 MiB.
func (r *reader) readOwnerFile(fsys fs.FS, p, source string) (data []byte, found bool) {
	entries, _ := fs.ReadDir(fsys, path.Dir(p)) // ponytail: an unlistable folder holds no file GitHub reads
	i := slices.IndexFunc(entries, func(d fs.DirEntry) bool { return d.Name() == path.Base(p) })
	if i < 0 {
		return nil, false
	}
	if !entries[i].Type().IsRegular() {
		r.find(CodeUnreadable, source, unreadableMessage(source, unreadableNotRegular))
		return nil, true
	}
	return r.readIn(fsys, p, source), true
}

// codeownersRule reads the CODEOWNERS data, cited as file: of the last rule
// matching the folder's pacto.yaml, the first team, else the first person.
func (r *reader) codeownersRule(file string, data []byte) {
	target := path.Join(r.in.FolderPath, "pacto.yaml")
	var owners []string
	line := 0
	for i, text := range strings.Split(string(data), "\n") {
		text, _, _ = strings.Cut(text, "#")
		fields := strings.Fields(text)
		if len(fields) > 0 && codeownersMatch(fields[0], target) {
			line, owners = i+1, fields[1:]
		}
	}
	if len(owners) == 0 {
		return
	}
	for _, dri := range []bool{false, true} {
		for _, o := range owners {
			name, ok := strings.CutPrefix(o, "@")
			if ok && strings.Contains(name, "/") != dri && contract.ValidTeamName(name) {
				r.ownerField(file, file, line, rankCodeowners, name, dri)
				return
			}
		}
	}
	r.find(CodeUnresolved, at(file, line), emailOwnersMessage(at(file, line)))
}

// codeownersMatch reports whether the CODEOWNERS pattern owns the file
// target: it matches the file, or, unless its last segment has a '*', a
// folder above it.
func codeownersMatch(pattern, target string) bool {
	if ignore.Matches(pattern, target, false) {
		return true
	}
	if strings.Contains(path.Base(strings.TrimSuffix(pattern, "/")), "*") {
		return false
	}
	for dir := path.Dir(target); dir != "."; dir = path.Dir(dir) {
		if ignore.Matches(pattern, dir, true) {
			return true
		}
	}
	return false
}

// catalogFile is the Backstage catalog file read at the folder root.
const catalogFile = "catalog-info.yaml"

// catalog reads the Backstage catalog file p, its documents docs: of the
// Component named like the service, else the only one, its owner, its system
// as the domain and the components it depends on. Its lifecycle has no
// contract field and is never read; a resource it depends on is left to the
// external-dependency reader.
func (r *reader) catalog(p string, docs []*yaml.Node) {
	var comps []*yaml.Node
	var names []string
	for _, doc := range docs {
		if obj := doc.Content[0]; scalar(contract.MapValue(obj, "kind")) == "Component" {
			comps = append(comps, obj)
			names = append(names, scalar(dig(obj, "metadata", "name")))
		}
	}
	i, ok := r.pick(p, "components", names)
	if !ok {
		return
	}
	spec := contract.MapValue(comps[i], "spec")
	if n := contract.MapValue(spec, "owner"); scalar(n) != "" {
		r.catalogOwner(p, n)
	}
	if n := contract.MapValue(spec, "system"); scalar(n) != "" {
		r.catalogSystem(p, n)
	}
	for _, n := range items(contract.MapValue(spec, "dependsOn")) {
		if kind, name := backstage.EntityRef(scalar(n)); kind == "component" && name != "" {
			r.catalogDependency(p, n.Line, path.Base(name))
		}
	}
}

// catalogOwner reads the owner n: a group, the default kind, is a team and a
// user is a person.
func (r *reader) catalogOwner(p string, n *yaml.Node) {
	if o, ok := backstage.OwnerFromRef(n.Value); ok {
		k, _ := o.Key() // OwnerFromRef sets exactly one of Team and DRI
		r.ownerField(p, p, n.Line, rankCatalog, k.Value, k.Kind == contract.OwnerKindDRI)
	}
}

// catalogSystem reads the system n as the domain, made a slug; one that is
// still not a domain is a finding.
func (r *reader) catalogSystem(p string, n *yaml.Node) {
	domain, ok := backstage.DomainFromRef(n.Value)
	if !ok {
		r.find(CodeUnresolved, at(p, n.Line), unresolvedSystemMessage(at(p, n.Line)))
		return
	}
	r.addAt(p, n.Line, rankCatalog, "service.domain", domain, "service:\n  domain: "+domain+"\n")
}

// catalogDependency reads the component name, less its namespace, the catalog file p says this
// one depends on at line. A catalog says it by convention, so it is possible,
// and which versions it accepts is unknown.
func (r *reader) catalogDependency(p string, line int, name string) {
	declare := "dependencies:\n" + DependencyYAML(Quote(name), "<where the contract of "+name+" is published>", "declare the versions of "+name+" this service accepts")
	r.add(entry{Inferred: contract.Inferred{Field: "dependencies[" + name + "]", Value: name, Source: at(p, line), Confidence: contract.ConfidencePossible, Declare: declare},
		rank: rankCatalog, file: p})
}
