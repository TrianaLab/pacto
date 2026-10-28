package app

import (
	"context"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// Explain reads one operation and its Stage and explains them. The Stage may
// be gone; the operation is still explained.
func (d *Delivery) Explain(ctx context.Context, ns, name string) (*delivery.Explanation, error) {
	op, err := d.Get(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	stage, err := d.Stage(ctx, ns, op.Spec.Stage)
	if err != nil {
		stage = nil
	}
	return delivery.ExplainOperation(op, stage), nil
}

// History lists what happened on a Stage: from its signed records when the
// recorder writes them, plus the operations not recorded yet; otherwise from
// the operations still in the cluster, concluded ones in stream order, then
// the ones in flight.
func (d *Delivery) History(ctx context.Context, ns, stage string) (*delivery.History, error) {
	h, _, _, err := d.history(ctx, ns, stage, nil)
	return h, err
}

// HistoryOf is History for a Stage the caller has just read or listed, so
// the Stage is not read again: a page of 25 histories was 25 GETs of Stages
// the page had just listed.
func (d *Delivery) HistoryOf(ctx context.Context, st delivery.StageObject) (*delivery.History, error) {
	h, _, _, err := d.history(ctx, st.Metadata.Namespace, st.Metadata.Name, &st)
	return h, err
}
