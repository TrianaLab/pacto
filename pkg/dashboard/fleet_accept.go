package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// AcceptPlanAnswer is what accepting every proposed range of a service (or
// one acceptance draft) would sign, and the pacto accept command a person
// runs to sign it; or why there is nothing to accept. The plan itself never
// signs and never writes; Execute says what the person can do here.
type AcceptPlanAnswer struct {
	Plan    *AcceptPlanView     `json:"plan,omitempty" doc:"What pacto accept would sign; absent with a refusal"`
	Command string              `json:"command,omitempty" doc:"The exact pacto accept command, with the plan's intent digest, a person runs to sign the plan"`
	Refusal *AcceptRefusal      `json:"refusal,omitempty" doc:"Why there is nothing to accept, and the command a person runs about it"`
	Execute *AcceptExecuteOffer `json:"execute,omitempty" doc:"What the person can do with the plan here; absent on a dashboard that only plans"`
}

// AcceptExecuteOffer says whether the person can sign the plan here, save it
// as an acceptance draft, or neither.
type AcceptExecuteOffer struct {
	Signing    DeclareSigning `json:"signing"`
	Write      DeclareWrite   `json:"write"`
	SignTicket string         `json:"signTicket,omitempty" doc:"Starts the signing login once: /auth/sign?ticket=<signTicket>&return=<page>; present only with signing mode fresh-login"`
}

// AcceptDraftView is one acceptance draft of the service, or why it cannot be
// read.
type AcceptDraftView struct {
	Tag        string                   `json:"tag" doc:"The pacto-accept-draft-<n> tag"`
	State      string                   `json:"state" enum:"draft,unreadable"`
	Reason     string                   `json:"reason,omitempty"`
	Ranges     []delivery.AcceptedRange `json:"ranges,omitempty" doc:"The ranges it asks a person to accept"`
	Break      *delivery.AcceptedBreak  `json:"break,omitempty" doc:"The break it asks a person to accept"`
	OnBehalfOf string                   `json:"onBehalfOf,omitempty" doc:"Whom it was written for, as its writer asserts"`
	Asker      string                   `json:"asker,omitempty" doc:"Who wrote it, when its signature names someone"`
	Signed     bool                     `json:"signed" doc:"It carries a signature, checked when a person accepts it"`
}

// AcceptTarget is the service an accept execution is for: its current
// revision and where it runs, the workloads (else Pacto resources) a person
// must be allowed to update.
type AcceptTarget struct {
	Key  string
	Rev  *fleet.ContractRevision
	Runs []DeclareWorkload
}

// AcceptExecuteInput carries out an accept plan the person reviewed.
type AcceptExecuteInput struct {
	ServiceKey   string `json:"serviceKey" minLength:"1" doc:"The service whose proposed ranges to accept"`
	IntentDigest string `json:"intentDigest" minLength:"1" doc:"The intent digest of the plan the person reviewed: a plan that moved since is refused with PlanStale"`
	Mode         string `json:"mode" enum:"sign,draft" doc:"sign: attach the acceptance signed by the person's fresh login; draft: save a pacto-accept-draft-<n> a person accepts later"`
	FromDraft    string `json:"fromDraft,omitempty" pattern:"^pacto-accept-draft-[1-9][0-9]*$" maxLength:"40" doc:"Accept this acceptance draft, signed by the person"`
}

// AcceptExecuteAnswer is what accepting a plan wrote, or why nothing was.
type AcceptExecuteAnswer struct {
	Record  string          `json:"record,omitempty" doc:"The acceptance attached, as <repository>@sha256:<hex>"`
	State   string          `json:"state,omitempty" doc:"How readers judge the acceptance attached"`
	Reason  string          `json:"reason,omitempty"`
	Draft   string          `json:"draft,omitempty" doc:"The acceptance draft written, as <repository>:pacto-accept-draft-<n>"`
	Command string          `json:"command,omitempty" doc:"The command a person accepts the draft with"`
	Refusal *DeclareRefusal `json:"refusal,omitempty"`
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
	Draft        string                   `json:"draft,omitempty" doc:"The acceptance draft this plan accepts"`
	OnBehalfOf   string                   `json:"onBehalfOf,omitempty" doc:"Whom the draft was written for, as its writer asserts"`
	Drafts       []AcceptDraftView        `json:"drafts,omitempty" doc:"The service's acceptance drafts, newest first, at most 20"`
	DraftsTotal  int                      `json:"draftsTotal,omitempty"`
}

