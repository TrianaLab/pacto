package delivery

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// GateManifests refuses a promote whose deploy artifact reaches outside the
// Stage namespace or carries a delivery object, and asks a person when Pacto
// cannot tell what it applies.
const GateManifests = "pacto.manifests"

// The layer media types of the deploy artifacts Pacto reads. pkg/publish
// derives its own from these: it imports this package, never the reverse.
const (
	FluxContentMediaType = "application/vnd.cncf.flux.content.v1.tar+gzip"
	HelmChartMediaType   = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
)

// Bounds of one inspection.
const (
	// MaxManifestBytes is the most uncompressed bytes Pacto reads from a
	// layer; beyond it the inspection is Unknown.
	MaxManifestBytes = 16 << 20
	// MaxManifestObjects is the most objects Pacto judges; beyond it the
	// inspection is Unknown.
	MaxManifestObjects = 1000
	// MaxListedObjects is the most objects, and refusal sentences, a
	// citation lists.
	MaxListedObjects = 50
)

// ScopeFunc says whether kind of group is namespaced, and whether it knows the
// kind at all. An unknown kind is never judged namespaced.
type ScopeFunc func(group, kind string) (namespaced, known bool)

// builtinScope is the core kinds every cluster serves: true is namespaced.
var builtinScope = map[string]bool{
	"/ConfigMap": true, "/Secret": true, "/Service": true, "/ServiceAccount": true, "/PersistentVolumeClaim": true,
	"/Pod": true, "/Endpoints": true, "/LimitRange": true, "/ResourceQuota": true,
	"apps/Deployment": true, "apps/StatefulSet": true, "apps/DaemonSet": true, "apps/ReplicaSet": true,
	"batch/Job": true, "batch/CronJob": true, "autoscaling/HorizontalPodAutoscaler": true,
	"policy/PodDisruptionBudget": true, "networking.k8s.io/Ingress": true, "networking.k8s.io/NetworkPolicy": true,
	"rbac.authorization.k8s.io/Role": true, "rbac.authorization.k8s.io/RoleBinding": true,
	"pacto.trianalab.io/Pacto": true,
	"/Namespace":               false, "/Node": false, "/PersistentVolume": false,
	"storage.k8s.io/StorageClass": false, "storage.k8s.io/CSIDriver": false,
	"scheduling.k8s.io/PriorityClass": false, "node.k8s.io/RuntimeClass": false,
	"networking.k8s.io/IngressClass":        false,
	"rbac.authorization.k8s.io/ClusterRole": false, "rbac.authorization.k8s.io/ClusterRoleBinding": false,
	"apiextensions.k8s.io/CustomResourceDefinition": false, "apiregistration.k8s.io/APIService": false,
	"admissionregistration.k8s.io/MutatingWebhookConfiguration":     false,
	"admissionregistration.k8s.io/ValidatingWebhookConfiguration":   false,
	"admissionregistration.k8s.io/ValidatingAdmissionPolicy":        false,
	"admissionregistration.k8s.io/ValidatingAdmissionPolicyBinding": false,
}

// BuiltinScope knows the core kinds without asking a cluster.
func BuiltinScope(group, kind string) (namespaced, known bool) {
	namespaced, known = builtinScope[group+"/"+kind]
	return namespaced, known
}

// ChainScope asks first, then then: the first that knows the kind wins. With
// BuiltinScope first, a mapper that calls a core cluster-scoped kind
// namespaced never turns it into a namespaced one.
func ChainScope(first, then ScopeFunc) ScopeFunc {
	return func(group, kind string) (bool, bool) {
		if ns, ok := first(group, kind); ok {
			return ns, ok
		}
		return then(group, kind)
	}
}

// CodeRawInfraInArtifact is the stable code of a pacto.manifests refusal of
// raw infrastructure under profile idp (ClassPolicy.RefuseRawInfra); it
// travels in the verdict's citation and the apply refusal record.
const CodeRawInfraInArtifact = "RawInfraInArtifact"

// rawInfraStep is the next step a raw infrastructure sentence names.
const rawInfraStep = "declare a dependency on an offering"

// InfraFunc says whether kind of group is infrastructure Crossplane manages
// (a claim, a composite or a managed resource, from the cluster's API
// discovery categories), and whether it could tell.
type InfraFunc func(group, kind string) (infra, known bool)

