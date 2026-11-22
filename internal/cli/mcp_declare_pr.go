package cli

import (
	"context"

	"github.com/trianalab/pacto/v4/internal/app"
)

// The stdio pacto_declare's pull request: the one place the MCP server opens
// one, in the person's own process with the Git token in its environment,
// through the CLI's exportPR. The HTTP declarer never reaches this file.

// planPR fills p's pull request: the Git host and the stdio process's token
// for it, with no request to the host.
func (m mcpDeclarer) planPR(ctx context.Context, req app.DeclareRequest, p *app.DeclarePlan) error {
	return (&exportPR{d: m.git, req: req}).resolve(ctx, p)
}

// declarePR writes req's draft, then opens its pull request. A failed pull
// request keeps the draft and comes back as the result's PullRequestError.
func (m mcpDeclarer) declarePR(ctx context.Context, req app.DeclareRequest) (*app.DeclareResult, error) {
	x := &exportPR{d: m.git, req: req}
	req.OnPlan = func(p *app.DeclarePlan) error { return x.resolve(ctx, p) }
	res, err := m.svc.Declare(ctx, req)
	if err != nil {
		return res, err
	}
	if err := x.export(ctx, res); res.PullRequestError == nil {
		return res, err // nil, or an unchanged plan's refusal before any host request
	}
	return res, nil
}
