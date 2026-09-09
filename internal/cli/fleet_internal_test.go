package cli

import (
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