// crossplaneGroup is any Crossplane or Upbound API group: XRDs,
// Compositions, packages and the providers' managed resources.
func crossplaneGroup(group string) bool {
	return strings.HasSuffix(group, ".crossplane.io") || strings.HasSuffix(group, ".upbound.io")
}

// ManifestObject is one object a version applies.
type ManifestObject struct{ Kind, Namespace, Name string }

// ManifestTarget is what the applier object adds from outside the artifact:
// the namespace every object must stay in, and what it already refuses or
// makes unreadable.
type ManifestTarget struct {
	Namespace string
	// Path is the directory of the artifact the applier builds, "" for its
	// root.
	Path    string
	Refused []string
	Unread  []string
	// Infra tells claims, composites and managed resources of a group
	// outside crossplaneGroup; nil tells them by group only.
	Infra InfraFunc
}

// ManifestInspection is what a version's deploy artifact applies, and what of
// it Pacto refuses or cannot read.
type ManifestInspection struct {
	// Artifact is manifests, kustomize or chart.
	Artifact  string
	Namespace string
	Objects   []ManifestObject
	// Total counts every object, listed or not.
	Total int
	// docs counts every document read, judged or not, against
	// MaxManifestObjects.
	docs    int
	Refused []string
	Unread  []string
	// Infra are the raw infrastructure objects, each a why sentence: refused
	// when RefuseInfra holds, else a question for a person.
	Infra       []string
	RefuseInfra bool
}

// ManifestTargetOf is the target of a Stage: its namespace, unless the applier
// object moves the objects elsewhere (Fail) or transforms the artifact
// (Unknown). applier is the object the applyRef names; ks are the
// Kustomizations read (ksErr why they could not be), of which those applying
// the applyRef OCIRepository count.
func ManifestTargetOf(stage StageObject, applier map[string]any, ks []KustomizationView, ksErr error) ManifestTarget {
	ns, ref := stage.Metadata.Namespace, stage.Spec.ApplyRef
	t := ManifestTarget{Namespace: ns}
	switch refKind(ref) {
	case ApplierHelmRelease:
		t.judgeApplier("HelmRelease "+ref.Name, str(applier, "spec", "targetNamespace"), present(applier, []string{"spec", "postRenderers"}))
	case ApplierArgoCD:
		t.judgeApplier("Application "+ref.Name, str(applier, "spec", "destination", "namespace"),
			present(applier, []string{"spec", "source", "helm"}, []string{"spec", "source", "kustomize"}, []string{"spec", "source", "plugin"},
				[]string{"spec", "source", "directory", "jsonnet"}))
		t.Path = cleanPath(str(applier, "spec", "source", "path"))
	default:
		if ksErr != nil {
			t.Unread = append(t.Unread, "the Kustomizations: "+ksErr.Error())
		}
		paths := map[string]bool{}
		for _, k := range ks {
			// A Kustomization in another namespace applies as that
			// namespace's identity: it is that namespace's concern, and a
			// tenant there cannot change this Stage's verdict.
			src := k.Spec.SourceRef
			if src.Kind != ApplierFlux || k.Metadata.Namespace != ns || cmp.Or(src.Namespace, ns) != ns || src.Name != ref.Name {
				continue
			}
			paths[cleanPath(k.Spec.Path)] = true
			var fields []string
			for f, raw := range map[string][]byte{"patches": k.Spec.Patches, "postBuild": k.Spec.PostBuild, "components": k.Spec.Components} {
				if len(raw) > 0 && string(raw) != "null" {
					fields = append(fields, "spec."+f)
				}
			}
			slices.Sort(fields)
			// Without spec.targetNamespace a Kustomization applies an object
			// that names no namespace into its own.
			t.judgeApplier("Kustomization "+k.Metadata.Name, cmp.Or(k.Spec.TargetNamespace, k.Metadata.Namespace), fields)
		}
		switch keys := slices.Sorted(maps.Keys(paths)); {
		case len(keys) == 1:
			t.Path = keys[0]
		case len(keys) > 1:
			t.Unread = append(t.Unread, "the Kustomizations build different paths of the artifact: "+strings.Join(keys, ", "))
		}
	}
	return t
}

