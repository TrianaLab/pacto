// Command project renders the canonical acceptance scenario into the artifacts a
// harness has to hand to a real cluster: the contract bundle directories it
// publishes to the registry, and one OTLP export per observation source for the
// operator to mount.
//
// It exists so the harness stops declaring the fixture. Bundles used to be
// heredocs and the observation export a 200-character JSON literal on a kubectl
// line, both of which had to agree by hand with the versions and source ids the
// Product gate expected — and silently did not have to.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/trianalab/pacto/v3/tests/acceptance/scenario"
)

func main() {
	dir := flag.String("dir", "", "directory to render the fixture into")
	domain := flag.String("domain", "", "OCI domain (registry host + org) the fixture publishes to")
	flag.Parse()

	s := scenario.OperationalGraph
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
		path := filepath.Join(*dir, src.ID+".json")
		if err := os.WriteFile(path, append(export, '\n'), 0o600); err != nil {
			exit(err)
		}
		fmt.Printf("  observation source %s -> %s\n", src.ID, path)
	}
	for _, svc := range s.Services {
		for _, rev := range svc.Revisions {
			fmt.Printf("  %s %s -> %s\n", svc.Name, rev.Version, filepath.Join(*dir, rev.Dir))
		}
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, "project:", err)
	os.Exit(1)
}
