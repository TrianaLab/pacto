package cli

import (
	"context"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// operationAccess says which Stage namespaces a signed-in caller may list
// operations in, by the caller's own access review through act.Can.
// Only namespaces the full snapshot knows are reviewed, so registry data
// cannot choose what is asked; any other namespace is refused without a
// review. A failed review is the error, never a smaller answer.
// ponytail: a Stage namespace with no Kubernetes target in the snapshot reads
// as refused; widen the known set to listed Stage namespaces if that hides
// real delivery.
func operationAccess(act *app.Delivery, full *fleet.Query) func(ctx context.Context, nss []string) (func(string) bool, error) {
	return func(ctx context.Context, nss []string) (func(string) bool, error) {
		known := full.Namespaces() // sorted
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
			g.Go(func() (err error) { allowed[i], err = act.Can(gctx, "list", "operations", ns); return err })
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		visible := map[string]bool{}
		for i, ns := range review {
			visible[ns] = allowed[i]
		}
		return func(ns string) bool { return visible[ns] }, nil
	}
}