// cleanPath is a path of the artifact as its tar names it: "" for the root.
func cleanPath(p string) string {
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

func (t *ManifestTarget) judgeApplier(what, targetNS string, transforms []string) {
	if targetNS != "" && targetNS != t.Namespace {
		t.Refused = append(t.Refused, what+" applies into "+targetNS+", outside "+t.Namespace)
	}
	for _, f := range transforms {
		t.Unread = append(t.Unread, what+" transforms the artifact: "+f)
	}
}

// field is the value at path in obj, nil when absent.
func field(obj map[string]any, path ...string) any {
	var cur any = obj
	for _, p := range path {
		m, _ := cur.(map[string]any)
		cur = m[p]
	}
	return cur
}

// str is the string at path in obj, "" when absent.
func str(obj map[string]any, path ...string) string {
	s, _ := field(obj, path...).(string)
	return s
}

// present lists, dotted, the paths set in obj.
func present(obj map[string]any, paths ...[]string) []string {
	var out []string
	for _, p := range paths {
		if field(obj, p...) != nil {
			out = append(out, strings.Join(p, "."))
		}
	}
	return out
}

// InspectArtifact judges the objects one deploy artifact layer applies into
// target. scope says which kinds are namespaced; a kind it does not know is
// Unknown, never namespaced.
func InspectArtifact(mediaType string, layer []byte, target ManifestTarget, scope ScopeFunc) ManifestInspection {
	in := ManifestInspection{Namespace: target.Namespace, Refused: slices.Clone(target.Refused), Unread: slices.Clone(target.Unread)}
	var files []tarFile
	var err error
	switch mediaType {
	case FluxContentMediaType, HelmChartMediaType:
		files, err = readTar(layer)
	default:
		in.Unread = append(in.Unread, "Pacto reads Flux and Helm OCI artifacts only, and this layer is "+mediaType)
		return in
	}
	if err != nil {
		in.Unread = append(in.Unread, err.Error())
		return in
	}
	if files = under(files, target.Path); target.Path != "" && len(files) == 0 {
		in.Unread = append(in.Unread, "the artifact holds nothing under "+target.Path+", the path its applier builds")
		return in
	}
	var docs []doc
	if mediaType == HelmChartMediaType {
		in.Artifact, docs = "chart", chartDocs(files, &in)
	} else {
		in.Artifact, docs = fluxDocs(files, &in)
	}
	k := kindInfo{scope, target.Infra}
	for _, d := range docs {
		if !in.judgeDoc(d, k) {
			in.Unread = append(in.Unread, fmt.Sprintf("the artifact holds more than %d objects", MaxManifestObjects))
			break
		}
	}
	return in
}

// under is the files under dir, named relative to it; every file for "".
func under(files []tarFile, dir string) []tarFile {
	if dir == "" {
		return files
	}
	var out []tarFile
	for _, f := range files {
		if rel, ok := strings.CutPrefix(f.name, dir+"/"); ok {
			out = append(out, tarFile{name: rel, data: f.data})
		}
	}
	return out
}

// tarFile is one regular file of a layer.
type tarFile struct {
	name string
	data []byte
}

// readTar reads the regular files of a gzipped tar, at most MaxManifestBytes
// uncompressed, with clean relative names.
func readTar(layer []byte) ([]tarFile, error) {
	gz, err := gzip.NewReader(bytes.NewReader(layer))
	if err != nil {
		return nil, errors.New("the artifact layer is not a gzipped tar: " + err.Error())
	}
	r := &limited{r: gz, left: MaxManifestBytes}
	tr := tar.NewReader(r)
	var out []tarFile
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err == nil && h.Typeflag == tar.TypeReg {
			name := cleanPath(h.Name)
			// Extracting keeps the last of two entries of one name, and the
			// inspector would judge the first.
			if slices.ContainsFunc(out, func(f tarFile) bool { return f.name == name }) {
				return nil, errors.New("the artifact holds " + name + " twice")
			}
			var b []byte
			if b, err = io.ReadAll(tr); err == nil {
				out = append(out, tarFile{name: name, data: b})
				continue
			}
		}
		if err != nil {
			if r.left < 0 {
				return nil, fmt.Errorf("the artifact holds more than %d MiB uncompressed", MaxManifestBytes>>20)
			}
			return nil, errors.New("the artifact layer is not a readable tar: " + err.Error())
		}
	}
}

// limited reads at most left bytes and fails past them.
type limited struct {
	r    io.Reader
	left int64
}

