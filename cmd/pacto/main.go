package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/cli"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/plugin"
)

// splitCSV splits a comma-separated env value into trimmed, non-empty entries.
func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

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

	keychain := oci.NewKeychain(oci.CredentialOptions{
		Username: os.Getenv("PACTO_REGISTRY_USERNAME"),
		Password: os.Getenv("PACTO_REGISTRY_PASSWORD"),
		Token:    os.Getenv("PACTO_REGISTRY_TOKEN"),
	})
	// PACTO_INSECURE_REGISTRIES marks specific registry hosts as plain-HTTP
	// (comma-separated), for a controlled in-cluster registry such as the
	// evidence-server E2E; https hosts are unaffected.
	var clientOpts []oci.ClientOption
	if v := os.Getenv("PACTO_INSECURE_REGISTRIES"); v != "" {
		clientOpts = append(clientOpts, oci.WithInsecureRegistries(splitCSV(v)...))
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
