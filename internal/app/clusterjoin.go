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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
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
	// Include is a full cluster UID read whatever the bounds, even when no
	// tag lists it: the cluster a person decides on.
	Include string
	// Resolve, when set, gives the options each read uses, so a long-running
	// reader follows the hub's detection: an error is a partial list that
	// says why, and no repository an empty one (the hub sets no registry).
	Resolve func(context.Context) (JoinOptions, error)
	// Candidates, when set, reads the clusters the hub's GitOps and cluster
	// managers know (the operator's clusters.json), matched to the members
	// by UID at the end of each read.
	Candidates func(context.Context) (detect.Clusters, error)
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
	if opts.Resolve != nil {
		r, err := opts.Resolve(ctx)
		if err != nil || r.Repository == "" {
			return unresolvedJoins(now, err)
		}
		opts = r
	}
	jr := &joinRead{s: s, opts: opts, now: now, recs: map[string]delivery.JoinRecords{},
		list: delivery.ClusterJoinList{Hub: delivery.ClusterRef{UID: opts.Hub}, Repository: opts.Repository, AsOf: now.UTC().Format(time.RFC3339),
			Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{}}}
	jr.read(ctx)
	if jr.partial {
		jr.list.Completeness = fleet.CompletenessPartial
	}
	if opts.Candidates == nil {
		return jr.list
	}
	return withCandidates(ctx, jr.list, opts.Candidates, now)
}

// withCandidates is l with the candidates read matched to its members: a
// failed read leaves the candidates partial and says why, never an empty
// list of them.
func withCandidates(ctx context.Context, l delivery.ClusterJoinList, read func(context.Context) (detect.Clusters, error), now time.Time) delivery.ClusterJoinList {
	c, err := read(ctx)
	if err != nil {
		l.CandidatesCompleteness = fleet.CompletenessPartial
		l.Limitations = append(l.Limitations, fleet.Limitation{Code: fleet.LimitationSourceUnavailable, Source: delivery.CandidatesID,
			Message: "the candidate clusters were not read: " + err.Error()})
		return l
	}
	return delivery.MatchCandidates(l, c, now)
}

// unresolvedJoins is the list of a hub whose join options could not be
// resolved (err), partial and saying why, or that sets no registry: empty,
// since no cluster can join it.
func unresolvedJoins(now time.Time, err error) delivery.ClusterJoinList {
	l := delivery.ClusterJoinList{AsOf: now.UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{}}
	if err != nil {
		l.Completeness = fleet.CompletenessPartial
		l.Limitations = []fleet.Limitation{{Code: fleet.LimitationSourceUnavailable, Source: clusterJoinsID, Message: "the member clusters were not read: " + err.Error()}}
	}
	return l
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
	// perCluster are the clusters each per-cluster limitation counts.
	perCluster map[clusterNote]map[string]bool
	// otherMappings counts the verified group mappings that name another
	// hub or cluster.
	otherMappings int
}

// clusterNote is a limitation that counts clusters: its message has one %s,
// the count, so it never names a cluster or an approver, which a reader who
// may not approve must not learn. Each row says why itself.
type clusterNote struct {
	code, message string
	uncertain     bool
}

// Per-cluster limitations.
var (
	noteRecordsUnread   = clusterNote{fleet.LimitationSourceUnavailable, "the join records of %s could not be read, so they stay pending", true}
	noteRecordsCut      = clusterNote{fleet.LimitationTargetSnapshotBound, "%s have more than " + fmt.Sprint(delivery.MaxJoinRecords) + " join records and stay pending until older ones are read", true}
	noteReviewFailed    = clusterNote{fleet.LimitationSourceUnavailable, "the approvers of %s could not be checked, so their decisions are not counted", true}
	noteReviewForbidden = clusterNote{fleet.LimitationSourceUnavailable, "these credentials may not ask the hub whether approvers of %s may approve: " +
		"grant create on subjectaccessreviews in the hub, or read the list on the hub dashboard", true}
	noteForks          = clusterNote{fleet.LimitationTargetSnapshotInvalid, "the join records of %s have tags that do not name their record", false}
	noteSnapshotUnread = clusterNote{fleet.LimitationSourceUnavailable, "the snapshots of %s an approval names could not be read, so they stay pending", true}
)

// note adds a limitation, and makes the list partial when uncertain.
func (jr *joinRead) note(code, message string, uncertain bool) {
	jr.mu.Lock()
	defer jr.mu.Unlock()
	jr.list.Limitations = append(jr.list.Limitations, fleet.Limitation{Code: code, Source: clusterJoinsID, Message: message})
	jr.partial = jr.partial || uncertain
}

