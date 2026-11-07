package pactify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// dropped are the golden path's own values a created service does not carry
// over, as dot paths.
var dropped = []string{"service.summary", "service.lifecycle", "service.offering", "service.domain", "metadata.repo", "metadata.summary", "readiness"}

// notCopied are the path files a created service does not get: its contract is
// created, its lock pins the path's references and a bundle never holds its
// .pactoignore.
var notCopied = []string{"pacto.yaml", "pacto.lock", ignore.IgnoreFileName}

// TemplateOptions refuses what creating from a golden path cannot do.
func TemplateOptions(opts Options) error {
	switch {
	case opts.Template == nil:
		return nil
	case opts.Owner == "":
		return errors.New("a service created from a golden path needs the team that owns it: pass --owner")
	case opts.Delivery:
		return errors.New("creating from a golden path writes the contract and the path's files only: drop --delivery and run pacto init --delivery once the service is published")
	case len(opts.FleetRoots) > 0:
		return errors.New("creating from a golden path reads no other services' contracts: drop --fleet")
	case len(opts.Inferred) > 0:
		return errors.New("creating from a golden path reads no running workload: drop --cluster")
	}
	return nil
}

// fromTemplate plans pacto.yaml and the path's files from the golden path:
// identity reset, publish rewritten, the caller's values applied, the template
// marker removed, the fixed inputs and origin set, then the whole folder
// validated before anything is planned.
func (p *planner) fromTemplate(ctx context.Context) error {
	t := p.opts.Template
	if _, err := os.Stat(p.r.Path); err == nil {
		return fmt.Errorf("%s already exists: a service is created from a golden path only in a folder with no contract", p.r.Path)
	}
	raw, err := fs.ReadFile(t.FS, "pacto.yaml")
	if err != nil {
		return fmt.Errorf("reading the golden path %s: %w", t.Ref, err)
	}
	path, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("reading the golden path %s: %w", t.Ref, err)
	}
	if path.Service.Template == nil {
		return fmt.Errorf("%s is not a golden path: its contract declares no service.template; to copy a service use pacto pull", t.Ref)
	}
	p.found("golden path %s %s from %s", path.Service.Name, path.Service.Version, t.Ref)
	var m map[string]any
	_ = contract.DecodeYAML(raw, &m) // ponytail: Parse just read the same bytes
	m["service"].(map[string]any)["version"] = "0.1.0"
	for _, k := range dropped {
		deletePath(m, k)
	}
	if md, ok := m["metadata"].(map[string]any); ok && len(md) == 0 {
		delete(m, "metadata")
	}
	registry := strings.Trim(strings.TrimPrefix(p.opts.Registry, "oci://"), "/")
	for _, f := range rewritePublish(m, path.Service.Name, p.r.Service, registry, p.opts.DescribeOnly) {
		p.found("%s", f)
	}
	m, err = p.applyValues(m)
	if err != nil {
		return err
	}
	text := marshalContract(m)
	p.file("pacto.yaml", text)
	if err := p.copyTemplate(); err != nil {
		return err
	}
	overlay, err := p.overlay(text)
	if err != nil {
		return err
	}
	c, err := p.validateTemplate(ctx, text, overlay)
	if err != nil {
		return err
	}
	steps, err := p.plugins(ctx, path.Service.Template.Plugins, c, overlay)
	if err != nil {
		return err
	}
	p.templateLines(path, c, m, registry)
	p.creationSteps(c, steps)
	return nil
}

// overlay is what the folder will hold: the created contract, the path's
// files it adds and the files it keeps as they are on disk.
func (p *planner) overlay(text string) (fstest.MapFS, error) {
	overlay := fstest.MapFS{"pacto.yaml": {Data: []byte(text)}}
	for _, f := range p.r.Files[1:] { // the contract is the first file planned
		data := []byte(f.Content)
		if f.Action == Keep {
			var err error
			if data, err = os.ReadFile(filepath.Join(p.r.Dir, f.Path)); err != nil {
				return nil, fmt.Errorf("reading %s, which the folder already holds: %w", f.Path, err)
			}
		}
		overlay[f.Path] = &fstest.MapFile{Data: data}
	}
	return overlay, nil
}

