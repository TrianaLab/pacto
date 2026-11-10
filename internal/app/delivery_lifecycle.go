package app

import (
	"context"
	"errors"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// Lifecycle follows one artifact of a service through its Stages the caller
// can see: it reads the Stages, the artifacts they know (at most
// delivery.LifecycleDigestLimit) to find the one q names, and each Stage's
// history. What the walk could not read makes the answer partial and is
// named. It reuses the audit's bounds.
func (d *Delivery) Lifecycle(ctx context.Context, q delivery.LifecycleQuery) (*delivery.Lifecycle, error) {
	if q.Service == "" {
		return nil, errors.New("name the service: pacto delivery lifecycle --service <name>")
	}
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	asOf := d.Now().UTC().Format(time.RFC3339)
	w, err := d.auditStages(ctx, q.Namespace, delivery.ServiceRef{Name: q.Service, Domain: q.Domain})
	if err != nil {
		return nil, err
	}
	ds, from, note := delivery.LifecycleDigests(q, w.Stages)
	revs := delivery.DescribeAll(ctx, len(ds), func(i int) (*delivery.StageObject, string) { return from[i], ds[i] }, d.Describe)
	r, err := delivery.ResolveLifecycleDigest(q, revs)
	if err != nil {
		return nil, err
	}
	hs := d.Histories(ctx, w.Stages)
	l := delivery.LifecycleOf(r, w.Stages, hs.Histories)
	l.AsOf, l.Service, l.Domain = asOf, q.Service, q.Domain
	l.Unreadable = append(w.Forbidden, hs.Unreadable...)
	if len(l.Unreadable) > 0 {
		l.Completeness = "partial"
	}
	l.Note += note
	return &l, nil
}
