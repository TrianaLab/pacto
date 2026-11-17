package discover

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/schemax"
)

// Version is the discovery version an inferred record names. Bump it when
// what Complete infers from the same folder changes.
const Version = "1"

// Complete is what the folder dir shows beyond what declared declares: one
// field per value, with its "<path>:<line>" source, confidence likely and the
// YAML that would declare it, and in the inputs every file a field cites with
// its digest. A configuration field carries a key and a type, never a value.
// It is nil when the folder adds nothing. A nil declared declares nothing.
func Complete(dir string, declared *contract.Contract, in contract.InferredInputs) (*contract.InferredRecord, error) {
	sc, err := Dir(dir)
	if err != nil {
		return nil, err
	}
	c := &completion{sc: sc, d: cmp.Or(declared, &contract.Contract{}), cited: map[string]bool{}}
	c.workload()
	c.api()
	c.health()
	c.configuration(dir)
	c.dependencies(cmp.Or(c.d.Service.Name, filepath.Base(dir)))
	if len(c.fields) == 0 {
		return nil, nil
	}
	slices.SortFunc(c.fields, func(a, b contract.Inferred) int { return strings.Compare(a.Field, b.Field) })
	in.Discovery, in.Sources = Version, nil
	for _, f := range slices.Sorted(maps.Keys(c.cited)) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		sum := sha256.Sum256(data)
		in.Sources = append(in.Sources, contract.InferredSourceFile{Path: f, Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	return &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: in, Fields: c.fields}, nil
}

// completion collects the fields Complete infers and the files they cite.
type completion struct {
	sc     *Scan
	d      *contract.Contract
	fields []contract.Inferred
	cited  map[string]bool
}

func (c *completion) add(field, value, file, source, declare string) {
	c.cited[file] = true
	c.fields = append(c.fields, contract.Inferred{Field: field, Value: value, Source: source, Confidence: contract.ConfidenceLikely, Declare: declare})
}

// workload is the workload kind and its team label, when not declared.
func (c *completion) workload() {
	w := c.sc.Workload
	if w == nil {
		return
	}
	if kind := WorkloadKinds[w.Kind]; c.d.Workload == "" {
		c.add("workload", fmt.Sprintf("%s (%s %s)", kind, w.Kind, w.Name), w.File, w.KindSource, "workload: "+kind+"\n")
	}
	if w.Team != "" && c.d.Service.Owner.IsEmpty() {
		c.add("service.owner", w.Team, w.File, w.TeamSource, "service:\n  owner:\n    team: "+Quote(w.Team)+"\n")
	}
}

// api is the OpenAPI file, unless a declared interface already refers to it.
func (c *completion) api() {
	f := c.sc.OpenAPI
	if f == "" || slices.ContainsFunc(c.d.Interfaces, func(i contract.Interface) bool { return i.Ref == f }) {
		return
	}
	c.add("interfaces[api]", "openapi "+f, f, f, InterfaceYAML(f))
}

// health is the workload's probe, unless a health capability is declared.
func (c *completion) health() {
	w := c.sc.Workload
	if w == nil || w.Probe == nil || slices.ContainsFunc(c.d.Capabilities, func(k contract.Capability) bool { return k.Type == "health" }) {
		return
	}
	value, p := w.Probe.Kind+"Probe", w.Probe.Path
	if !contract.ValidCapabilityPath(p) {
		p = ""
	}
	if p != "" {
		value = "GET " + p + " (" + value + ")"
	}
	c.add("capabilities[health]", value, w.File, w.Probe.Source, HealthYAML(p, c.sc.OpenAPI != ""))
}

// configuration is each environment key no declared local schema holds, as
// its key and type.
func (c *completion) configuration(dir string) {
	declared := declaredConfigKeys(dir, c.d)
	for _, e := range c.sc.Env {
		if declared[e.Name] {
			continue
		}
		prop := EnvProperty(e)
		value := prop["type"].(string)
		if prop["writeOnly"] == true {
			value += ", writeOnly"
		}
		data, _ := json.Marshal(prop) // ponytail: strings and booleans always marshal
		c.add("configuration."+e.Name, value, e.File, e.Source, fmt.Sprintf("%q: %s", e.Name, data))
	}
}

// declaredConfigKeys are the properties of every declared configuration's
// local schema; one that cannot be read declares none.
func declaredConfigKeys(dir string, d *contract.Contract) map[string]bool {
	out := map[string]bool{}
	for _, cfg := range d.Configurations {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(cfg.Schema)))
		if cfg.Schema == "" || err != nil {
			continue
		}
		for _, p := range schemax.Properties(data, cfg.Schema) {
			out[p.Key] = true
		}
	}
	return out
}

// dependencies are the services the environment points at, each named by
// its host alone, never by the value that holds it.
func (c *completion) dependencies(self string) {
	found, from := c.sc.dependencies(self)
	for i, f := range found {
		if declaredDependency(c.d, f.Name) {
			continue
		}
		declare := "dependencies:\n" + DependencyYAML(f.Name, "<where the contract of "+f.Name+" is published>", "declare the versions of "+f.Name+" this service accepts")
		c.add("dependencies["+f.Name+"]", f.Name, from[i].File, from[i].Source, declare)
	}
}

// declaredDependency reports whether d declares a dependency named name, or
// one whose ref's last path segment, less its tag or digest, is name.
func declaredDependency(d *contract.Contract, name string) bool {
	return slices.ContainsFunc(d.Dependencies, func(dep contract.Dependency) bool {
		base, _, _ := strings.Cut(path.Base(dep.Ref), "@")
		base, _, _ = strings.Cut(base, ":")
		return dep.Name == name || base == name
	})
}
