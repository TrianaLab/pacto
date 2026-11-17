package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// newExportCommand builds `pacto export`, the parent of one subcommand per
// format another tool reads.
func newExportCommand(svc *app.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write what Pacto knows in another tool's format",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newExportBackstageCommand(svc))
	return cmd
}

// newExportBackstageCommand builds `pacto export backstage`: the fleet, read
// from the shared source flags, as one file of Backstage catalog entities.
func newExportBackstageCommand(svc *app.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backstage",
		Short: "Write the services Pacto knows as Backstage catalog entities",
		Long: "Write the services Pacto knows as one multi-document YAML file of Backstage catalog entities: " +
			"a Component per service and a System per business domain. Every entity says how Pacto knows it " +
			"(pacto.trianalab.io/provenance), when (as-of) and how completely (completeness); an inferred value " +
			"never fills spec.owner or spec.system.\n\n" +
			"The export reads the same sources as pacto fleet, except that it scans no local directory unless " +
			"--local is set. When a source could not be read it writes nothing and exits 2, so a scheduled sync " +
			"never drops the services that source holds; --allow-partial writes the file anyway.",
		Example: "  pacto export backstage --oci ghcr.io/acme/payments --oci ghcr.io/acme/ledger > catalog/pacto-entities.yaml\n" +
			"  pacto export backstage --root oci://ghcr.io/acme/platform --k8s -o catalog/pacto-entities.yaml",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runExportBackstage(cmd, svc) },
	}
	// Declared before the shared flags so it wins: the shared default scans the
	// working directory, which would publish drafts and fixtures as services.
	cmd.Flags().StringArray("local", nil, "local bundle root(s) to scan (repeatable; none unless set)")
	addFleetSourceFlags(cmd.Flags())
	cmd.Flags().StringP("output", "o", "", "write the entities to this file instead of standard output")
	cmd.Flags().Bool("allow-partial", false, "write the file even when a source could not be read")
	return cmd
}

// runExportBackstage builds the snapshot, refuses a partial or empty one unless
// --allow-partial is set, and writes the entities.
func runExportBackstage(cmd *cobra.Command, svc *app.Service) error {
	noteK8sScope(cmd)
	q, err := buildQuery(cmd, svc)
	if err != nil {
		return err
	}
	meta := q.ProductMeta()
	warnProductPartial(cmd, meta)
	if allow, _ := cmd.Flags().GetBool("allow-partial"); meta.Completeness != fleet.CompletenessComplete && !allow {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "error: the fleet is "+string(meta.Completeness)+
			", so no file was written; pass --allow-partial to write it anyway")
		return &ExitError{Code: 2}
	}
	path, _ := cmd.Flags().GetString("output")
	var buf bytes.Buffer
	w := cmd.OutOrStdout()
	if path != "" {
		w = &buf
	}
	if err := backstage.Encode(w, backstage.Map(q)); err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// noteK8sScope says, with --k8s, which namespaces the export reads: everyone
// who reads the file sees what the exporter's credentials could.
func noteK8sScope(cmd *cobra.Command) {
	if k8s, _ := cmd.Flags().GetBool("k8s"); !k8s {
		return
	}
	scope := "all namespaces"
	if ns := flagNamespaces(cmd); len(ns) > 0 {
		scope = "namespaces " + strings.Join(ns, ", ")
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"note: reading Pacto CRs from %s with your Kubernetes credentials; everyone who reads the file sees what they hold\n", scope)
}
