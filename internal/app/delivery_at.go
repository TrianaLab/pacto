package app

import (
	"context"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// HistoryAt says which artifact each Stage ran at t, from its signed records
// (delivery.StateAt): Stage stage in ns, else every Stage in ns, or in every
// namespace the caller can list for "", of service of when it names one. A
// Stage whose history could not be read is unknown and says why; a namespace
// the caller may not list makes the answer partial and is named. Each
// revision key is derived (delivery.RevisionKeyOf).
func (d *Delivery) HistoryAt(ctx context.Context, ns string, of delivery.ServiceRef, stage string, t time.Time) (*delivery.AtAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	var w auditWalk
	if stage != "" {
		st, err := d.Stage(ctx, ns, stage)
		if err != nil {
			return nil, err
		}
		w.Stages = []delivery.StageObject{*st}
	} else {
		var err error
		if w, err = d.auditStages(ctx, ns, of); err != nil {
			return nil, err
		}
	}
	ats, unreadable := audit(ctx, w.Stages, func(ctx context.Context, s delivery.StageObject) (*delivery.StageAt, error) {
		h, err := d.HistoryOf(ctx, s)
		if err != nil {
			return new(delivery.UnreadAt(s, "its history could not be read: "+err.Error())), nil
		}
		return new(delivery.StateAt(s, *h, t)), nil
	})
	out := &delivery.AtAnswer{At: t.UTC().Format(time.RFC3339), AsOf: d.Now().UTC().Format(time.RFC3339), Stages: []delivery.StageAt{}}
	var on []stageDigests
	for i, a := range ats {
		out.Stages = append(out.Stages, *a)
		on = append(on, stageDigests{&w.Stages[i], []string{a.Digest}})
	}
	out.Describe(d.describeOn(ctx, on...))
	out.Complete(len(w.Forbidden) == 0, false, append(w.Forbidden, unreadable...))
	return out, nil
}
