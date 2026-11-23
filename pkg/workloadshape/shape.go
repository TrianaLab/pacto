// Package workloadshape reads the shape of a Kubernetes workload object (its
// image, ports and probes) from its decoded form. It imports only the
// standard library, so a cluster reader and a folder reader can share it.
// It never reads environment values, Secrets, ConfigMaps, probe headers, exec
// commands, container args or object metadata; of a Secret it reads only the
// name a pod template references.
package workloadshape

import (
	"slices"
	"strconv"
	"strings"
)

// WorkloadKinds are the workload kinds Pacto reads, in reading order.
var WorkloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob"}

// WorkloadShape is what Pacto shows of one workload. Every From names the
// object path the value was read from.
type WorkloadShape struct {
	Image, ImageFrom string
	Ports            []Port
	Probes           []Probe
}

// Port is one container port.
type Port struct {
	Number               int
	Name, Protocol, From string
}

// Probe is one HTTP probe of the main container: its kind (readiness,
// liveness or startup), path without query or fragment, and port.
type Probe struct {
	Kind, Path, Port, From string
}

// ReadWorkloadShape reads obj, a workload of kind named name. The pod
// template is spec.template.spec (spec.jobTemplate.spec.template.spec for a
// CronJob); the main container is the one named like the workload, else the
// first. Image and probes come from the main container, ports from every
// container.
func ReadWorkloadShape(kind, name string, obj map[string]any) WorkloadShape {
	podPath, pod := podTemplate(kind, obj)
	containers, _ := dig(pod, "containers").([]any)
	main := 0
	for i, c := range containers {
		if dig(c, "name") == name {
			main = i
			break
		}
	}
	var s WorkloadShape
	for i, c := range containers {
		cname, _ := dig(c, "name").(string)
		at := podPath + ".containers[name=" + cname + "]"
		if i == main {
			if img, ok := dig(c, "image").(string); ok && img != "" {
				s.Image, s.ImageFrom = img, at+".image"
			}
			s.Probes = readProbes(c, at)
		}
		ports, _ := dig(c, "ports").([]any)
		for j, p := range ports {
			n, ok := number(dig(p, "containerPort"))
			if !ok {
				continue
			}
			pname, _ := dig(p, "name").(string)
			proto, _ := dig(p, "protocol").(string)
			s.Ports = append(s.Ports, Port{Number: n, Name: pname, Protocol: proto, From: at + ".ports[" + strconv.Itoa(j) + "].containerPort"})
		}
	}
	return s
}

// podTemplate is the pod template of obj, a workload of kind, and its path:
// spec.template.spec, or spec.jobTemplate.spec.template.spec for a CronJob.
func podTemplate(kind string, obj map[string]any) (string, any) {
	if kind == "CronJob" {
		return "spec.jobTemplate.spec.template.spec", dig(obj, "spec", "jobTemplate", "spec", "template", "spec")
	}
	return "spec.template.spec", dig(obj, "spec", "template", "spec")
}

// SecretNames is every Secret name the pod template of obj, a workload of
// kind, references, sorted and unique: env[].valueFrom.secretKeyRef.name and
// envFrom[].secretRef.name of every container and init container,
// volumes[].secret.secretName and volumes[].projected.sources[].secret.name.
// Only the names are read, never a key, value or Secret.
func SecretNames(kind string, obj map[string]any) []string {
	_, pod := podTemplate(kind, obj)
	var names []string
	add := func(v any) {
		if n, ok := v.(string); ok && n != "" {
			names = append(names, n)
		}
	}
	for _, list := range []string{"containers", "initContainers"} {
		containers, _ := dig(pod, list).([]any)
		for _, c := range containers {
			env, _ := dig(c, "env").([]any)
			for _, e := range env {
				add(dig(e, "valueFrom", "secretKeyRef", "name"))
			}
			envFrom, _ := dig(c, "envFrom").([]any)
			for _, e := range envFrom {
				add(dig(e, "secretRef", "name"))
			}
		}
	}
	volumes, _ := dig(pod, "volumes").([]any)
	for _, v := range volumes {
		add(dig(v, "secret", "secretName"))
		sources, _ := dig(v, "projected", "sources").([]any)
		for _, src := range sources {
			add(dig(src, "secret", "name"))
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// readProbes is every readiness, liveness and startup probe of c, in that
// order: only its httpGet path (cut at the first ? or #) and port.
func readProbes(c any, at string) []Probe {
	var out []Probe
	for _, kind := range []string{"readiness", "liveness", "startup"} {
		p := dig(c, kind+"Probe")
		if p == nil {
			continue
		}
		path, _ := dig(p, "httpGet", "path").(string)
		path, _, _ = strings.Cut(path, "?")
		path, _, _ = strings.Cut(path, "#")
		port := ""
		switch v := dig(p, "httpGet", "port").(type) {
		case string:
			port = v
		default:
			if n, ok := number(v); ok {
				port = strconv.Itoa(n)
			}
		}
		out = append(out, Probe{Kind: kind, Path: path, Port: port, From: at + "." + kind + "Probe"})
	}
	return out
}

// number reads an int that YAML decodes as int or int64 and encoding/json as
// float64.
func number(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// dig walks nested maps by keys, nil when a step is missing.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}
