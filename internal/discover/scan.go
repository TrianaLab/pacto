package discover

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/ignore"
)

// Scan is what a folder says about the service in it.
type Scan struct {
	Dockerfile  string   // the Dockerfile used, relative and slash-separated
	Dockerfiles []string // every Dockerfile found
	Manifests   []string // YAML files that hold Kubernetes objects
	Secrets     []string // YAML files holding a Secret's values, never published
	objects     map[string]bool
	Services    map[string]bool // Kubernetes Services, by name
	workloads   []*Workload
	Workload    *Workload // the one the contract describes
	Charts      []string
	Kustomize   []string // kustomization.yaml files among the manifests
	ConfigMaps  map[string]ConfigMap
	// Env is the workload's environment, with every ConfigMap it reads
	// expanded, and EnvNotes what of it could not be read.
	Env       []EnvVar
	EnvNotes  []string
	ign       *ignore.Matcher   // .pactoignore: what it leaves out is not read
	sdkFiles  []string          // dependency manifests and lockfiles, in walk order
	tfFiles   []string          // Terraform configuration, JSON configuration and state, in walk order
	xpObjects []xpObject        // objects of a dotted API group a catalog may name a managed resource kind
	files     map[string][]byte // every YAML file read, by path, for source lines
}

// ConfigMap is a ConfigMap's data and the file it is in.
type ConfigMap struct {
	file string
	line int // where its document starts
	data map[string]string
}

// Workload is a Deployment, StatefulSet, DaemonSet, Job or CronJob.
type Workload struct {
	Kind, Name, File, Image, Team string
	// KindSource and TeamSource are the "<path>:<line>" of its kind and of
	// its team label.
	KindSource, TeamSource string
	Env                    []EnvVar
	// Probe is the main container's first readiness, liveness or startup
	// probe, nil when it has none.
	Probe *Probe
}

// EnvVar is one environment variable. A ConfigMap or Secret it is read from
// is named in from: "configmap" or "secret" with ref the object's name and
// key the key, or no key when every key of the object is read (envFrom, with
// name its prefix). secret marks a value that is never read. Source is the
// "<path>:<line>" that sets it.
type EnvVar struct {
	File, Name, Value string
	From, Ref, Key    string
	Secret            bool
	Source            string
}

// Probe is a container probe: which one, and the HTTP path it calls, empty
// for a TCP, gRPC or exec probe, and the "<path>:<line>" that declares it.
type Probe struct{ Kind, Path, Source string }

// WorkloadKinds maps each workload kind to the contract workload it is.
var WorkloadKinds = map[string]string{
	"Deployment": "service", "StatefulSet": "service", "DaemonSet": "service",
	"Job": "job", "CronJob": "scheduled",
}

// skipDirs are never read: tooling, dependencies and the cluster pieces
// pacto init writes itself.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true}

// Dir reads what the folder dir says about the service in it. A folder that
// does not exist yet reads as empty.
func Dir(dir string) (*Scan, error) {
	sc := &Scan{objects: map[string]bool{}, Services: map[string]bool{}, ConfigMaps: map[string]ConfigMap{}, files: map[string][]byte{}}
	// What .pactoignore leaves out of the bundle is not read either.
	ign, err := ignore.Load(os.DirFS(dir))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Join(dir, ignore.IgnoreFileName), err)
	}
	sc.ign = ign
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == dir {
			return fs.SkipAll // a folder that is not there yet is empty
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p) // ponytail: p is always under dir
		rel = filepath.ToSlash(rel)
		if rel != "." && ign.Ignored(rel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return sc.dir(p, rel, d.Name())
		}
		sc.file(p, rel, d.Name())
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	sort.SliceStable(sc.Dockerfiles, func(i, j int) bool { return dockerRank(sc.Dockerfiles[i]) < dockerRank(sc.Dockerfiles[j]) })
	if len(sc.Dockerfiles) > 0 {
		sc.Dockerfile = sc.Dockerfiles[0]
	}
	sc.Workload = pickWorkload(sc.workloads, filepath.Base(dir))
	sc.resolveEnv()
	return sc, nil
}

