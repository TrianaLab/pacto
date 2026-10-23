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
	workloads   []*workload
	workload    *workload // the one the contract describes
	openapi     string
	chart       string
	kustomize   []string // kustomization.yaml files among the manifests
}

// workload is a Deployment, StatefulSet, DaemonSet, Job or CronJob.
type workload struct {
	kind, name, file, image, team string
	ports                         []int
	env                           []envVar
}

type envVar struct{ file, name, value string }

var workloadKinds = map[string]string{
	"Deployment": "service", "StatefulSet": "service", "DaemonSet": "service",
	"Job": "job", "CronJob": "scheduled",
}

var openapiRE = regexp.MustCompile(`^(openapi|swagger)\.(ya?ml|json)$`)

// skipDirs are never read: tooling, dependencies and the cluster pieces
// pacto init writes itself.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true}

func scanDir(dir string) (*scan, error) {
	sc := &scan{objects: map[string]bool{}}
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
	found, secret := false, false
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
		if _, ok := doc["apiVersion"].(string); !ok || kind == "" {
			continue
		}
		found = true
		if kind == "Secret" && (doc["data"] != nil || doc["stringData"] != nil) {
			secret = true
		}
		name, _ := dig(doc, "metadata", "name").(string)
		sc.objects[name] = true
		if path.Base(rel) == "kustomization.yaml" || kind == "Kustomization" {
			sc.kustomize = append(sc.kustomize, rel)
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
	}
}

func readWorkload(rel, kind, name string, doc map[string]any) *workload {
	w := &workload{kind: kind, name: name, file: rel}
	w.team, _ = dig(doc, "metadata", "labels", "team").(string)
	pod := dig(doc, "spec", "template", "spec")
	if kind == "CronJob" {
		pod = dig(doc, "spec", "jobTemplate", "spec", "template", "spec")
	}
	containers, _ := dig(pod, "containers").([]any)
	for i, c := range containers {
		if image, _ := dig(c, "image").(string); i == 0 || dig(c, "name") == name {
			w.image = image
		}
		ports, _ := dig(c, "ports").([]any)
		for _, port := range ports {
			if n, ok := dig(port, "containerPort").(int); ok {
				w.ports = append(w.ports, n)
			}
		}
		env, _ := dig(c, "env").([]any)
		for _, e := range env {
			n, _ := dig(e, "name").(string)
			v, _ := dig(e, "value").(string)
			w.env = append(w.env, envVar{file: rel, name: n, value: v})
		}
	}
	return w
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
	if sc.workload == nil {
		return out
	}
	seen := map[string]bool{}
	for _, e := range sc.workload.env {
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