func (l *limited) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return n, errors.New("too large")
	}
	return n, err
}

// doc is one file to split into objects: name for messages, and data.
type doc struct {
	file string
	data []byte
	// override is the namespace a kustomization moves every object to.
	override string
	// chart marks a Helm template.
	chart bool
}

var kustomizationFiles = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// kustomizationFields are the kustomization fields that never move an object
// out of its namespace or change what it is; any other field transforms the
// manifests in ways Pacto does not read.
var kustomizationFields = []string{"apiVersion", "kind", "metadata", "resources", "namespace", "namePrefix", "nameSuffix",
	"commonLabels", "labels", "commonAnnotations", "images", "replicas", "sortOptions", "buildMetadata"}

// fluxDocs is what Flux content applies: every YAML or JSON file, as the
// kustomization kustomize-controller generates holds, or, with a root
// kustomization, only the plain files it lists.
func fluxDocs(files []tarFile, in *ManifestInspection) (string, []doc) {
	k := slices.IndexFunc(files, func(f tarFile) bool { return slices.Contains(kustomizationFiles, f.name) })
	if k < 0 {
		var docs []doc
		for _, f := range files {
			switch ext := path.Ext(f.name); {
			case slices.Contains(kustomizationFiles, path.Base(f.name)):
				// kustomize-controller builds a directory holding its own
				// kustomization as one, with its namespace and patches.
				in.Unread = append(in.Unread, path.Dir(f.name)+" holds a kustomization, which Pacto does not read")
			case ext == ".jsonnet" || ext == ".libsonnet":
				// Argo CD evaluates jsonnet in a directory source.
				in.Unread = append(in.Unread, f.name+" is jsonnet, which Pacto does not read")
			case ext == ".yaml" || ext == ".yml" || ext == ".json":
				docs = append(docs, doc{file: f.name, data: f.data})
			}
		}
		return "manifests", docs
	}
	return "kustomize", kustomizeDocs(files, files[k], in)
}

// kustomizeDocs reads a root kustomization that only lists plain files.
func kustomizeDocs(files []tarFile, k tarFile, in *ManifestInspection) []doc {
	var kz map[string]any
	if err := yaml.Unmarshal(k.data, &kz); err != nil {
		in.Unread = append(in.Unread, k.name+" is not readable YAML")
		return nil
	}
	var odd []string
	for f := range kz {
		if !slices.Contains(kustomizationFields, f) {
			odd = append(odd, f)
		}
	}
	slices.Sort(odd)
	resources, _ := kz["resources"].([]any)
	var docs []doc
	for _, r := range resources {
		name, _ := r.(string)
		i := slices.IndexFunc(files, func(f tarFile) bool { return f.name == cleanPath(name) })
		if i < 0 {
			odd = append(odd, "resources entry "+strconv.Quote(name))
			continue
		}
		docs = append(docs, doc{file: files[i].name, data: files[i].data})
	}
	if len(odd) > 0 {
		in.Unread = append(in.Unread, "Pacto reads plain manifests, and this kustomization transforms them: "+strings.Join(odd, ", "))
		return nil
	}
	ns, _ := kz["namespace"].(string)
	if ns != "" && ns != in.Namespace {
		in.Refused = append(in.Refused, "the kustomization moves every object to "+ns)
		return nil
	}
	for i := range docs {
		docs[i].override = ns
	}
	return docs
}

// chartDocs is every template a Helm chart renders; its CRDs are refused and
// a subchart is unread.
func chartDocs(files []tarFile, in *ManifestInspection) []doc {
	var docs []doc
	for _, f := range files {
		_, rel, _ := strings.Cut(f.name, "/") // a chart archive holds one top directory
		switch {
		case strings.HasPrefix(rel, "crds/"):
			in.Refused = append(in.Refused, "CustomResourceDefinition "+rel+" is cluster-scoped")
		case strings.HasPrefix(rel, "charts/"):
			in.Unread = append(in.Unread, "the chart carries a subchart: "+rel)
		case strings.HasPrefix(rel, "templates/") && !strings.HasPrefix(path.Base(rel), "_") && path.Base(rel) != "NOTES.txt":
			// ponytail: Helm renders every template whatever its extension, so
			// every one but partials and NOTES.txt is judged.
			docs = append(docs, doc{file: rel, data: f.data, chart: true})
		}
	}
	return docs
}

