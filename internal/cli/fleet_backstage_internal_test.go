package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

// TestWarnPartialNotesAnUnreadCatalog: a complete answer with a Backstage
// catalog that was not fully read says so, so a missing inferred owner never
// reads as "Backstage has none"; an available catalog and other advisory
// sources print nothing.
func TestWarnPartialNotesAnUnreadCatalog(t *testing.T) {
	var out strings.Builder
	cmd := &cobra.Command{Use: "x"}
	cmd.SetErr(&out)
	warnPartial(cmd, fleet.Meta{Completeness: fleet.CompletenessComplete,
		Sources: []fleet.SourceState{
			{ID: "backstage-catalog", Kind: backstage.SourceKind, Status: fleet.SourceUnavailable, Error: &fleet.SourceError{Message: "the catalog refused the request; set a reader token"}},
			{ID: "capped", Kind: backstage.SourceKind, Status: fleet.SourcePartial},
			{ID: "ok", Kind: backstage.SourceKind, Status: fleet.SourceAvailable},
			{ID: "detection", Kind: "detection", Status: fleet.SourceUnavailable, Advisory: true},
		},
		Limitations: []fleet.Limitation{{Code: fleet.LimitationSourcePartial, Source: "capped", Message: "the read stopped at 5000 components or 10 pages"}},
	})
	want := "note: source backstage-catalog is unavailable: the catalog refused the request; set a reader token; an inferred owner, domain or dependency may be missing\n" +
		"note: source capped is partial: the read stopped at 5000 components or 10 pages; an inferred owner, domain or dependency may be missing\n"
	if out.String() != want {
		t.Errorf("stderr =\n%s\nwant\n%s", out.String(), want)
	}
}
