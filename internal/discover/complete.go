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
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/schemax"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// Version is the discovery version an inferred record names. Bump it when
// what Complete infers from the same folder changes.
const Version = "3"

// Complete is CompleteIn with the repository root found from dir.
func Complete(dir string, declared *contract.Contract, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.InferredRecord, error) {
	return CompleteIn(dir, "", declared, in, cat)
}

// CompleteIn is what the folder dir, in the repository root repoDir (""
// finds it, as readDir does), shows beyond what declared declares: one field
// per value, with its "<path>:<line>" source, its confidence, the YAML that
// would declare it and the sources it outranks; each system outside the
// cluster the folder names, classified with cat (nil is the default
// catalog), as an external dependency; what discovery could not read as
// findings; and in the inputs the catalogs and every file a field cites with
// its digest. A configuration field carries a key and a type, never a value.
// It is nil when the folder adds nothing and holds nothing unreadable. A nil
// declared declares nothing.
func CompleteIn(dir, repoDir string, declared *contract.Contract, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.InferredRecord, error) {
	return completeWith(dir, repoDir, declared, nil, in, cat)
}

// CompletePlanned is Complete against the contract declared that pacto init
// plans to write with the files planned, by slash path: what the folder
// shows beyond the pacto.yaml it is about to hold, so a value the new
// contract declares is never listed apart from it.
func CompletePlanned(dir string, declared *contract.Contract, planned map[string]string, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.InferredRecord, error) {
	return completeWith(dir, "", declared, planned, in, cat)
}

func completeWith(dir, repoDir string, declared *contract.Contract, planned map[string]string, in contract.InferredInputs, cat *externalcatalog.Catalog) (*contract.InferredRecord, error) {
	sc, err := Dir(dir)
	if err != nil {
		return nil, err
	}
	d := cmp.Or(declared, &contract.Contract{})
	name := cmp.Or(d.Service.Name, filepath.Base(dir))
	var values []Value // held in memory to classify, never kept
	reading, err := readDir(dir, repoDir, name, func(v Value) { values = append(values, v) })
	if err != nil {
		return nil, err
	}
	cat = cmp.Or(cat, externalcatalog.Default())
	c := &completion{sc: sc, d: d, api: OpenAPIRef(reading.Fields) != ""}
	c.cfgKeys, c.cfgListed = declaredConfigKeys(dir, c.d, planned)
	c.workload()
	c.health()
	c.configuration()
	c.dependencies(name)
	for _, e := range reading.entries {
		if !c.declared(e.Inferred) {
			c.entries = append(c.entries, e)
		}
	}
	fields, cited := merge(c.entries)
	xs, unidentified := sc.externals(values, cat)
	sdk, unread, read := sc.sdkUses(dir, cat)
	tf, tfUnread, tfRead := sc.terraformUses(dir, cat)
	for _, f := range append(read, tfRead...) {
		cited[f] = true
	}
	xs = append(append(append(xs, sdk...), tf...), sc.crossplaneUses(cat)...)
	unread = append(unread, tfUnread...)
	xfields, clients := externalFields(xs, func(field string) bool {
		return slices.ContainsFunc(fields, func(f contract.Inferred) bool { return f.Field == field }) ||
			declaredDependency(c.d, strings.TrimSuffix(strings.TrimPrefix(field, "dependencies["), "]"))
	}, c.declaredExternal, cited)
	fields = append(fields, xfields...)
	unidentified = append(append(unidentified, unread...), clients...)
	slices.SortFunc(fields, func(a, b contract.Inferred) int { return strings.Compare(a.Field, b.Field) })
	findings := slices.DeleteFunc(slices.Clone(reading.Findings), func(f contract.InferredFinding) bool {
		return f.Code == CodeAmbiguous && slices.ContainsFunc(reading.ambiguousSpecs[f.Source], c.declaredRef)
	})
	dropped := reading.FindingsDropped
	for _, f := range unidentified {
		if slices.Contains(findings, f) { // the folder reader already names it
			continue
		}
		if len(findings) >= MaxFindings {
			dropped++
			continue
		}
		findings = append(findings, f)
	}
	if len(fields) == 0 && len(findings) == 0 && len(reading.ChartDependencies) == 0 {
		return nil, nil
	}
	in.Discovery, in.ChartDependencies, in.Catalog = Version, reading.ChartDependencies, CatalogInputs(cat)
	if in.Sources, err = citeSources(dir, repoDir, cited); err != nil {
		return nil, err
	}
	return &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: in, Fields: fields,
		Findings: findings, FindingsDropped: dropped}, nil
}