// dir decides whether Dir walks into a folder: fs.SkipDir to leave it
// out, nil to read it.
func (sc *Scan) dir(p, rel, name string) error {
	if rel != "." && (strings.HasPrefix(name, ".") || skipDirs[name] || rel == "delivery") {
		return fs.SkipDir
	}
	// A Helm chart's templates are not manifests until rendered, so a
	// chart folder is never read. When the folder itself is a chart,
	// only its templates, subcharts and CRDs are left out.
	if _, err := os.Stat(filepath.Join(p, "Chart.yaml")); err == nil {
		sc.Charts = append(sc.Charts, rel)
		if rel != "." {
			return fs.SkipDir
		}
	}
	if slices.Contains(sc.Charts, ".") && (rel == "templates" || rel == "charts" || rel == "crds") {
		return fs.SkipDir
	}
	return nil
}

// dockerRank orders Dockerfiles shallowest first, a plain Dockerfile first.
func dockerRank(p string) string {
	plain := "1"
	if path.Base(p) == "Dockerfile" {
		plain = "0"
	}
	return fmt.Sprintf("%03d%s%s", strings.Count(p, "/"), plain, p)
}

// pickWorkload is the workload named like the folder, else the first.
func pickWorkload(ws []*Workload, base string) *Workload {
	for _, w := range ws {
		if w.Name == base {
			return w
		}
	}
	if len(ws) > 0 {
		return ws[0]
	}
	return nil
}

func (sc *Scan) file(p, rel, base string) {
	switch {
	case base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") || strings.HasSuffix(base, ".Dockerfile"):
		sc.Dockerfiles = append(sc.Dockerfiles, rel)
	case base == "Chart.yaml": // its folder is a chart, recorded by Dir
	case sdkFile(base):
		sc.sdkFiles = append(sc.sdkFiles, rel)
	case terraformFile(base):
		sc.tfFiles = append(sc.tfFiles, rel)
	case strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"):
		data, err := os.ReadFile(p)
		if err == nil {
			sc.manifest(rel, data)
		}
	}
}

// toolGroups are apiVersion groups of tool configs written like Kubernetes
// objects that never go to a cluster: a Backstage catalog-info.yaml and a
// skaffold.yaml. Flux would refuse a kind the cluster does not serve.
// ponytail: a denylist of the collisions seen so far; .pactoignore keeps out
// the next one until it is added here.
var toolGroups = map[string]bool{"backstage.io": true, "skaffold": true}

// manifest reads every Kubernetes object in one YAML file. A file that is not
// YAML, or holds no object with apiVersion and kind, is not a manifest.
func (sc *Scan) manifest(rel string, data []byte) {
	sc.files[rel] = data
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// A kustomization.yaml needs no apiVersion or kind; its secretGenerator
	// holds a Secret's values as plainly as a Secret does.
	kust := path.Base(rel) == "kustomization.yaml"
	found, secret := kust, false
	for {
		doc, line, err := nextDoc(dec)
		if err == io.EOF {
			break
		}
		if err != nil {
			return
		}
		src := func(needle string) string { return sc.source(rel, line, needle) }
		kind, _ := doc["kind"].(string)
		secret = secret || holdsSecret(kust, kind, doc)
		api, ok := doc["apiVersion"].(string)
		group, _, _ := strings.Cut(api, "/")
		if !ok || kind == "" || toolGroups[group] {
			continue
		}
		found = true
		name, _ := Dig(doc, "metadata", "name").(string)
		sc.objects[name] = true
		sc.Services[name] = sc.Services[name] || kind == "Service"
		kust = kust || kind == "Kustomization"
		sc.object(rel, line, group, kind, name, doc, src)
	}
	switch {
	case secret:
		sc.Secrets = append(sc.Secrets, rel)
	case found:
		sc.Manifests = append(sc.Manifests, rel)
		if kust {
			sc.Kustomize = append(sc.Kustomize, rel)
		}
	}
}

// object records the object doc of the file rel, starting at line, when it
// is a ConfigMap, a workload or a candidate managed resource.
func (sc *Scan) object(rel string, line int, group, kind, name string, doc map[string]any, src func(needle string) string) {
	if kind == "ConfigMap" {
		sc.ConfigMaps[name] = ConfigMap{file: rel, line: line, data: stringMap(doc["data"])}
	}
	if _, ok := WorkloadKinds[kind]; ok {
		sc.workloads = append(sc.workloads, readWorkload(rel, kind, name, doc, src))
	} else if o, ok := crossplaneCandidate(group, kind, name, doc); ok {
		o.file, o.line = splitLine(src("kind: " + kind))
		sc.xpObjects = append(sc.xpObjects, o)
	}
}