// applyValues applies the caller's values while service.template is still
// there, so a value under it reaches real keys, drops every null, then removes
// the marker and sets what the caller fixed: name, owner, domain and origin.
func (p *planner) applyValues(m map[string]any) (map[string]any, error) {
	t := p.opts.Template
	data, _ := yaml.Marshal(m) // ponytail: a map DecodeYAML filled always encodes
	data, err := override.Apply(data, t.Overrides)
	if err != nil {
		return nil, fmt.Errorf("applying the values to the golden path %s: %w", t.Ref, err)
	}
	m = nil
	_ = contract.DecodeYAML(data, &m) // ponytail: Apply's output is yaml.Marshal's
	pruneNulls(m)
	svc, ok := m["service"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the values given for the golden path %s leave no service object", t.Ref)
	}
	delete(svc, "template")
	svc["name"] = p.r.Service
	svc["owner"] = map[string]any{"team": p.opts.Owner}
	if p.opts.Domain != "" {
		svc["domain"] = p.opts.Domain
	}
	svc["origin"] = map[string]any{"ref": t.Ref, "digest": t.Digest}
	if sign, ok := pathOf(m, "publish.sign").(map[string]any); ok && p.opts.NoTransparencyLog {
		sign["transparencyLog"] = false
	}
	return m, nil
}

// copyTemplate plans every path file a created service gets, add-only.
func (p *planner) copyTemplate() error {
	return fs.WalkDir(p.opts.Template.FS, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || slices.Contains(notCopied, name) {
			return err
		}
		data, err := fs.ReadFile(p.opts.Template.FS, name)
		if err != nil {
			return fmt.Errorf("reading %s from the golden path %s: %w", name, p.opts.Template.Ref, err)
		}
		p.file(name, string(data))
		return nil
	})
}

// validateTemplate validates the created contract over exactly what the
// folder will hold: the contract, the path's files it adds and the files it
// keeps as they are on disk. Any error refuses the creation.
func (p *planner) validateTemplate(ctx context.Context, text string, fsys fs.FS) (*contract.Contract, error) {
	t := p.opts.Template
	c, err := contract.Parse(strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("the contract created from %s is not valid: %w", t.Ref, err)
	}
	validate := t.Validate
	if validate == nil {
		validate = localStages
	}
	p.stages = validate(ctx, c, []byte(text), fsys)
	r := validation.Merged(p.stages)
	if r.IsValid() {
		return c, nil
	}
	lines := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		lines[i] = e.Code + " " + e.Path + ": " + e.Message
	}
	return nil, fmt.Errorf("the contract created from %s is not valid:\n%s", t.Ref, strings.Join(lines, "\n"))
}

// localStages validates with no resolver: references stay unchecked.
func localStages(_ context.Context, c *contract.Contract, raw []byte, fsys fs.FS) []validation.Stage {
	return validation.ValidateDocumentStages(c, contract.DecodeDocument(raw), fsys)
}

// templateLines reports what was read from the path, what did not carry over
// and what a person still has to do.
func (p *planner) templateLines(path, c *contract.Contract, m map[string]any, registry string) {
	t := p.opts.Template
	p.droppedLines(path, m)
	if hint := OwnerHint(p.opts.Owner); hint != "" {
		p.next("%s", hint)
	}
	if l := path.Service.Lifecycle; l != nil {
		next := "the golden path is deprecated since " + l.Deprecated
		if l.Successor != "" {
			next += "; successor " + l.Successor
		}
		p.next("%s", next)
	}
	if _, err := fs.Stat(t.FS, "pacto.lock"); err == nil {
		p.next("the golden path's pacto.lock pins its own references and was not copied: run pacto lock to pin the references")
	}
	p.pub = c.Publish
	if c.Publish == nil {
		return
	}
	if registry == "" && c.Publish.Bundle == "oci://registry.example/"+p.r.Service {
		p.unknown("registry: no --registry was given, so registry.example is a placeholder; pass --registry")
	}
	if s := c.Publish.Sign; s != nil && s.Key != "" {
		if _, err := os.Stat(filepath.Join(p.r.Dir, s.Key)); err != nil {
			p.next("put the signing key at %s: a cosign PEM private key, decrypted with COSIGN_PASSWORD", s.Key)
		}
	}
	p.next("run pacto publish --plan to see what publishing %s builds", p.r.Service)
}

