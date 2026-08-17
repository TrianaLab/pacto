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
// The Compose surface is projected in one, because it has no registry until it
// is already running:
//
//	demo     the whole distributable artifact — the same bundles and plan, a
//	         compose file, and evidence payloads pinned to digests computed from
//	         the bundle bytes rather than read back from a registry
package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/trianalab/pacto/v3/tests/acceptance/scenario"
)

// The demo artifact's two static files. They are static because they are
// PROCESS, not fixture: seed.sh reads the projected plan and knows nothing about
// the scenario, and the README explains Docker Compose to a person. Generating
// either would be templating for its own sake — the README's ports are the one
// thing that must agree with the projection, so they alone are substituted.
//
//go:embed demo
var static embed.FS

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

// demo renders the whole distributable Compose artifact into one directory.
//
// Every path INSIDE the artifact is a container path, because the artifact is
// read where it is mounted and not where it was built: the plan and the evidence
// payloads name /demo, while the files themselves are written under -dir.
func demo(s scenario.Scenario, argv []string) {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	dir := fs.String("dir", "", "directory to assemble the artifact in")
	// Both must be digest-qualified; the projection refuses anything else. The
	// registry image has a default because its pin is a decision this repository
	// makes once (scenario.ComposeDefaultRegistryImage) rather than a per-release
	// input, while the pacto image is whatever the transaction just published and
	// cannot be known here.
	pactoImage := fs.String("pacto-image", "", "the pinned pacto image the demo runs, as repo@sha256:...")
	registryImage := fs.String("registry-image", scenario.ComposeDefaultRegistryImage, "the OCI registry image the demo runs, as repo@sha256:...")
	artifactRepo := fs.String("artifact-repo", "", "the OCI repository the artifact is published to, for the README")
	version := fs.String("version", "", "the version being built, for the README")
	source := fs.String("source", "github.com/trianalab/pacto", "the project the artifact was built from")
	mustParse(fs, argv)

	if *dir == "" {
		exit(fmt.Errorf("-dir is required"))
	}
	if err := os.MkdirAll(*dir, 0o750); err != nil {
		exit(err)
	}

	// The bundles and the observation exports are the SAME projection the cluster
	// harness publishes; only the domain differs, because the demo brings its own
	// registry up under a name it chooses.
	if err := s.Materialize(*dir, scenario.ComposeDomain); err != nil {
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
	}

	plan, err := s.Plan(scenario.ComposeArtifactMount)
	if err != nil {
		exit(err)
	}
	write(filepath.Join(*dir, "plan.tsv"), plan)

	// No registry exists yet, so the digests come from the bytes just written. The
	// seed re-checks them against what it actually publishes.
	digests, err := s.Digests(*dir)
	if err != nil {
		exit(err)
	}
	payloads, err := s.EvidencePayloads(scenario.ComposeArtifactMount, scenario.ComposeDomain, digests)
	if err != nil {
		exit(err)
	}
	for _, path := range sorted(payloads) {
		write(filepath.Join(*dir, filepath.Base(path)), payloads[path])
	}

	compose, err := s.Compose(scenario.ComposeOptions{PactoImage: *pactoImage, RegistryImage: *registryImage})
	if err != nil {
		exit(err)
	}
	write(filepath.Join(*dir, "compose.yaml"), compose)
	write(filepath.Join(*dir, ".env"), scenario.ComposeEnv())

	seed, err := static.ReadFile("demo/seed.sh")
	if err != nil {
		exit(err)
	}
	write(filepath.Join(*dir, "seed.sh"), seed)
	write(filepath.Join(*dir, "README.md"), readme(*pactoImage, *artifactRepo, *version, *source))

	if err := readableByTheContainers(*dir); err != nil {
		exit(err)
	}
	fmt.Printf("  demo artifact -> %s\n", *dir)
}

// readableByTheContainers opens the artifact to every uid.
//
// The demo bind-mounts this directory into containers that run as a non-root
// user the host has never heard of, so a file only its owner can read is a file
// the demo cannot read. The default 0600 the other projections write is right for
// them — nothing but the harness ever opens those — and wrong here, where the
// failure would be a container exiting on a permission error several minutes into
// somebody's first look at Pacto.
func readableByTheContainers(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(path, 0o755)
		}
		return os.Chmod(path, 0o644)
	})
}

// readme renders the artifact's own instructions. The ports are substituted
// rather than written down, because a README that disagreed with the compose
// file would send a user to a port nothing is listening on.
func readme(pactoImage, artifactRepo, version, source string) []byte {
	body, err := static.ReadFile("demo/README.md.tmpl")
	if err != nil {
		exit(err)
	}
	t, err := template.New("readme").Option("missingkey=error").Parse(string(body))
	if err != nil {
		exit(err)
	}
	data := struct {
		Ports                                     []scenario.ComposePort
		DashboardPort                             int
		PactoImage, ArtifactRepo, Version, Source string
	}{Ports: scenario.ComposePorts(), PactoImage: pactoImage, ArtifactRepo: artifactRepo, Version: version, Source: source}
	for _, p := range data.Ports {
		if p.Service == "dashboard" {
			data.DashboardPort = p.Default
		}
	}
	var out strings.Builder
	if err := t.Execute(&out, data); err != nil {
		exit(err)
	}
	return []byte(out.String())
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
