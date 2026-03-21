package cli

import (
	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/pkg/override"
)

// optionalArg returns args[0] if present, otherwise "".
func optionalArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// addOverrideFlags registers --values/-f and --set flags on the given command.
func addOverrideFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayP("values", "f", nil, "values file to merge into the contract (can be repeated; last wins)")
	cmd.Flags().StringArray("set", nil, "set a contract value (e.g. --set service.version=2.0.0)")
}

// addOverrideFlagsNoShorthand registers --values and --set without the -f shorthand.
// Use this when -f is reserved for another flag (e.g. --force).
func addOverrideFlagsNoShorthand(cmd *cobra.Command) {
	cmd.Flags().StringArray("values", nil, "values file to merge into the contract (can be repeated; last wins)")
	cmd.Flags().StringArray("set", nil, "set a contract value (e.g. --set service.version=2.0.0)")
}

// getOverrides extracts override settings from the command flags.
func getOverrides(cmd *cobra.Command) override.Overrides {
	valueFiles, _ := cmd.Flags().GetStringArray("values")
	setValues, _ := cmd.Flags().GetStringArray("set")
	return override.Overrides{
		ValueFiles: valueFiles,
		SetValues:  setValues,
	}
}

// addDiffOverrideFlags registers old/new-specific override flags for the diff command.
func addDiffOverrideFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("old-values", nil, "values file to merge into the old contract (can be repeated)")
	cmd.Flags().StringArray("old-set", nil, "set a value on the old contract (e.g. --old-set service.version=1.0.0)")
	cmd.Flags().StringArray("new-values", nil, "values file to merge into the new contract (can be repeated)")
	cmd.Flags().StringArray("new-set", nil, "set a value on the new contract (e.g. --new-set service.version=2.0.0)")
}

// getDiffOverrides extracts old and new override settings from the diff command flags.
func getDiffOverrides(cmd *cobra.Command) (old, new override.Overrides) {
	oldValues, _ := cmd.Flags().GetStringArray("old-values")
	oldSet, _ := cmd.Flags().GetStringArray("old-set")
	newValues, _ := cmd.Flags().GetStringArray("new-values")
	newSet, _ := cmd.Flags().GetStringArray("new-set")
	return override.Overrides{
			ValueFiles: oldValues,
			SetValues:  oldSet,
		}, override.Overrides{
			ValueFiles: newValues,
			SetValues:  newSet,
		}
}
