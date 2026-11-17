package discover

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Helm is read statically: Chart.yaml, Chart.lock and the values files, as
// data. No template is rendered and no dependency is downloaded; a vendored
// charts/*.tgz is never opened.

// maxChartDepth is how deep under the folder a chart is read.
const maxChartDepth = 2

// valuesRE names a chart's values files: values.yaml, and values-<env>.yaml
// or values.<env>.yaml with the environment in group 1.
var valuesRE = regexp.MustCompile(`^values(?:[-.]([^.]+))?\.ya?ml$`)

// keepChartFile keeps the root of the parsed file p, named name, when the
// Helm reader reads it.
func (r *reader) keepChartFile(p, name string, docs []*yaml.Node) {
	if (name == "Chart.yaml" || name == "Chart.lock" || valuesRE.MatchString(name)) && len(docs) > 0 && len(docs[0].Content) > 0 {
		r.chartFiles[p] = docs[0].Content[0]
	}
}

// charts reads every chart at most maxChartDepth folders deep: the
// dependencies Chart.lock pins and what its values files say.
func (r *reader) charts() {
	for _, p := range slices.Sorted(maps.Keys(r.chartFiles)) {
		dir := path.Dir(p)
		if path.Base(p) != "Chart.yaml" || (dir != "." && strings.Count(dir, "/") >= maxChartDepth) {
			continue
		}
		r.chartDependencies(dir)
		r.chartValues(dir)
	}
}

// chartDependencies is each dependency of the chart in dir that Chart.lock
// pins, under the lock's digest; any other is a finding.
func (r *reader) chartDependencies(dir string) {
	chartYAML := path.Join(dir, "Chart.yaml")
	lock := r.chartFiles[path.Join(dir, "Chart.lock")]
	digest := scalar(contract.MapValue(lock, "digest"))
	locked := map[string]*yaml.Node{}
	for _, d := range items(contract.MapValue(lock, "dependencies")) {
		locked[scalar(contract.MapValue(d, "name"))] = d
	}
	for _, d := range items(contract.MapValue(r.chartFiles[chartYAML], "dependencies")) {
		name := scalar(contract.MapValue(d, "name"))
		l := locked[name]
		if l == nil || digest == "" {
			r.find(CodeChartDependencyUnlocked, fmt.Sprintf("%s:%d", chartYAML, d.Line), unlockedDependencyMessage(name))
			continue
		}
		r.chartDeps = append(r.chartDeps, contract.InferredChartDependency{Chart: dir, Name: name, Version: scalar(contract.MapValue(l, "version")),
			Repository: cleanURL(scalar(contract.MapValue(l, "repository"))), Digest: digest})
	}
}

// chartValues reads the values files of the chart in dir: values.yaml for
// the image, ports and environment keys, and each values-<env>.yaml for its
// environment keys alone, outranked by values.yaml.
// ponytail: P7 records each values-<env>.yaml as an environment here.
func (r *reader) chartValues(dir string) {
	for _, p := range slices.Sorted(maps.Keys(r.chartFiles)) {
		m := valuesRE.FindStringSubmatch(path.Base(p))
		if m == nil || path.Dir(p) != dir {
			continue
		}
		values, rank := r.chartFiles[p], rankChart
		if m[1] == "" {
			r.valuesImage(p, values)
			r.valuesPorts(p, values)
		} else {
			rank++
		}
		r.valuesEnv(p, rank, values)
	}
}

// valuesImage is the image the values file p sets: image as a string, or
// image.repository with image.tag.
func (r *reader) valuesImage(p string, values *yaml.Node) {
	img := contract.MapValue(values, "image")
	at := img
	value := scalar(img)
	if repo := scalar(contract.MapValue(img, "repository")); repo != "" {
		at, value = contract.MapValue(img, "repository"), repo
		if tag := scalar(contract.MapValue(img, "tag")); tag != "" {
			value += ":" + tag
		}
	}
	if value != "" {
		r.addChart(p, at.Line, rankChart, "image", value, "")
	}
}

// valuesPorts are the container ports the values file p sets as numbers:
// service.port, service.targetPort, containerPort and ports[].containerPort.
func (r *reader) valuesPorts(p string, values *yaml.Node) {
	svc := contract.MapValue(values, "service")
	for _, n := range []*yaml.Node{contract.MapValue(svc, "port"), contract.MapValue(svc, "targetPort"), contract.MapValue(values, "containerPort")} {
		r.valuesPort(p, n, "")
	}
	for _, it := range items(contract.MapValue(values, "ports")) {
		r.valuesPort(p, contract.MapValue(it, "containerPort"), scalar(contract.MapValue(it, "protocol")))
	}
}

// valuesPort is the port n sets over proto (tcp when empty), when n is a
// number; a named port is not read.
func (r *reader) valuesPort(p string, n *yaml.Node, proto string) {
	if n == nil || n.Tag != "!!int" {
		return
	}
	if proto != "" {
		proto = "/" + proto
	}
	if port, proto, ok := exposedPort(n.Value + proto); ok {
		r.addChart(p, n.Line, rankChart, fmt.Sprintf("ports[%d]", port), fmt.Sprintf("%d/%s", port, proto), "")
	}
}

// valuesEnv are the environment keys env and extraEnv set in the values file
// p, as map keys or list names. A value is never read.
func (r *reader) valuesEnv(p string, rank int, values *yaml.Node) {
	for _, key := range []string{"env", "extraEnv"} {
		n := contract.MapValue(values, key)
		var names []*yaml.Node
		if n != nil && n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				names = append(names, n.Content[i])
			}
		}
		for _, it := range items(n) {
			names = append(names, contract.MapValue(it, "name"))
		}
		for _, name := range names {
			if k := scalar(name); k != "" {
				value, declare := configField(EnvVar{File: p, Name: k})
				r.addChart(p, name.Line, rank, "configuration."+k, value, declare)
			}
		}
	}
}

func (r *reader) addChart(p string, line, rank int, field, value, declare string) {
	r.add(entry{Inferred: contract.Inferred{Field: field, Value: value, Source: fmt.Sprintf("%s:%d", p, line), Confidence: contract.ConfidenceLikely, Declare: declare}, rank: rank, file: p})
}

// scalar is n's text when n is a scalar, else "".
func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

// items are the entries of the sequence n, none when n is not one.
func items(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}
