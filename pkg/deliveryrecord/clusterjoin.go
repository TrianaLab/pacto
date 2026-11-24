package deliveryrecord

import (
	"context"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// ListClusterJoins reads the join records of cluster uid in repo, its
// pacto-targets repository, as [Reader.ListPredicate] reads them: at most
// delivery.MaxJoinRecords distinct bundles, cut when more matched, unverified.
// forks names the write-once tags that vanished or do not name the manifest
// they point at: a tag someone moved is never read as a record.
func (r *Reader) ListClusterJoins(ctx context.Context, repo *remote.Repository, uid string) (recs []Fetched, cut bool, forks []string, err error) {
	recs, cut, err = r.listPredicate(ctx, repo, delivery.ClusterJoinStream(uid), delivery.PredicateClusterJoin, "", delivery.MaxJoinRecords, &forks, nil)
	return recs, cut, forks, err
}

// EnsureAnchor pushes and tags stream's anchor in repo when it is missing,
// and probes the registry for the form records of it take there.
func EnsureAnchor(ctx context.Context, repo *remote.Repository, stream string) (capabilities.Capabilities, error) {
	art, tag := Anchor(stream), AnchorTag(stream)
	if got, err := repo.Resolve(ctx, tag); err != nil || got.Digest != art.ManifestDesc.Digest {
		if err := ocireferrer.Push(ctx, repo, art); err != nil {
			return capabilities.Capabilities{}, err
		}
		if err := repo.Tag(ctx, art.ManifestDesc, tag); err != nil {
			return capabilities.Capabilities{}, err
		}
	}
	return ocireferrer.Probes.Probe(ctx, repo, art.ManifestDesc.Digest.String(), true)
}

// PushClusterJoin writes bundle, a signed join record
// (delivery.SignClusterJoin), on its cluster's join stream in repo, the
// pacto-targets repository, anchoring the stream first, and returns where it
// is and the form it took.
func PushClusterJoin(ctx context.Context, repo *remote.Repository, bundle []byte) (string, capabilities.ReferrersMode, error) {
	st, err := delivery.OpenClusterJoin(bundle, func([]byte) error { return nil }) // its own signer made it
	if err != nil {
		return "", "", err
	}
	stream := delivery.ClusterJoinStream(st.Predicate.Cluster)
	caps, err := EnsureAnchor(ctx, repo, stream)
	if err != nil {
		return "", "", err
	}
	ref, err := Push(ctx, repo, stream, caps.Referrers, delivery.PredicateClusterJoin, bundle, nil)
	return ref, caps.Referrers, err
}

// ListGroupMappings reads the group mapping records of cluster uid in repo,
// its pacto-targets repository, as [Reader.ListPredicate] reads them: at
// most delivery.MaxJoinRecords distinct bundles, cut when more matched,
// unverified.
func (r *Reader) ListGroupMappings(ctx context.Context, repo *remote.Repository, uid string) ([]Fetched, bool, error) {
	return r.ListPredicate(ctx, repo, delivery.ClusterJoinStream(uid), delivery.PredicateClusterGroupMapping, "", delivery.MaxJoinRecords)
}

// PushClusterGroupMapping writes bundle, a signed group mapping
// (delivery.SignClusterGroupMapping), on its member's join stream in repo,
// the pacto-targets repository, anchoring the stream first, and returns
// where it is and the form it took.
func PushClusterGroupMapping(ctx context.Context, repo *remote.Repository, bundle []byte) (string, capabilities.ReferrersMode, error) {
	st, err := delivery.OpenClusterGroupMapping(bundle, func([]byte) error { return nil }) // its own signer made it
	if err != nil {
		return "", "", err
	}
	stream := delivery.ClusterJoinStream(st.Predicate.Cluster)
	caps, err := EnsureAnchor(ctx, repo, stream)
	if err != nil {
		return "", "", err
	}
	ref, err := Push(ctx, repo, stream, caps.Referrers, delivery.PredicateClusterGroupMapping, bundle, nil)
	return ref, caps.Referrers, err
}
