package main

import (
	"fmt"
	"os"

	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/cli"
	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/internal/plugin"
)

// version is set at build time via ldflags.
var version = "dev"

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	keychain := oci.NewKeychain(oci.CredentialOptions{
		Username: os.Getenv("PACTO_REGISTRY_USERNAME"),
		Password: os.Getenv("PACTO_REGISTRY_PASSWORD"),
		Token:    os.Getenv("PACTO_REGISTRY_TOKEN"),
	})
	store := oci.NewCachedStore(oci.NewClient(keychain))

	svc := app.NewService(store, &plugin.SubprocessRunner{})
	root := cli.NewRootCommand(svc, version)

	return root.Execute()
}
