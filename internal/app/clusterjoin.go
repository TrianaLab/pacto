package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// JoinOptions are how a hub reads the clusters that ask to join it.
type JoinOptions struct {
	// Repository is the <registry>/pacto-targets repository the members
	// publish to; Hub is the hub cluster's UID.
	Repository, Hub string
	// Authorize reviews, in the hub, whether the user an approval names
	// may approve clusterjoins named the member's UID. Required.
	Authorize authn.Review
	// Delivery reads the hub's approver signers (delivery.identities.signers).
	Delivery *Delivery
}

const (
	// joinReadsAtOnce bounds the clusters whose records are read at once.
	joinReadsAtOnce = 8
	// joinVerdictTTL is how long an approve review is remembered.
	joinVerdictTTL = time.Minute
	// maxJoinVerdicts bounds the reviews remembered.
	maxJoinVerdicts = 256
	// clusterJoinsID names the source that says the joins were read partly.
	clusterJoinsID = "cluster-joins"
)

// noSigners is why no join record counts in a hub without approver signers.
const noSigners = "this hub accepts no signed approvals: set delivery.identities.signers on the hub's operator"

// ClusterJoins lists every cluster that publishes to opts.Repository and
// where it stands with this hub. A record counts only when its keyless
// signature verifies under a configured approver signer, its certificate
// maps to the user it names, and the hub lets that user approve the
// cluster now. Anything that cannot be read leaves its cluster pending and
// the list partial; nothing unknown is ever approved.
func (s *Service) ClusterJoins(ctx context.Context, opts JoinOptions) delivery.ClusterJoinList {
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(s.sys.snapshotTimeout, delivery.TargetSnapshotReadTimeout))
	defer cancel()
	now := s.env().now()
	jr := &joinRead{s: s, opts: opts, now: now, recs: map[string]delivery.JoinRecords{},
		list: delivery.ClusterJoinList{Hub: delivery.ClusterRef{UID: opts.Hub}, Repository: opts.Repository, AsOf: now.UTC().Format(time.RFC3339),
			Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{}}}
	jr.read(ctx)
	if jr.partial {
		jr.list.Completeness = fleet.CompletenessPartial
	}
	return jr.list
}

// joinRead is one ClusterJoins call on its way to its list.
type joinRead struct {
	s      *Service
	opts   JoinOptions
	now    time.Time
	policy delivery.ClassPolicy
	// policyWhy is why no record counts: no signer, or none could be read.
	policyWhy string
	repo      *remote.Repository
	mu        sync.Mutex
	recs      map[string]delivery.JoinRecords
	list      delivery.ClusterJoinList
	// partial says what was not read may change which members are approved.
	partial bool
}

// note adds a limitation, and makes the list partial when uncertain.
func (jr *joinRead) note(code, message string, uncertain bool) {
	jr.mu.Lock()
	defer jr.mu.Unlock()
	jr.list.Limitations = append(jr.list.Limitations, fleet.Limitation{Code: code, Source: clusterJoinsID, Message: message})
	jr.partial = jr.partial || uncertain
}

