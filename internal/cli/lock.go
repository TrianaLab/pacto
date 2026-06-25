package cli

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v2/internal/app"
)

func newLockCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock [dir]",
		Short: "Resolve and pin dependencies into pacto.lock",
		Long:  "Resolves the full transitive dependency and reference closure and writes a committed pacto.lock pinning each to its OCI digest. With --check, verifies the existing lock without writing.",
		Example: `  pacto lock
  pacto lock --update
  pacto lock --update-name auth
  pacto lock --check`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			update, _ := cmd.Flags().GetBool("update")
			check, _ := cmd.Flags().GetBool("check")
			names, _ := cmd.Flags().GetStringArray("update-name")
			format := v.GetString(outputFormatKey)

			start := time.Now()
			sp, count := startSpinnerCounted(cmd, format, "Resolving lock")
			result, err := svc.Lock(cmd.Context(), app.LockOptions{
				Path:          optionalArg(args),
				Update:        update || len(names) > 0,
				UpdateNames:   names,
				Check:         check,
				Overrides:     getOverrides(cmd),
				OnDepResolved: func() { count.Add(1) },
			})
			if err != nil {
				sp.Stop()
				return err
			}
			sp.StopOK("Locked", start)
			return printLockResult(cmd, result, format)
		},
	}
	cmd.Flags().Bool("update", false, "re-resolve dependencies to the newest version within their constraint")
	cmd.Flags().StringArray("update-name", nil, "only update the named dependency (repeatable; implies --update)")
	cmd.Flags().Bool("check", false, "verify pacto.lock is up to date without writing (non-zero exit on drift)")
	addOverrideFlags(cmd)
	return cmd
}