// subject is one subject of a RoleBinding.
type subject struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

// facts are what judging an object reads from it.
type facts struct {
	apiVersion, kind, name string
	namespaces             []string
	roleRefKinds           []string
	roleRef                string
	subjects               []subject
	templated              bool
}

// Sentinels the template scan writes in place of a template action.
const (
	releaseNS = "__pacto_release_namespace__"
	tplValue  = "__pacto_templated__"
)

var (
	releaseNSAction = regexp.MustCompile(`\{\{-?\s*\.Release\.Namespace\s*(\|\s*quote\s*)?-?\}\}`)
	anyAction       = regexp.MustCompile(`\{\{.*?\}\}`)
	// control is a line-only action that renders nothing: control flow, a
	// comment or an assignment. Every other one may inject content.
	control   = regexp.MustCompile(`^\{\{-?\s*(/\*.*\*/\s*-?\}\}$|(if|else|end|range|with|define|block|break|continue)\b|\$\w*\s*:?=)`)
	indentArg = regexp.MustCompile(`\bn?indent\s+(\d+)`)
)

// templateFacts reads the facts of every document of a Helm template.
// ponytail: control flow is ignored, every document is judged as if it
// renders; a Helm SDK render would lift that ceiling and decision 3 keeps it
// out.
func templateFacts(data []byte) ([]facts, error) {
	var lines []string
	var injected []int // the indentation of each line-only action that injects content
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") && strings.Count(t, "{{") == 1 {
			if !control.MatchString(t) {
				// Without an indent function, every line the action
				// renders after its first starts at column 0.
				at := 0
				if m := indentArg.FindStringSubmatch(t); m != nil {
					at, _ = strconv.Atoi(m[1])
				}
				injected = append(injected, at)
			}
			continue
		}
		l = releaseNSAction.ReplaceAllString(l, releaseNS)
		lines = append(lines, anyAction.ReplaceAllString(l, tplValue))
	}
	// Content injected at the top level or one below may be a whole object
	// or set its kind, namespace, roleRef or subjects. Any injection on a
	// RoleBinding may set a subject.
	// ponytail: injections are not told apart by document; a file holding
	// one counts for every document in it.
	if slices.ContainsFunc(injected, func(at int) bool { return at < 4 }) {
		return []facts{{templated: true}}, nil
	}
	out, err := splitFacts([]byte(strings.Join(lines, "\n")))
	for i := range out {
		out[i].templated = out[i].templated || len(injected) > 0 && out[i].kind == "RoleBinding"
	}
	return out, err
}

// splitFacts reads the facts of every YAML document in data, keeping every
// value a key repeats (a template's branches).
func splitFacts(data []byte) ([]facts, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []facts
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		if len(n.Content) == 0 || n.Content[0].Kind == yaml.ScalarNode && n.Content[0].Value == "" {
			continue
		}
		out = append(out, factsOf(n.Content[0]))
	}
}

// factsOf reads one document node, keeping every value a template branch
// repeats.
func factsOf(n *yaml.Node) facts {
	var f facts
	var kinds []string
	for _, e := range entries(n) {
		// An alias or a merge key hides a value Pacto judges, and a scalar
		// where a mapping or list belongs was a template action.
		if e.key == "<<" || judged(e.key) && (hides(e.val) || wrongShape(e.key, e.val)) {
			f.templated = true
		}
		switch e.key {
		case "apiVersion":
			f.apiVersion = e.val.Value
		case "kind":
			kinds = append(kinds, e.val.Value)
		case "metadata":
			if names := valuesOf(e.val, "name"); len(names) > 0 {
				f.name = names[0]
			}
			f.namespaces = append(f.namespaces, valuesOf(e.val, "namespace")...)
		case "roleRef":
			f.roleRefKinds, f.roleRef = valuesOf(e.val, "kind"), strings.Join(valuesOf(e.val, "name"), " or ")
		case "subjects":
			for _, s := range e.val.Content {
				var sub subject
				_ = s.Decode(&sub)
				f.subjects = append(f.subjects, sub)
			}
		}
	}
	f.templated = f.templated || len(kinds) > 1
	if len(kinds) == 1 {
		f.kind = kinds[0]
	}
	return f
}

