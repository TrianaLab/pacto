package pactify

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/Masterminds/semver/v3"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// blankBody is the blank contract below service and above publish.
const blankBody = `
interfaces:
  - name: api
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: internal

configurations:
  - name: default
    schema: configuration/schema.json
    required: true

# dependencies:
#   - name: other-service
#     ref: oci://ghcr.io/my-org/other-service-pacto:1.0.0
#     required: true
#     compatibility: "^1.0.0"

workload: service

state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low

capabilities:
  - type: health
  - type: metrics
`

const blankOpenAPI = `openapi: "3.0.0"
info:
  title: %s
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
`

const blankSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
`

// signKey is where a new contract's signing key lives.
const signKey = "cosign.key"

// contract plans pacto.yaml: created from the golden path, else kept when it
// is there, else blank or read from the folder.
func (p *planner) contract(ctx context.Context) error {
	if p.opts.Template != nil {
		return p.fromTemplate(ctx)
	}
	if p.r.Mode == modePactify {
		p.r.Dependencies = p.sc.Dependencies(p.r.Service)
	}
	for _, d := range p.opts.Inferred {
		if !slices.ContainsFunc(p.r.Dependencies, func(f Finding) bool { return f.Name == d.Name }) {
			p.r.Dependencies = append(p.r.Dependencies, Finding{Name: d.Name, Where: d.Where})
		}
	}
	if data, err := os.ReadFile(p.r.Path); err == nil {
		p.r.Files = append(p.r.Files, File{Path: "pacto.yaml", Action: Keep})
		if c := p.existing(data); c != nil {
			return p.complete(c)
		}
		return nil
	}
	// Everything the folder shows, read once: the contract init writes draws
	// on it.
	in := contract.InferredInputs{Commit: p.opts.Commit, Pacto: p.opts.PactoVersion}
	cat, _ := p.catalog()
	all, err := discover.CompleteIn(p.r.Dir, "", &contract.Contract{Service: contract.Service{Name: p.r.Service}}, in, cat)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p.r.Dir, err)
	}
	fields := cmp.Or(all, &contract.InferredRecord{}).Fields
	p.openapi = discover.OpenAPIRef(fields)
	if p.r.Mode == modeBlank {
		err = p.blankContract()
	} else {
		err = p.pactifiedContract(fields)
	}
	if err != nil {
		return err
	}
	// A draft read from the cluster is never completed: what runs there never
	// reaches the inferred record. The folder's files are still named.
	if p.opts.cluster {
		p.discoveryLines(all)
		p.externals(all)
		return nil
	}
	return p.completePlanned(in)
}

// completePlanned puts in r.Inferred what the folder shows beyond the
// pacto.yaml init plans to write, and names it in Found and Unknown. Only a
// pactoVersion 2.5 contract publishes it, so an older one says so in Next.
func (p *planner) completePlanned(in contract.InferredInputs) error {
	planned := map[string]string{}
	for _, f := range p.r.Files {
		if f.Action == Add {
			planned[f.Path] = f.Content
		}
	}
	c, err := contract.Parse(strings.NewReader(planned["pacto.yaml"]))
	if err != nil { // ponytail: validate parsed this text a moment ago
		return fmt.Errorf("the contract pacto init would write is not valid: %w", err)
	}
	c.Service.Name = p.r.Service
	cat, _ := p.catalog()
	rec, err := discover.CompletePlanned(p.r.Dir, c, planned, in, cat)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p.r.Dir, err)
	}
	p.r.Inferred = rec
	p.discoveryLines(rec)
	p.externals(rec)
	if rec != nil && len(rec.Fields) > 0 && validation.InferredRecordNeedsRaise(c.PactoVersion) {
		p.next("pacto.yaml declares pactoVersion %s, so pacto pack publishes none of what Pacto inferred: set pactoVersion: \"2.5\" to publish it in %s", c.PactoVersion, contract.InferredRecordFile)
	}
	return nil
}

// blankContract plans the blank contract, its interface the OpenAPI file
// discovery found or, with none, a placeholder, and its files.
func (p *planner) blankContract() error {
	text := p.blank()
	files := p.environmentFiles()
	files[configSchema] = blankSchema
	if p.openapi != "" {
		text = strings.Replace(text, "ref: interfaces/openapi.yaml\n", "ref: "+discover.Quote(p.openapi)+"\n", 1)
		data, _ := contract.ReadBounded(os.DirFS(p.r.Dir), p.openapi) // ponytail: discovery read it a moment ago; unreadable now fails validation
		files[p.openapi] = string(data)
	} else {
		files["interfaces/openapi.yaml"] = fmt.Sprintf(blankOpenAPI, p.r.Service)
	}
	if err := validate(text, files); err != nil {
		return fmt.Errorf("the contract pacto init would write is not valid: %w", err)
	}
	p.file("pacto.yaml", text)
	if p.openapi == "" {
		p.file("interfaces/openapi.yaml", files["interfaces/openapi.yaml"])
	}
	p.file(configSchema, blankSchema)
	p.planEnvironmentFiles(files)
	return nil
}

// pactifiedContract plans the contract read from the folder, and its files.
func (p *planner) pactifiedContract(fields []contract.Inferred) error {
	p.contractNew = true
	p.ports = discoveredPorts(fields)
	text := p.pactified()
	files := p.environmentFiles()
	if p.openapi != "" {
		files[p.openapi] = "{}"
	}
	if p.schema != "" {
		files[configSchema] = p.schema
	}
	if err := validate(text, files); err != nil {
		return fmt.Errorf("the contract pacto init would write is not valid: %w", err)
	}
	p.file("pacto.yaml", text)
	if p.schema != "" {
		p.file(configSchema, p.schema)
	}
	p.planEnvironmentFiles(files)
	return nil
}

// discoveryLines names in Found what Pacto read statically from each Helm
// chart, and in Unknown every finding of rec: what discovery could not read
// or choose.
func (p *planner) discoveryLines(rec *contract.InferredRecord) {
	rec = cmp.Or(rec, &contract.InferredRecord{})
	for _, chart := range p.sc.Charts {
		p.found("Helm chart %s read statically%s", chart, chartSummary(chart, rec.Fields))
	}
	for _, f := range rec.Findings {
		p.unknown("%s", f.Message)
	}
	if rec.FindingsDropped > 0 {
		p.unknown("and %d more discovery findings", rec.FindingsDropped)
	}
}

// chartFile reports whether the source names a file of the chart folder:
// its Chart.yaml, a values file or a template.
func chartFile(chart, source string) bool {
	rel := strings.TrimPrefix(source, chart+"/")
	if chart == "." {
		rel = source
	}
	file, _, _ := strings.Cut(rel, ":")
	return file == "Chart.yaml" || strings.HasPrefix(file, "templates/") || (strings.HasPrefix(file, "values") && !strings.Contains(file, "/"))
}

// chartSummary is what fields read from the chart folder: its image, its
// ports and how many configuration keys, "" when none.
func chartSummary(chart string, fields []contract.Inferred) string {
	in := func(f contract.Inferred) bool {
		return slices.ContainsFunc(append([]string{f.Source}, f.Outranks...), func(s string) bool { return chartFile(chart, s) })
	}
	var parts []string
	keys := 0
	for _, f := range fields {
		switch {
		case !in(f):
		case f.Field == "image":
			parts = append(parts, "image "+f.Value)
		case strings.HasPrefix(f.Field, "ports["):
			parts = append(parts, "port "+strings.TrimSuffix(f.Value, "/tcp"))
		case strings.HasPrefix(f.Field, "configuration."):
			keys++
		}
	}
	switch {
	case keys == 1:
		parts = append(parts, "1 env key")
	case keys > 1:
		parts = append(parts, fmt.Sprintf("%d env keys", keys))
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, ", ")
}

// environmentFile is the values file init writes for env: no values yet.
func environmentFile(env string) (path, content string) {
	return "environments/" + env + ".yaml", "# Values " + env + " uses for each configuration, by configuration name.\n{}\n"
}

// environmentFiles is the values file of each environment, by path, when
// --delivery declares them in the contract; empty otherwise.
func (p *planner) environmentFiles() map[string]string {
	files := map[string]string{}
	if p.opts.Delivery {
		for _, env := range p.envs {
			path, content := environmentFile(env)
			files[path] = content
		}
	}
	return files
}

// planEnvironmentFiles plans the values file of each environment
// environmentFiles gave, in promotion order.
func (p *planner) planEnvironmentFiles(files map[string]string) {
	for _, env := range p.envs {
		if path, content := environmentFile(env); files[path] != "" {
			p.file(path, content)
		}
	}
}

// environments declares each --env in the contract, with --delivery.
func (p *planner) environments(b *strings.Builder) {
	if !p.opts.Delivery {
		return
	}
	b.WriteString("environments:\n")
	for _, env := range p.envs {
		path, _ := environmentFile(env)
		fmt.Fprintf(b, "  - name: %s\n    file: %s\n", env, path)
	}
}

// blank writes the contract of a folder with no service in it: the owner,
// domain and registry the caller gave, and placeholders named in Unknown.
func (p *planner) blank() string {
	name := p.r.Service
	var b strings.Builder
	fmt.Fprintf(&b, "pactoVersion: %q\nservice:\n  name: %s\n  version: 0.1.0\n", p.pactoVersion(), discover.Quote(name))
	p.owner(&b, p.opts.Owner, "no team was given")
	p.domain(&b)
	b.WriteString(blankBody)
	if p.opts.Delivery {
		b.WriteString("\n")
		p.environments(&b)
	}
	if p.opts.DescribeOnly {
		return b.String()
	}
	registry := strings.Trim(strings.TrimPrefix(p.opts.Registry, "oci://"), "/")
	if registry == "" {
		registry = "registry.example"
		p.unknown("registry: no --registry was given, so registry.example is a placeholder; pass --registry")
	}
	b.WriteString("\n")
	p.publish(&b, registry, registry, "")
	p.publishOnlyNext()
	return b.String()
}

// pactoVersion is the lowest version that holds what init writes: 2.5 for
// the compatibility unknown every dependency pactify writes has, else 2.4 for
// a domain, else 2.3 for the environments --delivery declares, else 2.1 for
// publish.
func (p *planner) pactoVersion() string {
	switch {
	case p.r.Mode == modePactify && len(p.r.Dependencies) > 0:
		return "2.5"
	case p.opts.Domain != "":
		return "2.4"
	case p.opts.Delivery:
		return "2.3"
	}
	return "2.1"
}

// owner writes service.owner, or a commented one and an Unknown line saying
// why there is none.
func (p *planner) owner(b *strings.Builder, team, why string) {
	if team == "" {
		fmt.Fprintf(b, "  # owner:\n  #   team: <the team that owns %s>\n", p.r.Service)
		p.unknown("owner: %s; pass --owner", why)
		return
	}
	fmt.Fprintf(b, "  owner:\n    team: %s\n", discover.Quote(team))
	if hint := OwnerHint(team); hint != "" {
		p.next("%s", hint)
	}
}

// domain writes service.domain when the caller gave one.
func (p *planner) domain(b *strings.Builder) {
	if p.opts.Domain != "" {
		fmt.Fprintf(b, "  domain: %s\n", discover.Quote(p.opts.Domain))
	}
}

// publishOnlyNext points a contract that only describes the service at
// --describe-only.
func (p *planner) publishOnlyNext() {
	p.next("the publish section is for pacto publish only: for a contract that only describes %s, run pacto init --describe-only instead (describe_only: true with pacto_create)", p.r.Service)
}

// transparencyLog says, when transparencyLog: false was asked for, that a
// contract init did not write (the user's own) does not carry it.
func (p *planner) transparencyLog() {
	if !p.opts.NoTransparencyLog {
		return
	}
	if p.pub != nil && p.pub.Sign != nil && p.pub.Sign.TransparencyLog != nil && !*p.pub.Sign.TransparencyLog {
		return
	}
	p.next("pacto.yaml was not written by this run, so it does not keep signatures out of the transparency log: add transparencyLog: false under publish.sign")
}

// existing reads what the cluster pieces need from the user's own contract
// and returns it, or nil when it cannot be read.
func (p *planner) existing(data []byte) *contract.Contract {
	c, err := contract.Parse(bytes.NewReader(data))
	if err != nil {
		p.unknown("pacto.yaml could not be read (%v), so nothing was taken from it", err)
		return nil
	}
	// A contract that declares no name keeps the one --name or the folder gave.
	p.r.Service, p.pub = cmp.Or(c.Service.Name, p.r.Service), c.Publish
	if p.opts.Delivery {
		missing := slices.DeleteFunc(slices.Clone(p.envs), func(env string) bool {
			return slices.ContainsFunc(c.Environments, func(e contract.Environment) bool { return e.Name == env })
		})
		if len(missing) > 0 {
			p.next("pacto.yaml declares no environment %s: add them with pacto_edit add_environments so the pacto.environment gate checks their values", strings.Join(missing, ", "))
		}
	}
	if c.Publish == nil && !p.opts.DescribeOnly {
		bump := ""
		if validation.PublishNeedsRaise(c.PactoVersion) {
			bump = "set pactoVersion: \"2.1\" and "
		}
		p.next("pacto.yaml has no publish section, so pacto publish refuses it: %sadd publish "+
			"(see Pactify a service in the docs), or move pacto.yaml aside and run pacto init again to have one written", bump)
	}
	return c
}

// complete puts what the folder shows beyond the user's contract c in
// r.Inferred, never changing c. Only a pactoVersion 2.5 contract is
// completed, and never from the cluster: discover.Complete reads the folder
// alone.
func (p *planner) complete(c *contract.Contract) error {
	in := contract.InferredInputs{Commit: p.opts.Commit, Pacto: p.opts.PactoVersion}
	// Complete leaves out the service itself under the name this run reports,
	// so the record and Result.Dependencies agree when pacto.yaml names none.
	named := *c
	named.Service.Name = p.r.Service
	cat, _ := p.catalog()
	rec, err := discover.Complete(p.r.Dir, &named, in, cat)
	if err != nil {
		return fmt.Errorf("completing pacto.yaml: %w", err)
	}
	// What the folder holds is named whether or not it completes pacto.yaml,
	// so a chart or a file Pacto could not read is never silent.
	p.discoveryLines(rec)
	p.externals(rec)
	switch {
	case validation.InferredRecordNeedsRaise(c.PactoVersion):
		p.next("pacto.yaml declares pactoVersion %s; set pactoVersion: \"2.5\" to have Pacto complete it", c.PactoVersion)
		return nil
	case len(p.opts.Inferred) > 0 || p.opts.cluster:
		p.next("what runs in the cluster never completes pacto.yaml: run pacto init %s without --cluster to complete it", p.r.Dir)
		return nil
	}
	p.r.Mode, p.r.Inferred = modeComplete, rec
	if rec == nil || len(rec.Fields) == 0 { // a record of findings alone completes no value
		p.found("pacto.yaml declares every value Pacto reads in this folder's manifests and specs, so Pacto infers nothing")
		return nil
	}
	shown := len(rec.Fields)
	// ponytail: the same files were read a moment ago; a second read that fails counts none as declared.
	if all, _ := discover.Complete(p.r.Dir, &contract.Contract{Service: contract.Service{Name: p.r.Service}}, in, cat); all != nil {
		shown = len(all.Fields)
	}
	p.found("pacto.yaml declares %d of the values Pacto reads in this folder's manifests and specs; Pacto completed %d in %s, published with the bundle", shown-len(rec.Fields), len(rec.Fields), contract.InferredRecordFile)
	return nil
}

// validate validates the contract text in a bundle that also holds files.
func validate(text string, files map[string]string) error {
	c, err := contract.Parse(strings.NewReader(text))
	if err == nil {
		fsys := fstest.MapFS{"pacto.yaml": {Data: []byte(text)}}
		for name, data := range files {
			fsys[name] = &fstest.MapFile{Data: []byte(data)}
		}
		if r := validation.Validate(c, []byte(text), fsys); !r.IsValid() {
			err = errors.New(r.Errors[0].Message)
		}
	}
	return err
}

func (p *planner) found(format string, a ...any) {
	p.r.Found = append(p.r.Found, fmt.Sprintf(format, a...))
}

func (p *planner) unknown(format string, a ...any) {
	p.r.Unknown = append(p.r.Unknown, fmt.Sprintf(format, a...))
}

func (p *planner) next(format string, a ...any) {
	p.r.Next = append(p.r.Next, fmt.Sprintf(format, a...))
}

// versionAndRegistry reads the version from the image tag and the registry
// from the image (or --registry), naming placeholders in Unknown. imgRepo is
// the image repository, "" when the image names none Pacto can push to.
func (p *planner) versionAndRegistry(w *discover.Workload, name string) (version, registry, imgRepo string) {
	tag := ""
	if w.Image != "" {
		imgRepo, tag = discover.ImageRepo(w.Image)
		if !refRE.MatchString("oci://" + imgRepo) {
			imgRepo = ""
		}
	}

	version = "0.1.0"
	if v, err := semver.StrictNewVersion(strings.TrimPrefix(tag, "v")); err == nil {
		version = v.String()
		p.found("version %s, from the image tag in %s", version, w.File)
	} else {
		p.unknown("version: no image tag is a version (x.y.z), so 0.1.0 is a placeholder")
	}

	registry = strings.Trim(strings.TrimPrefix(p.opts.Registry, "oci://"), "/")
	switch {
	case registry != "":
	case imgRepo != "" && !hubLibrary(imgRepo):
		registry = strings.TrimSuffix(path.Dir(imgRepo), "/images")
		p.found("registry %s, from the image %s", registry, w.Image)
	case imgRepo != "":
		registry = "registry.example"
		p.unknown("registry: the image %s is a Docker Hub official image, which only Docker publishes, so registry.example is a placeholder; pass --registry", w.Image)
	default:
		registry = "registry.example"
		p.unknown("registry: no image says where %s is published, so registry.example is a placeholder; pass --registry", name)
	}
	return version, registry, imgRepo
}

// hubLibrary reports whether repo is a Docker Hub official image
// (docker.io/library/...), which nobody but Docker can push to.
func hubLibrary(repo string) bool { return strings.HasPrefix(repo, "docker.io/library/") }

// pactified writes the contract the folder describes. Every value it could
// not read is left out or marked, and named in Unknown.
func (p *planner) pactified() string {
	sc, name, w := p.sc, p.r.Service, p.sc.Workload
	if w == nil {
		w = &discover.Workload{}
	} else {
		p.found("%s %s in %s", w.Kind, w.Name, w.File)
	}
	version, registry, imgRepo := p.versionAndRegistry(w, name)

	var b strings.Builder
	files := strings.Join(append(nonEmpty(sc.Dockerfile), sc.Manifests...), ", ")
	next := "see what publishing would do: pacto publish --plan"
	if p.opts.DescribeOnly {
		next = "validate it: pacto validate"
	}
	fmt.Fprintf(&b, "# Written by pacto init from %s.\n# Check it, then %s\n", files, next)
	fmt.Fprintf(&b, "pactoVersion: %q\nservice:\n  name: %s\n  version: %s\n", p.pactoVersion(), discover.Quote(name), discover.Quote(version))
	team := p.opts.Owner
	if team == "" && w.Team != "" {
		team = w.Team
		p.found("owner team %s, from the team label in %s", team, w.File)
	}
	p.owner(&b, team, "no team label says who owns "+name)
	p.domain(&b)
	if kind, ok := discover.WorkloadKinds[w.Kind]; ok {
		fmt.Fprintf(&b, "workload: %s\n", kind)
	} else {
		p.unknown("workload: no Kubernetes workload says whether %s is a service, a job or a scheduled job", name)
	}
	switch {
	case p.openapi != "":
		b.WriteString(discover.InterfaceYAML("api", "openapi", p.openapi))
		p.found("API description %s", p.openapi)
	case len(p.ports) > 0:
		p.unknown("interfaces: %s listens on port %s, but no OpenAPI file describes it", name, joinInts(p.ports))
	default:
		p.unknown("interfaces: nothing describes what %s serves", name)
	}
	// Bundles live beside the images, at registry/<name>, unless the image is
	// there already (the usual org/name layout): then they go under
	// registry/contracts, so a bundle tag never lands on the image.
	bundles := registry
	if imgRepo == registry+"/"+name {
		bundles = registry + "/contracts"
		p.found("contract bundles under %s, as the image %s already holds %s/%s", bundles, w.Image, registry, name)
	}
	p.health(&b, p.openapi != "")
	p.dependencyEntries(&b, bundles)
	p.configuration(&b)
	p.environments(&b)
	if p.opts.DescribeOnly {
		return b.String()
	}
	p.publish(&b, bundles, registry, imgRepo)
	p.publishOnlyNext()
	for _, f := range sc.Secrets {
		p.next("%s holds a Secret's values, so it is left out of the published manifests, which anyone who can pull them reads; "+
			"supply it another way (a SealedSecret, an ExternalSecret or SOPS) and move any other objects in it to their own file", f)
	}
	return b.String()
}

// health writes a health capability when the workload has a probe, bound
// to the api interface when the probe calls an HTTP path and an OpenAPI file
// describes what the service serves.
func (p *planner) health(b *strings.Builder, api bool) {
	w := p.sc.Workload
	if w == nil || w.Probe == nil {
		return
	}
	path := w.Probe.Path
	if !contract.ValidCapabilityPath(path) {
		path = ""
	}
	b.WriteString(discover.HealthYAML(path, api))
	switch {
	case path != "" && api:
		p.found("health check GET %s on api, from the %s probe in %s", path, w.Probe.Kind, w.File)
	case path != "":
		p.found("health check GET %s, from the %s probe in %s", path, w.Probe.Kind, w.File)
		p.unknown("the interface that serves the health check GET %s: no OpenAPI file describes what %s serves, so the check is bound to none", path, p.r.Service)
	default:
		p.found("health check, from the %s probe in %s", w.Probe.Kind, w.File)
	}
}

// dependencyEntries declares the services found, each with compatibility
// unknown and, when a version was read, a proposed range in a comment. One
// whose contract is in --fleet gets its bundle; any other gets the
// repository its contract would be published at.
func (p *planner) dependencyEntries(b *strings.Builder, bundles string) {
	written := p.observedEntries(b, bundles)
	header := len(written) == 0
	for _, d := range p.r.Dependencies {
		if written[d.Name] {
			continue
		}
		if header {
			b.WriteString("dependencies:\n")
			header = false
		}
		ref, version := p.provider(d.Name, bundles)
		if version == "" {
			fmt.Fprintf(b, "  # from %s\n  # no contract for %s was read (pass its folder with --fleet): ref is where it would be published\n", d.Where, d.Name)
			writeDependency(b, d.Name, ref, "no range proposed: no contract or running version of "+d.Name+" was read")
			p.undeclared(d.Name, "", "")
			continue
		}
		proposal := proposedRange([]string{version})
		fmt.Fprintf(b, "  # from %s\n", d.Where)
		writeDependency(b, d.Name, ref, fmt.Sprintf("proposed %s, from its contract in --fleet at %s", proposal, version))
		p.found("%s calls %s (%s), whose contract in --fleet is at %s", p.r.Service, d.Name, d.Where, version)
		p.undeclared(d.Name, proposal, "its contract in --fleet")
	}
}

// observedEntries writes the dependencies seen at runtime that the Pactos
// describing their provider name, each with compatibility unknown: with a
// range proposed from the lowest version those Pactos report in a comment,
// or the reason none is. A person declares the range: a guess never settles
// who a change breaks. It prints what kept a range from being proposed for
// every entry.
func (p *planner) observedEntries(b *strings.Builder, bundles string) map[string]bool {
	out := map[string]bool{}
	for _, d := range p.opts.Inferred {
		for _, u := range d.Unknown {
			p.unknown("%s", u)
		}
		if out[d.Name] || (len(d.Versions) == 0 && d.Ref == "" && len(d.Unknown) == 0) {
			continue // nothing read: dependencyEntries writes it
		}
		if len(out) == 0 {
			b.WriteString("dependencies:\n")
		}
		out[d.Name] = true
		services := strings.TrimPrefix(d.Where, "calls seen to ")
		fmt.Fprintf(b, "  # inferred from runtime calls (%s)\n", d.Where)
		if len(d.Versions) == 0 {
			p.unproposed(b, d, services, bundles)
			continue
		}
		described, versions, proposal := cmp.Or(theServices(d.described), services), joinAnd(d.Versions), proposedRange(d.Versions)
		writeDependency(b, d.Name, d.Ref, fmt.Sprintf("proposed %s, from the Pactos describing %s at %s", proposal, described, versions))
		if described == services {
			p.found("%s calls %s, described by %s %s", p.r.Service, services, d.Name, versions)
		} else {
			p.found("%s calls %s, and the Pactos describing %s report %s %s", p.r.Service, services, described, d.Name, versions)
		}
		p.undeclared(d.Name, proposal, "the lowest version the Pactos describing it report")
	}
	return out
}

// unproposed writes a dependency seen at runtime whose Pactos were read but
// propose no range: they name different contracts, or none reports a
// usable version. Its ref is the one they name, else where its contract
// would be published.
func (p *planner) unproposed(b *strings.Builder, d Inferred, services, bundles string) {
	why := "no reconciled Pacto describing " + services + " reports a contract version"
	if d.conflict {
		why = "the Pactos describing " + services + " name different contracts"
	}
	ref := d.Ref
	if ref == "" {
		ref = "oci://" + bundles + "/" + d.Name
		b.WriteString("  # no Pacto describing it names a contract ref: ref is where it would be published\n")
	}
	writeDependency(b, d.Name, ref, "no range proposed: "+why)
	p.found("%s calls %s", p.r.Service, services)
	p.undeclared(d.Name, "", "")
}

// writeDependency writes one dependency with compatibility unknown and
// comment after it.
func writeDependency(b *strings.Builder, name, ref, comment string) {
	b.WriteString(discover.DependencyYAML(name, ref, comment))
}

// undeclared says, under Unknown and next, that pacto.yaml leaves the range
// of dep to declare, with proposal and where it came from when there is one.
func (p *planner) undeclared(dep, proposal, from string) {
	svc := p.r.Service
	unknown := fmt.Sprintf("the versions of %s that %s accepts: pacto.yaml leaves compatibility unknown, so impact cannot tell whether a %s change breaks %s: "+
		"its verdict for %s is unknown, and a BREAKING change to %s stays unverified", dep, svc, dep, svc, svc, dep)
	next := fmt.Sprintf("declare the versions of %s that %s accepts: replace compatibility: unknown with a range", dep, svc)
	if proposal != "" {
		unknown += "; the proposal " + proposal + " comes from " + from
		next += ", such as the proposed " + proposal
	}
	p.unknown("%s", unknown)
	p.next("%s", next)
}

// proposedRange is ^MAJOR.MINOR.0 of the lowest strict-semver version, ""
// when there is none. It never widens across majors: the comment lists every
// version, so a person sees the spread.
func proposedRange(versions []string) string {
	var low *semver.Version
	for _, s := range versions {
		if v, err := semver.StrictNewVersion(s); err == nil && (low == nil || v.LessThan(low)) {
			low = v
		}
	}
	if low == nil {
		return ""
	}
	return fmt.Sprintf("^%d.%d.0", low.Major(), low.Minor())
}

// provider is the ref to declare name with and the version its contract in
// --fleet has, "" when none was found: then ref is where its contract would
// be published.
func (p *planner) provider(name, bundles string) (ref, version string) {
	ref = "oci://" + bundles + "/" + name
	if p.snap == nil {
		return ref, ""
	}
	var best *fleet.ContractRevision
	var bestV *semver.Version
	for _, rev := range p.snap.Revisions {
		v, err := semver.NewVersion(rev.Version)
		if rev.Service != name || err != nil || (bestV != nil && !v.GreaterThan(bestV)) {
			continue
		}
		best, bestV = rev, v
	}
	if best == nil {
		return ref, ""
	}
	if c := best.Contract; c != nil && c.Publish != nil && c.Publish.Bundle != "" {
		ref = c.Publish.Bundle
	}
	return ref, bestV.String()
}

// configSchema is where the configuration schema pacto init writes goes.
const configSchema = "configuration/schema.json"

// configuration writes a configuration entry whose schema, configSchema,
// lists the workload's environment: each variable's key and type, never its
// value. A key read from a Secret, whose name matches a sensitive pattern or
// whose value is a URL with credentials is writeOnly.
func (p *planner) configuration(b *strings.Builder) {
	for _, n := range p.sc.EnvNotes {
		p.unknown("%s", n)
	}
	if len(p.sc.Env) == 0 {
		return
	}
	props := map[string]map[string]any{}
	var from, sensitive []string
	literals := p.sc.Literals()
	for _, e := range p.sc.Env {
		prop := discover.EnvProperty(e)
		props[e.Name] = prop
		if prop["writeOnly"] == true {
			sensitive = append(sensitive, e.Name)
		}
		if !slices.Contains(from, e.File) {
			from = append(from, e.File)
		}
	}
	data, _ := json.MarshalIndent(struct { // ponytail: a map of strings and scalars always marshals
		Schema     string                    `json:"$schema"`
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
	}{"https://json-schema.org/draft/2020-12/schema", "object", props}, "", "  ")
	p.schema = string(data) + "\n"
	fmt.Fprintf(b, "configurations:\n  - name: env\n    schema: %s\n    required: true\n", configSchema)
	p.found("%s: the %d environment variables %s reads, from %s, with their types and no values", configSchema, len(p.sc.Env), p.r.Service, strings.Join(from, ", "))
	if len(sensitive) > 0 {
		slices.Sort(sensitive)
		p.found("sensitive keys, marked writeOnly: %s", strings.Join(sensitive, ", "))
	}
	for _, f := range slices.Sorted(maps.Keys(literals)) {
		keys := literals[f]
		slices.Sort(keys)
		values, them := literalValues(len(keys))
		p.next("%s sets %s to %s, and the bundle publishes that file to anyone who can pull it: "+
			"read %s from a Secret (valueFrom.secretKeyRef) instead", f, strings.Join(keys, ", "), values, them)
	}
}

// literalValues is how a next step names the literal values of n keys, and
// the pronoun that refers back to those keys.
func literalValues(n int) (values, them string) {
	if n == 1 {
		return "a literal value", "it"
	}
	return "literal values", "them"
}

// publish writes the publish section and keeps it for the cluster pieces.
func (p *planner) publish(b *strings.Builder, bundles, registry, imgRepo string) {
	sc, name := p.sc, p.r.Service
	p.pub = &contract.Publish{Bundle: "oci://" + bundles + "/" + name, Sign: &contract.PublishSign{Key: signKey}}
	fmt.Fprintf(b, "publish:\n  bundle: %s\n", p.pub.Bundle)
	if len(sc.Manifests) > 0 {
		p.pub.Deploy = "manifests"
		b.WriteString("  deploy: manifests\n")
	}
	if sc.Dockerfile != "" || len(sc.Manifests) > 0 {
		b.WriteString("  artifacts:\n")
	}
	pin := imgRepo
	if df := sc.Dockerfile; df != "" {
		if imgRepo == "" || hubLibrary(imgRepo) {
			imgRepo = registry + "/images/" + name
		}
		fmt.Fprintf(b, "    - name: api\n      type: image\n      ref: oci://%s\n      build:\n        dockerfile:\n          context: %s\n", imgRepo, discover.Quote(path.Dir(df)))
		if path.Base(df) != "Dockerfile" {
			fmt.Fprintf(b, "          file: %s\n", discover.Quote(df))
		}
		p.found("%s builds the image %s", df, imgRepo)
		if len(sc.Dockerfiles) > 1 {
			p.unknown("several Dockerfiles (%s): pacto init used %s", strings.Join(sc.Dockerfiles, ", "), df)
		}
	}
	if len(sc.Manifests) > 0 {
		cmd, kept := p.copyManifests()
		cmd += p.pinImage(pin, imgRepo, kept)
		if p.opts.Delivery {
			cmd += ` && sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml`
		}
		ref := "oci://" + registry + "/deploy/" + name
		p.pub.Artifacts = append(p.pub.Artifacts, contract.PublishArtifact{Name: "manifests", Type: contract.ArtifactManifests, Ref: ref})
		fmt.Fprintf(b, "    - name: manifests\n      type: manifests\n      ref: %s\n      build:\n        command: [sh, -c, %s]\n", ref, discover.Quote(cmd))
	}
	fmt.Fprintf(b, "  sign:\n    key: %s\n", signKey)
	if p.opts.NoTransparencyLog {
		f := false
		p.pub.Sign.TransparencyLog = &f
		b.WriteString("    transparencyLog: false\n")
	}
	if _, err := os.Stat(filepath.Join(p.r.Dir, signKey)); err != nil {
		p.next("put the signing key at %s: a cosign PEM private key, decrypted with COSIGN_PASSWORD", signKey)
	}
}

// pinImage is the build step that rewrites, in the published copy of every
// manifest that deploys the workload's image (the workload's own, and a Job
// or CronJob beside it), that image to the image this publish built, by
// digest (PACTO_REF_API@PACTO_DIGEST_API). Each version then deploys its own
// image, all of it, and nobody raises the tag by hand. pin is the workload's
// image repository, "" when it names none Pacto can push to; built is the
// image the Dockerfile builds; kept says the manifests keep their folders.
func (p *planner) pinImage(pin, built string, kept bool) string {
	w := p.sc.Workload
	if p.sc.Dockerfile == "" || w == nil {
		return ""
	}
	if pin == "" || !imageRE.MatchString(w.Image) {
		p.next("%s does not deploy %s, the image the Dockerfile builds, so its image is published as written, not pinned to the one each version builds", w.File, built)
		return ""
	}
	// The match is the whole value of an image: field, block or flow style,
	// never a substring, so a label, a name or another image that shares the
	// text is left alone. An image written in a form the match does not read
	// (a JSON manifest, say) is not pinned, and init says so; a build that
	// pinned nothing, as the file changed since, fails rather than ship the
	// tag unpinned.
	img := strings.ReplaceAll(w.Image, ".", `\.`)
	field := regexp.MustCompile(`(?m)(^|[{,\s])image:\s*["']?` + img + `["']?\s*([,}#]|$)`)
	// named is any image field, YAML or JSON, that holds the image: a file
	// that has one the match does not read is told it is not pinned; a file
	// that only shares the text (a Service's name, a label) is not.
	named := regexp.MustCompile(`image["']?\s*:\s*["']?` + regexp.QuoteMeta(w.Image) + `(["'\s,}#]|$)`)
	var cmd string
	var pinned []string
	for _, m := range p.sc.Manifests {
		raw, _ := os.ReadFile(filepath.Join(p.r.Dir, m))
		if !field.Match(raw) {
			if named.Match(raw) {
				p.next("%s names %s in a form pacto init cannot rewrite (write it as `image: %s`), so its image is published as written, not pinned to the one each version builds", m, w.Image, w.Image)
			}
			continue
		}
		out := path.Base(m)
		if kept {
			out = m
		}
		pinned = append(pinned, m)
		cmd += fmt.Sprintf(` && sed -E "s,(^|[{,[:space:]])(image:[[:space:]]*[\"']?)%s([\"']?[[:space:]]*([,}#]|$)),\1\2$PACTO_REF_API@$PACTO_DIGEST_API\3,g" %s > "$PACTO_OUT"/%s`+
			` && { grep -qF "$PACTO_DIGEST_API" "$PACTO_OUT"/%s || { echo "pacto: no image: %s line to pin" >&2; exit 1; }; }`,
			img, shq(m), shq(out), shq(out), w.Image)
	}
	if len(pinned) > 0 {
		verb := "deploys"
		if len(pinned) > 1 {
			verb = "deploy"
		}
		p.found("the published %s %s the image each version builds, pinned by digest", strings.Join(pinned, ", "), verb)
	}
	return cmd
}

// copyManifests is the build command that copies the manifests into
// $PACTO_OUT. Each file is named, never globbed: a YAML file added later (a
// Secret, say) is not shipped unseen. A kustomize layout, or files that share
// a name, keep their folders, since flattened one would overwrite the other.
func (p *planner) copyManifests() (string, bool) {
	byDir := map[string][]string{}
	var dirs, all []string
	seen := map[string]bool{}
	keep := len(p.sc.Kustomize) > 0
	for _, m := range p.sc.Manifests {
		d := path.Dir(m)
		if byDir[d] == nil {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], shq(m))
		all = append(all, shq(m))
		keep = keep || seen[path.Base(m)]
		seen[path.Base(m)] = true
	}
	if !keep {
		return "cp " + strings.Join(all, " ") + ` "$PACTO_OUT"/`, false
	}
	cmds := make([]string, len(dirs))
	for i, d := range dirs {
		out, mkdir := `"$PACTO_OUT"/`+shq(d)+"/", ""
		if d == "." {
			out = `"$PACTO_OUT"/`
		} else {
			mkdir = "mkdir -p " + out + " && "
		}
		cmds[i] = mkdir + "cp " + strings.Join(byDir[d], " ") + " " + out
	}
	p.unknown("the folder Flux builds: the manifests keep their folders (%s), as they use kustomize or share file names; "+
		"set spec.path of the Kustomization that applies them to it", strings.Join(dirs, ", "))
	return strings.Join(cmds, " && "), true
}