func (jr *joinRead) read(ctx context.Context) {
	jr.readPolicy(ctx)
	uids, ok := jr.listClusters(ctx)
	if !ok {
		return
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(joinReadsAtOnce)
	for _, uid := range uids {
		g.Go(func() error { jr.readRecords(gctx, uid); return nil })
	}
	_ = g.Wait() // readRecords notes its failures
	r := &snapshotReader{cache: &jr.s.snapshots, now: jr.now}
	var cands []delivery.JoinCandidate
	for _, uid := range jr.toFetch(uids) {
		cands = append(cands, jr.candidate(ctx, r, uid))
	}
	states, others := delivery.JoinStates(jr.opts.Hub, cands, jr.recs)
	for i := range states {
		if jr.policyWhy != "" && states[i].State == delivery.JoinPending {
			states[i].Reason = jr.policyWhy
		}
	}
	jr.list.Clusters, jr.list.OtherHubs = append(jr.list.Clusters, states...), others
}

// readPolicy reads the hub's approver signers.
func (jr *joinRead) readPolicy(ctx context.Context) {
	p, err := jr.opts.Delivery.classPolicy(ctx)
	switch {
	case err != nil:
		jr.policyWhy = "the hub's approver signers could not be read, so no approval counts: " + err.Error()
		jr.note(fleet.LimitationSourceUnavailable, jr.policyWhy, true)
	case len(p.Signers) == 0:
		jr.policyWhy = noSigners
		jr.note(fleet.LimitationSourcePartial, noSigners, false)
	}
	jr.policy = p
}

// listClusters lists the cluster UIDs that publish to the repository, the
// hub's own left out.
func (jr *joinRead) listClusters(ctx context.Context) ([]string, bool) {
	path := jr.opts.Repository
	repo, err := openTargets(path)
	var tags []string
	truncated := false
	if err == nil {
		tags, truncated, err = deliveryrecord.ListClusterTags(ctx, repo)
	}
	switch {
	case denied(err):
		jr.note(fleet.LimitationSourceUnavailable, "no pull credential reads "+path+": on a hub dashboard, add a pull-only Secret for it to dashboard.ociSecrets", true)
		return nil, false
	case err != nil:
		jr.note(fleet.LimitationSourceUnavailable, "the clusters in "+path+" could not be listed: "+err.Error(), true)
		return nil, false
	case truncated:
		jr.note(fleet.LimitationTargetSnapshotBound, path+" holds more tags than a read lists; the clusters past them are unknown", true)
	}
	jr.repo = repo
	var uids []string
	for _, t := range tags {
		if uid := strings.TrimPrefix(t, delivery.TargetSnapshotTagPrefix); uid != jr.opts.Hub {
			uids = append(uids, uid)
		}
	}
	return uids, true
}

// denied reports whether err is a registry's 401 or 403.
func denied(err error) bool {
	var resp *errcode.ErrorResponse
	return errors.As(err, &resp) && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden)
}

// openTargets opens the pacto-targets repository path with the ambient
// registry credentials.
func openTargets(path string) (*remote.Repository, error) {
	return ocireferrer.Open(path, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
}

// readRecords reads and judges the join records of uid, through its own
// repository client: a tag listing sets the client's page size.
func (jr *joinRead) readRecords(ctx context.Context, uid string) {
	repo, _ := openTargets(jr.opts.Repository) // it opened once
	fetched, cut, forks, err := jr.s.joinRecords.ListClusterJoins(ctx, repo, uid)
	rs := delivery.JoinRecords{Cut: cut}
	for _, f := range forks {
		jr.note(fleet.LimitationTargetSnapshotInvalid, "cluster "+uid+": "+f, false)
	}
	switch {
	case err != nil:
		rs.Err = err.Error()
		jr.note(fleet.LimitationSourceUnavailable, "the join records of cluster "+uid+" could not be read: "+err.Error(), true)
	case cut:
		jr.note(fleet.LimitationTargetSnapshotBound, fmt.Sprintf("cluster %s has more than %d join records; the newest could not be read", uid, delivery.MaxJoinRecords), true)
	}
	for _, f := range fetched {
		rs.Verified = append(rs.Verified, jr.judge(ctx, uid, f))
	}
	jr.mu.Lock()
	jr.recs[uid] = rs
	jr.mu.Unlock()
}

// judge verifies f, a join record of uid, and asks whether it counts. A
// record that does not verify is rejected, so it never takes the bound again;
// one whose approver may not approve is not, since a grant can be added.
func (jr *joinRead) judge(ctx context.Context, uid string, f deliveryrecord.Fetched) delivery.VerifiedJoin {
	if jr.policyWhy != "" {
		return delivery.VerifiedJoin{Ref: f.Manifest, Why: "a join record was not counted: " + jr.policyWhy}
	}
	v, ok := verifyRecord(jr.policy, f)
	if !ok {
		jr.s.joinRecords.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterJoin, f.Manifest)
		return v
	}
	if v.Record.Hub != jr.opts.Hub {
		return v // another hub's: counted there, never here
	}
	allowed, err := jr.s.authorizeRecord(ctx, jr.opts.Authorize, v.User, uid, jr.now)
	switch {
	case err != nil:
		v.Counted, v.Why = false, "an approval by "+v.User+" could not be checked: "+err.Error()
		jr.note(fleet.LimitationSourceUnavailable, "cluster "+uid+": "+v.Why, true)
	case !allowed:
		v.Counted, v.Why = false, "an approval by "+v.User+" was not counted: "+v.User+" may not approve clusterjoins"
	}
	return v
}

