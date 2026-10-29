package pactify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const blankContract = `pactoVersion: "2.0"

service:
  name: %s
  version: 0.1.0
  owner:
    team: my-team
    dri: my.handle
    contacts:
      - type: email
        value: my-team@example.com
        purpose: ownership

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

readiness:
  minScore: 80
  expires: 2099-12-31
  partialCredit: 0.5
  history:
    - date: 2099-01-01
      version: 0.1.0
      author: my.handle
      description: Initial readiness assessment
  claims:
    - id: runbook
      type: document
      category: documentation
      status: not-done
      evidence: docs/runbook.md
      weight: 40
    - id: security-review
      type: ticket
      category: security
      status: not-done
      evidence: SEC-0000
      weight: 60

metadata:
  team: my-team
  tier: standard
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

// contract plans pacto.yaml: kept when it is there, else blank or read from
// the folder.
func (p *planner) contract() error {
	if p.r.Mode == modePactify {
		p.r.Dependencies = p.sc.dependencies(p.r.Service)
	}
	for _, d := range p.opts.Inferred {
		if !slices.ContainsFunc(p.r.Dependencies, func(f Finding) bool { return f.Name == d.Name }) {
			p.r.Dependencies = append(p.r.Dependencies, Finding{Name: d.Name, Where: d.Where})
		}
	}
	if data, err := os.ReadFile(p.r.Path); err == nil {
		p.r.Files = append(p.r.Files, File{Path: "pacto.yaml", Action: Keep})
		p.existing(data)
		return nil
	}
	if c := p.sc.chart; c != "" {
		p.unknown("the Helm chart in %s is not read: render it with helm template %s %s > k8s/rendered.yaml and run pacto init again, "+
			"or add the chart under publish.artifacts as a chart", c, p.r.Service, c)
	}
	if p.r.Mode == modeBlank {
		p.file("pacto.yaml", fmt.Sprintf(blankContract, p.r.Service))
		p.file("interfaces/openapi.yaml", fmt.Sprintf(blankOpenAPI, p.r.Service))
		p.file("configuration/schema.json", blankSchema)
		return nil
	}
	p.contractNew = true
	text := p.pactified()
	files := map[string]string{}
	if p.sc.openapi != "" {
		files[p.sc.openapi] = "{}"
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
	return nil
}

// transparencyLog says, when transparencyLog: false was asked for, that a
// contract init did not write (the user's own, or a blank one) does not
// carry it.
func (p *planner) transparencyLog() {
	if !p.opts.NoTransparencyLog {
		return
	}
	if p.pub != nil && p.pub.Sign != nil && p.pub.Sign.TransparencyLog != nil && !*p.pub.Sign.TransparencyLog {
		return
	}
	p.next("pacto.yaml was not written by this run, so it does not keep signatures out of the transparency log: add transparencyLog: false under publish.sign")
}

// existing reads what the cluster pieces need from the user's own contract.
func (p *planner) existing(data []byte) {
	c, err := contract.Parse(bytes.NewReader(data))
	if err != nil {
		p.unknown("pacto.yaml could not be read (%v), so nothing was taken from it", err)
		return
	}
	p.r.Service, p.pub = c.Service.Name, c.Publish
	if c.Publish == nil && !p.opts.DescribeOnly {
		p.next("pacto.yaml has no publish section, so pacto publish refuses it: set pactoVersion: \"2.1\" and add publish " +
			"(see Pactify a service in the docs), or move pacto.yaml aside and run pacto init again to have one written")
	}
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
func (p *planner) versionAndRegistry(w *workload, name string) (version, registry, imgRepo string) {
	tag := ""
	if w.image != "" {
		imgRepo, tag = imageRepo(w.image)
		if !refRE.MatchString("oci://" + imgRepo) {
			imgRepo = ""
		}
	}

	version = "0.1.0"
	if v, err := semver.StrictNewVersion(strings.TrimPrefix(tag, "v")); err == nil {
		version = v.String()
		p.found("version %s, from the image tag in %s", version, w.file)
	} else {
		p.unknown("version: no image tag is a version (x.y.z), so 0.1.0 is a placeholder")
	}

	registry = strings.Trim(strings.TrimPrefix(p.opts.Registry, "oci://"), "/")
	switch {
	case registry != "":
	case imgRepo != "" && !hubLibrary(imgRepo):
		registry = strings.TrimSuffix(path.Dir(imgRepo), "/images")
		p.found("registry %s, from the image %s", registry, w.image)
	case imgRepo != "":
		registry = "registry.example"
		p.unknown("registry: the image %s is a Docker Hub official image, which only Docker publishes, so registry.example is a placeholder; pass --registry", w.image)
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
	sc, name, w := p.sc, p.r.Service, p.sc.workload
	if w == nil {
		w = &workload{}
	} else {
		p.found("%s %s in %s", w.kind, w.name, w.file)
	}
	version, registry, imgRepo := p.versionAndRegistry(w, name)

	var b strings.Builder
	files := strings.Join(append(nonEmpty(sc.dockerfile), sc.manifests...), ", ")
	next := "see what publishing would do: pacto publish --plan"
	if p.opts.DescribeOnly {
		next = "validate it: pacto validate"
	}
	fmt.Fprintf(&b, "# Written by pacto init from %s.\n# Check it, then %s\n", files, next)
	fmt.Fprintf(&b, "pactoVersion: \"2.1\"\nservice:\n  name: %s\n  version: %s\n", yq(name), yq(version))
	team := p.opts.Owner
	if team == "" && w.team != "" {
		team = w.team
		p.found("owner team %s, from the team label in %s", team, w.file)
	}
	if team != "" {
		fmt.Fprintf(&b, "  owner:\n    team: %s\n", yq(team))
		if hint := OwnerHint(team); hint != "" {
			p.next("%s", hint)
		}
	} else {
		fmt.Fprintf(&b, "  # owner:\n  #   team: <the team that owns %s>\n", name)
		p.unknown("owner: no team label says who owns %s; pass --owner", name)
	}
	if kind, ok := workloadKinds[w.kind]; ok {
		fmt.Fprintf(&b, "workload: %s\n", kind)
	} else {
		p.unknown("workload: no Kubernetes workload says whether %s is a service, a job or a scheduled job", name)
	}
	switch {
	case sc.openapi != "":
		fmt.Fprintf(&b, "interfaces:\n  - name: api\n    type: openapi\n    ref: %s\n", yq(sc.openapi))
		p.found("API description %s", sc.openapi)
	case len(w.ports) > 0:
		p.unknown("interfaces: %s listens on port %s, but no OpenAPI file describes it", name, joinInts(w.ports))
	default:
		p.unknown("interfaces: nothing describes what %s serves", name)
	}
	// Bundles live beside the images, at registry/<name>, unless the image is
	// there already (the usual org/name layout): then they go under
	// registry/contracts, so a bundle tag never lands on the image.
	bundles := registry
	if imgRepo == registry+"/"+name {
		bundles = registry + "/contracts"
		p.found("contract bundles under %s, as the image %s already holds %s/%s", bundles, w.image, registry, name)
	}
	p.health(&b, sc.openapi != "")
	p.dependencyEntries(&b, bundles)
	p.configuration(&b)
	if p.opts.DescribeOnly {
		return b.String()
	}
	p.publish(&b, bundles, registry, imgRepo)
	p.next("the publish section is for pacto publish only: for a contract that only describes %s, run pacto init --describe-only instead (describe_only: true with pacto_create)", name)
	for _, f := range sc.secrets {
		p.next("%s holds a Secret's values, so it is left out of the published manifests, which anyone who can pull them reads; "+
			"supply it another way (a SealedSecret, an ExternalSecret or SOPS) and move any other objects in it to their own file", f)
	}
	return b.String()
}

// health writes a health capability when the workload has a probe, bound
// to the api interface when the probe calls an HTTP path and an OpenAPI file
// describes what the service serves.
func (p *planner) health(b *strings.Builder, api bool) {
	w := p.sc.workload
	if w == nil || w.probe == nil {
		return
	}
	path := w.probe.path
	if !contract.ValidCapabilityPath(path) {
		path = ""
	}
	b.WriteString("capabilities:\n  - type: health\n")
	switch {
	case path != "" && api:
		fmt.Fprintf(b, "    binding:\n      type: http\n      interface: api\n      path: %s\n", yq(path))
		p.found("health check GET %s on api, from the %s probe in %s", path, w.probe.kind, w.file)
	case path != "":
		p.found("health check GET %s, from the %s probe in %s", path, w.probe.kind, w.file)
		p.unknown("the interface that serves the health check GET %s: no OpenAPI file describes what %s serves, so the check is bound to none", path, p.r.Service)
	default:
		p.found("health check, from the %s probe in %s", w.probe.kind, w.file)
	}
}

// placeholderRange is the compatibility written for a dependency whose
// contract was not found. It is named in Unknown, never passed off as known.
const placeholderRange = "^1.0.0"

// dependencyEntries declares the services found. One whose contract is in
// --fleet gets its bundle and a range from the version that contract has; any
// other gets the repository its contract would be published at and a
// placeholder range, both said in a comment and in Unknown.
func (p *planner) dependencyEntries(b *strings.Builder, bundles string) {
	pinned := p.pinned(b)
	header := len(pinned) == 0
	for _, d := range p.r.Dependencies {
		if pinned[d.Name] {
			continue
		}
		if header {
			b.WriteString("dependencies:\n")
			header = false
		}
		ref, version := p.provider(d.Name, bundles)
		compat := placeholderRange
		if version != "" {
			compat = "^" + version
			fmt.Fprintf(b, "  # from %s; its contract in --fleet is at %s\n", d.Where, version)
			p.found("%s calls %s (%s), whose contract in --fleet is at %s", p.r.Service, d.Name, d.Where, version)
			p.next("pacto.yaml declares %s compatible with %s, from the version its contract has: change it to the versions %s accepts", d.Name, compat, p.r.Service)
		} else {
			fmt.Fprintf(b, "  # from %s\n  # no contract for %s was read (pass its folder with --fleet): ref is where\n"+
				"  # it would be published and %s is a placeholder; set the versions %s accepts\n", d.Where, d.Name, placeholderRange, p.r.Service)
			p.unknown("the versions of %s that %s accepts: no contract for %s was read (pass its folder with --fleet), so compatibility %s in pacto.yaml is a placeholder", d.Name, p.r.Service, d.Name, placeholderRange)
		}
		fmt.Fprintf(b, "  - name: %s\n    ref: %s\n    required: true\n    compatibility: %s\n", d.Name, ref, yq(compat))
	}
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
// lists the workload's environment: each variable typed by its value, with
// that value as its default. A variable read from a Secret gets no default.
func (p *planner) configuration(b *strings.Builder) {
	for _, n := range p.sc.envNotes {
		p.unknown("%s", n)
	}
	if len(p.sc.env) == 0 {
		return
	}
	props := map[string]map[string]any{}
	var from []string
	for _, e := range p.sc.env {
		prop := map[string]any{"type": "string"}
		switch {
		case e.secret:
			prop["description"] = "Read from the Secret " + e.ref + ", whose values pacto init does not read."
		case e.value != "":
			prop["type"], prop["default"] = typed(e.value)
		}
		props[e.name] = prop
		if !slices.Contains(from, e.file) {
			from = append(from, e.file)
		}
	}
	data, _ := json.MarshalIndent(struct { // ponytail: a map of strings and scalars always marshals
		Schema     string                    `json:"$schema"`
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
	}{"https://json-schema.org/draft/2020-12/schema", "object", props}, "", "  ")
	p.schema = string(data) + "\n"
	fmt.Fprintf(b, "configurations:\n  - name: env\n    schema: %s\n    required: true\n", configSchema)
	p.found("%s: the %d environment variables %s reads, with the values in %s as defaults", configSchema, len(p.sc.env), p.r.Service, strings.Join(from, ", "))
}

// typed is an environment value as a JSON Schema type and default: an
// integer, a boolean or a string.
func typed(v string) (string, any) {
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return "integer", n
	}
	if v == "true" || v == "false" {
		return "boolean", v == "true"
	}
	return "string", v
}

// pinned writes the dependencies seen at runtime whose provider runs a
// contract version, each pinned to exactly that version. Never a wider range:
// "*" accepts every version, so a breaking change would read compatible and
// the caller would no longer hold it. An exact pin reads every new version as
// incompatible, which asks this service's owners until a person widens it.
func (p *planner) pinned(b *strings.Builder) map[string]bool {
	out := map[string]bool{}
	for _, d := range p.opts.Inferred {
		if d.Version == "" || out[d.Name] {
			continue
		}
		if len(out) == 0 {
			b.WriteString("dependencies:\n")
		}
		out[d.Name] = true
		fmt.Fprintf(b, "  # inferred from runtime calls: pinned to the version seen running; widen it once you know\n"+
			"  - name: %s\n    ref: %s\n    required: true\n    compatibility: %s\n", d.Name, d.Ref, yq("="+d.Version))
		p.found("%s calls %s %s (%s)", p.r.Service, d.Name, d.Version, d.Where)
		p.next("pacto.yaml declares %s pinned to =%s, the version seen running: widen compatibility to the versions %s accepts, or every new %s asks %s's owners", d.Name, d.Version, p.r.Service, d.Name, p.r.Service)
	}
	return out
}

// publish writes the publish section and keeps it for the cluster pieces.
func (p *planner) publish(b *strings.Builder, bundles, registry, imgRepo string) {
	sc, name := p.sc, p.r.Service
	p.pub = &contract.Publish{Bundle: "oci://" + bundles + "/" + name, Sign: &contract.PublishSign{Key: signKey}}
	fmt.Fprintf(b, "publish:\n  bundle: %s\n", p.pub.Bundle)
	if len(sc.manifests) > 0 {
		p.pub.Deploy = "manifests"
		b.WriteString("  deploy: manifests\n")
	}
	b.WriteString("  artifacts:\n")
	pin := imgRepo
	if df := sc.dockerfile; df != "" {
		if imgRepo == "" || hubLibrary(imgRepo) {
			imgRepo = registry + "/images/" + name
		}
		fmt.Fprintf(b, "    - name: api\n      type: image\n      ref: oci://%s\n      build:\n        dockerfile:\n          context: %s\n", imgRepo, yq(path.Dir(df)))
		if path.Base(df) != "Dockerfile" {
			fmt.Fprintf(b, "          file: %s\n", yq(df))
		}
		p.found("%s builds the image %s", df, imgRepo)
		if len(sc.dockerfiles) > 1 {
			p.unknown("several Dockerfiles (%s): pacto init used %s", strings.Join(sc.dockerfiles, ", "), df)
		}
	}
	if len(sc.manifests) > 0 {
		cmd, kept := p.copyManifests()
		cmd += p.pinImage(pin, imgRepo, kept)
		if p.opts.Delivery {
			cmd += ` && sed "s/@PACTO_VERSION@/$PACTO_VERSION/" delivery/pacto.yaml > "$PACTO_OUT"/pacto-resource.yaml`
		}
		ref := "oci://" + registry + "/deploy/" + name
		p.pub.Artifacts = append(p.pub.Artifacts, contract.PublishArtifact{Name: "manifests", Type: contract.ArtifactManifests, Ref: ref})
		fmt.Fprintf(b, "    - name: manifests\n      type: manifests\n      ref: %s\n      build:\n        command: [sh, -c, %s]\n", ref, yq(cmd))
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
	w := p.sc.workload
	if p.sc.dockerfile == "" || w == nil {
		return ""
	}
	if pin == "" || !imageRE.MatchString(w.image) {
		p.next("%s does not deploy %s, the image the Dockerfile builds, so its image is published as written, not pinned to the one each version builds", w.file, built)
		return ""
	}
	// The match is the whole value of an image: field, block or flow style,
	// never a substring, so a label, a name or another image that shares the
	// text is left alone. An image written in a form the match does not read
	// (a JSON manifest, say) is not pinned, and init says so; a build that
	// pinned nothing, as the file changed since, fails rather than ship the
	// tag unpinned.
	img := strings.ReplaceAll(w.image, ".", `\.`)
	field := regexp.MustCompile(`(?m)(^|[{,\s])image:\s*["']?` + img + `["']?\s*([,}#]|$)`)
	// named is any image field, YAML or JSON, that holds the image: a file
	// that has one the match does not read is told it is not pinned; a file
	// that only shares the text (a Service's name, a label) is not.
	named := regexp.MustCompile(`image["']?\s*:\s*["']?` + regexp.QuoteMeta(w.image) + `(["'\s,}#]|$)`)
	var cmd string
	var pinned []string
	for _, m := range p.sc.manifests {
		raw, _ := os.ReadFile(filepath.Join(p.r.Dir, m))
		if !field.Match(raw) {
			if named.Match(raw) {
				p.next("%s names %s in a form pacto init cannot rewrite (write it as `image: %s`), so its image is published as written, not pinned to the one each version builds", m, w.image, w.image)
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
			img, shq(m), shq(out), shq(out), w.image)
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
	keep := len(p.sc.kustomize) > 0
	for _, m := range p.sc.manifests {
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
		if p.r.Mode == modePactify {
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
		if rel.Type != fleet.RelationshipDependency || from == nil || from.Name == name {
			continue
		}
		if (to == nil || to.Name != name) && (bundle == "" || refRepo(rel.To) != bundle) {
			continue
		}
		f := Finding{Name: from.Name, Where: strings.TrimSpace("declares " + name + " " + rel.Compatibility)}
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
	data, err := os.ReadFile(filepath.Join(p.r.Dir, ".pactoignore"))
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
	if len(missing) == 0 {
		p.r.Files = append(p.r.Files, File{Path: ".pactoignore", Action: Keep})
		return
	}
	p.r.Files = append(p.r.Files, File{Path: ".pactoignore", Action: Append, Content: strings.Join(missing, "\n") + "\n"})
}

var (
	refRE  = regexp.MustCompile(`^oci://[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$`)
	safeRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	// imageRE is an image reference with nothing sh or sed would read.
	imageRE = regexp.MustCompile(`^[A-Za-z0-9._/:@-]+$`)
	// plainRE starts with a letter, a dot or a slash, or is x.y.z, so YAML
	// never reads it as a number.
	plainRE = regexp.MustCompile(`^([A-Za-z./][A-Za-z0-9._/-]*|[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9._+-]*)$`)
)

// yq writes s as a YAML string: plain when YAML reads it back as that same
// string, else single-quoted.
func yq(s string) string {
	if plainRE.MatchString(s) && !yamlWords[strings.ToLower(s)] {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// yamlWords read as booleans or null when plain.
var yamlWords = map[string]bool{"true": true, "false": true, "yes": true, "no": true, "on": true, "off": true, "null": true, "y": true, "n": true}

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