// nextDoc decodes the next YAML document, through a node for the line it
// starts at.
func nextDoc(dec *yaml.Decoder) (map[string]any, int, error) {
	var node yaml.Node
	var doc map[string]any
	if err := dec.Decode(&node); err != nil {
		return nil, 0, err
	}
	err := node.Decode(&doc)
	return doc, node.Line, err
}

// holdsSecret reports whether doc holds a Secret's values: a Secret with data,
// or a kustomization's secretGenerator.
func holdsSecret(kust bool, kind string, doc map[string]any) bool {
	if (kust || kind == "Kustomization") && doc["secretGenerator"] != nil {
		return true
	}
	return kind == "Secret" && (doc["data"] != nil || doc["stringData"] != nil)
}

// readWorkload reads one workload document; src is the source of a needle in it.
func readWorkload(rel, kind, name string, doc map[string]any, src func(needle string) string) *Workload {
	w := &Workload{Kind: kind, Name: name, File: rel, KindSource: src("kind: " + kind), TeamSource: src("team:")}
	w.Team, _ = Dig(doc, "metadata", "labels", "team").(string)
	pod := Dig(doc, "spec", "template", "spec")
	if kind == "CronJob" {
		pod = Dig(doc, "spec", "jobTemplate", "spec", "template", "spec")
	}
	containers, _ := Dig(pod, "containers").([]any)
	main := 0
	for i, c := range containers {
		if Dig(c, "name") == name {
			main = i
		}
	}
	for i, c := range containers {
		if i == main {
			w.Image, _ = Dig(c, "image").(string)
			w.Probe = readProbe(c, src)
		}
		// envFrom first: an env entry of the same name wins, as in Kubernetes.
		envFrom, _ := Dig(c, "envFrom").([]any)
		for _, e := range envFrom {
			prefix, _ := Dig(e, "prefix").(string)
			if n, ok := Dig(e, "configMapRef", "name").(string); ok {
				w.Env = append(w.Env, EnvVar{File: rel, Name: prefix, From: "configmap", Ref: n})
			} else if n, ok := Dig(e, "secretRef", "name").(string); ok {
				w.Env = append(w.Env, EnvVar{File: rel, Name: prefix, From: "secret", Ref: n, Secret: true})
			}
		}
		env, _ := Dig(c, "env").([]any)
		for _, e := range env {
			v := EnvVar{File: rel}
			v.Name, _ = Dig(e, "name").(string)
			v.Source = src("name: " + v.Name)
			v.Value, _ = Dig(e, "value").(string)
			if n, ok := Dig(e, "valueFrom", "configMapKeyRef", "name").(string); ok {
				v.From, v.Ref = "configmap", n
				v.Key, _ = Dig(e, "valueFrom", "configMapKeyRef", "key").(string)
			} else if n, ok := Dig(e, "valueFrom", "secretKeyRef", "name").(string); ok {
				v.From, v.Ref, v.Secret = "secret", n, true
				v.Key, _ = Dig(e, "valueFrom", "secretKeyRef", "key").(string)
			}
			w.Env = append(w.Env, v)
		}
	}
	return w
}

// readProbe is a container's first readiness, liveness or startup probe.
func readProbe(c any, src func(needle string) string) *Probe {
	for _, kind := range []string{"readiness", "liveness", "startup"} {
		p := Dig(c, kind+"Probe")
		if p == nil {
			continue
		}
		path, _ := Dig(p, "httpGet", "path").(string)
		return &Probe{Kind: kind, Path: path, Source: src(kind + "Probe:")}
	}
	return nil
}

