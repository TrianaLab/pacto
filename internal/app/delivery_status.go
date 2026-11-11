package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// statusWhyLimit is how many Progress answers one status walk plans: each
// is a plan dry-run (Pactos, Kustomizations, Stages and registry reads).
const statusWhyLimit = 16

// Status says where each Stage of one service stands, as the caller can see
// it: the shared row per Stage (delivery.RowOf) with its offers narrowed by
// the caller's RBAC, the undo of the change it runs and where its candidates
// come from, in promotion order. With Why it also says, for the first
// statusWhyLimit Stages, why each can or cannot move to its candidate. It
// reuses the audit's bounds: one listing, at most auditLimit Stages, within
// auditTimeout.
func (d *Delivery) Status(ctx context.Context, q delivery.StatusQuery) (*delivery.ServiceStatus, error) {
	if q.Service == "" {
		return nil, errors.New("name the service: pacto delivery status --service <name>")
	}
	caller, err := d.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	asOf := d.Now().UTC().Format(time.RFC3339)
	w, err := d.auditStages(ctx, q.Namespace, delivery.ServiceRef{Name: q.Service, Domain: q.Domain})
	if err != nil {
		return nil, err
	}
	out := &delivery.ServiceStatus{Listing: delivery.Listing{AsOf: asOf, Completeness: "complete", Unreadable: w.Forbidden},
		Service: q.Service, Domain: q.Domain, Stages: []delivery.StageRow{}}
	stages, note := inPromotionOrder(w.Stages)
	blames, unread := audit(ctx, stages, func(ctx context.Context, st delivery.StageObject) (*blameRead, error) {
		b, err := d.blameOf(ctx, st.Metadata.Namespace, st.Metadata.Name, &st, "")
		return &blameRead{b, err}, nil
	})
	out.Unreadable = append(out.Unreadable, unread...) // only the Stages past auditLimit: the read never fails
	stages = stages[:len(blames)]
	ops, opsErr := d.operationsByNamespace(ctx, stages)
	for ns, err := range opsErr {
		out.Unreadable = append(out.Unreadable, "operations in "+ns+": "+err.Error())
	}
	read := d.describedAll(ctx, stages)
	byStage := stagesByName(w.All)
	for i, st := range stages {
		row := delivery.RowOf(st, delivery.RowInput{Held: heldOn(ops[st.Metadata.Namespace], st.Metadata.Name),
			HeldUnknown: opsErr[st.Metadata.Namespace] != nil, Caller: caller, Read: read, WithService: true})
		if o := delivery.UndoOfferFor(st, blames[i].b, blames[i].err, caller); o != nil {
			row.Offers = append(row.Offers, *o)
		}
		row.Upstream = delivery.UpstreamListed(&stages[i], byStage, w.read, "")
		out.Stages = append(out.Stages, row)
	}
	rows := make([]*delivery.StageRow, len(out.Stages))
	for i := range out.Stages {
		rows[i] = &out.Stages[i]
	}
	delivery.NarrowOffers(ctx, rows, delivery.CreateOperations(d.Can))
	if q.Why {
		note += d.whyAll(ctx, caller, stages, out.Stages, ops, opsErr, read, asOf)
	}
	if len(out.Unreadable) > 0 {
		out.Completeness = "partial"
	}
	out.Note = "the Stages of " + q.Service + " you can see, " + note
	return out, nil
}

// blameRead is one Stage's blame, or why its history could not be read.
type blameRead struct {
	b   *delivery.Blame
	err error
}

// stagesByName indexes Stages by namespace/name.
func stagesByName(stages []delivery.StageObject) map[string]*delivery.StageObject {
	out := map[string]*delivery.StageObject{}
	for i := range stages {
		out[stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name] = &stages[i]
	}
	return out
}

// inPromotionOrder sorts stages by service, then their environment's place in
// the promotion order their spec.candidates declare, then namespace and
// name; the note says so and names the environments placed by name.
func inPromotionOrder(stages []delivery.StageObject) ([]delivery.StageObject, string) {
	order, _, cycle, unordered := delivery.PromotionOrder(stages)
	at := func(st delivery.StageObject) int { return slices.Index(order, st.Spec.Environment) }
	slices.SortStableFunc(stages, func(a, b delivery.StageObject) int {
		return cmp.Or(cmp.Compare(a.Spec.Service.Name, b.Spec.Service.Name), cmp.Compare(at(a), at(b)),
			cmp.Compare(a.Metadata.Namespace, b.Metadata.Namespace), cmp.Compare(a.Metadata.Name, b.Metadata.Name))
	})
	note := "in promotion order, from each Stage's spec.candidates"
	if len(cycle) > 0 {
		note += "; " + strings.Join(slices.Sorted(maps.Keys(cycle)), ", ") + " sit on a cycle of upstream Stages, so they are placed by name"
	}
	if len(unordered) > 0 {
		note += "; " + strings.Join(slices.Sorted(maps.Keys(unordered)), ", ") + " are ordered both ways by different Stages, so they are placed by name"
	}
	return stages, note
}

