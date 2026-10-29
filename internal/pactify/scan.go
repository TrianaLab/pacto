package pactify

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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// scan is what a folder says about the service in it.
type scan struct {
	dockerfile  string   // the Dockerfile used, relative and slash-separated
	dockerfiles []string // every Dockerfile found
	manifests   []string // YAML files that hold Kubernetes objects
	secrets     []string // YAML files holding a Secret's values, never published
	objects     map[string]bool
	services    map[string]bool // Kubernetes Services, by name
	workloads   []*workload
	workload    *workload // the one the contract describes
	openapi     string
	chart       string
	kustomize   []string // kustomization.yaml files among the manifests
	configMaps  map[string]configMap
	// env is the workload's environment, with every ConfigMap it reads
	// expanded, and envNotes what of it could not be read.
	env      []envVar
	envNotes []string
}

// configMap is a ConfigMap's data and the file it is in.
type configMap struct {
	file string
	data map[string]string
}

// workload is a Deployment, StatefulSet, DaemonSet, Job or CronJob.
type workload struct {
	kind, name, file, image, team string
	ports                         []int
	env                           []envVar
	// probe is the main container's first readiness, liveness or startup
	// probe, nil when it has none.
	probe *probe
}

// envVar is one environment variable. A ConfigMap or Secret it is read from
// is named in from: "configmap" or "secret" with ref the object's name and
// key the key, or no key when every key of the object is read (envFrom, with
// name its prefix). secret marks a value that is never read.
type envVar struct {
	file, name, value string
	from, ref, key    string
	secret            bool
}

// probe is a container probe: which one, and the HTTP path it calls, empty
// for a TCP, gRPC or exec probe.
type probe struct{ kind, path string }

var workloadKinds = map[string]string{
	"Deployment": "service", "StatefulSet": "service", "DaemonSet": "service",
	"Job": "job", "CronJob": "scheduled",
}

var openapiRE = regexp.MustCompile(`^(openapi|swagger)\.(ya?ml|json)$`)

// skipDirs are never read: tooling, dependencies and the cluster pieces
// pacto init writes itself.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true}

func scanDir(dir string) (*scan, error) {
	sc := &scan{objects: map[string]bool{}, services: map[string]bool{}, configMaps: map[string]configMap{}}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == dir {
			return fs.SkipAll // a folder that is not there yet is empty
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p) // ponytail: p is always under dir
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()] || rel == "delivery") {
				return fs.SkipDir
			}
			return nil
		}
		sc.file(p, rel, d.Name())
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	sort.SliceStable(sc.dockerfiles, func(i, j int) bool { return dockerRank(sc.dockerfiles[i]) < dockerRank(sc.dockerfiles[j]) })
	if len(sc.dockerfiles) > 0 {
		sc.dockerfile = sc.dockerfiles[0]
	}
	sc.workload = pickWorkload(sc.workloads, filepath.Base(dir))
	sc.resolveEnv()
	return sc, nil
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
func pickWorkload(ws []*workload, base string) *workload {
	for _, w := range ws {
		if w.name == base {
			return w
		}
	}
	if len(ws) > 0 {
		return ws[0]
	}
	return nil
}

func (sc *scan) file(p, rel, base string) {
	switch {
	case base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") || strings.HasSuffix(base, ".Dockerfile"):
		sc.dockerfiles = append(sc.dockerfiles, rel)
	case base == "Chart.yaml":
		if sc.chart == "" {
			sc.chart = path.Dir(rel)
		}
	case openapiRE.MatchString(base):
		if sc.openapi == "" || strings.Count(rel, "/") < strings.Count(sc.openapi, "/") {
			sc.openapi = rel
		}
	case strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml"):
		data, err := os.ReadFile(p)
		if err == nil {
			sc.manifest(rel, data)
		}
	}
}

// manifest reads every Kubernetes object in one YAML file. A file that is not
// YAML, or holds no object with apiVersion and kind, is not a manifest.
func (sc *scan) manifest(rel string, data []byte) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// A kustomization.yaml needs no apiVersion or kind; its secretGenerator
	// holds a Secret's values as plainly as a Secret does.
	kust := path.Base(rel) == "kustomization.yaml"
	found, secret := kust, false
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return
		}
		kind, _ := doc["kind"].(string)
		secret = secret || holdsSecret(kust, kind, doc)
		if _, ok := doc["apiVersion"].(string); !ok || kind == "" {
			continue
		}
		found = true
		name, _ := dig(doc, "metadata", "name").(string)
		sc.objects[name] = true
		sc.services[name] = sc.services[name] || kind == "Service"
		kust = kust || kind == "Kustomization"
		if kind == "ConfigMap" {
			sc.configMaps[name] = configMap{file: rel, data: stringMap(doc["data"])}
		}
		if _, ok := workloadKinds[kind]; ok {
			sc.workloads = append(sc.workloads, readWorkload(rel, kind, name, doc))
		}
	}
	switch {
	case secret:
		sc.secrets = append(sc.secrets, rel)
	case found:
		sc.manifests = append(sc.manifests, rel)
		if kust {
			sc.kustomize = append(sc.kustomize, rel)
		}
	}
}

// holdsSecret reports whether doc holds a Secret's values: a Secret with data,
// or a kustomization's secretGenerator.
func holdsSecret(kust bool, kind string, doc map[string]any) bool {
	if (kust || kind == "Kustomization") && doc["secretGenerator"] != nil {
		return true
	}
	return kind == "Secret" && (doc["data"] != nil || doc["stringData"] != nil)
}

