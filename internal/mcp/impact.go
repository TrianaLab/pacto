package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// ImpactRequest is one pacto_impact call: a contract change from OldRef to
// NewRef, or, when Event is set, a what-if about one fleet service.
type ImpactRequest struct {
	OldRef, NewRef string
	// Domain qualifies the changed service when NewRef is a local path that
	// names no registry; empty derives it from NewRef.
	Domain string
	// NewDirName is the folder name NewRef's service.name is inferred from
	// when NewRef is a staged copy of that folder (an edit); empty uses
	// NewRef's own folder.
	NewDirName      string
	IncludeObserved bool
	TracesPath      string
	Event           *impact.Event
	// Policy is the range policy a BREAKING answer is held to.
	Policy impact.Policy
	// Running judges NewRef against the revisions of its service that run
	// on known targets; OldRef is empty.
	Running bool
}

// impactProvider answers req over snap, the snapshot the fleet tools answer
// from. It is supplied by the command layer (from app.Service) so this package
// depends only on the pure result type — never on OCI, Kubernetes or
// the app service.
type impactProvider func(ctx context.Context, snap *fleet.FleetSnapshot, req ImpactRequest) (*impact.Result, error)

func impactTool() *mcpsdk.Tool {
	return &mcpsdk.Tool{
		Name:        "pacto_impact",
		Annotations: readOnly(true),
		Description: "Analyze which consumers a change affects, from an old to a new contract revision, " +
			"projected onto the operational graph. Reports the semantic classification, breaking " +
			"changes, affected consumers (with confidence and compatibility verdict), active targets " +
			"and owners to review. The answer ends in 'gate' (blocked, unverified or clear) and 'gateReason', " +
			"the same gate `pacto impact` exits on (1, 2, 0): unverified means Pacto could not show the change " +
			"is safe, never that it is. 'ranges' is the range coverage of the direct consumers (declared, accepted and unknown ranges, with the range Pacto proposes for each unknown one) " +
			"and 'policy' the range policy the answer was held to: pass 'policy' as the --policy values of `pacto impact` (unknown-range=warn|fail, uncontracted=warn|fail:<window>); " +
			"a fail policy blocks a BREAKING change, a warn adds a POLICY_WARNING line to 'notes'. A consumer at depth 1 with provenance and confidence 'inferred' (via 'inferred') is named only by " +
			"its own inferred record, pacto.inferred.yaml: a direct consumer whose compatibilityVerdict is unknown, which never clears the gate. " +
			"'advisories' are possible breaks on interfaces only pacto.inferred.yaml names: the breaking findings of the spec diff, the inferred source path:line, the direct consumers " +
			"(inferred, compatibility unknown) and the YAML that declares the interface. An advisory never clears or blocks: a change that is not BREAKING reads unverified " +
			"with the gate code INFERRED_POSSIBLE_BREAK until the interface is declared. " +
			"On a BREAKING change a compatible consumer only matches by range; read " +
			"its 'note'. 'consumerTallies' counts every consumer by outcome (incompatible includes a consumer that refuses a " +
			"required guarantee) and ranks their owners; state counts from it rather than by counting the list. Over stdio, when no fleet source holds the changed service, the new revision is added to the snapshot " +
			"('newRevisionAdded'); on a server whose callers sign in it is not, and the answer says " +
			"SERVICE_NOT_IN_FLEET with an unverified gate. Over HTTP, old_ref and new_ref must name revisions your fleet view holds, " +
			"and the server reads each revision from where your fleet view says it lives. On a stdio server that names no target source of its own and was not started with " +
			"--target-snapshot off, the answer reads the change's signed target snapshot ('registry', 'targetSnapshots'); an HTTP server reads none until per-cluster review exists. " +
			"Over stdio the answer also finds the consumers published to the organization index (<registry>/pacto-index, derived from the provider's oci:// reference) " +
			"and says how it read it in 'consumerIndex'; a consumer the fleet holds that the index does not list adds CONSUMER_NOT_INDEXED, and an index with no " +
			"target snapshot to check it against adds CONSUMER_INDEX_UNCHECKED, both making the answer partial. Over HTTP the index is never read and the answer comes from your fleet view. " +
			"Pass 'event' (unavailable, removed or regressed) with 'service' " +
			"(and 'revision' for regressed: an artifact digest reads delivery's recorded failures) instead of old_ref and new_ref " +
			"to ask what happens if that service becomes unavailable, is removed or regressed; consumers carry 'effect' " +
			"(breaks, may-degrade, unknown) and the gate is never blocked for an event. Pass 'offering' instead of 'service' " +
			"to ask it of every provider of a platform offering (unavailable or removed only); 'providers' lists them, and the top-level " +
			"'service' then holds the offering's name, the same as 'event.offering'. " +
			"Pass 'external' (an external system's identity, such as stripe) instead of 'service' with event changed or unavailable " +
			"to ask what a change or an outage of a system nobody here controls does to every service that uses it: consumers carry " +
			"'sources' (declared, observed, pod-spec, repository) and 'client' (the client library major, always possible), a changed " +
			"answer gives every consumer the effect attention, 'external' names the system and the change of its inferred contract, " +
			"'pin' is the dependencies YAML that pins a range on it, and the gate is always unverified. " +
			"Pass 'running' with new_ref and no old_ref to judge new_ref against the revisions of its service that run on known targets " +
			"('running' in the answer names them, the one judged and the ones Pacto could not read, which never let the gate clear); " +
			"a running revision whose digest is new_ref's is left out of the baseline and listed in 'running.excluded'; " +
			"with none running, only consumers' ranges are checked, and a server whose answers are restricted to your view refuses it. " +
			"Read-only: it lists review targets, never recommends or takes actions.",
		InputSchema: inputSchema(map[string]property{
			"old_ref":          {Type: "string", Description: "Old contract revision: a local directory or oci:// ref over stdio, an oci:// ref of a revision in your fleet view over HTTP"},
			"new_ref":          {Type: "string", Description: "New contract revision: a local directory or oci:// ref over stdio, an oci:// ref of a revision in your fleet view over HTTP"},
			"include_observed": {Type: "boolean", Description: "Let observed (runtime) relationships raise consumer confidence; with old_ref and new_ref only, since an event always reads observed callers"},
			"traces":           {Type: "string", Description: "Path to an OTLP/JSON trace file; its observed edges corroborate and surface consumers (implies include_observed)"},
			"event":            {Type: "string", Description: "What happens to 'service' instead of a contract change; changed only with 'external'", Enum: impactEvents},
			"service":          {Type: "string", Description: "The fleet service key the event is about"},
			"offering":         {Type: "string", Description: "A platform offering instead of 'service': the event is about every provider of it; unavailable or removed only"},
			"external":         {Type: "string", Description: "An external system's identity instead of 'service' or 'offering': changed or unavailable only"},
			"revision":         {Type: "string", Description: "For regressed: the revision that regressed, a version or an artifact digest (sha256:...)"},
			"policy":           {Type: "array", Items: "string", Description: "Range policy for a BREAKING change, as key=value: unknown-range=warn|fail and uncontracted=warn|fail:<window> (such as fail:7d); default warn"},
			"running":          {Type: "boolean", Description: "Judge new_ref against the revisions of its service that run on known targets; one whose digest is new_ref's is left out and listed in 'running.excluded'; leave old_ref empty"},
		}, nil),
	}
}