// noteCluster counts uid under n.
func (jr *joinRead) noteCluster(n clusterNote, uid string) {
	jr.mu.Lock()
	defer jr.mu.Unlock()
	if jr.perCluster == nil {
		jr.perCluster = map[clusterNote]map[string]bool{}
	}
	if jr.perCluster[n] == nil {
		jr.perCluster[n] = map[string]bool{}
	}
	jr.perCluster[n][uid] = true
}

// flushClusterNotes adds one limitation per per-cluster note, with its count.
func (jr *joinRead) flushClusterNotes() {
	for _, n := range []clusterNote{noteRecordsUnread, noteRecordsCut, noteReviewFailed, noteReviewForbidden, noteForks, noteSnapshotUnread,
		noteMappingsUnread, noteMappingsCut, noteMappingsSkipped} {
		if c := len(jr.perCluster[n]); c > 0 {
			jr.note(n.code, fmt.Sprintf(n.message, plural(c, "cluster")), n.uncertain)
		}
	}
}

// plural is "1 cluster" or "n clusters".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
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
	cand := map[string]delivery.JoinCandidate{}
	for _, uid := range jr.toFetch(uids) {
		c := jr.candidate(ctx, r, uid)
		cand[uid] = c
		cands = append(cands, c)
	}
	states, others := delivery.JoinStates(jr.opts.Hub, cands, jr.recs)
	for i, st := range states {
		c := cand[st.UID]
		if st.State == delivery.JoinPending && c.Err != "" && jr.decided(st.UID, delivery.JoinApprove) {
			jr.noteCluster(noteSnapshotUnread, st.UID) // an approved member may be missing
		}
		switch {
		case jr.policyWhy == "" || st.State != delivery.JoinPending:
		case c.Err != "" || c.Join == nil || !c.SelfSigned: // what the snapshot says stays
			states[i].Reason = jr.policyWhy + "; " + st.Reason
		default:
			states[i].Reason = jr.policyWhy
		}
	}
	jr.readMappings(ctx, states)
	jr.flushClusterNotes()
	jr.list.Clusters, jr.list.OtherHubs = append(jr.list.Clusters, states...), others+jr.otherMappings
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
	if in := jr.opts.Include; in != "" && in != jr.opts.Hub && !slices.ContainsFunc(uids, func(u string) bool { return strings.HasPrefix(u, in) }) {
		uids = append(uids, in) // its tag may be gone, its records not
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
	rs := delivery.JoinRecords{Cut: cut, Forks: forks}
	if len(forks) > 0 {
		jr.noteCluster(noteForks, uid)
	}
	switch {
	case err != nil:
		rs.Err = err.Error()
		jr.noteCluster(noteRecordsUnread, uid)
	case cut:
		jr.noteCluster(noteRecordsCut, uid)
	}
	for _, f := range fetched {
		rs.Verified = append(rs.Verified, jr.judge(ctx, uid, f))
	}
	for _, ref := range supersededJoins(jr.opts.Hub, uid, rs.Verified) {
		jr.s.joinRecords.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterJoin, ref)
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
	what := "an approval"
	if v.Record.Decision == delivery.JoinRemove {
		what = "a removal"
	}
	allowed, err := jr.s.authorizeRecord(ctx, jr.opts.Authorize, v.User, uid, jr.now)
	switch {
	case apierrors.IsForbidden(err):
		v.Counted, v.Why = false, what+" by "+v.User+" could not be checked: these credentials may not create subjectaccessreviews in the hub"
		jr.noteCluster(noteReviewForbidden, uid)
	case err != nil:
		v.Counted, v.Why = false, what+" by "+v.User+" could not be checked: "+err.Error()
		jr.noteCluster(noteReviewFailed, uid)
	case !allowed:
		v.Counted, v.Why = false, what+" by "+v.User+" was not counted: "+v.User+" may not approve clusterjoins"
	}
	return v
}

// supersededJoins are the records of uid's join stream that can never decide
// again for hub, so they stop taking the record bound: those naming another
// cluster, those older than hub's newest counted record and another hub's
// older than that hub's newest verified one. A record that does not count
// supersedes nothing, so no one evicts a member without the grant.
func supersededJoins(hub, uid string, vs []delivery.VerifiedJoin) []string {
	newest := map[string]time.Time{}
	for _, v := range vs {
		if v.Verified && v.Record.Cluster == uid && (v.Counted || v.Record.Hub != hub) && v.SignedAt.After(newest[v.Record.Hub]) {
			newest[v.Record.Hub] = v.SignedAt
		}
	}
	var out []string
	for _, v := range vs {
		if v.Verified && (v.Record.Cluster != uid || v.SignedAt.Before(newest[v.Record.Hub])) {
			out = append(out, v.Ref)
		}
	}
	return out
}

