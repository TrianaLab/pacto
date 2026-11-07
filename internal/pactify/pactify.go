// Package pactify is what `pacto init` and the pacto_create MCP tool do to a
// folder. An empty folder gets a blank contract with the owner, domain and
// registry the caller gave. A folder that already
// holds a service gets a contract read from its Dockerfile and Kubernetes
// manifests: its environment as a configuration schema, the services its
// environment points at as dependencies, its probe as a health check and,
// unless DescribeOnly, a publish section. With Delivery it also gets the
// cluster pieces that ship it. Planning changes nothing. Applying only adds files and lines:
// a file that is already there is the user's and is never rewritten, so
// running it twice is safe. With Template the folder gets a contract created
// from a golden path: its identity reset, the caller's values applied, its
// origin recorded and the result validated strictly before anything is
// planned.
package pactify

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// Options says which folder to pactify and what the caller already knows.
type Options struct {
	// Dir is the folder. Empty means Name, and "." when Name is empty too.
	Dir string
	// Name is service.name. Empty means the workload's name, then the folder's.
	Name  string
	Owner string
	// Domain is service.domain, such as payments; it raises pactoVersion to 2.4.
	Domain   string
	Registry string
	// Delivery also plans the Pacto resource, a Stage per environment and the
	// Flux OCIRepository and Kustomization each one applies through.
	Delivery bool
	// Environments are the Stages, in promotion order. Empty means test, prod.
	Environments []string
	// FleetRoots are folders of other services' contracts, searched for the
	// services that declare this one as a dependency.
	FleetRoots []string
	// Plan only plans: nothing is written.
	Plan bool
	// DescribeOnly writes a contract that only describes the service: no
	// publish section, so no signing key and nothing for pacto publish.
	DescribeOnly bool
	// NoTransparencyLog writes transparencyLog: false under publish.sign, for
	// a key whose signatures do not belong in the public transparency log
	// (a throwaway key, or offline signing).
	NoTransparencyLog bool
	// InsecureRegistry makes the Flux OCIRepositories Delivery writes read the
	// registry over plain HTTP (spec.insecure), for a local registry only.
	InsecureRegistry bool
	// RegistrySecret names the docker-registry Secret, in each environment's
	// namespace, that Delivery's Flux OCIRepositories and Pacto resource log
	// in to a private registry with.
	RegistrySecret string
	// Inferred are dependencies seen at runtime (pacto init --cluster). One
	// with a Version is written as an entry pinned to exactly that version;
	// one without gets a placeholder range, like a dependency read from the env.
	Inferred []Inferred
	// Template creates the contract from a golden path. It needs Owner and
	// refuses Delivery, FleetRoots and Inferred.
	Template *Template
}

// Template is the golden path a service is created from, already read and,
// when it came from a registry, checked by the caller.
type Template struct {
	// FS holds the path's files: its pacto.yaml and everything it copies.
	FS fs.FS
	// Ref and Digest are recorded as service.origin.
	Ref, Digest string
	// Signature is the signature step the caller established.
	Signature fleet.CreationStep
	// Overrides are the caller's values, applied before the path's template
	// marker is removed.
	Overrides override.Overrides
	// Validate validates the created contract over the files the folder will
	// hold. Nil validates locally, with no resolver.
	Validate func(ctx context.Context, c *contract.Contract, raw []byte, fsys fs.FS) []validation.Stage
}

// Inferred is a service this one was seen calling at runtime.
type Inferred struct {
	Name    string `json:"name"`
	Ref     string `json:"ref,omitempty"`
	Version string `json:"version,omitempty"`
	Where   string `json:"where"`
}

// File actions.
const (
	Add    = "add"    // the file is not there; it is written with Content
	Append = "append" // the file is there; Content is added at its end
	Keep   = "keep"   // the file is there and is left as it is
)

// File is one file the plan touches, relative to Result.Dir.
type File struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Content string `json:"content,omitempty"`
}

// Finding is a service found to depend on, or use, this one.
type Finding struct {
	Name  string `json:"name"`
	Where string `json:"where"`
}

// Result is the plan and, once applied, what was done.
type Result struct {
	Dir     string `json:"dir"`
	Path    string `json:"path"`
	Service string `json:"service"`
	// Mode is "template" for a contract created from a golden path, "blank"
	// for a folder with no service in it, else "pactify".
	Mode    string `json:"mode"`
	Applied bool   `json:"applied"`
	// Found is what was read from the folder, one line each.
	Found        []string  `json:"found"`
	Dependencies []Finding `json:"dependencies"`
	Consumers    []Finding `json:"consumers"`
	// Unknown is what Pacto could not establish. It is never guessed.
	Unknown []string `json:"unknown"`
	// Next is what a person still has to do.
	Next  []string `json:"next"`
	Files []File   `json:"files"`
	// Steps say how far creating from a golden path got, and what Pacto
	// cannot tell yet.
	Steps []fleet.CreationStep `json:"steps,omitempty"`
}

const (
	modeBlank    = "blank"
	modePactify  = "pactify"
	modeTemplate = "template"
)

var (
	nameRE    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	nonNameRE = regexp.MustCompile(`[^a-z0-9]+`)
)

