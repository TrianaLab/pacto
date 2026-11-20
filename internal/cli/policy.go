package cli

import (
	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

// addPolicyFlag adds the repeatable --policy flag of pacto impact and pacto ci.
func addPolicyFlag(cmd *cobra.Command) {
	cmd.Flags().StringArray("policy", nil, "range policy for a BREAKING change, repeatable: unknown-range=warn|fail and uncontracted=warn|fail:<window> (such as fail:7d); default warn")
}

// parsePolicy reads the --policy values.
func parsePolicy(cmd *cobra.Command) (impact.Policy, error) {
	values, _ := cmd.Flags().GetStringArray("policy")
	return impact.ParsePolicy(values)
}
