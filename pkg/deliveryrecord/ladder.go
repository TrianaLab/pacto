package deliveryrecord

import (
	"context"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// ListLadder reads the installation ladder records of cluster uid in repo,
// the approvals repository, as [Reader.ListPredicate] reads them: at most
// delivery.MaxLadderRecords distinct bundles, cut when more matched,
// unverified. forks names the write-once tags that vanished or do not name
// the manifest they point at: a tag someone moved is never read as a record.
func (r *Reader) ListLadder(ctx context.Context, repo *remote.Repository, uid string) (recs []Fetched, cut bool, forks []string, err error) {
	recs, cut, err = r.listPredicate(ctx, repo, delivery.LadderStream(uid), delivery.PredicateEnvironmentLadder, "", delivery.MaxLadderRecords, &forks)
	return recs, cut, forks, err
}
