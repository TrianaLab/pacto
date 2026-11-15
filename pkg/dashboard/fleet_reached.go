package dashboard

import (
	"context"
	"fmt"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// attachReached attaches to h when each change of service svc reached its
// Stages, read as the caller (deliveryFor): what the caller may not list
// decides hidden or partial, never the number of changes. Nothing it reads
// is cached.
// ponytail: every Stage of the service is read at once; page them as the
// history route does if a service ever has hundreds of Stages.
func (s *Server) attachReached(ctx context.Context, q *fleet.Query, svc fleet.ServiceKey, h *fleet.ChangeHistoryPreview) {
	if s.opts.Delivery == nil {
		q.AttachReached(h, nil, fleet.TimesNotRead, "this host has no cluster to read delivery from", nil)
		return
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		q.AttachReached(h, nil, fleet.TimesNotRead, err.Error(), nil)
		return
	}
	stages, _, l, err := s.lists(ctx, api, false)
	switch {
	case forbidden(err):
		q.AttachReached(h, nil, fleet.TimesHidden, err.Error(), nil)
		return
	case err != nil:
		q.AttachReached(h, nil, fleet.TimesNotRead, "delivery could not be read: "+err.Error(), nil)
		return
	}
	domain, name := fleet.ParseServiceKey(svc)
	stages = slices.DeleteFunc(stages, func(st delivery.StageObject) bool { return !delivery.OfService(st, name, domain) })
	hs, on, unreadable := readHistories(ctx, api, stages)
	unreadable = append(slices.Clone(l.Unreadable), unreadable...)
	read := describedAll(ctx, api, nil, on...)
	byContract := delivery.Arrivals(hs, func(d string) string { return delivery.ContractDigest(read[d].Contract) })
	times, reason := fleet.TimesRead, ""
	if len(unreadable) > 0 || l.Completeness == "partial" {
		times, reason = fleet.TimesPartial, fmt.Sprintf("%d namespaces or Stages could not be read", len(unreadable))
	}
	q.AttachReached(h, byContract, times, reason, unreadable)
}

// readHistories reads each of stages' histories and the digests each names
// on its Stage; a Stage whose history cannot be read is named in unreadable.
func readHistories(ctx context.Context, api DeliveryAPI, stages []delivery.StageObject) (out []delivery.StageHistory, on []digestsOn, unreadable []string) {
	hs, errs := historiesOf(ctx, api, stages, true)
	for i, h := range hs {
		if errs[i] != nil {
			unreadable = append(unreadable, stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name+": "+errs[i].Error())
			continue
		}
		out = append(out, delivery.StageHistory{Namespace: stages[i].Metadata.Namespace, History: *h})
		on = append(on, digestsOn{stage: &stages[i], digests: delivery.HistoryDigests(*h)})
	}
	return out, on, unreadable
}
