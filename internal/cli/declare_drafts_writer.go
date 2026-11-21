package cli

import (
	"bytes"
	"context"
	"slices"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
)

// writeServerDraft writes the draft a server (the dashboard, the HTTP MCP
// server) saves for who with an installation credential: unsigned, on who's
// behalf, through req.Store, with one audit line. When the newest draft
// already holds the same pacto.yaml, requests and on-behalf-of person, it
// answers that draft and writes nothing, so a repeated click or a looping
// agent cannot fill the repository. p and decl are the plan the caller
// checked (intent digest and repository owner) before.
func writeServerDraft(ctx context.Context, svc *app.Service, req app.DeclareRequest, p *app.DeclarePlan, decl declarationTexts, who string) (*dashboard.DeclareAcceptAnswer, error) {
	if tag := sameDraft(ctx, req, p, decl.Contract(), who); tag != "" {
		return &dashboard.DeclareAcceptAnswer{Tag: tag, Draft: true, Command: acceptDraftCommand(req, tag)}, nil
	}
	return runDeclare(ctx, svc, req, "server draft", who)
}

// acceptDraftCommand is the command a person accepts tag with.
func acceptDraftCommand(req app.DeclareRequest, tag string) string {
	return "pacto declare " + req.Service + " --registry " + req.Registry + " --from-draft " + tag
}

// sameDraft is the newest draft when it is this one again: the same
// pacto.yaml, the same requested ranges and the same on-behalf-of person;
// "" otherwise.
func sameDraft(ctx context.Context, req app.DeclareRequest, p *app.DeclarePlan, raw []byte, who string) string {
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
	if err != nil || !bytes.Equal(bytes.TrimSpace(b.RawYAML), bytes.TrimSpace(raw)) {
		return ""
	}
	return newest.Tag
}