// operationsByNamespace lists the operations that did not conclude Succeeded
// in each namespace of stages, auditParallel at once; errs names the
// namespaces whose list failed.
func (d *Delivery) operationsByNamespace(ctx context.Context, stages []delivery.StageObject) (map[string][]delivery.OperationObject, map[string]error) {
	var nss []string
	for _, st := range stages {
		if !slices.Contains(nss, st.Metadata.Namespace) {
			nss = append(nss, st.Metadata.Namespace)
		}
	}
	lists, errs := make([][]delivery.OperationObject, len(nss)), make([]error, len(nss))
	var g errgroup.Group
	g.SetLimit(auditParallel)
	for i, ns := range nss {
		g.Go(func() error {
			lists[i], errs[i] = d.OperationsNotSucceeded(ctx, ns)
			return nil
		})
	}
	_ = g.Wait()
	ops, failed := map[string][]delivery.OperationObject{}, map[string]error{}
	for i, ns := range nss {
		if errs[i] != nil {
			failed[ns] = errs[i]
			continue
		}
		ops[ns] = lists[i]
	}
	return ops, failed
}

// heldOn counts the operations on stage that wait for a person.
func heldOn(ops []delivery.OperationObject, stage string) int {
	n := 0
	for _, o := range ops {
		if o.Spec.Stage == stage && o.Status.Phase == delivery.PhaseHeld {
			n++
		}
	}
	return n
}

// describedAll reads what each pointer and candidate of stages carries,
// describeParallel at once.
func (d *Delivery) describedAll(ctx context.Context, stages []delivery.StageObject) map[string]delivery.Described {
	var ask []string
	from := map[string]*delivery.StageObject{}
	for i, st := range stages {
		for _, dg := range []string{st.Status.Pointer, st.Status.Candidate} {
			if _, seen := from[dg]; dg != "" && !seen {
				ask, from[dg] = append(ask, dg), &stages[i]
			}
		}
	}
	revs := delivery.DescribeAll(ctx, len(ask), func(i int) (*delivery.StageObject, string) { return from[ask[i]], ask[i] }, d.Describe)
	out := map[string]delivery.Described{}
	for i, dg := range ask {
		r := revs[i]
		desc := delivery.Described{Contract: r.ContractRef, Source: r.SourceRevision, Err: r.Err}
		if r.Bundle != nil {
			desc.Version, desc.Team = r.Bundle.Contract.Service.Version, r.Bundle.Contract.Service.Owner.DisplayString()
		}
		out[dg] = desc
	}
	return out
}

// whyAll sets Progress on the first statusWhyLimit rows; the note it returns
// names the rest.
func (d *Delivery) whyAll(ctx context.Context, caller Caller, stages []delivery.StageObject, rows []delivery.StageRow,
	ops map[string][]delivery.OperationObject, opsErr map[string]error, read map[string]delivery.Described, asOf string) string {
	n := min(len(stages), statusWhyLimit)
	var g errgroup.Group
	g.SetLimit(auditParallel)
	for i := range n {
		g.Go(func() error {
			st := stages[i]
			pin := delivery.ProgressInput{Stage: st, Caller: caller, Team: read[st.Status.Pointer].Team, AsOf: asOf}
			if err := opsErr[st.Metadata.Namespace]; err != nil {
				pin.OpsErr = err.Error()
			}
			for _, o := range ops[st.Metadata.Namespace] {
				if o.Spec.Stage == st.Metadata.Name {
					pin.Ops = append(pin.Ops, o)
				}
			}
			rows[i].Progress = new(d.progressOf(ctx, pin))
			return nil
		})
	}
	_ = g.Wait()
	if len(stages) > n {
		return fmt.Sprintf("; why is answered for the first %d of %d Stages only: name a namespace with -n for the others", n, len(stages))
	}
	return ""
}

// progressOf completes in with the caller's access review for the Stage's
// namespace and the plan of its candidate, then says why it can or cannot
// move (delivery.ProgressOf). A review or plan that fails makes the answer
// unknown, never a refusal.
func (d *Delivery) progressOf(ctx context.Context, in delivery.ProgressInput) delivery.Progress {
	if ok, err := delivery.CreateOperations(d.Can)(ctx, in.Stage.Metadata.Namespace); err != nil {
		in.RBACErr = err.Error()
	} else {
		in.MayCreate = &ok
	}
	if req, ok := delivery.ProgressPlanRequest(in.Stage); ok {
		if p, _, err := d.Plan(ctx, req); err != nil {
			in.PlanErr = err.Error()
		} else {
			in.Plan = &p
		}
	}
	return delivery.ProgressOf(in)
}