// judged are the top-level keys whose values Pacto judges.
func judged(key string) bool {
	return slices.Contains([]string{"apiVersion", "kind", "metadata", "roleRef", "subjects"}, key)
}

// hides reports whether n, or any value under it, is an alias or a merge key.
func hides(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return true
	}
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 && c.Value == "<<" || hides(c) {
			return true
		}
	}
	return false
}

// wrongShape reports whether the value of key is not what Kubernetes reads
// there: metadata and roleRef are mappings, subjects a list of mappings (or
// null).
func wrongShape(key string, n *yaml.Node) bool {
	switch key {
	case "metadata", "roleRef":
		return n.Kind != yaml.MappingNode
	case "subjects":
		if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
			return false
		}
		return n.Kind != yaml.SequenceNode || slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return c.Kind != yaml.MappingNode })
	}
	return false
}

// valuesOf lists the values of key in mapping n.
func valuesOf(n *yaml.Node, key string) []string {
	var out []string
	for _, e := range entries(n) {
		if e.key == key {
			out = append(out, e.val.Value)
		}
	}
	return out
}

// entry is one key and value of a mapping node.
type entry struct {
	key string
	val *yaml.Node
}

// entries lists every entry of a mapping node, repeated keys included.
func entries(n *yaml.Node) []entry {
	var out []entry
	for i := 0; n.Kind == yaml.MappingNode && i+1 < len(n.Content); i += 2 {
		out = append(out, entry{n.Content[i].Value, n.Content[i+1]})
	}
	return out
}

// kindInfo is what the cluster says about a kind: its scope and whether it is
// infrastructure.
type kindInfo struct {
	scope ScopeFunc
	infra InfraFunc
}

// rawInfra says whether kind of group is raw infrastructure, and why Pacto
// cannot tell when it cannot. A core kind never is one, and a kind scope does
// not know is already unread.
func (k kindInfo) rawInfra(group, kind string) (raw bool, unread string) {
	if crossplaneGroup(group) {
		return true, ""
	}
	if _, builtin := BuiltinScope(group, kind); builtin || k.infra == nil {
		return false, ""
	}
	if _, known := k.scope(group, kind); !known {
		return false, ""
	}
	raw, known := k.infra(group, kind)
	if !known {
		return false, "Pacto cannot tell whether kind " + group + "/" + kind + " is infrastructure"
	}
	return raw, ""
}

// judgeDoc splits d into objects and judges each, and reports false once
// more than MaxManifestObjects documents were read, judged or not.
func (in *ManifestInspection) judgeDoc(d doc, k kindInfo) bool {
	var fs []facts
	var err error
	if d.chart {
		fs, err = templateFacts(d.data)
	} else {
		fs, err = splitFacts(d.data)
	}
	if err != nil {
		in.Unread = append(in.Unread, d.file+" is not readable YAML")
	}
	for _, f := range fs {
		if in.docs++; in.docs > MaxManifestObjects {
			return false
		}
		if d.override != "" {
			f.namespaces = []string{d.override}
		}
		in.judgeObject(d.file, f, k)
	}
	return true
}

// judgeObject judges one object and lists it.
func (in *ManifestInspection) judgeObject(file string, f facts, k kindInfo) {
	name := f.name
	if name == "" || strings.Contains(name, tplValue) || strings.Contains(name, releaseNS) {
		name = "in " + file
	}
	what := f.kind + " " + name
	if why := unreadable(f, what, file); why != "" {
		in.Unread = append(in.Unread, why)
		return
	}
	in.Total++
	if len(in.Objects) < MaxListedObjects {
		ns := ""
		if len(f.namespaces) > 0 {
			ns = strings.ReplaceAll(f.namespaces[0], releaseNS, in.Namespace)
		}
		in.Objects = append(in.Objects, ManifestObject{Kind: f.kind, Namespace: ns, Name: name})
	}
	if group := groupOf(f.apiVersion); !deliveryGroup(group) {
		raw, why := k.rawInfra(group, f.kind)
		if raw {
			in.Infra = append(in.Infra, what+" is raw infrastructure: "+rawInfraStep)
		}
		if why != "" {
			in.Unread = append(in.Unread, what+": "+why)
		}
	}
	refused, unread := judgeFacts(in.Namespace, what, name, f, k.scope)
	if refused != "" {
		in.Refused = append(in.Refused, refused)
	}
	if unread != "" {
		in.Unread = append(in.Unread, unread)
	}
}

