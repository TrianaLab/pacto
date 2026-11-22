package app

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// Per-cluster limitations of group mapping records.
var (
	noteMappingsUnread = clusterNote{fleet.LimitationSourceUnavailable, "the group mappings of %s could not be read, so their reads use none", true}
	noteMappingsCut    = clusterNote{fleet.LimitationTargetSnapshotBound, "%s have more than " + fmt.Sprint(delivery.MaxJoinRecords) +
		" group mapping records and use none until older ones are read", true}
	noteMappingsSkipped = clusterNote{fleet.LimitationTargetSnapshotInvalid, "%s have group mapping records that were not counted", false}
)

// VerifiedGroupMapping is one group mapping record that verified: signed
// under a hub approver signer by the user it names as Approver.
type VerifiedGroupMapping struct {
	Record   delivery.ClusterGroupMapping
	User     string
	Ref      string
	SignedAt time.Time
}

// readMappings sets the group mappings of every approved member in states.
// No record counts in a hub whose approver signers are unknown.
func (jr *joinRead) readMappings(ctx context.Context, states []delivery.ClusterJoinState) {
	if jr.policyWhy != "" {
		return
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(joinReadsAtOnce)
	for i := range states {
		if states[i].State == delivery.JoinApproved {
			g.Go(func() error { states[i].Mappings = jr.mappings(gctx, states[i].UID); return nil })
		}
	}
	_ = g.Wait() // mappings notes its failures
}

// mappings are the group mappings of uid, one per issuer, newest approved
// first. A listing that failed or was cut uses none: the newest record may
// be the one unread. A record that does not verify is rejected, so it never
// takes the bound again.
func (jr *joinRead) mappings(ctx context.Context, uid string) []delivery.GroupMappingState {
	repo, _ := openTargets(jr.opts.Repository) // it opened once
	fetched, cut, err := jr.s.joinRecords.ListGroupMappings(ctx, repo, uid)
	switch {
	case err != nil:
		jr.noteCluster(noteMappingsUnread, uid)
		return nil
	case cut:
		jr.noteCluster(noteMappingsCut, uid)
		return nil
	}
	byIssuer := map[string][]VerifiedGroupMapping{}
	for _, f := range fetched {
		v, why := verifyMapping(jr.policy, f)
		switch {
		case why != "":
			jr.s.joinRecords.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterGroupMapping, f.Manifest)
			jr.noteCluster(noteMappingsSkipped, uid)
		case v.Record.Hub != jr.opts.Hub || v.Record.Cluster != uid:
			jr.mu.Lock()
			jr.otherMappings++ // another hub's, or a misplaced record: never here
			jr.mu.Unlock()
		default:
			byIssuer[v.Record.Issuer] = append(byIssuer[v.Record.Issuer], v)
		}
	}
	var out []delivery.GroupMappingState
	for _, issuer := range slices.Sorted(maps.Keys(byIssuer)) {
		if v := jr.decideMapping(ctx, uid, byIssuer[issuer]); v != nil {
			out = append(out, delivery.GroupMappingState{Issuer: issuer, Rules: v.Record.Rules, ApprovedBy: v.User,
				ApprovedAt: v.SignedAt.UTC().Format(time.RFC3339), Record: v.Ref})
		}
	}
	slices.SortStableFunc(out, func(a, b delivery.GroupMappingState) int { return cmp.Compare(b.ApprovedAt, a.ApprovedAt) })
	return out
}

// decideMapping is the mapping vs, the verified records of one issuer, put
// in force: the newest by verified signing time (At is display only) whose
// approver the hub lets approve the member now, a clear first on a tie. A
// clear in force, or one whose approver may no longer approve, leaves none:
// clearing only narrows reads. A review that fails leaves none, since the
// record it would count may be newer than any in force. Once a record is
// in force the older ones are rejected, so they stop taking the bound.
func (jr *joinRead) decideMapping(ctx context.Context, uid string, vs []VerifiedGroupMapping) *VerifiedGroupMapping {
	slices.SortStableFunc(vs, func(a, b VerifiedGroupMapping) int {
		return cmp.Or(b.SignedAt.Compare(a.SignedAt), cmp.Compare(len(a.Record.Rules), len(b.Record.Rules)))
	})
	for i, v := range vs {
		allowed, err := jr.s.authorizeRecord(ctx, jr.opts.Authorize, v.User, uid, jr.now)
		switch {
		case apierrors.IsForbidden(err):
			jr.noteCluster(noteReviewForbidden, uid)
			return nil
		case err != nil:
			jr.noteCluster(noteReviewFailed, uid)
			return nil
		case allowed:
			for _, old := range vs[i+1:] {
				jr.s.joinRecords.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterGroupMapping, old.Ref)
			}
			if len(v.Record.Rules) == 0 {
				return nil
			}
			return &vs[i]
		case len(v.Record.Rules) == 0:
			return nil
		}
		jr.noteCluster(noteMappingsSkipped, uid)
	}
	return nil
}

// verifyMapping opens f, a group mapping record, as verifyPerson does, and
// says why it does not verify, "" when it does.
func verifyMapping(policy delivery.ClassPolicy, f deliveryrecord.Fetched) (VerifiedGroupMapping, string) {
	v := VerifiedGroupMapping{Ref: f.Manifest}
	var why string
	v.User, v.SignedAt, why = verifyPerson(policy, "group mapping", func(verify func([]byte) error) (string, error) {
		st, err := delivery.OpenClusterGroupMapping(f.Bundle, verify)
		v.Record = st.Predicate
		return st.Predicate.Approver, err
	})
	return v, why
}

// WriteClusterGroupMapping signs rec, a person's group mapping for a member,
// with signer and pushes it on the member's join stream in repo, its
// pacto-targets repository, through the ambient registry credentials. It
// returns where the record is and the form it took.
func (s *Service) WriteClusterGroupMapping(ctx context.Context, rec delivery.ClusterGroupMapping, signer delivery.Signer, repo string) (string, capabilities.ReferrersMode, error) {
	if err := rec.Validate(); err != nil {
		return "", "", err
	}
	ctx, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	r, err := openTargets(repo)
	if err != nil {
		return "", "", err
	}
	signed, err := delivery.SignClusterGroupMapping(ctx, signer, delivery.NewClusterGroupMappingStatement(rec))
	if err != nil {
		return "", "", err
	}
	return deliveryrecord.PushClusterGroupMapping(ctx, r, signed)
}
