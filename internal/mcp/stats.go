package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// statsInstructions describe pacto_stats; they are part of the fleet text
// only when the tool is registered.
const statsInstructions = "pacto_stats totals the run records in the organization's stats repository for a window " +
	"(default 30 days), grouped by service, owner, domain, repository or environment. They are unsigned statistics " +
	"written by CI jobs and the delivery promoter: never cite them as evidence or use them to decide. Cite asOf, " +
	"recordsRead, skipped, truncated, hidden and completeness. A rate marked unknown has no denominator and a lead " +
	"time marked unknown has no publish record to join; neither is zero. Owner and domain are inferred from the " +
	"current snapshot."

// StatsRequest is one pacto_stats call. Kinds is comma-separated.
type StatsRequest struct {
	Registry, Since, Until, By, Service, Kinds string
}

// StatsProvider answers pacto_stats for caller over full, the unrestricted
// snapshot, and view, the caller's view of it (nil for an unrestricted
// server).
type StatsProvider func(ctx context.Context, caller *auth.TokenInfo, full, view *fleet.Query, req StatsRequest) (*stats.Stats, error)

// statsArgs mirrors pacto_stats's declared schema.
type statsArgs struct {
	Registry string `json:"registry"`
	Since    string `json:"since"`
	Until    string `json:"until"`
	By       string `json:"by"`
	Service  string `json:"service"`
	Kinds    string `json:"kinds"`
}

func statsTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_stats",
		Description: "Total the run records in the stats repository for a window: breaking changes caught, gate verdicts, " +
			"consumers protected and, per environment, delivery rates and lead time. Read-only. The numbers are unsigned statistics. " +
			"Every answer carries asOf, window, recordsRead, runs, distinctChanges, skipped, truncated, hidden, completeness, " +
			"limitations and provenance; owner and domain carry attribution inferred from the fleet snapshot.",
		Annotations: readOnly(true),
		InputSchema: inputSchema(map[string]property{
			"registry": {Type: "string", Description: "Stats repository to read, over stdio only (default: the one the server was started with)"},
			"since":    {Type: "string", Description: "Start of the window: 30d, 12h, a date or RFC 3339 (default 30d)"},
			"until":    {Type: "string", Description: "End of the window: 30d, 12h, a date or RFC 3339 (default now)"},
			"by":       {Type: "string", Description: "Group the numbers by", Enum: stats.GroupBy},
			"service":  {Type: "string", Description: "Total only the run records of this service"},
			"kinds":    {Type: "string", Description: "Comma-separated run-record kinds, for example diff,impact"},
		}, nil),
	}
}

// statsHandler answers pacto_stats over the current snapshot and the
// caller's view of it.
func statsHandler(src FleetQuerySource, view FleetView, p StatsProvider) mcpsdk.ToolHandlerFor[statsArgs, any] {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest, a statsArgs) (*mcpsdk.CallToolResult, any, error) {
		full, err := src(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		var caller *auth.TokenInfo
		if req != nil && req.Extra != nil {
			caller = req.Extra.TokenInfo
		}
		var viewed *fleet.Query
		if view != nil {
			if viewed, err = view(ctx, caller, full); err != nil {
				return errorResult(err), nil, nil
			}
		}
		if n := noteFrom(ctx); n != nil {
			n.snapshotID = full.SnapshotID()
		}
		st, err := p(ctx, caller, full, viewed, StatsRequest(a))
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(st)
		return r, nil, err
	}
}