// unreadable says why Pacto cannot judge an object at all: no literal kind, or
// a substitution in a value it judges.
func unreadable(f facts, what, file string) string {
	values := append(append([]string{f.kind}, f.roleRefKinds...), f.namespaces...)
	for _, s := range f.subjects {
		values = append(values, s.Kind, s.Namespace)
	}
	switch {
	case f.templated:
		return "a document in " + file + " is built by a template Pacto cannot read"
	case f.kind == "" || strings.Contains(f.kind, tplValue):
		return "a document in " + file + " names no literal kind"
	case slices.ContainsFunc(values, func(v string) bool { return strings.Contains(v, "${") }):
		return what + " holds a substitution (${...}) Pacto cannot resolve"
	}
	return ""
}

// judgeFacts judges one object into ns, in this order: a delivery or applier
// object, a cluster-scoped kind and an object in another namespace are
// refused, then a RoleBinding is judged, and a kind scope does not know is
// unread.
func judgeFacts(ns, what, name string, f facts, scope ScopeFunc) (refused, unread string) {
	group := groupOf(f.apiVersion)
	if deliveryGroup(group) {
		return what + " is a delivery or applier object, and a version cannot change how it is delivered", ""
	}
	namespaced, known := scope(group, f.kind)
	if known && !namespaced {
		return what + " is cluster-scoped", ""
	}
	if refused, unread = judgeNamespaces(ns, what, f.namespaces); refused+unread != "" {
		return refused, unread
	}
	if group == "rbac.authorization.k8s.io" && f.kind == "RoleBinding" {
		if refused, unread = judgeBinding(ns, what, name, f); refused+unread != "" {
			return refused, unread
		}
	}
	if !known {
		return "", what + ": Pacto cannot tell whether kind " + strings.TrimPrefix(group+"/"+f.kind, "/") + " is namespaced"
	}
	return "", ""
}

// groupOf is the API group of apiVersion; the core group is "".
func groupOf(apiVersion string) string {
	if g, _, ok := strings.Cut(apiVersion, "/"); ok {
		return g
	}
	return ""
}

// deliveryGroup is a Pacto delivery, Flux or Argo CD API group.
func deliveryGroup(group string) bool {
	return group == "delivery.pacto.trianalab.io" || group == "argoproj.io" || strings.HasSuffix(group, ".toolkit.fluxcd.io")
}

// judgeNamespaces refuses a namespace other than ns; a templated one is
// unread.
func judgeNamespaces(ns, what string, namespaces []string) (refused, unread string) {
	for _, n := range namespaces {
		switch {
		case n == releaseNS || n == "" || n == ns:
		case strings.Contains(n, tplValue):
			return "", what + " takes its namespace from a template value"
		default:
			return what + " is in " + n + ", outside " + ns, ""
		}
	}
	return "", ""
}

// judgeBinding judges a RoleBinding's roleRef and subjects: a ClusterRole is
// refused; a Group, a User or a ServiceAccount of another namespace is a
// grant a person decides on.
func judgeBinding(ns, what, name string, f facts) (refused, unread string) {
	switch {
	case slices.Contains(f.roleRefKinds, "ClusterRole"):
		return "RoleBinding " + name + " binds ClusterRole " + f.roleRef, ""
	case len(f.roleRefKinds) == 0 || slices.ContainsFunc(f.roleRefKinds, func(k string) bool { return k != "Role" }):
		return "", "Pacto cannot read the roleRef of " + what
	}
	for _, s := range f.subjects {
		if s.Kind == "ServiceAccount" && (s.Namespace == "" || s.Namespace == ns || s.Namespace == releaseNS) {
			continue
		}
		who := strings.TrimSpace(s.Kind + " " + strings.ReplaceAll(s.Name, tplValue, "(templated)"))
		if s.Namespace != "" {
			who += " of " + strings.ReplaceAll(s.Namespace, tplValue, "(templated)")
		}
		return "", "RoleBinding " + name + " grants " + who + ", outside " + ns
	}
	return "", ""
}

