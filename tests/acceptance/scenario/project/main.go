// Command project renders the canonical acceptance scenario into the artifacts a
// harness has to hand to a real cluster.
//
// It exists so the harness stops declaring the fixture. Bundles used to be
// heredocs and the observation export a 200-character JSON literal on a kubectl
// line, both of which had to agree by hand with the versions and source ids the
// Product gate expected — and silently did not have to. The same was true one
// level up, of which directory went to which repository, which revision was
// deployed, and who signed what evidence about whom.
//
// The Kubernetes surface is projected in two steps, because a digest does not
// exist until the push has happened:
//
//	bundles  before the push: the bundle directories, the observation exports and
//	         the execution plan naming everything the harness has to do with them
//	helm     the chart values that come from the scenario
//	cluster  after it: the Pacto CRs and the evidence payloads, pinned to the
//	         digests the registry assigned
//
// The Compose surface is projected in one, into ONE FILE, because it has no
// registry until it is already running and no directory at all once it is
// published:
//
//	demo     the whole distributable artifact as a single compose file — the same
//	         bundles and plan carried inline as configs, and evidence payloads
//	         pinned to digests computed from the bundle bytes rather than read
//	         back from a registry
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v3/tests/acceptance/scenario"
)

func main() {
	if len(os.Args) < 2 {
		exit(fmt.Errorf("usage: project bundles|helm|cluster|demo [flags]"))
	}
	s := scenario.OperationalGraph
	switch os.Args[1] {
	case "bundles":
		bundles(s, os.Args[2:])
	case "helm":
		helm(s, os.Args[2:])
	case "cluster":
		cluster(s, os.Args[2:])
	case "demo":
		demo(s, os.Args[2:])
	default:
		exit(fmt.Errorf("unknown subcommand %q: expected bundles, helm, cluster or demo", os.Args[1]))
	}
}

// bundles renders everything that exists before anything has been published.
func bundles(s scenario.Scenario, argv []string) {
	fs := flag.NewFlagSet("bundles", flag.ExitOnError)
	dir := fs.String("dir", "", "directory to render the fixture into")
	domain := fs.String("domain", "", "OCI domain (registry host + org) the fixture publishes to")
	plan := fs.String("plan", "", "file to write the execution plan to")
	mustParse(fs, argv)

	if err := s.Materialize(*dir, *domain); err != nil {
		exit(err)
	}
	for _, src := range s.Sources {
		if src.Kind != scenario.SourceObservation {
			continue
		}
		export, err := s.TraceExport(src.ID)
		if err != nil {
			exit(err)
		}
		write(filepath.Join(*dir, src.ID+".json"), export)
		fmt.Printf("  observation source %s\n", src.ID)
	}
	for _, svc := range s.Services {
		for _, rev := range svc.Revisions {
			fmt.Printf("  %s %s -> %s\n", svc.Name, rev.Version, filepath.Join(*dir, rev.Dir))
		}
	}
	body, err := s.Plan(*dir)
	if err != nil {
		exit(err)
	}
	if *plan == "" {
		exit(fmt.Errorf("-plan is required: the harness reads the plan rather than restating it"))
	}
	write(*plan, body)
	fmt.Printf("  plan -> %s\n", *plan)
}

// helm renders the chart values the scenario decides, one per line, for a
// harness that turns each into a `--set` argument. A file rather than stdout so
// the harness reads it the same way it reads the plan.
func helm(s scenario.Scenario, argv []string) {
	fs := flag.NewFlagSet("helm", flag.ExitOnError)
	out := fs.String("out", "", "file to write the chart values to, one key=value per line")
	mustParse(fs, argv)

	if *out == "" {
		exit(fmt.Errorf("-out is required: the harness reads the projected values rather than assembling its own"))
	}
	values, err := s.HelmValues()
	if err != nil {
		exit(err)
	}
	write(*out, []byte(strings.Join(values, "\n")+"\n"))
	fmt.Printf("  %d chart values -> %s\n", len(values), *out)
}

