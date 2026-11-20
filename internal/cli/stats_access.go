package cli

import (
	"context"
	"errors"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

var errOperationReview = errors.New("the apiserver could not review whether you may list operations")

// operationAccess says which Stage namespaces a signed-in caller may list
// operations in, by the caller's own access review through act.Can, and
// that it was reviewed in home, the sign-in cluster, alone: delivery is one
// cluster. Only namespaces the full snapshot knows in home are reviewed, so
// registry data cannot choose what is asked; any other namespace is refused
// without a review. A failed review is the error, never a smaller answer,
// and the error names no namespace: the caller's view of the fleet may not
// hold it.
// ponytail: a Stage namespace with no Kubernetes target in the snapshot reads
// as refused; widen the known set to listed Stage namespaces if that hides
// real delivery.
func operationAccess(act *app.Delivery, full *fleet.Query, home func(context.Context) (string, bool)) func(ctx context.Context, nss []string) (stats.OperationAccess, error) {
	return func(ctx context.Context, nss []string) (stats.OperationAccess, error) {
		known, clusters := homeNamespaces(ctx, home, full.Namespaces())
		visible, err := reviewOperations(ctx, known, nss, func(ctx context.Context, ns string) (bool, error) {
			return act.Can(ctx, "list", "operations", ns)
		})
		return stats.OperationAccess{Visible: visible, Clusters: clusters}, err
	}
}

// homeNamespaces is the sorted, unique namespaces of pairs in the sign-in
// cluster, whose cached id home reads, or with no cluster, and the clusters
// operation records may come from: that id, or none while it cannot be read
// (nil home included), so every record naming a cluster is hidden.
func homeNamespaces(ctx context.Context, home func(context.Context) (string, bool), pairs []fleet.ClusterNamespace) (known, clusters []string) {
	uid, ok := "", false
	if home != nil {
		uid, ok = home(ctx)
	}
	clusters = []string{}
	if ok {
		clusters = append(clusters, uid)
	}
	known = make([]string, 0, len(pairs))
	for _, cn := range pairs {
		if cn.Cluster == "" || ok && cn.Cluster == uid {
			known = append(known, cn.Namespace)
		}
	}
	slices.Sort(known)
	return slices.Compact(known), clusters
}

// reviewOperations asks may, at most reviewsInFlight at once, about each of
// nss that the sorted known holds, and says which may list operations; any
// other namespace is refused without a review. A failed review is the error,
// naming no namespace.
func reviewOperations(ctx context.Context, known, nss []string, may func(ctx context.Context, ns string) (bool, error)) (func(string) bool, error) {
	var review []string
	for _, ns := range nss {
		if _, ok := slices.BinarySearch(known, ns); ok {
			review = append(review, ns)
		}
	}
	allowed := make([]bool, len(review))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(reviewsInFlight)
	for i, ns := range review {
		g.Go(func() (err error) { allowed[i], err = may(gctx, ns); return err })
	}
	if err := g.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errOperationReview
	}
	visible := map[string]bool{}
	for i, ns := range review {
		visible[ns] = allowed[i]
	}
	return func(ns string) bool { return visible[ns] }, nil
}
