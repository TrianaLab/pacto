package app

import (
	"context"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// Lifecycle follows one artifact of a service through its Stages the caller
// can see: it reads the Stages, the artifacts they know (at most
// delivery.LifecycleDigestLimit) to find the one q names, and each Stage's
// history. What the walk could not read makes the answer partial and is
// named. It reuses the audit's bounds.
func (d *Delivery) Lifecycle(ctx context.Context, q delivery.LifecycleQuery) (*delivery.Lifecycle, error) {
	if err := q.Check(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	asOf := d.Now().UTC().Format(time.RFC3339)
	w, err := d.auditStages(ctx, q.Namespace, delivery.ServiceRef{Name: q.Service, Domain: q.Domain})
	if err != nil {
		return nil, err
	}
	ds, from, gap := delivery.LifecycleDigests(q, w.Stages)
	revs := delivery.DescribeAll(ctx, len(ds), func(i int) (*delivery.StageObject, string) { return from[i], ds[i] }, d.Describe)
	r, note, err := delivery.ResolveLifecycleDigest(q, revs, delivery.LifecycleGaps(gap, w.Forbidden, false))
	if err != nil {
		return nil, err
	}
	hs := d.Histories(ctx, w.Stages)
	l := delivery.LifecycleOf(r, w.Stages, hs.Histories)
	l.AsOf, l.Service, l.Domain = asOf, q.Service, q.Domain
	l.Unreadable = append(w.Forbidden, hs.Unreadable...)
	if len(l.Unreadable) > 0 || note != "" {
		l.Completeness = "partial"
	}
	l.Note += note
	return &l, nil
}