// Init plans the folder and, unless opts.Plan, applies the plan with
// writeFile.
func Init(ctx context.Context, opts Options, writeFile func(string, []byte, os.FileMode) error) (*Result, error) {
	r, err := plan(ctx, opts)
	if err != nil || opts.Plan {
		return r, err
	}
	if err := apply(r, writeFile); err != nil {
		return r, err
	}
	for i := range r.Steps {
		if r.Steps[i].Step == fleet.StepContract {
			r.Steps[i].Status, r.Steps[i].Detail = fleet.StepStatusDone, "written to "+r.Path
		}
	}
	return r, nil
}

func plan(ctx context.Context, opts Options) (*Result, error) {
	dir := opts.Dir
	if dir == "" {
		dir = opts.Name
	}
	if dir == "" {
		dir = "."
	}
	if err := TemplateOptions(opts); err != nil {
		return nil, err
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("%s is a file, not a folder", dir)
	}
	sc, err := scanDir(dir)
	if err != nil {
		return nil, err
	}
	name, err := serviceName(opts.Name, sc, dir)
	if err != nil {
		return nil, err
	}
	envs, err := environments(opts)
	if err != nil {
		return nil, err
	}
	r := &Result{Dir: dir, Path: filepath.Join(dir, "pacto.yaml"), Service: name, Mode: modeBlank,
		Found: []string{}, Dependencies: []Finding{}, Consumers: []Finding{}, Unknown: []string{}, Next: []string{}, Files: []File{}}
	switch {
	case opts.Template != nil:
		r.Mode = modeTemplate
	case sc.dockerfile != "" || len(sc.manifests) > 0:
		r.Mode = modePactify
	}
	p := &planner{r: r, sc: sc, opts: opts, envs: envs}
	if err := p.readFleet(ctx); err != nil {
		return nil, err
	}
	if err := p.contract(ctx); err != nil {
		return nil, err
	}
	p.transparencyLog()
	p.consumers()
	if opts.Delivery {
		p.delivery()
	}
	p.ignore()
	return r, nil
}

// environments checks the delivery options and returns the environments,
// test and prod unless the caller named others.
func environments(opts Options) ([]string, error) {
	envs := opts.Environments
	if len(envs) == 0 {
		envs = []string{"test", "prod"}
	}
	for _, e := range envs {
		if !nameRE.MatchString(e) {
			return nil, fmt.Errorf("environment %q is not a valid namespace name", e)
		}
	}
	if opts.DescribeOnly && opts.Delivery {
		return nil, fmt.Errorf("delivery ships what publish builds, and describe-only writes no publish section: drop one of the two")
	}
	if opts.DescribeOnly && opts.NoTransparencyLog {
		return nil, fmt.Errorf("transparencyLog goes under publish.sign, and describe-only writes no publish section: drop one of the two")
	}
	if opts.InsecureRegistry && !opts.Delivery {
		return nil, fmt.Errorf("an insecure registry changes only the Flux OCIRepositories that delivery writes, so it needs delivery")
	}
	if opts.RegistrySecret != "" && !opts.Delivery {
		return nil, fmt.Errorf("a registry Secret is named only in the cluster pieces that delivery writes, so it needs delivery")
	}
	if opts.RegistrySecret != "" && !nameRE.MatchString(opts.RegistrySecret) {
		return nil, fmt.Errorf("registry Secret %q is not a valid Secret name (lowercase letters, digits and hyphens)", opts.RegistrySecret)
	}
	return envs, nil
}

// serviceName is the caller's name, else the workload's, else the folder's
// made DNS-safe.
func serviceName(name string, sc *scan, dir string) (string, error) {
	if name == "" && sc.workload != nil {
		name = sc.workload.name
	}
	if name == "" {
		abs, _ := filepath.Abs(dir) // ponytail: Abs fails only without a working directory
		name = strings.Trim(nonNameRE.ReplaceAllString(strings.ToLower(filepath.Base(abs)), "-"), "-")
	}
	if !nameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a valid service name (lowercase letters, digits and hyphens); pass one with --name", name)
	}
	return name, nil
}

// planner fills one Result.
type planner struct {
	r    *Result
	sc   *scan
	opts Options
	envs []string
	// pub is the publish section written or found; the cluster pieces read it.
	pub             *contract.Publish
	snap            *fleet.FleetSnapshot // the contracts in FleetRoots, nil when none were given
	schema          string               // the configuration schema written, "" when none
	stages          []validation.Stage   // what validating a contract created from a golden path found
	contractNew     bool
	deliveryPlanned bool
}

// file plans path: Add when it is not there, else Keep.
func (p *planner) file(path, content string) {
	action := Add
	if _, err := os.Stat(filepath.Join(p.r.Dir, path)); err == nil {
		action, content = Keep, ""
	}
	p.r.Files = append(p.r.Files, File{Path: path, Action: action, Content: content})
}

// apply writes every Add and Append. It never touches a Keep.
func apply(r *Result, writeFile func(string, []byte, os.FileMode) error) error {
	// ponytail: the plan was made a moment ago in this same call; a file created
	// in between is overwritten. Re-stat here if plan and apply ever split.
	for _, f := range r.Files {
		path := filepath.Join(r.Dir, f.Path)
		data := []byte(f.Content)
		switch f.Action {
		case Keep:
			continue
		case Append:
			old, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if len(old) > 0 && old[len(old)-1] != '\n' {
				old = append(old, '\n')
			}
			data = append(old, data...)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", filepath.Dir(path), err)
		}
		if err := writeFile(path, data, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
	}
	r.Applied = true
	return nil
}
