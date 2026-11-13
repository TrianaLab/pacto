package dashboard

import (
	"context"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// fleetImpactEvent answers what happens if b.ServiceKey becomes unavailable,
// is removed or regressed, or b.OfferingKey (every provider of it) becomes
// unavailable or is removed, over the viewer's snapshot. A service or an
// offering the viewer may not see answers exactly as one that does not exist.
func (s *Server) fleetImpactEvent(ctx context.Context, q *fleet.Query, b impactRequest) (*fleetImpactPostOutput, error) {
	if err := eventRequestErr(b); err != nil {
		return nil, err
	}
	// Read only: neither the lookup nor BuildProductOutcome keeps or changes
	// it, so no clone of a fleet-sized snapshot per request.
	snap := q.SnapshotForSerialization()
	key := fleet.ServiceKey(b.ServiceKey)
	switch {
	case b.OfferingKey != "" && !providesOffering(snap, b.OfferingKey):
		return nil, huma.Error404NotFound("no offering with this offeringKey")
	case b.OfferingKey == "" && snap.Services[key] == nil:
		return nil, huma.Error404NotFound("no service with this serviceKey")
	}
	ev := impact.Event{Kind: b.Event, Service: b.ServiceKey, Offering: b.OfferingKey, Revision: b.Revision}
	var reg *impact.Regression
	if ev.Kind == impact.EventRegressed {
		reg = s.regressionEvidence(ctx, key, ev.Revision)
	}
	res := impact.Analyze(ctx, nil, nil, nil, nil, snap, impact.Options{Event: &ev, IncludeObserved: b.IncludeObserved, Regression: reg})
	return &fleetImpactPostOutput{Body: toProductImpact(impact.BuildProductOutcome(q.ProductMeta(), snap, key, nil, nil, res, b.Limit, b.Offset, b.Outcome))}, nil
}

// eventRequestErr refuses an event request that does not name exactly one
// subject, or that pairs a field with the wrong event.
func eventRequestErr(b impactRequest) error {
	switch {
	case (b.ServiceKey == "") == (b.OfferingKey == ""):
		return huma.Error422UnprocessableEntity("serviceKey", errors.New("an event request needs exactly one of serviceKey and offeringKey"))
	case b.FromRevisionKey != "" || b.ToRevisionKey != "":
		return huma.Error422UnprocessableEntity("event", errors.New("an event is asked of one service or offering: send it without fromRevisionKey and toRevisionKey"))
	case b.OfferingKey != "" && b.Event == impact.EventRegressed:
		return huma.Error422UnprocessableEntity("offeringKey", errors.New("event regressed names one service: a regression belongs to one revision"))
	case b.Event == impact.EventRegressed && b.Revision == "":
		return huma.Error422UnprocessableEntity("revision", errors.New("event regressed needs the revision that regressed"))
	case b.Event != impact.EventRegressed && b.Revision != "":
		return huma.Error422UnprocessableEntity("revision", errors.New("revision is read only with event regressed"))
	}
	return nil
}

// providesOffering reports whether a service of snap provides offering. The
// snapshot is the viewer's, so an offering whose every provider is hidden is
// provided by none and answers as a missing one.
func providesOffering(snap *fleet.FleetSnapshot, offering string) bool {
	for _, svc := range snap.Services {
		if svc.Offering == offering {
			return true
		}
	}
	return false
}

// regressionEvidence reads what delivery recorded against digest on the
// Stages of service, as the caller: the Stages that list it as failed, their
// re-check failures and the rollbacks off it. What could not be read is named
// in Unavailable, never taken as "no failure".
func (s *Server) regressionEvidence(ctx context.Context, service fleet.ServiceKey, digest string) *impact.Regression {
	if s.opts.Delivery == nil {
		return &impact.Regression{Unavailable: "this host has no cluster to read delivery from"}
	}
	if !strings.HasPrefix(digest, "sha256:") {
		return &impact.Regression{Unavailable: "name the artifact digest (sha256:...) to read delivery evidence: a version does not identify one artifact"}
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return &impact.Regression{Unavailable: deliveryReason(err)}
	}
	all, _, listing, err := s.lists(ctx, api, false)
	if err != nil {
		return &impact.Regression{Unavailable: deliveryReason(err)}
	}
	domain, name := fleet.ParseServiceKey(service)
	var stages []delivery.StageObject
	for _, st := range all {
		if delivery.OfService(st, name, domain) {
			stages = append(stages, st)
		}
	}
	// Every Stage's history is read: a person's rollback off the digest
	// leaves failedDigests unchanged, so the list cannot narrow the read.
	var why []string
	if len(listing.Unreadable) > 0 {
		why = append(why, "Stages in these namespaces were not read (* is every namespace outside the fleet's): "+strings.Join(listing.Unreadable, ", "))
	}
	var histories []delivery.StageHistory
	hs, errs := historiesOf(ctx, api, stages, true)
	for i, h := range hs {
		where := stages[i].Metadata.Namespace + "/" + stages[i].Metadata.Name
		switch {
		case errs[i] != nil:
			why = append(why, "the history of "+where+" was not read: "+deliveryReason(errs[i]))
		case h != nil:
			histories = append(histories, delivery.StageHistory{Namespace: stages[i].Metadata.Namespace, History: *h})
		}
	}
	evidence, unknown := delivery.RegressionEvidence(stages, histories, digest)
	return &impact.Regression{Evidence: evidence, Unavailable: strings.Join(append(why, unknown...), "; ")}
}

// deliveryReason is why a delivery read failed: a refusal's own detail, or
// the error.
func deliveryReason(err error) string {
	var de *DeliveryError
	if errors.As(err, &de) {
		return de.Detail
	}
	return "delivery evidence could not be read: " + err.Error()
}