// declaredExternal reports whether the contract declares the external id:
// an external dependency of that identity, or one of that name with no
// identity. A service dependency of the same name declares no external.
func (c *completion) declaredExternal(name, id string) bool {
	return slices.ContainsFunc(c.d.Dependencies, func(dep contract.Dependency) bool {
		return dep.Identity == id || dep.External && dep.Identity == "" && dep.Name == name
	})
}

// declaredRef reports whether a declared interface names the file p as its
// ref: the contract already says which of several specs the service serves.
func (c *completion) declaredRef(p string) bool {
	return slices.ContainsFunc(c.d.Interfaces, func(i contract.Interface) bool { return i.Ref == p })
}

// readSource reads a cited file. Read read it a moment ago, so it fails only
// when the file changes underneath; tests make it fail.
var readSource = os.ReadFile

// citeSources is every file in cited with its digest: a folder path under
// dir, or a "repository:<path>" under the repository root repoDir names (""
// finds it, as readDir does).
func citeSources(dir, repoDir string, cited map[string]bool) ([]contract.InferredSourceFile, error) {
	var out []contract.InferredSourceFile
	for _, f := range slices.Sorted(maps.Keys(cited)) {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if rel, ok := strings.CutPrefix(f, "repository:"); ok {
			abs, _ := filepath.Abs(dir) // ponytail: ReadDir just made dir absolute
			p = filepath.Join(repoFor(abs, repoDir), filepath.FromSlash(rel))
		}
		data, err := readSource(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		sum := sha256.Sum256(data)
		out = append(out, contract.InferredSourceFile{Path: f, Digest: "sha256:" + hex.EncodeToString(sum[:])})
	}
	return out, nil
}

// CompleteBundle overlays on b, read from the folder dir, what Complete
// infers with the catalog cat (nil is the default): b.FS serves it as
// pacto.inferred.yaml in place of any copy the folder holds (none when
// nothing is inferred, or when completion fails, so a stale copy never reads
// as computed). A contract before pactoVersion 2.5 is
// left as read.
func CompleteBundle(b *contract.Bundle, dir string, in contract.InferredInputs, cat *externalcatalog.Catalog) error {
	return CompleteBundleIn(b, dir, "", in, cat)
}

// CompleteBundleIn is CompleteBundle in the repository root repoDir (""
// finds it, as CompleteIn does).
func CompleteBundleIn(b *contract.Bundle, dir, repoDir string, in contract.InferredInputs, cat *externalcatalog.Catalog) error {
	if validation.InferredRecordNeedsRaise(b.Contract.PactoVersion) {
		return nil
	}
	rec, err := CompleteIn(dir, repoDir, b.Contract, in, cat)
	if err != nil || rec == nil {
		b.FS = contract.WithoutInferredRecord(b.FS)
		return err
	}
	b.FS = contract.WithInferredRecord(b.FS, rec.Marshal())
	return nil
}

// completion collects the fields Complete infers, before the merge.
type completion struct {
	sc      *Scan
	d       *contract.Contract
	api     bool // an OpenAPI file describes the api interface
	entries []entry
	// cfgKeys are the configuration keys declared; cfgListed is false when
	// they cannot be listed, so whether a key is declared is unknown.
	cfgKeys   map[string]bool
	cfgListed bool
}

func (c *completion) add(field, value, file, source, declare string) {
	c.addRanked(rankScan, field, value, file, source, declare)
}

func (c *completion) addRanked(rank int, field, value, file, source, declare string) {
	c.entries = append(c.entries, entry{Inferred: contract.Inferred{Field: field, Value: value, Source: source, Confidence: contract.ConfidenceLikely, Declare: declare}, rank: rank, file: file})
}

// declared reports whether the contract declares the field f a reader
// gives: a health capability, a summary, an owner, a domain, a dependency,
// an interface with its name or its ref, an interface on that port, an
// environment with that name, or a configuration key (any, when the declared
// keys cannot be listed).
func (c *completion) declared(f contract.Inferred) bool {
	field := f.Field
	if key, ok := strings.CutPrefix(field, "configuration."); ok {
		return !c.cfgListed || c.cfgKeys[key]
	}
	if name, ok := strings.CutPrefix(field, "interfaces["); ok {
		_, ref, _ := strings.Cut(f.Value, " ")
		return slices.ContainsFunc(c.d.Interfaces, func(i contract.Interface) bool { return i.Name+"]" == name || i.Ref == ref })
	}
	if env, ok := strings.CutPrefix(field, "environments["); ok {
		return slices.ContainsFunc(c.d.Environments, func(e contract.Environment) bool { return e.Name+"].file" == env })
	}
	if name, ok := strings.CutPrefix(field, "dependencies["); ok {
		return declaredDependency(c.d, strings.TrimSuffix(name, "]"))
	}
	switch field {
	case "service.owner":
		return !c.d.Service.Owner.IsEmpty()
	case "service.domain":
		return c.d.Service.Domain != ""
	case "capabilities[health]":
		return slices.ContainsFunc(c.d.Capabilities, func(k contract.Capability) bool { return k.Type == "health" })
	case "service.summary":
		return c.d.Service.Summary != ""
	}
	port, ok := strings.CutPrefix(field, "ports[")
	return ok && slices.ContainsFunc(c.d.Interfaces, func(i contract.Interface) bool { return i.Port != 0 && strconv.Itoa(i.Port)+"]" == port })
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
		value, declare := ownerYAML(w.Team, false)
		c.addRanked(rankTeamLabel, "service.owner", value, w.File, w.TeamSource, declare)
	}
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
	c.add("capabilities[health]", value, w.File, w.Probe.Source, HealthYAML(p, c.api))
}