// Verdict is the pacto.manifests verdict: Fail on any refusal, else Unknown on
// anything unread, else Pass. Raw infrastructure is a refusal with code
// CodeRawInfraInArtifact when RefuseInfra holds, else unread. The citation
// lists the objects and the refusals, each bounded to MaxListedObjects.
func (in ManifestInspection) Verdict() Verdict {
	v := Verdict{Gate: GateManifests, Phase: "pre", Result: Pass, Citation: map[string]string{"artifact": in.Artifact}}
	names := make([]string, len(in.Objects))
	for i, o := range in.Objects {
		names[i] = o.Kind + " " + o.Name
	}
	if objects := strings.Join(names, ", "); in.Total > len(names) {
		v.Citation["objects"] = fmt.Sprintf("%s and %d more", objects, in.Total-len(names))
	} else {
		v.Citation["objects"] = objects
	}
	refused, unread := in.Refused, in.Unread
	if in.RefuseInfra && len(in.Infra) > 0 {
		refused, v.Citation["code"] = slices.Concat(refused, in.Infra), CodeRawInfraInArtifact
	} else {
		unread = slices.Concat(unread, in.Infra)
	}
	switch {
	case len(refused) > 0:
		v.Result, v.Citation["refused"] = Fail, listed(refused)
		v.Message = "the manifest inspector refuses what the version applies: " + v.Citation["refused"]
		if len(in.Refused) == 0 {
			v.Message = "the version carries raw infrastructure: " + v.Citation["refused"]
		}
	case len(unread) > 0:
		v.Result = Unknown
		v.Message = "Pacto cannot tell what the version applies: " + listed(unread)
	default:
		v.Message = fmt.Sprintf("every one of the %d objects stays in %s", in.Total, in.Namespace)
	}
	if len(unread) > 0 {
		v.Citation["unread"] = listed(unread)
	}
	return v
}

// listed joins at most MaxListedObjects sentences with "; ", the separator
// readers split on, so no sentence holds one, and counts the rest.
func listed(sentences []string) string {
	out := make([]string, min(len(sentences), MaxListedObjects))
	for i := range out {
		out[i] = strings.ReplaceAll(sentences[i], "; ", ", ")
	}
	if more := len(sentences) - len(out); more > 0 {
		out = append(out, fmt.Sprintf("and %d more", more))
	}
	return strings.Join(out, "; ")
}

// maxLayerBytes is the most compressed bytes InspectRevision downloads.
const maxLayerBytes = MaxManifestBytes

// InspectRevision reads the deploy artifact repo@digest through the registry,
// bounded, and inspects it. A read that fails is an Unknown inspection that
// says why.
func InspectRevision(ctx context.Context, reg Registry, repo, digest string, target ManifestTarget, scope ScopeFunc) ManifestInspection {
	bounded, cancel := WithRegistryTimeout(ctx)
	defer cancel()
	mediaType, layer, err := reg.Layer(bounded, repo+"@"+digest, maxLayerBytes)
	if err != nil {
		why := err.Error()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			why = "the registry did not answer within " + registryTimeout.String()
		}
		return ManifestInspection{Namespace: target.Namespace, Refused: slices.Clone(target.Refused),
			Unread: append(slices.Clone(target.Unread), "Pacto could not read the artifact "+short(digest)+": "+why)}
	}
	return InspectArtifact(mediaType, layer, target, scope)
}

// manifestsGate judges pacto.manifests with in.Manifests, which it calls only
// here, after pacto.published did not fail and pacto.applier passed. A Fail
// rejects. An Unknown with an unknown publication asks one question naming
// both and stops the chain, as the publication's own question does;
// otherwise it returns what Pacto cannot tell, for the caller to ask after
// the later gates. It reports whether the promote may go on.
func manifestsGate(ctx context.Context, in PlanInput, p *Plan, pub Verdict) (bool, string) {
	ins := in.Manifests(ctx)
	ins.RefuseInfra = in.RefuseRawInfra
	v := ins.Verdict()
	p.Verdicts = append(p.Verdicts, v)
	switch v.Result {
	case Fail:
		return false, ""
	case Unknown:
		what := "what " + short(p.To) + " applies: " + v.Citation["unread"]
		if pub.Result == Unknown {
			p.Floor = Floor{Human: true, Decision: AcceptUnknown}
			p.Question = "Pacto cannot tell whether pacto publish published " + short(p.To) + ": " + pub.Message + "; and " + what + ". Accept the unknown?"
			return false, ""
		}
		return true, what
	}
	return true, ""
}