// droppedLines names each of the path's own values the created contract does
// not hold.
func (p *planner) droppedLines(path *contract.Contract, m map[string]any) {
	for _, k := range dropped {
		if pathOf(m, k) != nil {
			continue
		}
		if v := pathOfContract(path, k); v != "" && k == "service.domain" {
			p.unknown("%s: the golden path's %s was not carried over; pass --domain", k, v)
		} else if v != "" {
			p.unknown("%s: the golden path's %s was not carried over", k, v)
		}
	}
}

// pathOfContract is what the path declared at a dropped dot path, for the
// Unknown line that names it.
func pathOfContract(c *contract.Contract, k string) string {
	switch k {
	case "service.summary":
		if c.Service.Summary != "" {
			return "summary"
		}
	case "service.lifecycle":
		if c.Service.Lifecycle != nil {
			return "lifecycle (" + c.Service.Lifecycle.String() + ")"
		}
	case "service.offering":
		if c.Service.Offering != "" {
			return "offering " + c.Service.Offering
		}
	case "service.domain":
		if c.Service.Domain != "" {
			return "domain " + c.Service.Domain
		}
	case "metadata.repo", "metadata.summary":
		if c.Metadata[strings.TrimPrefix(k, "metadata.")] != nil {
			return k
		}
	case "readiness":
		if c.Readiness != nil {
			return "readiness evidence"
		}
	}
	return ""
}

// creationSteps lists how far the creation got, the plugin steps after
// validation. Only what Pacto established is done; the rest is planned,
// skipped or unknown with the reason.
func (p *planner) creationSteps(c *contract.Contract, plugins []fleet.CreationStep) {
	sig := p.opts.Template.Signature
	sig.Step = fleet.StepSignature
	if sig.Status == "" {
		sig.Status, sig.Detail = fleet.StepStatusUnknown, "signature not checked"
	}
	p.r.Steps = []fleet.CreationStep{sig,
		{Step: fleet.StepContract, Status: fleet.StepStatusPlanned, Detail: "planned at " + p.r.Path},
		validationStep(p.stages)}
	p.r.Steps = append(p.r.Steps, plugins...)
	p.r.Steps = append(p.r.Steps, []fleet.CreationStep{
		{Step: fleet.StepPublished, Status: fleet.StepStatusUnknown, Detail: "not observed at creation: publish the service with pacto publish"},
		{Step: fleet.StepFleet, Status: fleet.StepStatusUnknown, Detail: "not observed at creation: the fleet links the service to its golden path once a source reads it"},
		{Step: fleet.StepDeployed, Status: fleet.StepStatusUnknown, Detail: "not observed at creation: Pacto has seen no target that runs the service"},
		provisioningStep(c),
	}...)
}

// validationStep is done only when every reference was checked.
func validationStep(stages []validation.Stage) fleet.CreationStep {
	var codes []string
	for _, st := range stages {
		for _, w := range st.Warnings {
			codes = append(codes, w.Code)
		}
		for _, e := range st.Errors {
			codes = append(codes, e.Code)
		}
	}
	codes = slices.DeleteFunc(codes, func(code string) bool {
		return !strings.HasSuffix(code, "_UNVERIFIED") && !strings.HasSuffix(code, "_NOT_ENFORCED")
	})
	if len(codes) == 0 {
		return fleet.CreationStep{Step: fleet.StepValidation, Status: fleet.StepStatusDone, Detail: "strict, references resolved"}
	}
	slices.Sort(codes)
	return fleet.CreationStep{Step: fleet.StepValidation, Status: fleet.StepStatusUnknown,
		Detail: "valid, with references left unchecked: " + strings.Join(slices.Compact(codes), ", ")}
}

