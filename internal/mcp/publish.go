package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/publish"
)

// PublishFunc runs `pacto publish` on the contract at path; plan decides and
// writes nothing. The command layer adapts app.Service.Publish to it.
type PublishFunc func(ctx context.Context, path, revision string, plan bool) (*publish.Result, error)

const publishInstructions = "pacto_publish_plan shows what publishing a contract's version would write (each artifact " +
	"absent, identical, adopt, conflict or unreadable) and which of Pacto's checks refuse it, such as a breaking change " +
	"published as a minor. pacto_publish publishes it the same way `pacto publish` does in CI: never over an existing " +
	"tag, signed, the deploy artifact last. Running it again changes nothing."

// NewPublishServer builds a server with the authoring tools and the two
// publish tools.
func NewPublishServer(version string, run PublishFunc, validate Validator) *mcpsdk.Server {
	server := newServer(version, baseInstructions+"\n\n"+publishInstructions, validate)
	schema := inputSchema(map[string]property{
		"path":     {Type: "string", Description: "Contract directory (default: the server's working directory)"},
		"revision": {Type: "string", Description: "Source revision to stamp (default: the git commit of path, whose checkout must have no uncommitted changes)"},
	}, nil)
	type args struct {
		Path     string `json:"path"`
		Revision string `json:"revision"`
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "pacto_publish_plan",
		Description: "Plan publishing the contract's version: what is missing, what conflicts and which checks refuse it. Writes nothing.",
		InputSchema: schema,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a args) (*mcpsdk.CallToolResult, any, error) {
		res, err := run(ctx, a.Path, a.Revision, true)
		if err != nil {
			return resultOrError(nil, err)
		}
		return resultOrError(res.Plan, nil)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_publish",
		Description: "Publish the contract's version: build, push by digest, sign, attach SBOMs and tag, the deploy " +
			"artifact last. Never overwrites a tag; a second call publishes nothing new.",
		InputSchema: schema,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a args) (*mcpsdk.CallToolResult, any, error) {
		res, err := run(ctx, a.Path, a.Revision, false)
		if err != nil && res != nil {
			r, jerr := jsonResult(struct {
				Error  string          `json:"error"`
				Result *publish.Result `json:"result"`
			}{err.Error(), res})
			r.IsError = true
			return r, nil, jerr
		}
		return resultOrError(res, err)
	})
	return server
}
