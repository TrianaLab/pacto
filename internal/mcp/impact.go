package mcp

import (
	"context"
	"errors"
	"slices"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// ImpactRequest is one pacto_impact call: a contract change from OldRef to
// NewRef, or, when Event is set, a what-if about one fleet service.
type ImpactRequest struct {
	OldRef, NewRef  string
	IncludeObserved bool
	TracesPath      string
	Event           *impact.Event
}

// impactProvider answers req over snap, the snapshot the fleet tools answer
// from. It is supplied by the command layer (from app.Service) so this package
// depends only on the pure result type — never on OCI, Kubernetes or
// the app service.
type impactProvider func(ctx context.Context, snap *fleet.FleetSnapshot, req ImpactRequest) (*impact.Result, error)

func impactTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name: "pacto_impact",
		Description: "Analyze which consumers a change affects, from an old to a new contract revision, " +
			"projected onto the operational graph. Reports the semantic classification, breaking " +
			"changes, affected consumers (with confidence and compatibility verdict), active targets " +
			"and owners to review. The answer ends in 'gate' (blocked, unverified or clear) and 'gateReason', " +
			"the same gate `pacto impact` exits on (1, 2, 0): unverified means Pacto could not show the change " +
			"is safe, never that it is. On a BREAKING change a compatible consumer only matches by range; read " +
			"its 'note'. 'consumerTallies' counts every consumer by outcome (incompatible includes a consumer that refuses a " +
			"required guarantee) and ranks their owners; state counts from it rather than by counting the list. When no fleet source holds the changed service, the new revision is added to the snapshot " +
			"('newRevisionAdded'); on a server whose callers sign in it is not, and the answer says " +
			"SERVICE_NOT_IN_FLEET with an unverified gate. Pass 'event' (unavailable, removed or regressed) with 'service' " +
			"(and 'revision' for regressed: an artifact digest reads delivery's recorded failures) instead of old_ref and new_ref " +
			"to ask what happens if that service becomes unavailable, is removed or regressed; consumers carry 'effect' " +
			"(breaks, may-degrade, unknown) and the gate is never blocked for an event. " +
			"Read-only: it lists review targets, never recommends or takes actions.",
		InputSchema: inputSchema(map[string]property{
			"old_ref":          {Type: "string", Description: "Old contract revision (local directory or oci:// ref)"},
			"new_ref":          {Type: "string", Description: "New contract revision (local directory or oci:// ref)"},
			"include_observed": {Type: "boolean", Description: "Let observed (runtime) relationships raise consumer confidence"},
			"traces":           {Type: "string", Description: "Path to an OTLP/JSON trace file; its observed edges corroborate and surface consumers (implies include_observed)"},
			"event":            {Type: "string", Description: "What happens to 'service' instead of a contract change", Enum: []string{impact.EventUnavailable, impact.EventRemoved, impact.EventRegressed}},
			"service":          {Type: "string", Description: "The fleet service key the event is about"},
			"revision":         {Type: "string", Description: "For regressed: the revision that regressed, a version or an artifact digest (sha256:...)"},
		}, nil),
	}
}

type impactArgs struct {
	OldRef          string `json:"old_ref"`
	NewRef          string `json:"new_ref"`
	IncludeObserved bool   `json:"include_observed"`
	Traces          string `json:"traces"`
	Event           string `json:"event"`
	Service         string `json:"service"`
	Revision        string `json:"revision"`
}

// errImpactArgs is the answer to a call that names neither a change nor an event.
var errImpactArgs = errors.New("pass either old_ref and new_ref, or event and service")

// request checks the arguments and turns them into an ImpactRequest.
func (a impactArgs) request() (ImpactRequest, error) {
	req := ImpactRequest{OldRef: a.OldRef, NewRef: a.NewRef, IncludeObserved: a.IncludeObserved, TracesPath: a.Traces}
	pair := a.OldRef != "" || a.NewRef != ""
	event := a.Event != "" || a.Service != "" || a.Revision != ""
	switch {
	case pair == event, pair && (a.OldRef == "" || a.NewRef == ""):
		return ImpactRequest{}, errImpactArgs
	case event:
		if err := a.eventErr(); err != nil {
			return ImpactRequest{}, err
		}
		req.Event = &impact.Event{Kind: a.Event, Service: a.Service, Revision: a.Revision}
	}
	return req, nil
}

// eventErr checks the arguments of an event, as `pacto impact` checks its
// event flags.
func (a impactArgs) eventErr() error {
	switch {
	case a.Event == "" || a.Service == "":
		return errImpactArgs
	case !slices.Contains([]string{impact.EventUnavailable, impact.EventRemoved, impact.EventRegressed}, a.Event):
		return errors.New("event is one of unavailable, removed or regressed")
	case a.Event == impact.EventRegressed && a.Revision == "":
		return errors.New("a regressed event names its 'revision'")
	case a.Revision != "" && a.Event != impact.EventRegressed:
		return errors.New("revision is read only with event regressed")
	case a.IncludeObserved:
		return errors.New("include_observed applies to old_ref and new_ref: an event always reads observed callers")
	}
	return nil
}

// impactHandler analyses over snap: the current snapshot, or the caller's view
// of it.
func impactHandler(provide impactProvider, snap *fleet.FleetSnapshot) mcpsdk.ToolHandlerFor[impactArgs, any] {
	return func(ctx context.Context, _ *mcpsdk.CallToolRequest, a impactArgs) (*mcpsdk.CallToolResult, any, error) {
		req, err := a.request()
		if err != nil {
			return errorResult(err), nil, nil
		}
		res, err := provide(ctx, snap, req)
		if err != nil {
			return errorResult(err), nil, nil
		}
		r, err := jsonResult(res)
		return r, nil, err
	}
}