// verifyRecord opens f under the approver signer whose issuer its
// certificate names, and maps the certificate to the user the record must
// name. ok is false when it does not verify or names another user than its
// certificate maps to; then Why says so.
func verifyRecord(policy delivery.ClassPolicy, f deliveryrecord.Fetched) (delivery.VerifiedJoin, bool) {
	v := delivery.VerifiedJoin{Ref: f.Manifest}
	var signer delivery.PublisherIdentity
	st, err := delivery.OpenClusterJoin(f.Bundle, func(b []byte) error {
		var errs []error
		for _, s := range policy.Signers {
			id, at, err := signing.VerifyPerson(b, s.Issuer, s.TrustedRoot)
			if err == nil {
				signer, v.SignedAt = id, at
				return nil
			}
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	})
	if err != nil {
		v.Why = "a join record was not counted: " + err.Error()
		return v, false
	}
	v.Record = st.Predicate
	id, source, err := delivery.ApproverIdentity(signer.Issuer, signer.Subject, policy)
	if err == nil {
		if code, why := delivery.KeylessRefusal(id, source); code != "" {
			err = errors.New(why)
		}
	}
	v.User = id.User
	switch {
	case err != nil:
		v.Why = "a join record signed by " + signer.Subject + " was not counted: " + err.Error()
		return v, false
	case id.User != v.Record.Approver:
		v.Why = "a join record claiming approver " + v.Record.Approver + " is signed as " + id.User + " and was not counted"
		return v, false
	}
	v.Counted = true
	return v, true
}

// joinVerdictCache remembers approve reviews by user and member.
type joinVerdictCache struct {
	mu sync.Mutex
	m  map[string]joinVerdict
}

type joinVerdict struct {
	allowed bool
	until   time.Time
}

// authorizeRecord asks review whether user, with no groups (a certificate
// names none), may approve clusterjoins named uid in the hub. An answer is
// remembered for a minute; an error never is.
func (s *Service) authorizeRecord(ctx context.Context, review authn.Review, user, uid string, now time.Time) (bool, error) {
	c := &s.joinVerdicts
	key := user + "\x00" + uid
	c.mu.Lock()
	v, ok := c.m[key]
	c.mu.Unlock()
	if ok && now.Before(v.until) {
		return v.allowed, nil
	}
	res := authn.ClusterJoinResource
	res.Name = uid
	allowed, err := review(ctx, authn.Person{User: user}, "", res)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// ponytail: cleared whole when full; an LRU if a hub ever reviews more.
	if c.m == nil || len(c.m) >= maxJoinVerdicts {
		c.m = map[string]joinVerdict{}
	}
	c.m[key] = joinVerdict{allowed: allowed, until: now.Add(joinVerdictTTL)}
	return allowed, nil
}

// toFetch are the UIDs whose snapshots are read: every one a counted
// approval names, then at most delivery.MaxTargetSnapshots others. The
// others past that bound are counted, never read.
func (jr *joinRead) toFetch(uids []string) []string {
	var approved, pending []string
	for _, uid := range uids {
		if slices.ContainsFunc(jr.recs[uid].Verified, func(v delivery.VerifiedJoin) bool {
			return v.Counted && v.Record.Hub == jr.opts.Hub && v.Record.Decision == delivery.JoinApprove
		}) {
			approved = append(approved, uid)
		} else {
			pending = append(pending, uid)
		}
	}
	if n := len(pending) - delivery.MaxTargetSnapshots; n > 0 {
		jr.note(fleet.LimitationTargetSnapshotBound, fmt.Sprintf("%d pending clusters were not read: a read takes %d", n, delivery.MaxTargetSnapshots), true)
		pending = pending[:delivery.MaxTargetSnapshots]
	}
	out := append(approved, pending...)
	slices.Sort(out)
	return out
}

// candidate reads the snapshot of uid and checks its own key signed it.
func (jr *joinRead) candidate(ctx context.Context, r *snapshotReader, uid string) delivery.JoinCandidate {
	c := delivery.JoinCandidate{UID: uid, Status: fleet.SourceUnavailable}
	bundle, ref, err := r.fetch(ctx, jr.repo, jr.opts.Repository, delivery.TargetSnapshotTagPrefix+uid)
	if err != nil {
		c.Err = "its snapshot could not be read: " + err.Error()
		return c
	}
	c.Ref = ref
	st, _, err := delivery.OpenTargetSnapshot(bundle, nil, signing.OpenKey)
	snap := st.Predicate
	switch {
	case err != nil:
		c.Err = "its snapshot was not used: " + err.Error()
		return c
	case snap.Cluster != uid:
		c.Err = "its snapshot names cluster " + snap.Cluster
		return c
	}
	asOf, _ := time.Parse(time.RFC3339, snap.AsOf) // Check parsed it
	c.Join, c.AsOf, c.Status = snap.Join, snap.AsOf, fleet.SourceAvailable
	if jr.now.Sub(asOf) > delivery.TargetSnapshotFreshness {
		c.Status = fleet.SourceStale
	}
	if snap.Join != nil {
		self := delivery.JoinTrust([]delivery.ClusterJoinState{{UID: uid, Name: snap.Join.Name, PublicKey: snap.Join.PublicKey, State: delivery.JoinApproved}})
		_, v, err := delivery.OpenTargetSnapshot(bundle, self, signing.OpenKey)
		c.SelfSigned = err == nil && v.Scoped
	}
	return c
}

// clusterJoinSources are the sources of the members this hub approved, read
// through r, the build's one snapshot reader, each verified only under its
// approved key scoped to its UID. A member r already read is skipped. A list
// read partly adds one partial source that says what was not read.
func (s *Service) clusterJoinSources(ctx context.Context, r *snapshotReader, opts JoinOptions) []fleet.Source {
	list := s.ClusterJoins(ctx, opts)
	var out []fleet.Source
	if list.Completeness != fleet.CompletenessComplete {
		id := r.claim(clusterJoinsID)
		out = append(out, fleet.NewMemorySource(id, fleet.SourceKindTargetSnapshot, &fleet.Collection{
			State: &fleet.SourceState{Status: fleet.SourcePartial}, Limitations: withSource(id, list.Limitations)}))
	}
	m := *r // shares used, read and names: one reader per build
	m.trust = delivery.JoinTrust(list.Clusters)
	var repo *remote.Repository
	for _, st := range list.Clusters {
		if st.State != delivery.JoinApproved || r.read[st.UID] {
			continue
		}
		if repo == nil {
			repo, _ = openTargets(opts.Repository) // it listed
		}
		r.names[st.UID] = &fleet.Cluster{ID: st.UID, Name: st.Name, NameSource: st.NameSource}
		out = append(out, m.snapshot(ctx, repo, opts.Repository, delivery.TargetSnapshotTagPrefix+st.UID)...)
	}
	return out
}
