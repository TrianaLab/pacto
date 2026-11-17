// Command genexternalcatalog checks and publishes the default external catalog.
//
//	genexternalcatalog check [-f extension.yaml] [--digest-only]
//	genexternalcatalog push oci://<repository>:<tag>
//
// check validates the embedded default (or an extension file merged over it)
// and prints its artifact digest and entry count. push writes the embedded
// default as the catalog artifact and refuses a digest other than the embedded
// one. The logic lives in pkg/externalcatalog; this file only wires flags.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

const usage = "usage: genexternalcatalog check [-f file] [--digest-only] | push oci://<repository>:<tag>"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	switch args[0] {
	case "check":
		fs := flag.NewFlagSet("check", flag.ContinueOnError)
		file := fs.String("f", "", "an organization extension file to validate over the default")
		digestOnly := fs.Bool("digest-only", false, "print only the digest")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var ext []byte
		if *file != "" {
			b, err := os.ReadFile(*file)
			if err != nil {
				return err
			}
			ext = b
		}
		s, err := externalcatalog.Check(externalcatalog.DefaultBytes(), ext)
		if err != nil {
			return err
		}
		if *digestOnly {
			fmt.Println(s.Digest)
			return nil
		}
		fmt.Printf("%s %d entries\n", s.Digest, s.Entries)
		return nil
	case "push":
		if len(args) != 2 {
			return fmt.Errorf("%s", usage)
		}
		c := oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.WithInsecureRegistries(oci.EnvInsecureRegistries()...))
		d, err := externalcatalog.Push(context.Background(), func(ctx context.Context, ref string, b []byte) (string, error) {
			return externalcatalog.Write(ctx, c, ref, b)
		}, args[1])
		if err != nil {
			return err
		}
		fmt.Println(d)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q; %s", args[0], usage)
	}
}
