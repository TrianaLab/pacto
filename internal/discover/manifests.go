package discover

import (
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// manifests reads each workload in the YAML documents of the file p, a plain
// manifest outside any chart: every container's ports and environment names,
// and the main container's image. An environment value is never read.
func (r *reader) manifests(p string, docs []*yaml.Node) {
	for _, doc := range docs {
		obj := doc.Content[0]
		kind := scalar(contract.MapValue(obj, "kind"))
		if _, ok := WorkloadKinds[kind]; !ok || scalar(contract.MapValue(obj, "apiVersion")) == "" {
			continue
		}
		pod := dig(obj, "spec", "template", "spec")
		if kind == "CronJob" {
			pod = dig(obj, "spec", "jobTemplate", "spec", "template", "spec")
		}
		containers := items(contract.MapValue(pod, "containers"))
		main := 0
		for i, c := range containers {
			if scalar(contract.MapValue(c, "name")) == scalar(dig(obj, "metadata", "name")) {
				main = i
			}
		}
		for i, c := range containers {
			r.container(p, c, i == main)
		}
	}
}

// container reads one container of the manifest p.
func (r *reader) container(p string, c *yaml.Node, main bool) {
	if img := contract.MapValue(c, "image"); main && scalar(img) != "" {
		r.addAt(p, img.Line, rankManifest, "image", img.Value, "")
	}
	for _, port := range items(contract.MapValue(c, "ports")) {
		r.port(p, rankManifest, contract.MapValue(port, "containerPort"), scalar(contract.MapValue(port, "protocol")))
	}
	for _, e := range items(contract.MapValue(c, "env")) {
		if name := contract.MapValue(e, "name"); scalar(name) != "" {
			r.addKey(p, name.Line, rankManifest, name.Value)
		}
	}
}

// dig walks nested mappings by key.
func dig(n *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		n = contract.MapValue(n, k)
	}
	return n
}
