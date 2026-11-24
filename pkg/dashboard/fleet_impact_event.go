package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/externalid"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// fleetImpactEvent answers what happens if b.ServiceKey becomes unavailable,
// is removed or regressed, or b.OfferingKey (every provider of it) becomes
// unavailable or is removed, or b.ExternalKey changes or becomes unavailable,
// over the viewer's snapshot. A service, an offering or an external system
// the viewer may not see answers exactly as one that does not exist.
func (s *Server) fleetImpactEvent(ctx context.Context, q *fleet.Query, b impactRequest) (*fleetImpactPostOutput, error) {
	if err := eventRequestErr(b); err != nil {
		return nil, err
	}
	// Read only: neither the lookup nor BuildProductOutcome keeps or changes
	// it, so no clone of a fleet-sized snapshot per request.
	snap := q.SnapshotForSerialization()
	key := fleet.ServiceKey(b.ServiceKey)
	switch {
	case b.ExternalKey != "":
		// The viewer's snapshot holds no external only hidden consumers name.
		if snap.Externals[fleet.ExternalKey(b.ExternalKey)] == nil {
			return nil, huma.Error404NotFound("no external system with this externalKey")
		}
	case b.OfferingKey != "" && !providesOffering(snap, b.OfferingKey):
		return nil, huma.Error404NotFound("no offering with this offeringKey")
	case b.OfferingKey == "" && snap.Services[key] == nil:
		return nil, huma.Error404NotFound("no service with this serviceKey")
	}
	ev := impact.Event{Kind: b.Event, Service: b.ServiceKey, Offering: b.OfferingKey, External: b.ExternalKey, Revision: b.Revision}
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
	if err := eventSubjectErr(b); err != nil {
		return err
	}
	switch {
	case b.FromRevisionKey != "" || b.ToRevisionKey != "":
		return huma.Error422UnprocessableEntity("event", errors.New("an event is asked of one service, offering or external system: send it without fromRevisionKey and toRevisionKey"))
	case b.OfferingKey != "" && b.Event == impact.EventRegressed:
		return huma.Error422UnprocessableEntity("offeringKey", errors.New("event regressed names one service: a regression belongs to one revision"))
	case b.Event == impact.EventRegressed && b.Revision == "":
		return huma.Error422UnprocessableEntity("revision", errors.New("event regressed needs the revision that regressed"))
	case b.Event != impact.EventRegressed && b.Revision != "":
		return huma.Error422UnprocessableEntity("revision", errors.New("revision is read only with event regressed"))
	}
	return nil
}

// eventSubjectErr refuses an event request that names no subject or more
// than one, or asks an external system an event it cannot answer.
func eventSubjectErr(b impactRequest) error {
	subjects := 0
	for _, k := range []string{b.ServiceKey, b.OfferingKey, b.ExternalKey} {
		if k != "" {
			subjects++
		}
	}
	switch {
	case subjects != 1:
		return huma.Error422UnprocessableEntity("serviceKey", errors.New("an event request needs exactly one of serviceKey, offeringKey and externalKey"))
	case b.Event == impact.EventChanged && b.ExternalKey == "":
		return huma.Error422UnprocessableEntity("event", errors.New("event changed is asked of an external system: send externalKey"))
	case b.ExternalKey != "" && b.Event != impact.EventChanged && b.Event != impact.EventUnavailable:
		return huma.Error422UnprocessableEntity("externalKey", errors.New("an external system is asked with event changed or unavailable"))
	}
	if b.ExternalKey != "" {
		// Syntax only, before the existence check: a malformed key is in no
		// snapshot, so refusing it reveals nothing about hidden externals.
		if _, err := externalid.Parse(b.ExternalKey); err != nil {
			return huma.Error422UnprocessableEntity("externalKey", fmt.Errorf("%w; find its identity with pacto fleet search", err))
		}
	}
	return nil
}

// providesOffering reports whether a service of snap provides offering. The
// snapshot is the viewer's, so an offering whose every provider is hidden is
// provided by none and answers as a missing one.
func providesOffering(snap *fleet.FleetSnapshot, offering string) bool {
	for _, svc := range snap.Services {
		if svc.Offering == offering && !svc.Inferred() {
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
