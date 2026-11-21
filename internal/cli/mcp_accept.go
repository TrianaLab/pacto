package cli

import (
	"context"
	"encoding/json"
	"os"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// mcpAcceptor is pacto_accept over stdio: it reads the registry and writes
// acceptance drafts with this process's registry credentials. Every MCP
// caller is an agent, so a run never signs an acceptance: an ambient CI
// token signs the draft, a person's ambient token is never used and no
// token leaves it unsigned.
type mcpAcceptor struct {
	svc *app.Service
	// trust reads the installation signer policy, as pacto accept and
	// pacto_impact do, so every surface judges a record the same way.
	trust func(context.Context) (app.Installation, error)
}

func (m mcpAcceptor) Plan(ctx context.Context, a pactomcp.AcceptArgs) (json.RawMessage, *pactomcp.Refused, error) {
	opts := m.options(a)
	opts.Intent = ""
	p, err := m.svc.AcceptPlan(ctx, opts)
	return declareAnswer(p, err)
}

func (m mcpAcceptor) Execute(ctx context.Context, a pactomcp.AcceptArgs) (json.RawMessage, *pactomcp.Refused, error) {
	res, err := m.svc.Accept(ctx, m.options(a))
	return declareAnswer(res, err)
}

// options is the agent's pacto accept run for a, signing drafts against
// public-good Sigstore and judging records with the installation policy.
func (m mcpAcceptor) options(a pactomcp.AcceptArgs) app.AcceptOptions {
	opts := app.AcceptOptions{Trust: m.trust, Getenv: os.Getenv}
	acceptSubject(&opts, a.Service)
	opts.Proposed, opts.Fields, opts.Break, opts.From = a.Proposed, a.Fields, a.Break, a.From
	opts.OnBehalfOf, opts.Intent, opts.Agent = a.OnBehalfOf, a.IntentDigest, true
	opts.Signing.FulcioURL, opts.Signing.RekorURL = signing.PublicFulcio, signing.PublicRekor
	return opts
}
