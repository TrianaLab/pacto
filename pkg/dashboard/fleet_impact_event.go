package dashboard

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// fleetImpactEvent answers what happens if b.ServiceKey becomes unavailable,
// is removed or regressed, over the viewer's snapshot. A service the viewer
// may not see answers exactly as one that does not exist.
func (s *Server) fleetImpactEvent(ctx context.Context, q *fleet.Query, b impactRequest) (*fleetImpactPostOutput, error) {
	switch {
	case b.ServiceKey == "":
		return nil, huma.Error422UnprocessableEntity("serviceKey", errors.New("an event request needs serviceKey"))
	case b.FromRevisionKey != "" || b.ToRevisionKey != "":
		return nil, huma.Error422UnprocessableEntity("event", errors.New("an event is asked of one service: send serviceKey without fromRevisionKey and toRevisionKey"))
	case b.Event == impact.EventRegressed && b.Revision == "":
		return nil, huma.Error422UnprocessableEntity("revision", errors.New("event regressed needs the revision that regressed"))
	case b.Event != impact.EventRegressed && b.Revision != "":
		return nil, huma.Error422UnprocessableEntity("revision", errors.New("revision is read only with event regressed"))
	}
	snap := q.Snapshot()
	key := fleet.ServiceKey(b.ServiceKey)
	if snap.Services[key] == nil {
		return nil, huma.Error404NotFound("no service with this serviceKey")
	}
	ev := impact.Event{Kind: b.Event, Service: b.ServiceKey, Revision: b.Revision}
	var reg *impact.Regression
	if ev.Kind == impact.EventRegressed {
		reg = s.regressionEvidence(ctx, key, ev.Revision)
	}
	res := impact.Analyze(ctx, nil, nil, nil, nil, q.SnapshotForSerialization(), impact.Options{Event: &ev, IncludeObserved: b.IncludeObserved, Regression: reg})
	return &fleetImpactPostOutput{Body: toProductImpact(impact.BuildProductOutcome(q.ProductMeta(), snap, key, nil, nil, res, b.Limit, b.Offset, b.Outcome))}, nil
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
	var stages, failed []delivery.StageObject
	for _, st := range all {
		if delivery.OfService(st, name, domain) {
			stages = append(stages, st)
			if slices.Contains(st.Status.FailedDigests, digest) {
				failed = append(failed, st)
			}
		}
	}
	// Only a Stage that lists the digest as failed can hold a rollback off it
	// worth citing, so only those histories are read.
	var why []string
	if len(listing.Unreadable) > 0 {
		why = append(why, "Stages in these namespaces were not read (* is every namespace outside the fleet's): "+strings.Join(listing.Unreadable, ", "))
	}
	var histories []delivery.StageHistory
	hs, errs := historiesOf(ctx, api, failed, true)
	for i, h := range hs {
		where := failed[i].Metadata.Namespace + "/" + failed[i].Metadata.Name
		switch {
		case errs[i] != nil:
			why = append(why, "the history of "+where+" was not read: "+deliveryReason(errs[i]))
		case h != nil:
			histories = append(histories, delivery.StageHistory{Namespace: failed[i].Metadata.Namespace, History: *h})
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
