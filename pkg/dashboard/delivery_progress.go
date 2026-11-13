package dashboard

import (
	"context"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

type progressInput struct {
	Namespace string `query:"namespace" required:"true" doc:"The Stage's namespace"`
	Stage     string `query:"stage" required:"true" doc:"The Stage"`
}

type progressOutput struct {
	Body struct {
		Namespace string `json:"namespace"`
		Stage     string `json:"stage"`
		delivery.Progress
		Labels
	}
}

// deliveryProgress says why a Stage can or cannot move to its newest
// candidate, for the caller. An operations list, an access review or a plan
// that fails makes the answer unknown, never a refusal: it only describes.
func (s *Server) deliveryProgress(ctx context.Context, in *progressInput) (*progressOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	asOf := time.Now().UTC().Format(time.RFC3339)
	st, err := api.Stage(ctx, in.Namespace, in.Stage)
	if err != nil {
		return nil, err
	}
	pin := delivery.ProgressInput{Stage: *st, Caller: caller, AsOf: asOf}
	if ops, err := api.Operations(ctx, in.Namespace); err != nil {
		pin.OpsErr = err.Error()
	} else {
		for _, o := range ops {
			if o.Spec.Stage == in.Stage && (o.Status.Phase != delivery.PhaseConcluded || o.Status.Outcome != delivery.Succeeded) {
				pin.Ops = append(pin.Ops, o)
			}
		}
	}
	if ok, err := delivery.CreateOperations(api.Can)(ctx, in.Namespace); err != nil {
		pin.RBACErr = err.Error()
	} else {
		pin.MayCreate = &ok
	}
	on := digestsOn{st, []string{st.Status.Pointer, st.Status.Candidate}}
	read := describedAll(ctx, api, nil, on)
	pin.Team = read[st.Status.Pointer].Team
	if req, ok := delivery.ProgressPlanRequest(*st); ok {
		if p, err := api.Plan(ctx, req); err != nil {
			pin.PlanErr = err.Error()
		} else {
			pin.Plan = &p
		}
	}
	out := &progressOutput{}
	out.Body.Namespace, out.Body.Stage, out.Body.Progress = in.Namespace, in.Stage, delivery.ProgressOf(pin)
	out.Body.Labels = Labels{Versions: versionsIn(nil, read), Revisions: s.revisionLabels(ctx, read, on)}
	return out, nil
}
