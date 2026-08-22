package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/cli"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/plugin"
)

// Build-time variables set via ldflags.
var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// signalContext returns a context cancelled on SIGINT/SIGTERM so a Ctrl+C aborts
// in-flight work (OCI pulls, plugin runs) instead of being ignored until completion.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func run() error {
	ctx, stop := signalContext()
	defer stop()

	keychain := oci.NewKeychain(oci.EnvCredentialOptions())
	var clientOpts []oci.ClientOption
	if hosts := oci.EnvInsecureRegistries(); len(hosts) > 0 {
		clientOpts = append(clientOpts, oci.WithInsecureRegistries(hosts...))
	}
	store := oci.NewCachedStore(oci.NewClient(keychain, clientOpts...))

	svc := app.NewService(store, &plugin.SubprocessRunner{})
	app.SetBuildVersion(version)
	root := cli.NewRootCommand(svc, cli.VersionInfo{
		Version:   version,
		GitCommit: gitCommit,
		BuildDate: buildDate,
	})

	return root.ExecuteContext(ctx)
}
