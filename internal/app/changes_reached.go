package app

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ReachedAnswer is when each contract of a service reached each Stage the
// caller may read (fleet.ChangeHistoryPreview.Times says how they were read).
type ReachedAnswer struct {
	// ByContract is keyed by contract digest.
	ByContract map[string][]fleet.ChangeReach
	Times      string
	Reason     string
	Unreadable []string
}

// Reached reads, as the caller, when each contract of service svc first
// reached each of its Stages (delivery.Arrivals). A refused Stage list is
// hidden; refused namespaces or unreadable Stages make it partial and are
// named; any other error reads nothing. An error is never "not recorded".
func (d *Delivery) Reached(ctx context.Context, svc fleet.ServiceKey) ReachedAnswer {
	ctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	domain, name := fleet.ParseServiceKey(svc)
	w, err := d.auditStages(ctx, "", delivery.ServiceRef{Domain: domain, Name: name})
	switch {
	case apierrors.IsForbidden(err):
		return ReachedAnswer{Times: fleet.TimesHidden, Reason: err.Error()}
	case err != nil:
		return ReachedAnswer{Times: fleet.TimesNotRead, Reason: "delivery could not be read: " + err.Error()}
	case len(w.Forbidden) > 0 && len(w.Listed) == 0:
		return ReachedAnswer{Times: fleet.TimesHidden, Reason: "no namespace's Stages could be read", Unreadable: w.Forbidden}
	}
	hs := d.Histories(ctx, w.Stages)
	out := ReachedAnswer{ByContract: delivery.Arrivals(hs.Histories, d.contractsOf(ctx, w.Stages, hs.Histories)), Times: fleet.TimesRead,
		Unreadable: append(w.Forbidden, hs.Unreadable...)}
	if len(out.Unreadable) > 0 {
		out.Times, out.Reason = fleet.TimesPartial, fmt.Sprintf("%d namespaces or Stages could not be read", len(out.Unreadable))
	}
	return out
}

// contractsOf reads the artifact every move of hs brought, at once, and
// answers the contract digest each carries ("" when unread).
func (d *Delivery) contractsOf(ctx context.Context, stages []delivery.StageObject, hs []delivery.StageHistory) func(string) string {
	read := d.describeOn(ctx, historyDigests(stages, hs)...)
	return func(dg string) string { return delivery.ContractDigest(read[dg].Contract) }
}
