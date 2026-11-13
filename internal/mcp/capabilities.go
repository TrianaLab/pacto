package mcp

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const capabilitiesInstructions = "Call pacto_capabilities to learn which modes this server serves, " +
	"whether delivery is enabled for you and whether writes are exposed."

// capabilitiesAnswer is what pacto_capabilities returns. fleet, impact,
// observed and delivery mean what they mean on the dashboard's
// /api/capabilities.
type capabilitiesAnswer struct {
	Modes     []string           `json:"modes"`
	Authoring []string           `json:"authoring"`
	Fleet     bool               `json:"fleet"`
	Impact    bool               `json:"impact"`
	Observed  bool               `json:"observed"`
	Delivery  bool               `json:"delivery"`
	Caller    capabilitiesCaller `json:"caller"`
	Writes    bool               `json:"writes"`
	Transport string             `json:"transport"`
	AsOf      time.Time          `json:"asOf"`
}

// capabilitiesCaller says who calls and how Pacto knows: user is observed
// (the apiserver reviewed it) only with callerSource reviewed. class is
// inferred from the identity policy, from its defaults when policyError says
// the policy could not be read; both are present only with delivery.
type capabilitiesCaller struct {
	User         string               `json:"user"`
	CallerSource string               `json:"callerSource"`
	Class        *delivery.ActorClass `json:"class,omitempty"`
	PolicyError  *string              `json:"policyError,omitempty"`
	Writes       bool                 `json:"writes"`
}

// registerCapabilitiesTool adds pacto_capabilities, which reports what this
// server serves to the caller.
func registerCapabilitiesTool(server *mcpsdk.Server, a Authoring, m Modes) {
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_capabilities",
		Annotations: readOnly(false),
		Description: "Report what this server serves: its modes (fleet, delivery, bundle), the contract tools it offers, " +
			"whether fleet, impact, observed edges and delivery are available, who you are to it (caller.user with callerSource " +
			"reviewed, token or local; with delivery your class and whether the identity policy was read), whether writes are " +
			"exposed (delivery propose and cancel for you, or a bundle's mutating operations) and the transport. Read-only.",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, _ map[string]any) (*mcpsdk.CallToolResult, any, error) {
		out, err := capabilities(ctx, req, a, m)
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(out)
		return r, nil, err
	})
}

func capabilities(ctx context.Context, req *mcpsdk.CallToolRequest, a Authoring, m Modes) (capabilitiesAnswer, error) {
	out := capabilitiesAnswer{Modes: []string{}, Authoring: authoringNames(a), Transport: m.Transport, AsOf: time.Now().UTC()}
	out.Caller.User, out.Caller.CallerSource = callerOf(ctx, req)
	if out.Caller.CallerSource == "token" {
		// A token subject is not a name the apiserver confirmed.
		out.Caller.User = "unknown"
	}
	if f := m.Fleet; f != nil {
		q, err := viewQuery(ctx, req, f.Src, f.Opts.View)
		if err != nil {
			return out, err
		}
		out.Modes = append(out.Modes, "fleet")
		out.Fleet, out.Impact, out.Observed = true, f.Opts.Impact != nil, q.HasObserved()
	}
	if d := m.Delivery; d != nil {
		out.Modes = append(out.Modes, "delivery")
		out.Delivery, out.Caller.Writes = true, d.Writes
		out.Caller.Class, out.Caller.PolicyError = &d.Caller.Class, &d.Caller.PolicyErr
	}
	if b := m.Bundle; b != nil {
		out.Modes = append(out.Modes, "bundle")
		out.Writes = b.Opts.AllowWrites
	}
	out.Writes = out.Writes || out.Caller.Writes
	return out, nil
}

// authoringNames are the contract tools registerTools adds for a.
func authoringNames(a Authoring) []string {
	if a.Remote {
		return []string{"pacto_schema"}
	}
	return []string{"pacto_create", "pacto_edit", "pacto_check", "pacto_schema"}
}