// readFleet reads the contracts in FleetRoots, once, for the dependencies
// and the consumers.
func (p *planner) readFleet(ctx context.Context) error {
	if len(p.opts.FleetRoots) == 0 {
		return nil
	}
	var sources []fleet.Source
	for i, root := range p.opts.FleetRoots {
		sources = append(sources, fleetsrc.NewLocalSource("local-"+strconv.Itoa(i), root))
	}
	snap, err := fleet.Build(ctx, fleet.BuildOptions{}, sources...)
	if err != nil {
		return fmt.Errorf("reading the contracts in %s: %w", strings.Join(p.opts.FleetRoots, ", "), err)
	}
	p.snap = snap
	return nil
}

// consumers looks for the services that declare this one, in FleetRoots.
func (p *planner) consumers() {
	if p.snap == nil {
		if p.r.Mode == modePactify || p.r.Mode == modeComplete {
			p.unknown("who uses %s: no other services' contracts were given (--fleet), so none were looked at", p.r.Service)
		}
		return
	}
	bundle := ""
	if p.pub != nil {
		bundle = strings.TrimPrefix(p.pub.Bundle, "oci://")
	}
	p.r.Consumers = declaredBy(p.snap, p.r.Service, bundle)
	if p.snap.Completeness != fleet.CompletenessComplete {
		p.unknown("who uses %s: the contracts in %s were read only in part (%s)", p.r.Service, strings.Join(p.opts.FleetRoots, ", "), p.snap.Completeness)
	}
	p.unknown("who calls %s without declaring it: no contract says", p.r.Service)
}