// verifyRecord opens f, a join record, as verifyPerson does. ok is false
// when it does not verify or names another user than its certificate maps
// to; then Why says so.
func verifyRecord(policy delivery.ClassPolicy, f deliveryrecord.Fetched) (delivery.VerifiedJoin, bool) {
	v := delivery.VerifiedJoin{Ref: f.Manifest}
	v.User, v.SignedAt, v.Why = verifyPerson(policy, "join record", func(verify func([]byte) error) (string, error) {
		st, err := delivery.OpenClusterJoin(f.Bundle, verify)
		v.Record = st.Predicate
		return st.Predicate.Approver, err
	})
	v.Verified, v.Counted = v.Why == "", v.Why == ""
	return v, v.Verified
}

// verifyPerson opens a person's record (a what) through open, under the
// approver signer whose issuer its certificate names, and maps the
// certificate to the user the record must name, the approver open returns.
// It returns that user, the verified signing time and why the record does
// not verify, "" when it does.
func verifyPerson(policy delivery.ClassPolicy, what string, open func(verify func([]byte) error) (approver string, err error)) (string, time.Time, string) {
	var signer delivery.PublisherIdentity
	var at time.Time
	approver, err := open(func(b []byte) error {
		var errs []error
		for _, s := range policy.Signers {
			id, t, err := signing.VerifyPerson(b, s.Issuer, s.TrustedRoot)
			if err == nil {
				signer, at = id, t
				return nil
			}
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	})
	if err != nil {
		return "", at, "a " + what + " was not counted: " + err.Error()
	}
	id, source, err := delivery.ApproverIdentity(signer.Issuer, signer.Subject, policy)
	if err == nil {
		if code, why := delivery.KeylessRefusal(id, source); code != "" {
			err = errors.New(why)
		}
	}
	switch {
	case err != nil:
		return id.User, at, "a " + what + " signed by " + signer.Subject + " was not counted: " + err.Error()
	case id.User != approver:
		return id.User, at, "a " + what + " claiming approver " + approver + " is signed as " + id.User + " and was not counted"
	}
	return id.User, at, ""
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

// decided reports whether a counted record of this hub on uid decides dec,
// any decision when dec is "".
func (jr *joinRead) decided(uid string, dec delivery.JoinDecision) bool {
	return slices.ContainsFunc(jr.recs[uid].Verified, func(v delivery.VerifiedJoin) bool {
		return v.Counted && v.Record.Hub == jr.opts.Hub && v.Record.Cluster == uid && (dec == "" || v.Record.Decision == dec)
	})
}

// toFetch are the UIDs whose snapshots are read: every one a counted
// approval or removal names and the one a person decides on, then at most
// delivery.MaxTargetSnapshots others. The others past that bound are
// counted, never read.
func (jr *joinRead) toFetch(uids []string) []string {
	var approved, pending []string
	for _, uid := range uids {
		if uid == jr.opts.Include || jr.decided(uid, "") {
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
	c.SelfSigned = snap.Join != nil && selfSigned(bundle, uid, *snap.Join)
	return c
}

// selfSigned reports whether the key j names signed bundle, the snapshot
// of uid, scoped to uid.
func selfSigned(bundle []byte, uid string, j delivery.SnapshotJoin) bool {
	self := delivery.JoinTrust([]delivery.ClusterJoinState{{UID: uid, Name: j.Name, PublicKey: j.PublicKey, State: delivery.JoinApproved}})
	_, v, err := delivery.OpenTargetSnapshot(bundle, self, signing.OpenKey)
	return err == nil && v.Scoped
}

// clusterJoinSources are the sources of the members this hub approved, read
// through r, the build's one snapshot reader, each verified only under its
// approved key scoped to its UID. A member r already read is skipped. A list
// read partly adds one partial source that says what was not read.
func (s *Service) clusterJoinSources(ctx context.Context, r *snapshotReader, opts JoinOptions) []fleet.Source {
	list := s.ClusterJoins(ctx, membersOnly(opts))
	var out []fleet.Source
	if list.Completeness != fleet.CompletenessComplete { // its limitations name no cluster
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
			repo, _ = openTargets(list.Repository) // it listed
		}
		r.names[st.UID] = &fleet.Cluster{ID: st.UID, Name: st.Name, NameSource: st.NameSource}
		out = append(out, m.snapshot(ctx, repo, list.Repository, delivery.TargetSnapshotTagPrefix+st.UID)...)
	}
	return out
}

// membersOnly is opts reading no candidates, however it resolves: the fleet
// reads members only, so no candidate reaches a source, a gate or a count.
func membersOnly(opts JoinOptions) JoinOptions {
	opts.Candidates = nil
	if resolve := opts.Resolve; resolve != nil {
		opts.Resolve = func(ctx context.Context) (JoinOptions, error) {
			r, err := resolve(ctx)
			r.Candidates = nil
			return r, err
		}
	}
	return opts
}

// WriteClusterJoin signs rec, a person's decision on a member, with signer
// and pushes it on the member's join stream in repo, its pacto-targets
// repository, through the ambient registry credentials. It first reads the
// member's snapshot again and refuses an approval when its join block now
// names another key than the one the person checked (rec.KeyID), so a
// swapped tag gets nothing signed. A removal needs no snapshot: it revokes
// the approved key even when the member stopped publishing. It returns where
// the record is and the form it took.
func (s *Service) WriteClusterJoin(ctx context.Context, rec delivery.ClusterJoin, signer delivery.Signer, repo string) (string, capabilities.ReferrersMode, error) {
	if err := rec.Check(); err != nil {
		return "", "", err
	}
	ctx, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	r, err := openTargets(repo)
	if err != nil {
		return "", "", err
	}
	if rec.Decision == delivery.JoinApprove {
		if err := checkApprovable(ctx, r, rec); err != nil {
			return "", "", err
		}
	}
	signed, err := delivery.SignClusterJoin(ctx, signer, delivery.NewClusterJoinStatement(rec))
	if err != nil {
		return "", "", err
	}
	return deliveryrecord.PushClusterJoin(ctx, r, signed)
}

// checkApprovable reads the snapshot rec approves again and refuses unless
// its own key, the one rec names, signed it.
func checkApprovable(ctx context.Context, r *remote.Repository, rec delivery.ClusterJoin) error {
	bundle, _, err := deliveryrecord.FetchTargetSnapshot(ctx, r, delivery.TargetSnapshotTagPrefix+rec.Cluster)
	if err != nil {
		return fmt.Errorf("reading the snapshot of %s again: %w", rec.Cluster, err)
	}
	st, _, err := delivery.OpenTargetSnapshot(bundle, nil, signing.OpenKey)
	switch {
	case err != nil:
		return fmt.Errorf("the snapshot of %s: %w", rec.Cluster, err)
	case st.Predicate.Cluster != rec.Cluster:
		return fmt.Errorf("the snapshot tagged for %s names cluster %s", rec.Cluster, st.Predicate.Cluster)
	case st.Predicate.Join == nil:
		return fmt.Errorf("ClusterKeyMismatch: the snapshot of %s now carries no join block; you checked %s", rec.Cluster, shortKey(rec.KeyID))
	case st.Predicate.Join.KeyID != rec.KeyID:
		return fmt.Errorf("ClusterKeyMismatch: the snapshot of %s is now signed by %s; you checked %s", rec.Cluster, shortKey(st.Predicate.Join.KeyID), shortKey(rec.KeyID))
	case !selfSigned(bundle, rec.Cluster, *st.Predicate.Join):
		return fmt.Errorf("the snapshot of %s is not signed by the key it names, so it cannot be approved", rec.Cluster)
	}
	return nil
}

// shortKey is a key id as messages show it: sha256: and its first 12 hex.
func shortKey(id string) string { return "sha256:" + id[:min(12, len(id))] + "..." }

// ClassPolicy is the hub's published identity policy, its approver signers
// (delivery.identities.signers) among it, read as a join read reads it.
func (d *Delivery) ClassPolicy(ctx context.Context) (delivery.ClassPolicy, error) {
	return d.classPolicy(ctx)
}

// AdvertisedTargets is the pacto-targets repository the operator's detection
// at ref advertises (installation.targets): k8s:<namespace>/<name> of its
// ConfigMap, read with dyn, or a file. It is "" when an available detection
// names no registry. A detection that is stale, unreadable or not the
// operator's is an error, and so is any advertisement while PACTO_REGISTRY_*
// credentials are set: they answer any host, and a detection a ConfigMap
// writer can forge must not choose where they are sent.
func AdvertisedTargets(ctx context.Context, dyn dynamic.Interface, ref string, now func() time.Time) (string, error) {
	src := fleetsrc.NewDetectionFileSource("detection", ref, now)
	if rest, ok := strings.CutPrefix(ref, "k8s:"); ok {
		ns, name, _ := strings.Cut(rest, "/")
		src = fleetsrc.NewDetectionK8sSource("detection", dyn, ns, name, now)
	}
	res, st := src.Read(ctx)
	switch {
	case st.Status != fleet.SourceAvailable:
		why := string(st.Status)
		if st.Error != nil {
			why = st.Error.Message
		}
		return "", fmt.Errorf("the detection %s names no registry: %s", ref, why)
	case res.Installation == nil || res.Installation.Targets == "":
		return "", nil
	}
	if env := oci.EnvCredentialOptions(); env.Token != "" || (env.Username != "" && env.Password != "") {
		return "", fmt.Errorf("the detection %s advertises %s, not contacted: PACTO_REGISTRY_* credentials answer any host; pass --registry %s",
			ref, res.Installation.Targets, res.Installation.Targets)
	}
	return res.Installation.Targets, nil
}
