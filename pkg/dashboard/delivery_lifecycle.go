package dashboard

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

type lifecycleInput struct {
	Service  string `query:"service" required:"true" doc:"The service, by exact name"`
	Domain   string `query:"domain" doc:"The service's registry domain, empty for the default. A Stage that names another domain is left out; one that names none matches any"`
	Digest   string `query:"digest" doc:"The artifact digest Stages point at (one of digest, version or contract)"`
	Version  string `query:"version" doc:"The version its contract declares (one of digest, version or contract)"`
	Contract string `query:"contract" doc:"Its contract: a manifest digest or a digest-pinned reference (one of digest, version or contract)"`
}

type lifecycleOutput struct {
	Body struct {
		delivery.Lifecycle
		Labels
	}
}

// deliveryLifecycle follows one artifact of a service through its Stages the
// caller can see. The Stages come from lists, already scoped to this
// dashboard's namespaces; a version or contract is found among at most
// delivery.LifecycleDigestLimit artifacts they know.
func (s *Server) deliveryLifecycle(ctx context.Context, in *lifecycleInput) (*lifecycleOutput, error) {
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	q := delivery.LifecycleQuery{Service: in.Service, Domain: in.Domain, Digest: in.Digest, Version: in.Version, Contract: in.Contract}
	if err := q.Check(); err != nil {
		return nil, Refuse(http.StatusBadRequest, "LifecycleInvalid", err.Error())
	}
	stages, _, l, err := s.lists(ctx, api, false)
	if err != nil {
		return nil, err
	}
	stages = slices.DeleteFunc(stages, func(st delivery.StageObject) bool { return !delivery.OfService(st, in.Service, in.Domain) })
	ds, from, gap := delivery.LifecycleDigests(q, stages)
	revs := describeAll(ctx, api, len(ds), func(i int) (*delivery.StageObject, string) { return from[i], ds[i] })
	r, note, err := delivery.ResolveLifecycleDigest(q, revs, delivery.LifecycleGaps(gap, l.Unreadable, l.Completeness != "complete"))
	if miss := (*delivery.LifecycleMiss)(nil); errors.As(err, &miss) && miss.Definite {
		return nil, Refuse(http.StatusNotFound, "LifecycleNotFound", err.Error())
	}
	if err != nil {
		return nil, Refuse(http.StatusBadRequest, "LifecycleUnresolved", err.Error())
	}
	hs, errs := historiesOf(ctx, api, stages, true)
	var read []delivery.StageHistory
	for i, h := range hs {
		switch {
		case errs[i] != nil:
			l.Unreadable = append(l.Unreadable, stages[i].Metadata.Namespace+"/"+stages[i].Metadata.Name+": "+errs[i].Error())
		case h != nil:
			sh := delivery.StageHistory{Namespace: stages[i].Metadata.Namespace, History: *h}
			sh.Stage.Name = stages[i].Metadata.Name // read by index: the host may name it otherwise
			read = append(read, sh)
		}
	}
	lc := delivery.LifecycleOf(r, stages, read)
	lc.AsOf, lc.Service, lc.Domain, lc.Unreadable, lc.Note = l.AsOf, in.Service, in.Domain, l.Unreadable, lc.Note+note
	if len(lc.Unreadable) > 0 || l.Completeness != "complete" || note != "" {
		lc.Completeness = "partial"
	}
	out := &lifecycleOutput{}
	out.Body.Lifecycle = lc
	described := map[string]delivery.Described{r.Digest: {Version: lc.Version, Contract: r.ContractRef}}
	on := digestsOn{&delivery.StageObject{Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: in.Service, Domain: in.Domain}}}, []string{r.Digest}}
	out.Body.Labels = Labels{Versions: versionsIn(nil, described), Revisions: s.revisionLabels(ctx, described, on)}
	return out, nil
}