// declaredBy are the services in snap that declare name, or its bundle, as a
// dependency, each once and sorted.
func declaredBy(snap *fleet.FleetSnapshot, name, bundle string) []Finding {
	out := []Finding{}
	seen := map[Finding]bool{}
	for _, rel := range snap.Relationships {
		from, to := snap.Services[rel.FromService], snap.Services[rel.ToService]
		// Dependencies only: these suggest runtime dependencies to declare. An
		// inferred edge is no declaration, so it never reads "declares".
		if rel.Type != fleet.RelationshipDependency || rel.Provenance == fleet.ProvenanceInferred || from == nil || from.Name == name {
			continue
		}
		// The ref matches even when nothing in snap resolves it, where rel.To
		// is only the dependency's name.
		if (to == nil || to.Name != name) && (bundle == "" || refRepo(cmp.Or(rel.RequestedRef, rel.To)) != bundle) {
			continue
		}
		f := Finding{Name: from.Name, Where: strings.TrimSpace("declares " + name + " " + contract.RangeLabel(rel.Compatibility))}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// refRepo is an oci:// reference without its scheme, tag or digest.
func refRepo(ref string) string {
	ref = strings.TrimPrefix(ref, "oci://")
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

// ignore keeps the signing key, and the cluster pieces, out of the bundle.
func (p *planner) ignore() {
	var need []string
	if p.pub != nil && p.pub.Sign != nil && p.pub.Sign.Key != "" && filepath.IsLocal(p.pub.Sign.Key) {
		need = append(need, filepath.ToSlash(p.pub.Sign.Key))
	}
	if p.deliveryPlanned {
		need = append(need, "delivery/")
	}
	if len(need) == 0 {
		return
	}
	// A plugin may already have planned one; its lines are kept and the
	// missing ones added to it.
	i := slices.IndexFunc(p.r.Files, func(f File) bool { return strings.EqualFold(f.Path, ".pactoignore") })
	data, planned, err := p.currentIgnore(i)
	if err != nil {
		p.file(".pactoignore", strings.Join(need, "\n")+"\n")
		return
	}
	m := ignore.New(strings.Split(string(data), "\n"))
	var missing []string
	for _, n := range need {
		if !m.Ignored(strings.TrimSuffix(n, "/"), strings.HasSuffix(n, "/")) {
			missing = append(missing, n)
		}
	}
	f := File{Path: ".pactoignore", Action: Keep}
	switch {
	case planned:
		f = p.r.Files[i]
		if len(missing) > 0 {
			f.Content = strings.TrimSuffix(f.Content, "\n") + "\n" + strings.Join(missing, "\n") + "\n"
		}
	case len(missing) > 0:
		f = File{Path: ".pactoignore", Action: Append, Content: strings.Join(missing, "\n") + "\n"}
	}
	if i >= 0 {
		p.r.Files[i] = f
		return
	}
	p.r.Files = append(p.r.Files, f)
}

// currentIgnore is the .pactoignore planned at i when it is added, else the
// one in the folder, and whether it was the planned one.
func (p *planner) currentIgnore(i int) ([]byte, bool, error) {
	if i >= 0 && p.r.Files[i].Action == Add {
		return []byte(p.r.Files[i].Content), true, nil
	}
	data, err := os.ReadFile(filepath.Join(p.r.Dir, ".pactoignore"))
	return data, false, err
}

var (
	refRE  = regexp.MustCompile(`^oci://[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$`)
	safeRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	// imageRE is an image reference with nothing sh or sed would read.
	imageRE = regexp.MustCompile(`^[A-Za-z0-9._/:@-]+$`)
)

// shq quotes s for sh when it needs it.
func shq(s string) string {
	if safeRE.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// discoveredPorts are the container ports among the discovered fields, in
// numeric order.
func discoveredPorts(fields []contract.Inferred) []int {
	var out []int
	for _, f := range fields {
		if n, ok := strings.CutPrefix(f.Field, "ports["); ok {
			port, _ := strconv.Atoi(strings.TrimSuffix(n, "]")) // ponytail: discovery writes ports[<n>] only for a number
			out = append(out, port)
		}
	}
	slices.Sort(out)
	return out
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// OwnerHint says what is wrong with team as owner.team, "" when nothing is.
// owner.team is the team's bare name, such as billing, usually the team label
// on its workloads; a team/ or teams/ prefix only repeats that it is a team.
func OwnerHint(team string) string {
	for _, prefix := range []string{"team/", "teams/"} {
		if rest, ok := strings.CutPrefix(strings.ToLower(team), prefix); ok && rest != "" {
			return fmt.Sprintf("owner %q starts with %s: owner.team is the team's bare name, so write %q", team, prefix, team[len(prefix):])
		}
	}
	return ""
}
