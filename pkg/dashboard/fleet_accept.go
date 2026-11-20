package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// AcceptPlanAnswer is what accepting every proposed range of a service would
// sign, and the pacto accept command a person runs to sign it; or why there
// is nothing to accept. The dashboard never signs and never writes.
type AcceptPlanAnswer struct {
	Plan    *AcceptPlanView `json:"plan,omitempty" doc:"What pacto accept would sign; absent with a refusal"`
	Command string          `json:"command,omitempty" doc:"The exact pacto accept command, with the plan's intent digest, a person runs to sign the plan"`
	Refusal *AcceptRefusal  `json:"refusal,omitempty" doc:"Why there is nothing to accept, and the command a person runs about it"`
}

// AcceptPlanView is the acceptance record a plan would sign: the proposed
// ranges of the service's newest published version, who may accept them and
// the acceptances already attached.
type AcceptPlanView struct {
	Service      string                   `json:"service" doc:"The accepting service's fleet key"`
	Version      string                   `json:"version" doc:"The newest published version the record attaches to"`
	Subject      string                   `json:"subject" doc:"The bundle the record attaches to: <repository>@sha256:<hex>"`
	Ranges       []delivery.AcceptedRange `json:"ranges" doc:"Each proposed range the record accepts, with its source"`
	WhoMayAccept string                   `json:"whoMayAccept" doc:"Whose signature counts: the service's declared owner, or the route to one"`
	Existing     []impact.Acceptance      `json:"existing,omitempty" doc:"The acceptances already attached to the version, each with its state; only counted accepts"`
	Notes        []string                 `json:"notes,omitempty" doc:"What the plan could not read or how records were verified"`
	IntentDigest string                   `json:"intentDigest" doc:"The digest that names exactly this plan: pacto accept refuses a plan that moved since"`
}

// AcceptRefusal says why there is nothing to accept.
type AcceptRefusal struct {
	Code    string `json:"code" enum:"AcceptNothingProposed,NotPublished" doc:"AcceptNothingProposed: the newest published version proposes no range; NotPublished: the service has no published version to attach an acceptance to"`
	Message string `json:"message"`
	Command string `json:"command" doc:"The command a person runs about it"`
}

type fleetAcceptInput struct {
	Body struct {
		ServiceKey string `json:"serviceKey" minLength:"1" doc:"The service whose proposed ranges to accept"`
	}
}

type fleetAcceptOutput struct{ Body AcceptPlanAnswer }

// registerAcceptOperation registers the read-only accept plan when the host
// plans acceptances.
func (s *Server) registerAcceptOperation(api huma.API) {
	if s.opts.AcceptPlan == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-accept",
		Method:      http.MethodPost,
		Path:        "/api/fleet/accept",
		Summary:     "Plan accepting every proposed range of a service",
		Description: "Plans the acceptance record pacto accept would sign for every dependency range the service's newest " +
			"published version proposes, and returns the exact pacto accept command with its intent digest. " +
			"It never signs and never writes: a person runs the command. Signed-in and local viewers only.",
		Tags: []string{"Fleet"},
	}, s.fleetAccept)
}

func (s *Server) fleetAccept(ctx context.Context, in *fleetAcceptInput) (*fleetAcceptOutput, error) {
	if !s.readsAcceptances(ctx) {
		return nil, huma.Error403Forbidden("sign in to plan an acceptance: the plan reads the acceptances attached to the service")
	}
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	// The viewer's query: a hidden service answers as a missing one.
	key := fleet.ServiceKey(in.Body.ServiceKey)
	rev := q.ProviderCurrentRevision(key)
	if rev == nil || rev.Contract == nil {
		return nil, huma.Error404NotFound("no service with this serviceKey")
	}
	ans, err := s.opts.AcceptPlan(ctx, key, rev)
	if err != nil {
		return nil, impactHTTPError(err)
	}
	return &fleetAcceptOutput{Body: ans}, nil
}

type acceptanceReadsKey struct{}

// readsAcceptances reports whether the caller may have acceptances read for
// them with the host's registry credentials: a local viewer or a signed-in
// person, never an anonymous one or a sign-in link session.
func (s *Server) readsAcceptances(ctx context.Context) bool {
	a := s.opts.Auth
	if a == nil {
		return false
	}
	st := s.authState(ctx)
	return a.Local || (st.SignedIn && !st.FleetOnly)
}

// withAcceptanceReads marks ctx when the caller may have acceptances read.
func (s *Server) withAcceptanceReads(ctx context.Context) context.Context {
	if !s.readsAcceptances(ctx) {
		return ctx
	}
	return context.WithValue(ctx, acceptanceReadsKey{}, true)
}

// ReadsAcceptances reports whether the impact provider may read the
// acceptances attached to the consumers of an announced break for this
// request: true only for a local viewer or a signed-in person.
func ReadsAcceptances(ctx context.Context) bool {
	ok, _ := ctx.Value(acceptanceReadsKey{}).(bool)
	return ok
}