// configuration is each environment key no declared configuration lists, as
// its key and type. When a declared configuration's keys cannot be listed,
// whether a key is declared is unknown, so no key is inferred.
func (c *completion) configuration() {
	if !c.cfgListed {
		return
	}
	for _, e := range c.sc.Env {
		if c.cfgKeys[e.Name] {
			continue
		}
		value, declare := configField(e)
		c.add("configuration."+e.Name, value, e.File, e.Source, declare)
	}
}

// configField is the value and the declaring schema property of the
// configuration key e: its type, never its value.
func configField(e EnvVar) (value, declare string) {
	prop := EnvProperty(e)
	value = prop["type"].(string)
	if prop["writeOnly"] == true {
		value += ", writeOnly"
	}
	data, _ := json.Marshal(prop) // ponytail: strings and booleans always marshal
	return value, fmt.Sprintf("%q: %s", e.Name, data)
}

// declaredConfigKeys are the keys every declared configuration lists: its
// values and its local schema's top-level properties. listed is false when
// one cannot be listed: a ref, a schema Pacto cannot read, or one that builds
// its keys through a composing keyword. A schema in planned is read from
// there, not from dir.
func declaredConfigKeys(dir string, d *contract.Contract, planned map[string]string) (keys map[string]bool, listed bool) {
	keys = map[string]bool{}
	for _, cfg := range d.Configurations {
		for k := range cfg.Values {
			keys[k] = true
		}
		if cfg.Schema == "" { // a ref: its keys live in another contract
			return nil, false
		}
		text, ok := planned[cfg.Schema]
		data := []byte(text)
		if !ok {
			var err error
			if data, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(cfg.Schema))); err != nil {
				return nil, false
			}
		}
		spec, err := schemax.Parse(data, cfg.Schema)
		if err != nil || slices.ContainsFunc(composingKeywords, func(k string) bool { return spec[k] != nil }) {
			return nil, false
		}
		for _, p := range spec.Properties() {
			keys[p.Key] = true
		}
	}
	return keys, true
}

// composingKeywords build a schema's keys somewhere its properties do not list.
var composingKeywords = []string{"allOf", "anyOf", "oneOf", "$ref", "patternProperties"}

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
