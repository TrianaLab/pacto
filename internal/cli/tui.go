package cli

import (
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/tui"
	"github.com/trianalab/pacto/v3/pkg/logging"
)

// osExecutable is a seam so the TTY-less test can exercise the failure path.
var osExecutable = os.Executable

// tuiRun is a seam so the test can check the flags reach tui.Options without
// starting a full-screen program.
var tuiRun = tui.Run

// newTUICommand builds `pacto tui`: the full-screen terminal front-end. It
// takes the same fleet source flags as `pacto fleet`, because the snapshot it
// navigates is the same snapshot.
func newTUICommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse the fleet and run pacto commands in a full-screen terminal UI",
		Long: "Opens a full-screen terminal UI over the same fleet snapshot `pacto fleet` " +
			"queries. Navigate services, revisions, targets, owners and sources, then run " +
			"pacto's verbs against whatever is selected — the selection becomes the " +
			"argument, so you never type a path.\n\n" +
			"Read verbs run in-process against the loaded snapshot. Write verbs shell out " +
			"to this same binary so they own the terminal, and every one of them asks for " +
			"confirmation first. Pass --read-only to hide the write verbs entirely.\n\n" +
			"Requires an interactive terminal. In a pipeline or CI, use the plain commands.",
		Example: `  # Browse the local fleet
  pacto tui --local .

  # Browse a live cluster plus the local bundles, without write verbs
  pacto tui --local . --k8s --read-only`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTerminal(cmd.OutOrStdout()) {
				return errors.New("pacto tui needs an interactive terminal; use the plain commands in a pipeline")
			}
			exe, err := osExecutable()
			if err != nil {
				return err
			}
			readOnly, _ := cmd.Flags().GetBool("read-only")
			// Send the logs nowhere. root.go's PersistentPreRunE points the
			// logger at stderr, which here is the terminal the alt screen owns,
			// so a warning from a snapshot build or an in-process read verb
			// writes raw text over the frame with nothing to repaint it. At -v
			// the whole of pkg/oci logs on every reload. Discarding is the right
			// trade for a screen: the Long text above already sends anyone who
			// wants the detail to the plain commands, which log normally.
			//
			// Not verbose, whatever the flag says: nothing reads these records,
			// so formatting them at Debug is work for a writer that drops them.
			ctx := logging.WithLogger(cmd.Context(), logging.New(io.Discard, false))
			return tuiRun(ctx, tui.Options{
				Svc:   svc,
				Fleet: fleetOptions(cmd),
				// The yanked line has to resolve the snapshot on screen, not one
				// rebuilt from the defaults, so it carries the source flags verbatim.
				SourceArgs: fleetSourceArgs(cmd),
				ReadOnly:   readOnly,
				Exe:        exe,
				// The same switch the CLI spinner obeys, so --no-anim,
				// PACTO_NO_ANIM and NO_COLOR turn the motion off here too rather
				// than needing a second flag nobody would find.
				Anim: animate(cmd, cmd.OutOrStdout()),
			})
		},
	}
	addFleetSourceFlags(cmd.Flags())
	cmd.Flags().Bool("read-only", false, "hide every write verb")
	return cmd
}