type impactArgs struct {
	OldRef          string   `json:"old_ref"`
	NewRef          string   `json:"new_ref"`
	IncludeObserved bool     `json:"include_observed"`
	Traces          string   `json:"traces"`
	Event           string   `json:"event"`
	Service         string   `json:"service"`
	Offering        string   `json:"offering"`
	External        string   `json:"external"`
	Revision        string   `json:"revision"`
	Policy          []string `json:"policy"`
	Running         bool     `json:"running"`
}

// errImpactArgs is the answer to a call that names neither a change nor an event.
var errImpactArgs = errors.New("pass either old_ref and new_ref, or event and service (or offering, or external)")

// impactEvents are the event kinds pacto_impact takes.
var impactEvents = []string{impact.EventUnavailable, impact.EventRemoved, impact.EventRegressed, impact.EventChanged}

// request checks the arguments and turns them into an ImpactRequest.
func (a impactArgs) request() (ImpactRequest, error) {
	policy, err := impact.ParsePolicy(a.Policy)
	if err != nil {
		return ImpactRequest{}, err
	}
	req := ImpactRequest{OldRef: a.OldRef, NewRef: a.NewRef, IncludeObserved: a.IncludeObserved, TracesPath: a.Traces, Policy: policy}
	pair := a.OldRef != "" || a.NewRef != ""
	event := a.Event != "" || a.Service != "" || a.Offering != "" || a.External != "" || a.Revision != ""
	switch {
	case a.Running:
		if err := a.runningErr(event); err != nil {
			return ImpactRequest{}, err
		}
		req.Running = true
	case pair == event, pair && (a.OldRef == "" || a.NewRef == ""):
		return ImpactRequest{}, errImpactArgs
	case event:
		if err := a.eventErr(); err != nil {
			return ImpactRequest{}, err
		}
		req.Event = &impact.Event{Kind: a.Event, Service: a.Service, Offering: a.Offering, External: a.External, Revision: a.Revision}
	}
	return req, nil
}