func readWorkload(rel, kind, name string, doc map[string]any) *workload {
	w := &workload{kind: kind, name: name, file: rel}
	w.team, _ = dig(doc, "metadata", "labels", "team").(string)
	pod := dig(doc, "spec", "template", "spec")
	if kind == "CronJob" {
		pod = dig(doc, "spec", "jobTemplate", "spec", "template", "spec")
	}
	containers, _ := dig(pod, "containers").([]any)
	main := 0
	for i, c := range containers {
		if dig(c, "name") == name {
			main = i
		}
	}
	for i, c := range containers {
		if i == main {
			w.image, _ = dig(c, "image").(string)
			w.probe = readProbe(c)
		}
		ports, _ := dig(c, "ports").([]any)
		for _, port := range ports {
			if n, ok := dig(port, "containerPort").(int); ok {
				w.ports = append(w.ports, n)
			}
		}
		// envFrom first: an env entry of the same name wins, as in Kubernetes.
		envFrom, _ := dig(c, "envFrom").([]any)
		for _, e := range envFrom {
			prefix, _ := dig(e, "prefix").(string)
			if n, ok := dig(e, "configMapRef", "name").(string); ok {
				w.env = append(w.env, envVar{file: rel, name: prefix, from: "configmap", ref: n})
			} else if n, ok := dig(e, "secretRef", "name").(string); ok {
				w.env = append(w.env, envVar{file: rel, name: prefix, from: "secret", ref: n, secret: true})
			}
		}
		env, _ := dig(c, "env").([]any)
		for _, e := range env {
			v := envVar{file: rel}
			v.name, _ = dig(e, "name").(string)
			v.value, _ = dig(e, "value").(string)
			if n, ok := dig(e, "valueFrom", "configMapKeyRef", "name").(string); ok {
				v.from, v.ref = "configmap", n
				v.key, _ = dig(e, "valueFrom", "configMapKeyRef", "key").(string)
			} else if n, ok := dig(e, "valueFrom", "secretKeyRef", "name").(string); ok {
				v.from, v.ref, v.secret = "secret", n, true
				v.key, _ = dig(e, "valueFrom", "secretKeyRef", "key").(string)
			}
			w.env = append(w.env, v)
		}
	}
	return w
}

// readProbe is a container's first readiness, liveness or startup probe.
func readProbe(c any) *probe {
	for _, kind := range []string{"readiness", "liveness", "startup"} {
		p := dig(c, kind+"Probe")
		if p == nil {
			continue
		}
		path, _ := dig(p, "httpGet", "path").(string)
		return &probe{kind: kind, path: path}
	}
	return nil
}

// resolveEnv expands the workload's environment: every variable read from a
// ConfigMap in the folder gets its value, and one whose ConfigMap or Secret
// cannot be read is named in envNotes. A later variable of the same name
// replaces an earlier one, keeping the earlier one's place.
func (sc *scan) resolveEnv() {
	if sc.workload == nil {
		return
	}
	at := map[string]int{}
	add := func(v envVar) {
		if i, ok := at[v.name]; ok {
			sc.env[i] = v
			return
		}
		at[v.name] = len(sc.env)
		sc.env = append(sc.env, v)
	}
	for _, v := range sc.workload.env {
		cm, found := sc.configMaps[v.ref]
		switch {
		case v.from == "secret" && v.key == "":
			sc.envNotes = append(sc.envNotes, fmt.Sprintf("the environment %s reads from the Secret %s: its keys are not read, so they are not in the configuration schema", v.file, v.ref))
		case v.from == "configmap" && !found:
			sc.envNotes = append(sc.envNotes, fmt.Sprintf("the ConfigMap %s, which %s reads its environment from, is not in the folder, so its keys are not in the configuration schema", v.ref, v.file))
			if v.key != "" {
				add(v)
			}
		case v.from == "configmap" && v.key == "":
			keys := make([]string, 0, len(cm.data))
			for k := range cm.data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				add(envVar{file: cm.file, name: v.name + k, value: cm.data[k], from: "configmap", ref: v.ref, key: k})
			}
		case v.from == "configmap":
			v.file, v.value = cm.file, cm.data[v.key]
			add(v)
		default:
			add(v)
		}
	}
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

// dig walks nested maps.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}

// hostRE reads the host of a URL or host:port. Only a bare name or a cluster
// name (name.namespace.svc...) is another service; anything else with a dot is
// outside the cluster.
var hostRE = regexp.MustCompile(`^(?:([a-z][a-z0-9+.-]*)://)?(?:[^@/]*@)?([a-z0-9]([a-z0-9-]*[a-z0-9])?)((?:\.[a-z0-9-]+)*)(:[0-9]+)?(/.*)?$`)

// dependencies are the services the workload's environment points at,
// leaving out itself and anything its own manifests define.
func (sc *scan) dependencies(self string) []Finding {
	out := []Finding{}
	seen := map[string]bool{}
	for _, e := range sc.env {
		m := hostRE.FindStringSubmatch(e.value)
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
		out = append(out, Finding{Name: host, Where: e.file + ", env " + e.name})
	}
	return out
}

// imageRepo splits an image reference into its repository, with Docker Hub
// spelled out, and its tag.
func imageRepo(image string) (repo, tag string) {
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
