package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/publish"
)

// PublishArgs are the publish tools' arguments: the contract directory, the
// revision to stamp and, for a contract with no publish section, how the one
// Pacto derives is shaped (the flags of the same name on pacto publish).
type PublishArgs struct {
	Path              string `json:"path"`
	Revision          string `json:"revision"`
	Image             string `json:"image"`
	Registry          string `json:"registry"`
	SignKey           string `json:"sign_key"`
	NoTransparencyLog bool   `json:"no_transparency_log"`
}

// PublishFunc runs `pacto publish` with a; plan decides and writes nothing.
// The command layer adapts app.Service.Publish to it.
type PublishFunc func(ctx context.Context, a PublishArgs, plan bool) (*publish.Result, error)

const publishInstructions = "pacto_publish_plan shows what publishing a contract's version would write (each artifact " +
	"absent, identical, adopt, conflict or unreadable) and which of Pacto's checks refuse it, such as a breaking change " +
	"published as a minor. pacto_publish publishes it the same way `pacto publish` does in CI: never over an existing " +
	"tag, signed, the deploy artifact last. Running it again changes nothing."

// NewPublishServer builds a server with the authoring tools, the two publish
// tools and pacto_capabilities. It serves stdio only.
func NewPublishServer(version string, run PublishFunc, a Authoring) *mcpsdk.Server {
	m := Modes{Transport: "stdio", publish: true}
	server := newServer(version, baseFor(a)+"\n\n"+publishInstructions+"\n\n"+capabilitiesInstructions, a, editOptions(m))
	registerCapabilitiesTool(server, a, m)
	schema := inputSchema(map[string]property{
		"path":                {Type: "string", Description: "Contract directory (default: the server's working directory)"},
		"revision":            {Type: "string", Description: "Source revision to stamp (default: the git commit of path, whose checkout must have no uncommitted changes)"},
		"image":               {Type: "string", Description: "With no publish section: the image to deploy, pinned by digest (repo@sha256:...); Pacto builds, signs and copies none"},
		"registry":            {Type: "string", Description: "With no publish section: the registry to publish to (default: what the workload image names)"},
		"sign_key":            {Type: "string", Description: "With no publish section: sign with this cosign key file instead of keyless"},
		"no_transparency_log": {Type: "boolean", Description: "With sign_key: keep the signatures out of the transparency log"},
	}, nil)
	type args = PublishArgs
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_publish_plan",
		Annotations: readOnly(true),
		Description: "Plan publishing the contract's version: what is missing, what conflicts and which checks refuse it. Writes nothing.",
		InputSchema: schema,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a args) (*mcpsdk.CallToolResult, any, error) {
		res, err := run(ctx, a, true)
		if err != nil {
			return resultOrError(nil, err)
		}
		// The plan with the notes pacto publish --plan prints (a Secret
		// left out, not reproducible, ...), so an agent sees them too.
		return resultOrError(struct {
			publish.Plan
			Notes []string `json:"notes,omitempty"`
		}{res.Plan, res.Notes}, nil)
	})
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_publish",
		Annotations: writes(false, true, true),
		Description: "Publish the contract's version: build, push by digest, sign, attach SBOMs and tag, the deploy " +
			"artifact last. Never overwrites a tag; a second call publishes nothing new.",
		InputSchema: schema,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a args) (*mcpsdk.CallToolResult, any, error) {
		res, err := run(ctx, a, false)
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
