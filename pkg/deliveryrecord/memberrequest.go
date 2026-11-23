package deliveryrecord

import (
	"context"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// ListOperationRequests reads the requests on member uid's request stream in
// repo, its pacto-targets repository, as [Reader.ListPredicate] reads them:
// at most delivery.MaxOperationRequests distinct bundles, cut when more
// matched, unverified. The caller [Reader.Reject]s every request it is done
// with, so newer ones are read on the next call.
func (r *Reader) ListOperationRequests(ctx context.Context, repo *remote.Repository, uid string) ([]Fetched, bool, error) {
	return r.ListPredicate(ctx, repo, delivery.ClusterRequestStream(uid), delivery.PredicateOperationRequest, "", delivery.MaxOperationRequests)
}

// ListRequestOutcomes reads the outcomes of one request, by its manifest
// digest, on member uid's request stream in repo: at most
// delivery.MaxRequestOutcomes distinct bundles, cut when more matched,
// unverified.
func (r *Reader) ListRequestOutcomes(ctx context.Context, repo *remote.Repository, uid, request string) ([]Fetched, bool, error) {
	return r.ListPredicate(ctx, repo, delivery.ClusterRequestStream(uid), delivery.PredicateRequestOutcome, request, delivery.MaxRequestOutcomes)
}

// PushRequestOutcome writes bundle, a signed outcome
// (delivery.SignRequestOutcome), on its member's request stream in repo, the
// pacto-targets repository, anchoring the stream first and keyed by the
// request it answers, and returns where it is and the form it took.
func PushRequestOutcome(ctx context.Context, repo *remote.Repository, bundle []byte) (string, capabilities.ReferrersMode, error) {
	st, err := delivery.OpenRequestOutcome(bundle, nil, "", nil) // its own signer made it
	if err != nil {
		return "", "", err
	}
	stream := delivery.ClusterRequestStream(st.Predicate.Cluster)
	caps, err := EnsureAnchor(ctx, repo, stream)
	if err != nil {
		return "", "", err
	}
	ref, err := Push(ctx, repo, stream, caps.Referrers, delivery.PredicateRequestOutcome, bundle, map[string]string{AnnUID: st.Predicate.Request})
	return ref, caps.Referrers, err
}