// AcceptRefusal says why there is nothing to accept.
type AcceptRefusal struct {
	Code    string `json:"code" enum:"AcceptNothingProposed,NotPublished,DraftNotFound,PlanStale,SignerUnverified" doc:"AcceptNothingProposed: the newest published version proposes no range; NotPublished: the service has no published version to attach an acceptance to; DraftNotFound, PlanStale and SignerUnverified: the acceptance draft is missing, moved or not signed by whom it names"`
	Message string `json:"message"`
	Command string `json:"command" doc:"The command a person runs about it"`
}

type fleetAcceptInput struct {
	Body struct {
		ServiceKey string `json:"serviceKey" minLength:"1" doc:"The service whose proposed ranges to accept"`
		FromDraft  string `json:"fromDraft,omitempty" pattern:"^pacto-accept-draft-[1-9][0-9]*$" maxLength:"40" doc:"Plan accepting this acceptance draft instead"`
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
	ans, err := s.opts.AcceptPlan(ctx, key, rev, in.Body.FromDraft)
	if err != nil {
		return nil, impactHTTPError(err)
	}
	// A plan of another service (a contract naming a hidden service's
	// repository) answers as a hidden one.
	if ans.Plan != nil && !fleet.NamesService(key, fleet.ServiceKey(ans.Plan.Service)) {
		return nil, huma.Error404NotFound("no service with this serviceKey")
	}
	if d := s.opts.Declare; d != nil && ans.Plan != nil {
		offer := d.AcceptOffer(requestFrom(ctx), ans.Plan.IntentDigest)
		ans.Execute = &offer
	}
	return &fleetAcceptOutput{Body: ans}, nil
}

type acceptExecuteInput struct{ Body AcceptExecuteInput }
type acceptExecuteOutput struct{ Body AcceptExecuteAnswer }

// registerAcceptExecuteOperation registers carrying out an accept plan when
// the host plans acceptances and its declarer can write.
func (s *Server) registerAcceptExecuteOperation(api huma.API) {
	d := s.opts.Declare
	if s.opts.AcceptPlan == nil || d == nil || !d.CanWrite() {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-accept-execute",
		Method:      http.MethodPost,
		Path:        "/api/fleet/accept/execute",
		Summary:     "Sign the accepted ranges or save them as an acceptance draft",
		Description: "Carries out an accept plan the person reviewed, by its intent digest: attaches the acceptance signed by " +
			"the person's fresh login, saves a pacto-accept-draft-<n> a person accepts later, or accepts a draft. It needs " +
			"update on a workload (else the Pacto resource) of the service and writes only into its bundle repository.",
		Tags: []string{"Fleet"},
	}, func(ctx context.Context, in *acceptExecuteInput) (*acceptExecuteOutput, error) {
		t, err := s.acceptTarget(ctx, in.Body.ServiceKey)
		if err != nil {
			return nil, err
		}
		ans, err := d.AcceptRanges(requestFrom(ctx), t, in.Body)
		if err != nil {
			return nil, err
		}
		return &acceptExecuteOutput{Body: *ans}, nil
	})
}

// acceptTarget is the service key names in the viewer's snapshot, with its
// current revision and the Kubernetes targets it runs as, after the caller's
// identity is checked: a hidden service answers as a missing one.
func (s *Server) acceptTarget(ctx context.Context, key string) (AcceptTarget, error) {
	if err := s.declareIdentity(ctx); err != nil {
		return AcceptTarget{}, err
	}
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return AcceptTarget{}, err
	}
	sk := fleet.ServiceKey(key)
	rev := q.ProviderCurrentRevision(sk)
	if rev == nil || rev.Contract == nil {
		return AcceptTarget{}, huma.Error404NotFound("no service with this serviceKey")
	}
	snap := q.SnapshotForSerialization()
	t := AcceptTarget{Key: key, Rev: rev}
	for _, tk := range snap.Services[sk].Targets {
		tr := snap.Targets[tk]
		if tr == nil || tr.Kind != fleet.SourceKindKubernetes {
			continue
		}
		cluster, _, _, _, _ := fleet.ParseTargetKey(tk)
		// "Pacto" is authn.PactoKind: the Pacto resource stands in for an unknown workload.
		w := DeclareWorkload{Key: string(tk), Name: tr.Name, Cluster: cluster, Namespace: tr.Scope, Kind: "Pacto"}
		if wl := tr.RunsAs(); wl != nil {
			w.Kind, w.Name = wl.Kind, wl.Name
		}
		t.Runs = append(t.Runs, w)
	}
	return t, nil
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
