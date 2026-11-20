package cli

import (
	"context"
	"errors"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var errOperationReview = errors.New("the apiserver could not review whether you may list operations")

// operationAccess says which Stage namespaces a signed-in caller may list
// operations in, by the caller's own access review through act.Can.
// Only namespaces the full snapshot knows are reviewed, so registry data
// cannot choose what is asked; any other namespace is refused without a
// review. A failed review is the error, never a smaller answer, and the
// error names no namespace: the caller's view of the fleet may not hold it.
// ponytail: a Stage namespace with no Kubernetes target in the snapshot reads
// as refused; widen the known set to listed Stage namespaces if that hides
// real delivery.
func operationAccess(act *app.Delivery, full *fleet.Query) func(ctx context.Context, nss []string) (func(string) bool, error) {
	return func(ctx context.Context, nss []string) (func(string) bool, error) {
		return reviewOperations(ctx, knownNamespaces(full.Namespaces()), nss, func(ctx context.Context, ns string) (bool, error) {
			return act.Can(ctx, "list", "operations", ns)
		})
	}
}

// knownNamespaces is the sorted, unique namespaces of pairs, whatever their
// cluster. ponytail: adapter until P5 scopes operation records by cluster.
func knownNamespaces(pairs []fleet.ClusterNamespace) []string {
	out := make([]string, 0, len(pairs))
	for _, cn := range pairs {
		out = append(out, cn.Namespace)
	}
	slices.Sort(out)
	return slices.Compact(out)
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
