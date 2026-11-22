package mcp

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
	// Installation is the one answer GET /api/capabilities gives too.
	Installation fleet.Installation `json:"installation"`
}

// capabilitiesCaller says who calls and how Pacto knows: user is observed
// (the apiserver reviewed it) only with callerSource reviewed. class is
// inferred from the identity policy, from its defaults when policyError says
// the policy could not be read; both are present only with delivery, with
// classAsOf, when they were read (at startup over stdio, where the write
// tools also follow that class; pacto_delivery_caller reads it live).
type capabilitiesCaller struct {
	User         string               `json:"user"`
	CallerSource string               `json:"callerSource"`
	Class        *delivery.ActorClass `json:"class,omitempty"`
	PolicyError  *string              `json:"policyError,omitempty"`
	ClassAsOf    *time.Time           `json:"classAsOf,omitempty"`
	Writes       bool                 `json:"writes"`
	// FleetOnly is a caller whose token reads the fleet only; Hint says how
	// to get a token that also reaches the cluster.
	FleetOnly bool   `json:"fleetOnly,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// registerCapabilitiesTool adds pacto_capabilities, which reports what this
// server serves to the caller.
func registerCapabilitiesTool(server *mcpsdk.Server, a Authoring, m Modes) {
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_capabilities",
		Annotations: readOnly(false),
		Description: "Report what this server serves: its modes (fleet, delivery, bundle or publish), the contract tools it offers, " +
			"whether fleet, impact, observed edges and delivery are available, who you are to it (caller.user with callerSource " +
			"reviewed, token or local; with delivery your class, whether the identity policy was read and classAsOf, when the class was read; " +
			"fleetOnly, with a hint, when your token reads the fleet only), whether writes are " +
			"exposed (delivery propose and cancel for you, a bundle's mutating operations, pacto_publish or declaration drafts), the transport " +
			"and how the installation runs (profile, registry, delivery and its appliers, provisioning engine, clusters and MCP, each with source and asOf). " +
			"A null installation value is unknown, never a no. Read-only.",
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
	out.Caller.FleetOnly, out.Caller.Hint = m.FleetOnlyHint != "", m.FleetOnlyHint
	if out.Caller.CallerSource == "token" {
		// A token subject is not a name the apiserver confirmed.
		out.Caller.User = "unknown"
	}
	in := fleet.InstallationInput{MCPServed: true, MCPSource: "this MCP server, over " + m.Transport, DeliveryHidden: m.DeliveryHidden, Now: time.Now().UTC()}
	var q *fleet.Query
	if f := m.Fleet; f != nil {
		var err error
		if q, err = viewQuery(ctx, req, f.Src, f.Opts.View); err != nil {
			return out, err
		}
		out.Modes = append(out.Modes, "fleet")
		out.Fleet, out.Impact, out.Observed = true, f.Opts.Impact != nil, q.HasObserved()
		if cfg := f.Opts.InstallationConfig; cfg != nil {
			d, st := cfg(ctx)
			in.Declared, in.DeclaredState = d, &st
		}
	}
	if d := m.Delivery; d != nil {
		out.Modes = append(out.Modes, "delivery")
		out.Delivery, out.Caller.Writes = true, d.Writes
		in.DeliveryWired, in.DeliveryProbed = true, d.Installed != nil
		if d.Installed != nil {
			// As the dashboard: a probe that fails is not proof of absence.
			// installation keeps the raw answer.
			in.DeliveryInstalled, in.DeliveryErr = d.Installed(ctx)
			if d.InstalledAt != nil {
				in.DeliveryAsOf = d.InstalledAt().UTC()
			}
			out.Delivery = in.DeliveryInstalled || in.DeliveryErr != nil
		}
		at := d.CallerAt.UTC()
		out.Caller.Class, out.Caller.PolicyError, out.Caller.ClassAsOf = &d.Caller.Class, &d.Caller.PolicyErr, &at
	}
	if b := m.Bundle; b != nil {
		out.Modes = append(out.Modes, "bundle")
		out.Writes = b.Opts.AllowWrites
	}
	if m.publish {
		out.Modes = append(out.Modes, "publish")
		out.Writes = true // pacto_publish pushes to the registry
	}
	// pacto_declare and pacto_accept write drafts over stdio only.
	out.Writes = out.Writes || out.Caller.Writes || ((a.Declare != nil || a.Accept != nil) && !a.Remote)
	out.Installation = q.Installation(in)
	return out, nil
}

// authoringNames are the contract tools registerTools adds for a.
func authoringNames(a Authoring) []string {
	names := []string{"pacto_create", "pacto_edit", "pacto_check", "pacto_schema"}
	if a.Remote {
		names = names[3:]
	}
	if a.Declare != nil {
		names = append(names, "pacto_declare")
	}
	if a.Accept != nil && !a.Remote {
		names = append(names, "pacto_accept")
	}
	return names
}
