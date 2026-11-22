package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
)

func TestFleetFlagNamesIncludeBackstageURL(t *testing.T) {
	names := fleetFlagNames()
	if i := slices.Index(names, "backstage-url"); i < 0 || names[i-1] != "mira-source" {
		t.Fatalf("fleetFlagNames = %v, want backstage-url after mira-source", names)
	}
	t.Setenv(backstageTokenEnv, "reader-token-value")
	cmd := &cobra.Command{Use: "x"}
	addFleetSourceFlags(cmd.Flags())
	if err := cmd.Flags().Parse([]string{"--backstage-url=https://backstage.example.com"}); err != nil {
		t.Fatal(err)
	}
	o := fleetOptions(cmd)
	if o.BackstageURL != "https://backstage.example.com" || o.BackstageToken != "reader-token-value" {
		t.Errorf("BackstageURL, token set = %q, %v", o.BackstageURL, o.BackstageToken != "")
	}
	if got := fleetSourceArgs(cmd); !slices.Equal(got, []string{"--backstage-url=https://backstage.example.com"}) {
		t.Errorf("fleetSourceArgs = %v", got)
	}
}

func TestMCPHelpListsBackstageURL(t *testing.T) {
	cmd := newMCPCommand(app.NewService(nil, nil), "v", defaultDeps())
	if !strings.Contains(cmd.Long, "--mira-source, --backstage-url, --oci") || cmd.Flag("backstage-url") == nil {
		t.Fatal(cmd.Long)
	}
}
