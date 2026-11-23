package discover

import (
	"io/fs"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Environments come from a file named for one: environments/<env>.yaml, the
// file pacto init writes (likely); a chart's values-<env>.yaml or
// values.<env>.yaml and an overlays/<env>/ kustomization (possible, a
// convention). An Argo CD or Flux object that names one of these confirms it:
// its line joins the environment's outranks, and it chooses the file only
// when no file is named for the environment. The record holds which file;
// whether its shape is a Pacto environment file is checked when a person
// accepts it.

// envDir is the folder at the folder root that holds the Pacto environment
// files.
const envDir = "environments"

// environment adds the file value, read at src in the file file, as the
// environment env's file. A name the contract does not accept is a finding.
func (r *reader) environment(src, file, env, value string, rank int, confidence string) {
	if !contract.ValidSlug(env) {
		r.find(CodeUnresolved, src, envNameMessage(src))
		return
	}
	r.add(entry{Inferred: contract.Inferred{Field: "environments[" + env + "].file", Value: value, Source: src, Confidence: confidence}, rank: rank, file: file})
}

// envFile reads the file p, named name, in the folder's environments
// folder, as the environment its name says.
func (r *reader) envFile(p, name string) {
	env := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
	r.environment(p, p, env, p, rankEnvFile, contract.ConfidenceLikely)
}

// overlay reads the kustomization p as the environment of the overlays/<env>/
// folder that holds it, when one does.
func (r *reader) overlay(p string) {
	if dir := path.Dir(p); path.Base(path.Dir(dir)) == "overlays" {
		r.environment(p, p, path.Base(dir), p, rankEnvOverlay, contract.ConfidencePossible)
	}
}

// gitops reads the Argo CD and Flux objects in the YAML documents of the file
// p for the environments their paths name. An ApplicationSet generates
// Applications, which Pacto does not expand.
func (r *reader) gitops(p string, docs []*yaml.Node) {
	for _, doc := range docs {
		obj := doc.Content[0]
		group, _, _ := strings.Cut(scalar(contract.MapValue(obj, "apiVersion")), "/")
		kind := contract.MapValue(obj, "kind")
		spec := contract.MapValue(obj, "spec")
		switch group + " " + scalar(kind) {
		case "argoproj.io Application":
			r.argoSource(p, contract.MapValue(spec, "source"))
			for _, s := range items(contract.MapValue(spec, "sources")) {
				r.argoSource(p, s)
			}
		case "argoproj.io ApplicationSet":
			r.find(CodeUnsupportedFile, at(p, kind.Line), unsupportedMessage(at(p, kind.Line), unsupportedApplicationSet))
		case "kustomize.toolkit.fluxcd.io Kustomization":
			if fromGit(spec) {
				r.gitopsPath(p, contract.MapValue(spec, "path"), "")
			}
		case "helm.toolkit.fluxcd.io HelmRelease":
			if chart := dig(spec, "chart", "spec"); fromGit(chart) {
				for _, vf := range items(contract.MapValue(chart, "valuesFiles")) {
					r.gitopsPath(p, vf, "")
				}
			}
		}
	}
}

// fromGit reports whether the Flux spec takes its files from a Git
// repository, where its paths are repository paths. Another source (an
// OCIRepository, such as the one pacto init writes under delivery/, a
// Bucket or a HelmRepository) has paths inside an artifact, never in this
// folder, so they are not read.
func fromGit(spec *yaml.Node) bool {
	kind := scalar(contract.MapValue(contract.MapValue(spec, "sourceRef"), "kind"))
	return kind == "" || kind == "GitRepository"
}

// argoSource reads one source of the Argo CD Application in the file p: its
// path, repository-relative, and its Helm value files, relative to the path.
func (r *reader) argoSource(p string, src *yaml.Node) {
	dir := contract.MapValue(src, "path")
	if scalar(dir) == "" {
		return
	}
	r.gitopsPath(p, dir, "")
	for _, vf := range items(dig(src, "helm", "valueFiles")) {
		r.gitopsPath(p, vf, dir.Value)
	}
}

// gitopsPath reads the path n of a GitOps object in the file p, relative to
// base in the repository, for the environment it names: an overlays/<env>
// folder or a values-<env>.yaml file. A path that leaves the folder, one
// that names another source ($values/...) or one the folder does not hold is
// a finding.
func (r *reader) gitopsPath(p string, n *yaml.Node, base string) {
	raw := scalar(n)
	if raw == "" {
		return
	}
	src := at(p, n.Line)
	rel, ok := "", false
	if !strings.HasPrefix(raw, "$") && !path.IsAbs(raw) {
		rel, ok = r.inFolder(path.Join(base, raw))
	}
	if !ok {
		r.find(CodeUnresolved, src, gitopsPathMessage(src))
		return
	}
	// An object deploying from another repository (an Argo CD repoURL, a
	// Flux GitRepository) names paths there: a path the folder lacks is
	// never this service's environment file.
	if _, err := fs.Stat(r.in.Folder, rel); err != nil {
		r.find(CodeUnresolved, src, gitopsMissingMessage(src))
		return
	}
	env := ""
	if path.Base(path.Dir(rel)) == "overlays" {
		env = path.Base(rel)
	} else if m := valuesRE.FindStringSubmatch(path.Base(rel)); m != nil {
		env = m[1]
	}
	if env != "" {
		r.environment(src, p, env, rel, rankGitOps, contract.ConfidenceLikely)
	}
}

// inFolder is the repository path p as a path in the folder, ok false when
// it is outside the folder.
func (r *reader) inFolder(p string) (string, bool) {
	p = path.Clean(p)
	if r.in.FolderPath == "." {
		return p, fs.ValidPath(p)
	}
	if p == r.in.FolderPath {
		return ".", true
	}
	return strings.CutPrefix(p, r.in.FolderPath+"/")
}