// provisioningStep names what the service consumes, which nothing at
// creation reports as provisioned.
func provisioningStep(c *contract.Contract) fleet.CreationStep {
	var consumed []string
	for _, cfg := range c.Configurations {
		if cfg.Ref != "" {
			consumed = append(consumed, "configuration "+cfg.Name+" ("+cfg.Ref+")")
		}
	}
	for _, d := range c.Dependencies {
		consumed = append(consumed, "dependency "+d.Name)
	}
	if len(consumed) == 0 {
		return fleet.CreationStep{Step: fleet.StepProvisioning, Status: fleet.StepStatusNotApplicable, Detail: "the service consumes no configuration ref or dependency"}
	}
	return fleet.CreationStep{Step: fleet.StepProvisioning, Status: fleet.StepStatusUnknown,
		Detail: "no report says what the service consumes is provisioned: " + strings.Join(consumed, ", ")}
}

// rewritePublish gives the created service its own publish coordinates and
// returns a line for each artifact it renamed. With describeOnly the publish
// section goes.
func rewritePublish(m map[string]any, oldName, newName, registry string, describeOnly bool) []string {
	pub, ok := m["publish"].(map[string]any)
	if !ok {
		return nil
	}
	if describeOnly {
		delete(m, "publish")
		return nil
	}
	if registry == "" {
		registry = "registry.example"
	}
	pub["bundle"] = "oci://" + registry + "/" + newName
	var found []string
	arts, _ := pub["artifacts"].([]any)
	for _, a := range arts {
		art, _ := a.(map[string]any)
		ref, _ := art["ref"].(string)
		i := strings.LastIndex(ref, "/")
		if i < 0 || ref[i+1:] != oldName || oldName == newName {
			continue
		}
		art["ref"] = ref[:i+1] + newName
		found = append(found, fmt.Sprintf("publish.artifacts %v: %s becomes %s", art["name"], ref, art["ref"]))
	}
	return found
}

// pruneNulls removes every key whose value is null, at any depth: a null
// value removes a field.
func pruneNulls(v any) {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if x == nil {
				delete(v, k)
				continue
			}
			pruneNulls(x)
		}
	case []any:
		for _, x := range v {
			pruneNulls(x)
		}
	}
}

// pathOf is the value at a dot path of objects, nil when it is not there.
func pathOf(m map[string]any, dotted string) any {
	var v any = m
	for _, k := range strings.Split(dotted, ".") {
		obj, _ := v.(map[string]any)
		v = obj[k]
	}
	return v
}

// deletePath removes the value at a dot path of objects.
func deletePath(m map[string]any, dotted string) {
	parent, key := m, dotted
	if i := strings.LastIndex(dotted, "."); i >= 0 {
		parent, _ = pathOf(m, dotted[:i]).(map[string]any)
		key = dotted[i+1:]
	}
	delete(parent, key)
}

// marshalContract renders m with pactoVersion and service first, and name,
// version and owner first in service, so the contract reads like one written
// by hand. Every other key follows in sorted order.
func marshalContract(m map[string]any) string {
	var b strings.Builder
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	_ = e.Encode(ordered(m, "pactoVersion", "service")) // ponytail: a node of encoded values always encodes
	_ = e.Close()
	return b.String()
}

// ordered is m as a YAML mapping with the keys in first leading.
func ordered(m map[string]any, first ...string) *yaml.Node {
	rank := func(k string) int {
		if i := slices.Index(first, k); i >= 0 {
			return i
		}
		return len(first)
	}
	keys := slices.Sorted(maps.Keys(m))
	slices.SortStableFunc(keys, func(a, b string) int { return rank(a) - rank(b) })
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		var key, val yaml.Node
		_ = key.Encode(k) // ponytail: strings and decoded YAML values always encode
		if svc, ok := m[k].(map[string]any); ok && k == "service" {
			val = *ordered(svc, "name", "version", "owner", "domain")
		} else {
			_ = val.Encode(m[k])
		}
		n.Content = append(n.Content, &key, &val)
	}
	return n
}
