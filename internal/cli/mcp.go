package cli

import (
	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/internal/app"
	pactomcp "github.com/trianalab/pacto/internal/mcp"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newMCPCommand(svc *app.Service, version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start an MCP server over stdio",
		Long:  "Starts a Model Context Protocol (MCP) server communicating over stdin/stdout, exposing Pacto tools for AI agents.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			server := pactomcp.NewServer(svc, version)
			return server.Run(cmd.Context(), &mcpsdk.StdioTransport{})
		},
	}
	return cmd
}
