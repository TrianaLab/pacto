package cli

import (
	"bytes"
	"context"
	"slices"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
)

// writeServerDraft writes the draft a server (the dashboard, the HTTP MCP
// server; via names it in the audit line) saves for who with an
// installation credential: unsigned, on who's behalf, through req.Store,
// with one audit line. A bundle repository whose baseline or drafts were
// inferred from another workload is refused. When the newest draft already
// holds the same pacto.yaml, requests and on-behalf-of person, it answers
// that draft and writes nothing, so a repeated click or a looping agent
// cannot fill the repository. p and decl are the plan whose intent digest
// the caller checked.
func writeServerDraft(ctx context.Context, svc *app.Service, req app.DeclareRequest, p *app.DeclarePlan, decl declarationTexts, who, via string) (*dashboard.DeclareAcceptAnswer, error) {
	if refusal := ownedBy(p); refusal != nil {
		return &dashboard.DeclareAcceptAnswer{Refusal: refusal}, nil
	}
	if tag := sameDraft(ctx, req, p, decl, who); tag != "" {
		return &dashboard.DeclareAcceptAnswer{Tag: tag, Draft: true, Command: req.AcceptCLI(p.Service, tag)}, nil
	}
	return runDeclare(ctx, svc, req, via, who)
}

// sameDraft is the newest draft when it is this one again: the same
// pacto.yaml (for a create, every file but the record, since its
// per-environment values sit in the environment files), the same requested
// ranges and the same on-behalf-of person; "" otherwise.
func sameDraft(ctx context.Context, req app.DeclareRequest, p *app.DeclarePlan, decl declarationTexts, who string) string {
	if len(p.Drafts) == 0 {
		return ""
	}
	newest := p.Drafts[0]
	var want []string
	for _, r := range p.Ranges {
		if r.Accepted {
			want = append(want, r.Dependency+" "+r.Compatibility)
		}
	}
	if newest.OnBehalfOf != who || !slices.Equal(slices.Sorted(slices.Values(newest.Requested)), slices.Sorted(slices.Values(want))) {
		return ""
	}
	b, err := req.Store.Pull(ctx, p.BundleRepo+"@"+newest.Digest)
	switch {
	case err != nil:
		return ""
	case req.Create != nil && !app.SameDraftFiles(decl.Files(), b.FS):
		return ""
	case req.Create == nil && !bytes.Equal(bytes.TrimSpace(b.RawYAML), bytes.TrimSpace(decl.Contract())):
		return ""
	}
	return newest.Tag
}
