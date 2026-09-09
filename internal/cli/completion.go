package cli

import "github.com/spf13/cobra"

// staticCompletions adapts a fixed vocabulary to cobra's completion signature.
// NoFileComp matters: without it a closed set silently falls back to offering
// filenames, which is worse than offering nothing.
//
// Every caller passes a vocabulary the code already owns — the same slice or
// constants the validator accepts from — so completion can never advertise a
// value the command would reject.
func staticCompletions(values ...string) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// noCompletions is the empty vocabulary: no candidates, and no filename
// fallback either. It is for a positional whose values only a built snapshot
// knows — offering nothing is honest, offering filenames is not.
var noCompletions = staticCompletions()
