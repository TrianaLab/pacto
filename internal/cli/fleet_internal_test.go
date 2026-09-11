package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

// TestFleetOptionsReadersDeclareEverySharedFlag: fleetOptions reads all ten
// source flags by name off whatever command it is handed, so a command that
// builds a snapshot but forgets one silently drops that whole source — the
// reader gets an answer, just not over the fleet they asked for. --root is the
// sharpest of them (blast radius is the answer a dependency closure matters most
// for, because a consumer nobody listed is exactly the one a breaking change
// surprises), but every one of them is a source.
func TestFleetOptionsReadersDeclareEverySharedFlag(t *testing.T) {
	for _, cmd := range []*cobra.Command{
		newImpactCommand(newTestService(t), viper.New()),
		newMCPCommand(newTestService(t), "test"),
		newTUICommand(newTestService(t), viper.New()),
	} {
		for _, name := range fleetFlagNames() {
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("pacto %s does not declare --%s, so fleetOptions reads nothing for it", cmd.Name(), name)
			}
		}
	}
}

// divergentFleetFlagTypes records every command that declares a shared fleet
// source flag under a different type, keyed by "<command path> --<flag>". Each
// entry is a deliberate narrowing whose value the command reads itself, so
// fleetOptions contributing nothing for it is correct rather than lossy.
var divergentFleetFlagTypes = map[string]string{
	// One trace document, read as bytes and handed to impact.Analyze as ad-hoc
	// observed edges. That path names every observed endpoint it cannot map to a
	// unique fleet service as a limitation; folding the same file in as a
	// repeatable observation source instead would resolve them silently.
	"pacto impact --traces": "string",
	// Reconciliation compares the snapshot's declared edges against exactly one
	// observed trace document, which it reads itself.
	"pacto fleet reconcile --traces": "string",
}

// yankedFleetSubcommands returns the `pacto fleet` subcommands the TUI can put
// in a yanked command line. It reads internal/tui/yank.go rather than importing
// it: the list is a set of literals inside fleetLine calls, and exporting a seam
// that exists only for this assertion would be a worse trade than a regexp.
func yankedFleetSubcommands(t *testing.T) []string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "tui", "yank.go"))
	if err != nil {
		t.Fatalf("read yank.go: %v", err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`fleetLine\("([a-z-]+)"`).FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	return out
}

// TestYankedFleetSubcommandsTakeTheSharedFlagTypes closes the argv round trip.
// The TUI renders SourceArgs from its OWN flag set, where every shared source
// flag is repeatable, then pastes them after `pacto fleet <sub>`. A subcommand
// that narrows one of those flags to a single value parses the repeated form
// without complaint and keeps only the last, so a reader whose session loaded
// two trace files would be handed a line that quietly uses one.
//
// divergentFleetFlagTypes above records every narrowing, and yank.go chooses
// every subcommand. Nothing in the type system says those two lists must not
// intersect, so this does.
func TestYankedFleetSubcommandsTakeTheSharedFlagTypes(t *testing.T) {
	subs := yankedFleetSubcommands(t)
	if len(subs) == 0 {
		t.Fatal("found no fleetLine subcommands in internal/tui/yank.go; the scan is broken, not the TUI")
	}
	for _, sub := range subs {
		for _, flag := range fleetFlagNames() {
			typ, narrowed := divergentFleetFlagTypes["pacto fleet "+sub+" --"+flag]
			if !narrowed {
				continue
			}
			t.Errorf("yank.go yanks `pacto fleet %s`, which narrows --%s to %s.\n"+
				"SourceArgs renders --%s repeatably, so the pasted line keeps only its last value.\n"+
				"Either drop the subcommand from yank.go or stop narrowing the flag there.", sub, flag, typ, flag)
		}
	}
}

// TestSharedFleetFlagTypesAreConsistent walks the command tree and fails any
// command declaring a shared fleet source flag under a type addFleetSourceFlags
// does not use. Nothing else catches this: the flag parses, the user sees it
// accepted, and fleetOptions' typed lookup then returns the zero value with an
// error it discards — so the value simply never reaches the snapshot. The two
// commands that diverge on purpose carry their reason in
// divergentFleetFlagTypes.
func TestSharedFleetFlagTypesAreConsistent(t *testing.T) {
	shared := pflag.NewFlagSet("shared", pflag.ContinueOnError)
	addFleetSourceFlags(shared)

	seen := map[string]bool{}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			want := shared.Lookup(f.Name)
			if want == nil || f.Value.Type() == want.Value.Type() {
				return
			}
			key := cmd.CommandPath() + " --" + f.Name
			seen[key] = true
			if divergentFleetFlagTypes[key] != f.Value.Type() {
				t.Errorf("%s is declared %s where the shared fleet flag is %s; fleetOptions reads the zero value for it",
					key, f.Value.Type(), want.Value.Type())
			}
		})
		for _, c := range cmd.Commands() {
			walk(c)
		}
	}
	walk(NewRootCommand(newTestService(t), VersionInfo{Version: "test"}))

	// A stale exception is as misleading as a missing one: it documents a
	// divergence that no longer exists and would silently bless the next.
	for key := range divergentFleetFlagTypes {
		if !seen[key] {
			t.Errorf("divergentFleetFlagTypes lists %q, which no longer diverges", key)
		}
	}
}