// runningErr checks the arguments of a running question, as `pacto impact
// --running` checks its argument and flags.
func (a impactArgs) runningErr(event bool) error {
	switch {
	case a.NewRef == "":
		return errors.New("running judges new_ref: pass new_ref")
	case a.OldRef != "":
		return errors.New("running judges new_ref against the revisions that run: leave old_ref empty")
	case event:
		return errors.New("running asks about a contract change: pass no event, service, offering, external or revision")
	}
	return nil
}

// eventErr checks the arguments of an event, as `pacto impact` checks its
// event flags.
func (a impactArgs) eventErr() error {
	switch {
	case a.Event == "":
		return errImpactArgs
	case nonEmpty(a.Service, a.Offering, a.External) != 1:
		return errors.New("an event names exactly one of 'service', 'offering' and 'external'")
	case a.Offering != "" && a.Event == impact.EventRegressed:
		return errors.New("a regressed event names one 'service': a regression belongs to one revision")
	case !slices.Contains(impactEvents, a.Event):
		return errors.New("event is one of unavailable, removed, regressed or changed")
	case a.Event == impact.EventRegressed && a.Revision == "":
		return errors.New("a regressed event names its 'revision'")
	case a.Revision != "" && a.Event != impact.EventRegressed:
		return errors.New("revision is read only with event regressed")
	case a.IncludeObserved:
		return errors.New("include_observed applies to old_ref and new_ref: an event always reads observed callers")
	}
	return a.externalErr()
}

// externalErr checks the external system of an event: changed needs one,
// removed and regressed refuse one, and its identity is read as written
// (identities are canonical lower-case, so one Pacto would have to rewrite
// is refused).
func (a impactArgs) externalErr() error {
	switch {
	case a.Event == impact.EventChanged && a.External == "":
		return errors.New("a changed event names an 'external' system; a change to a service of your own is old_ref and new_ref")
	case a.External == "":
		return nil
	case a.Event == impact.EventRemoved || a.Event == impact.EventRegressed:
		return errors.New("an external system is not yours to remove or regress; ask unavailable or changed")
	}
	if _, err := externalid.Parse(a.External); err != nil {
		return fmt.Errorf("%w; find its identity with pacto_fleet_search (pacto fleet search)", err)
	}
	return nil
}

// nonEmpty counts the values that are set.
func nonEmpty(values ...string) int {
	n := 0
	for _, v := range values {
		if v != "" {
			n++
		}
	}
	return n
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
