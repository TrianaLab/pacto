package cli

import (
	"context"
	"slices"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// joinListTTL is how long a served join list is kept: each read lists the
// registry and every member's records.
// ponytail: one list per host for 30s; per-cluster invalidation if people
// need an approval to show sooner than that.
const joinListTTL = 30 * time.Second

// clusterJoinsGVR is the virtual resource a person may approve.
var clusterJoinsGVR = schema.GroupVersionResource{Group: authn.ClusterJoinResource.Group, Resource: authn.ClusterJoinResource.Resource}

// joinLister lists opts' joins, read again at most once per joinListTTL;
// nil without opts. A read outlives the request that started it, as every
// waiter shares it.
func joinLister(svc *app.Service, opts *app.JoinOptions, now func() time.Time) func(context.Context) delivery.ClusterJoinList {
	if opts == nil {
		return nil
	}
	var mu sync.Mutex
	var last delivery.ClusterJoinList
	var at time.Time
	return func(ctx context.Context) delivery.ClusterJoinList {
		mu.Lock()
		defer mu.Unlock()
		if at.IsZero() || now().Sub(at) >= joinListTTL {
			last, at = svc.ClusterJoins(context.WithoutCancel(ctx), *opts), now()
		}
		return last
	}
}

// dashboardJoins are how a dashboard in the hub reads its members: in the
// sign-in cluster, from the registry its operator's detection advertises,
// with the approver signers published in policyNS, resolved at each read.
// nil without a sign-in cluster or a detection.
func dashboardJoins(d deps, cluster *rest.Config, detected, policyNS string) *app.JoinOptions {
	if cluster == nil || detected == "" {
		return nil
	}
	return &app.JoinOptions{Resolve: func(ctx context.Context) (app.JoinOptions, error) {
		dyn, err := d.dynamicFor(cluster)
		if err != nil {
			return app.JoinOptions{}, err
		}
		return joinsOn(ctx, dyn, "", detected, policyNS)
	}}
}

// clusterJoins serves list as each request's reader may see it: a
// signed-in person sees pending and removed clusters only when the hub lets
// them approve clusterjoins (reviewed through the shared cache); a dashboard
// without sign-in serves the whole list. nil without joins.
func (id *dashboardIdentity) clusterJoins(list func(context.Context) delivery.ClusterJoinList) func(context.Context) (delivery.ClusterJoinList, error) {
	if list == nil {
		return nil
	}
	return func(ctx context.Context) (delivery.ClusterJoinList, error) {
		l := list(ctx)
		if id.viewer == nil {
			return visibleJoins(l, true, nil), nil
		}
		who, err := id.subject(ctx)
		may := false
		if err == nil {
			may, err = id.shared.may(ctx, who, authn.ClusterJoinResource.Verb, clusterJoinsGVR, "", "")
		}
		return visibleJoins(l, may, err), nil
	}
}

// mcpClusters serves pacto_cluster_list over joins: to a server whose
// callers sign in (restricted), each caller's list as the hub's
// SubjectAccessReview lets them approve clusterjoins; to any other, the
// whole list. nil without joins.
func mcpClusters(svc *app.Service, d deps, joins *app.JoinOptions, restricted bool) pactomcp.ClustersProvider {
	list := joinLister(svc, joins, time.Now)
	if list == nil {
		return nil
	}
	if !restricted {
		return func(ctx context.Context) (delivery.ClusterJoinList, error) {
			return visibleJoins(list(ctx), true, nil), nil
		}
	}
	cluster, _ := d.clusterConfig()
	review := clusterReview(cluster, d.dynamicFor, reviewCluster)
	return func(ctx context.Context) (delivery.ClusterJoinList, error) {
		l := list(ctx)
		p, ok := personFrom(ctx)
		if !ok {
			return visibleJoins(l, false, errNobodyReviewed), nil
		}
		may, err := review(ctx, p, "", authn.ClusterJoinResource)
		return visibleJoins(l, may, err), nil
	}
}

// approverReview names the review behind visibleJoins' limitation.
const approverReview = "cluster-join-approver"

// visibleJoins is l as one reader sees it: approved members always, without
// their group mappings and inventory environments unless the reader may
// approve; pending and removed rows and the candidate clusters only when
// may says the reader may approve clusterjoins (an unrestricted reader
// passes true), otherwise counted in Hidden and HiddenCandidates. A review
// that failed (err) hides and counts them too and makes the list partial:
// a reader is never shown an empty list for a forbidden one.
func visibleJoins(l delivery.ClusterJoinList, may bool, err error) delivery.ClusterJoinList {
	l.CanApprove = may && err == nil
	if l.CanApprove {
		return l
	}
	rows := make([]delivery.ClusterJoinState, 0, len(l.Clusters))
	for _, c := range l.Clusters {
		if c.State == delivery.JoinApproved {
			// The group mappings name a member's RBAC groups and approver:
			// for those who may approve. Readers see the mapping in force
			// on the link of a cluster their view shows.
			c.Mappings = nil
			// Inventory environments name objects in the hub's GitOps
			// namespaces, as the candidates do.
			c.InventoryEnvironments = nil
			rows = append(rows, c)
		}
	}
	l.Hidden += len(l.Clusters) - len(rows)
	l.Clusters = rows
	l.HiddenCandidates += len(l.Candidates)
	l.Candidates, l.CandidatesAsOf, l.CandidatesCompleteness = nil, "", ""
	l.Limitations = slices.DeleteFunc(slices.Clone(l.Limitations), func(lim fleet.Limitation) bool { return lim.Source == delivery.CandidatesID })
	if err != nil {
		l.Limitations = append(slices.Clone(l.Limitations), fleet.Limitation{Code: fleet.LimitationSourceUnavailable, Source: approverReview,
			Message: "whether you may approve clusterjoins could not be checked, so pending and removed clusters are hidden: " + err.Error()})
		l.Completeness = fleet.CompletenessPartial
	}
	return l
}