// resolveEnv expands the workload's environment: every variable read from a
// ConfigMap in the folder gets its value, and one whose ConfigMap or Secret
// cannot be read is named in envNotes. A later variable of the same name
// replaces an earlier one, keeping the earlier one's place.
func (sc *Scan) resolveEnv() {
	if sc.Workload == nil {
		return
	}
	at := map[string]int{}
	add := func(v EnvVar) {
		if i, ok := at[v.Name]; ok {
			sc.Env[i] = v
			return
		}
		at[v.Name] = len(sc.Env)
		sc.Env = append(sc.Env, v)
	}
	for _, v := range sc.Workload.Env {
		cm, found := sc.ConfigMaps[v.Ref]
		switch {
		case v.From == "secret" && v.Key == "":
			sc.EnvNotes = append(sc.EnvNotes, fmt.Sprintf("the environment %s reads from the Secret %s: its keys are not read, so they are not in the configuration schema", v.File, v.Ref))
		case v.From == "configmap" && !found:
			sc.EnvNotes = append(sc.EnvNotes, fmt.Sprintf("the ConfigMap %s, which %s reads its environment from, is not in the folder, so its keys are not in the configuration schema", v.Ref, v.File))
			if v.Key != "" {
				add(v)
			}
		case v.From == "configmap" && v.Key == "":
			keys := make([]string, 0, len(cm.data))
			for k := range cm.data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				add(EnvVar{File: cm.file, Name: v.Name + k, Value: cm.data[k], From: "configmap", Ref: v.Ref, Key: k, Source: sc.source(cm.file, cm.line, k+":")})
			}
		case v.From == "configmap":
			v.File, v.Value, v.Source = cm.file, cm.data[v.Key], sc.source(cm.file, cm.line, v.Key+":")
			add(v)
		default:
			add(v)
		}
	}
}

// source is "<file>:<n>" for the first line n at or after line from that
// holds needle, else the bare file.
// ponytail: first textual match; a yaml.Node walk when one file repeats a key across documents.
func (sc *Scan) source(file string, from int, needle string) string {
	for i, l := range strings.Split(string(sc.files[file]), "\n") {
		if i+1 >= from && strings.Contains(l, needle) {
			return fmt.Sprintf("%s:%d", file, i+1)
		}
	}
	return file
}

// stringMap is a YAML mapping's string values.
func stringMap(v any) map[string]string {
	m, _ := v.(map[string]any)
	out := make(map[string]string, len(m))
	for k, val := range m {
		if val != nil {
			out[k] = fmt.Sprint(val)
		}
	}
	return out
}

// Dig walks nested maps.
func Dig(v any, keys ...string) any {
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

// Finding is a service found to depend on, or use, this one.
type Finding struct {
	Name  string `json:"name"`
	Where string `json:"where"`
}

// hostRE reads the host of a URL or host:port. Only a bare name or a cluster
// name (name.namespace.svc...) is another service; anything else with a dot is
// outside the cluster.
var hostRE = regexp.MustCompile(`^(?:([a-z][a-z0-9+.-]*)://)?(?:[^@/]*@)?([a-z0-9]([a-z0-9-]*[a-z0-9])?)((?:\.[a-z0-9-]+)*)(:[0-9]+)?(/.*)?$`)

// Dependencies are the services the workload's environment points at,
// leaving out itself and anything its own manifests define. A dotted name
// outside the cluster is left to externals.
func (sc *Scan) Dependencies(self string) []Finding {
	out, _ := sc.dependencies(self)
	return out
}

// dependencies are Dependencies and, for each, the variable that names it.
func (sc *Scan) dependencies(self string) ([]Finding, []EnvVar) {
	out, from := []Finding{}, []EnvVar{}
	seen := map[string]bool{}
	for _, e := range sc.Env {
		m := hostRE.FindStringSubmatch(e.Value)
		if m == nil || (m[1] == "" && m[5] == "") {
			continue
		}
		host, rest := m[2], m[4]
		if rest != "" && !strings.Contains(rest+".", ".svc.") {
			continue
		}
		if host == self || host == "localhost" || sc.objects[host] || seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, Finding{Name: host, Where: e.File + ", env " + e.Name})
		from = append(from, e)
	}
	return out, from
}

// EnvDependencies are the services env points at, leaving out self and every
// object named in objects: Dependencies over a scan that holds only them.
func EnvDependencies(env []EnvVar, objects map[string]bool, self string) []Finding {
	return (&Scan{Env: env, objects: objects}).Dependencies(self)
}

// ImageRepo splits an image reference into its repository, with Docker Hub
// spelled out, and its tag.
func ImageRepo(image string) (repo, tag string) {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image, tag = image[:i], image[i+1:]
	}
	first, _, slash := strings.Cut(image, "/")
	if !slash {
		image = "library/" + image
	}
	if !slash || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		image = "docker.io/" + image
	}
	return image, tag
}
