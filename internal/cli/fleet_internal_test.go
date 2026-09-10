package cli

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
)

func newTestService(t *testing.T) *app.Service {
	t.Helper()
	return app.NewService(nil, nil)
}

func TestFleetOptionsIgnoresUndeclaredFlags(t *testing.T) {
	// A command that declares none of the shared source flags must still get a
	// usable zero-value FleetOptions rather than panicking or hanging.
	cmd := &cobra.Command{Use: "bare"}
	got := fleetOptions(cmd)
	if len(got.LocalRoots) != 0 || got.IncludeK8s || got.FreshnessWindow != 0 {
		t.Fatalf("want zero-value options, got %+v", got)
	}
}

func TestFleetFlagNamesMatchDeclaredFlags(t *testing.T) {
	root := newFleetCommand(newTestService(t), viper.New())
	for _, name := range fleetFlagNames() {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("fleetFlagNames lists %q but newFleetCommand does not declare it", name)
		}
	}
	n := 0
	root.PersistentFlags().VisitAll(func(*pflag.Flag) { n++ })
	if n != len(fleetFlagNames()) {
		t.Errorf("newFleetCommand declares %d persistent flags, fleetFlagNames lists %d", n, len(fleetFlagNames()))
	}
}

// TestFleetOptionsCarriesTheCatalogRoots pins the flag that makes a fleet
// follow declarations instead of stopping at what someone listed. It is the
// same --root `pacto mcp` discovers a catalog from, so a reader who learned it
// there does not have to learn a second spelling here.
func TestFleetOptionsCarriesTheCatalogRoots(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	addFleetSourceFlags(cmd.Flags())
	if err := cmd.Flags().Parse([]string{"--root=./svc", "--root=oci://ghcr.io/acme/platform:1.4.0"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"./svc", "oci://ghcr.io/acme/platform:1.4.0"}
	if got := fleetOptions(cmd).CatalogRoots; !slices.Equal(got, want) {
		t.Errorf("CatalogRoots = %v, want %v", got, want)
	}
	// And it survives the round trip back to argv, so the command the TUI hands
	// a reader resolves the same closure the screen is showing.
	wantArgs := []string{"--root=./svc", "--root=oci://ghcr.io/acme/platform:1.4.0"}
	if got := fleetSourceArgs(cmd); !slices.Equal(got, wantArgs) {
		t.Errorf("fleetSourceArgs = %v, want %v", got, wantArgs)
	}
}

// TestImpactDeclaresTheCatalogRoots: blast radius is the answer the closure
// matters most for, because a consumer nobody listed is exactly the one a
// breaking change surprises.
func TestImpactDeclaresTheCatalogRoots(t *testing.T) {
	cmd := newImpactCommand(newTestService(t), viper.New())
	if cmd.Flags().Lookup("root") == nil {
		t.Error("pacto impact does not declare --root")
	}
}