// demo renders the whole distributable Compose artifact as one compose file.
//
// One file because that is the artifact: `docker compose publish` uploads the
// compose file, so anything not in it is not distributed. Every fixture document
// the demo reads is carried inline by the projection as a config, and every path
// INSIDE the artifact is a container path, because the artifact is read where
// Compose materializes it and not where it was built.
func demo(s scenario.Scenario, argv []string) {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	out := fs.String("out", "", "compose file to write the artifact to")
	// Both must be digest-qualified; the projection refuses anything else. The
	// registry image has a default because its pin is a decision this repository
	// makes once (scenario.ComposeDefaultRegistryImage) rather than a per-release
	// input, while the pacto image is whatever the transaction just published and
	// cannot be known here.
	pactoImage := fs.String("pacto-image", "", "the pinned pacto image the demo runs, as repo@sha256:...")
	registryImage := fs.String("registry-image", scenario.ComposeDefaultRegistryImage, "the OCI registry image the demo runs, as repo@sha256:...")
	version := fs.String("version", "", "the version being built; the artifact records it in x-pacto-demo")
	mustParse(fs, argv)

	if *out == "" {
		exit(fmt.Errorf("-out is required"))
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
		exit(err)
	}
	compose, err := s.Compose(scenario.ComposeOptions{
		PactoImage:    *pactoImage,
		RegistryImage: *registryImage,
		Version:       *version,
	})
	if err != nil {
		exit(err)
	}
	write(*out, compose)
	fmt.Printf("  demo artifact -> %s\n", *out)
}

// cluster renders the projections that need real digests.
func cluster(s scenario.Scenario, argv []string) {
	fs := flag.NewFlagSet("cluster", flag.ExitOnError)
	dir := fs.String("dir", "", "directory the fixture was rendered into")
	domain := fs.String("domain", "", "OCI domain (registry host + org) the fixture published to")
	namespace := fs.String("namespace", "", "namespace to declare the Pacto CRs in")
	crs := fs.String("crs", "", "file to write the Pacto custom resources to")
	var digests digestMap
	fs.Var(&digests, "digest", "a published digest, as service@version=sha256:...; repeatable")
	mustParse(fs, argv)

	if *crs == "" {
		exit(fmt.Errorf("-crs is required: the harness applies the projected CRs rather than writing its own"))
	}
	body, err := s.PactoCRs(*namespace, *domain, digests)
	if err != nil {
		exit(err)
	}
	write(*crs, body)
	fmt.Printf("  pacto CRs -> %s\n", *crs)

	payloads, err := s.EvidencePayloads(*dir, *domain, digests)
	if err != nil {
		exit(err)
	}
	for _, path := range sorted(payloads) {
		write(path, payloads[path])
		fmt.Printf("  evidence payload -> %s\n", path)
	}
}

// digestMap collects repeated -digest key=value flags. Explicit about a duplicate
// key: two digests for one published revision means the caller lost track of
// which push produced which, and silently keeping the last is how a CR ends up
// pinned to the wrong content.
type digestMap map[string]string

func (d digestMap) String() string { return fmt.Sprintf("%v", map[string]string(d)) }

func (d *digestMap) Set(v string) error {
	key, digest, ok := strings.Cut(v, "=")
	if !ok || key == "" || digest == "" {
		return fmt.Errorf("expected service@version=digest, got %q", v)
	}
	if *d == nil {
		*d = digestMap{}
	}
	if prior, dup := (*d)[key]; dup {
		return fmt.Errorf("%s was already published as %s", key, prior)
	}
	(*d)[key] = digest
	return nil
}

func sorted(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustParse(fs *flag.FlagSet, argv []string) {
	if err := fs.Parse(argv); err != nil {
		exit(err)
	}
	if fs.NArg() > 0 {
		exit(fmt.Errorf("%s takes no positional arguments, got %q", fs.Name(), fs.Arg(0)))
	}
}

func write(path string, body []byte) {
	if err := os.WriteFile(path, body, 0o600); err != nil {
		exit(err)
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, "project:", err)
	os.Exit(1)
}
