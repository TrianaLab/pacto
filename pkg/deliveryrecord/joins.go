package deliveryrecord

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// JoinsSourceID names the source that says the joins were read partly.
const JoinsSourceID = "cluster-joins"

// joinReadsAtOnce bounds the clusters whose records are read at once.
const joinReadsAtOnce = 8

// JoinRead is how a hub reads the clusters that ask to join it, everything
// Kubernetes or cache related given as a function, so the CLI's surfaces and
// the operator's promoter compute the same states.
type JoinRead struct {
	// Repository is the <registry>/pacto-targets repository the members
	// publish to; Hub is the hub cluster's UID; Include is a full cluster
	// UID read whatever the bounds, even when no tag lists it.
	Repository, Hub, Include string
	// Policy holds the hub's approver signers. PolicyWhy is why no record
	// counts ("" when some may), and PolicyUnread says the signers could not
	// be read, which leaves the list partial.
	Policy       delivery.ClassPolicy
	PolicyWhy    string
	PolicyUnread bool
	// Records reads and remembers the join and group mapping records.
	Records *Reader
	// Authorize asks the hub whether user may approve clusterjoins named uid.
	Authorize func(ctx context.Context, user, uid string) (bool, error)
	// Fetch reads the bundle at reference in repo and the ref it resolved to.
	Fetch func(ctx context.Context, repo *remote.Repository, reference string) ([]byte, string, error)
	// Forbidden reports whether a review error is the hub refusing to be asked.
	Forbidden func(error) bool
	// VerifyPerson opens a person's record (a what) through open under policy:
	// the user it maps to, the verified signing time and why it does not
	// verify, "" when it does.
	VerifyPerson func(policy delivery.ClassPolicy, what string, open func(verify func([]byte) error) (string, error)) (string, time.Time, string)
	// OpenKey opens a snapshot's key, as signing.OpenKey does.
	OpenKey func(delivery.TrustedKey) (func([]byte) error, error)
	// Open opens a pacto-targets repository with the registry credentials.
	Open func(path string) (*remote.Repository, error)
	Now  time.Time
}

// ReadJoins lists every cluster that publishes to in.Repository and where it
// stands with this hub, and the repository it listed (nil when it could not).
// A record counts only when it verifies, names the user its signer maps to,
// and the hub lets that user approve the cluster now. Anything that cannot be
// read leaves its cluster pending and the list partial; nothing unknown is
// ever approved.
func ReadJoins(ctx context.Context, in JoinRead) (delivery.ClusterJoinList, *remote.Repository) {
	jr := &joinRead{in: in, recs: map[string]delivery.JoinRecords{},
		list: delivery.ClusterJoinList{Hub: delivery.ClusterRef{UID: in.Hub}, Repository: in.Repository, AsOf: in.Now.UTC().Format(time.RFC3339),
			Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{}}}
	jr.read(ctx)
	if jr.partial {
		jr.list.Completeness = fleet.CompletenessPartial
	}
	return jr.list, jr.repo
}

// joinRead is one ReadJoins call on its way to its list.
type joinRead struct {
	in   JoinRead
	repo *remote.Repository
	mu   sync.Mutex
	recs map[string]delivery.JoinRecords
	list delivery.ClusterJoinList
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
	noteMappingsUnread = clusterNote{fleet.LimitationSourceUnavailable, "the group mappings of %s could not be read, so their reads use none", true}
	noteMappingsCut    = clusterNote{fleet.LimitationTargetSnapshotBound, "%s have more than " + fmt.Sprint(delivery.MaxJoinRecords) +
		" group mapping records and use none until older ones are read", true}
	noteMappingsSkipped = clusterNote{fleet.LimitationTargetSnapshotInvalid, "%s have group mapping records that were not counted", false}
)

// note adds a limitation, and makes the list partial when uncertain.
func (jr *joinRead) note(code, message string, uncertain bool) {
	jr.mu.Lock()
	defer jr.mu.Unlock()
	jr.list.Limitations = append(jr.list.Limitations, fleet.Limitation{Code: code, Source: JoinsSourceID, Message: message})
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
	switch {
	case jr.in.PolicyWhy == "":
	case jr.in.PolicyUnread:
		jr.note(fleet.LimitationSourceUnavailable, jr.in.PolicyWhy, true)
	default:
		jr.note(fleet.LimitationSourcePartial, jr.in.PolicyWhy, false)
	}
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
	var cands []delivery.JoinCandidate
	cand := map[string]delivery.JoinCandidate{}
	for _, uid := range jr.toFetch(uids) {
		c := jr.candidate(ctx, uid)
		cand[uid] = c
		cands = append(cands, c)
	}
	states, others := delivery.JoinStates(jr.in.Hub, cands, jr.recs)
	for i, st := range states {
		c := cand[st.UID]
		if st.State == delivery.JoinPending && c.Err != "" && jr.decided(st.UID, delivery.JoinApprove) {
			jr.noteCluster(noteSnapshotUnread, st.UID) // an approved member may be missing
		}
		switch {
		case jr.in.PolicyWhy == "" || st.State != delivery.JoinPending:
		case c.Err != "" || c.Join == nil || !c.SelfSigned: // what the snapshot says stays
			states[i].Reason = jr.in.PolicyWhy + "; " + st.Reason
		default:
			states[i].Reason = jr.in.PolicyWhy
		}
	}
	jr.readMappings(ctx, states)
	jr.flushClusterNotes()
	jr.list.Clusters, jr.list.OtherHubs = append(jr.list.Clusters, states...), others+jr.otherMappings
}

// listClusters lists the cluster UIDs that publish to the repository, the
// hub's own left out.
func (jr *joinRead) listClusters(ctx context.Context) ([]string, bool) {
	path := jr.in.Repository
	repo, err := jr.in.Open(path)
	var tags []string
	truncated := false
	if err == nil {
		tags, truncated, err = ListClusterTags(ctx, repo)
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
		if uid := strings.TrimPrefix(t, delivery.TargetSnapshotTagPrefix); uid != jr.in.Hub {
			uids = append(uids, uid)
		}
	}
	if in := jr.in.Include; in != "" && in != jr.in.Hub && !slices.ContainsFunc(uids, func(u string) bool { return strings.HasPrefix(u, in) }) {
		uids = append(uids, in) // its tag may be gone, its records not
	}
	return uids, true
}

// denied reports whether err is a registry's 401 or 403.
func denied(err error) bool {
	var resp *errcode.ErrorResponse
	return errors.As(err, &resp) && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden)
}

// readRecords reads and judges the join records of uid, through its own
// repository client: a tag listing sets the client's page size.
func (jr *joinRead) readRecords(ctx context.Context, uid string) {
	repo, _ := jr.in.Open(jr.in.Repository) // it opened once
	fetched, cut, forks, err := jr.in.Records.ListClusterJoins(ctx, repo, uid)
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
	for _, ref := range supersededJoins(jr.in.Hub, uid, rs.Verified) {
		jr.in.Records.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterJoin, ref)
	}
	jr.mu.Lock()
	jr.recs[uid] = rs
	jr.mu.Unlock()
}

// judge verifies f, a join record of uid, and asks whether it counts. A
// record that does not verify is rejected, so it never takes the bound again;
// one whose approver may not approve is not, since a grant can be added.
func (jr *joinRead) judge(ctx context.Context, uid string, f Fetched) delivery.VerifiedJoin {
	if jr.in.PolicyWhy != "" {
		return delivery.VerifiedJoin{Ref: f.Manifest, Why: "a join record was not counted: " + jr.in.PolicyWhy}
	}
	v, ok := jr.verifyRecord(f)
	if !ok {
		jr.in.Records.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterJoin, f.Manifest)
		return v
	}
	if v.Record.Hub != jr.in.Hub {
		return v // another hub's: counted there, never here
	}
	what := "an approval"
	if v.Record.Decision == delivery.JoinRemove {
		what = "a removal"
	}
	allowed, err := jr.in.Authorize(ctx, v.User, uid)
	switch {
	case jr.in.Forbidden(err):
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

// verifyRecord opens f, a join record, through VerifyPerson. ok is false when
// it does not verify or names another user than its certificate maps to;
// then Why says so.
func (jr *joinRead) verifyRecord(f Fetched) (delivery.VerifiedJoin, bool) {
	v := delivery.VerifiedJoin{Ref: f.Manifest}
	v.User, v.SignedAt, v.Why = jr.in.VerifyPerson(jr.in.Policy, "join record", func(verify func([]byte) error) (string, error) {
		st, err := delivery.OpenClusterJoin(f.Bundle, verify)
		v.Record = st.Predicate
		return st.Predicate.Approver, err
	})
	v.Verified, v.Counted = v.Why == "", v.Why == ""
	return v, v.Verified
}

// decided reports whether a counted record of this hub on uid decides dec,
// any decision when dec is "".
func (jr *joinRead) decided(uid string, dec delivery.JoinDecision) bool {
	return slices.ContainsFunc(jr.recs[uid].Verified, func(v delivery.VerifiedJoin) bool {
		return v.Counted && v.Record.Hub == jr.in.Hub && v.Record.Cluster == uid && (dec == "" || v.Record.Decision == dec)
	})
}

// toFetch are the UIDs whose snapshots are read: every one a counted
// approval or removal names and the one a person decides on, then at most
// delivery.MaxTargetSnapshots others. The others past that bound are
// counted, never read.
func (jr *joinRead) toFetch(uids []string) []string {
	var approved, pending []string
	for _, uid := range uids {
		if uid == jr.in.Include || jr.decided(uid, "") {
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
func (jr *joinRead) candidate(ctx context.Context, uid string) delivery.JoinCandidate {
	c := delivery.JoinCandidate{UID: uid, Status: fleet.SourceUnavailable}
	bundle, ref, err := jr.in.Fetch(ctx, jr.repo, delivery.TargetSnapshotTagPrefix+uid)
	if err != nil {
		c.Err = "its snapshot could not be read: " + err.Error()
		return c
	}
	c.Ref = ref
	st, _, err := delivery.OpenTargetSnapshot(bundle, nil, jr.in.OpenKey)
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
	if jr.in.Now.Sub(asOf) > delivery.TargetSnapshotFreshness {
		c.Status = fleet.SourceStale
	}
	c.SelfSigned = snap.Join != nil && SelfSigned(bundle, uid, *snap.Join, jr.in.OpenKey)
	return c
}

// OpenMember opens bundle, the snapshot member uid's tag holds, under trust
// (each approved key covers only its own member's stream): one that names
// another cluster is an error, so the member's consumers read unknown.
func OpenMember(bundle []byte, trust *delivery.TrustConfig, openKey func(delivery.TrustedKey) (func([]byte) error, error), uid string) (delivery.TargetSnapshot, error) {
	st, _, err := delivery.OpenTargetSnapshot(bundle, trust, openKey)
	if err == nil && st.Predicate.Cluster != uid {
		err = errors.New("its tag holds the snapshot of cluster " + st.Predicate.Cluster)
	}
	return st.Predicate, err
}

// SelfSigned reports whether the key j names signed bundle, the snapshot of
// uid, scoped to uid; openKey opens a key as signing.OpenKey does.
func SelfSigned(bundle []byte, uid string, j delivery.SnapshotJoin, openKey func(delivery.TrustedKey) (func([]byte) error, error)) bool {
	self := delivery.JoinTrust([]delivery.ClusterJoinState{{UID: uid, Name: j.Name, PublicKey: j.PublicKey, State: delivery.JoinApproved}})
	_, v, err := delivery.OpenTargetSnapshot(bundle, self, openKey)
	return err == nil && v.Scoped
}

// verifiedMapping is one group mapping record that verified: signed under a
// hub approver signer by the user it names as Approver.
type verifiedMapping struct {
	Record   delivery.ClusterGroupMapping
	User     string
	Ref      string
	SignedAt time.Time
}

// readMappings sets the group mappings of every approved member in states.
// No record counts in a hub whose approver signers are unknown.
func (jr *joinRead) readMappings(ctx context.Context, states []delivery.ClusterJoinState) {
	if jr.in.PolicyWhy != "" {
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
	repo, _ := jr.in.Open(jr.in.Repository) // it opened once
	fetched, cut, err := jr.in.Records.ListGroupMappings(ctx, repo, uid)
	switch {
	case err != nil:
		jr.noteCluster(noteMappingsUnread, uid)
		return nil
	case cut:
		jr.noteCluster(noteMappingsCut, uid)
		return nil
	}
	byIssuer := map[string][]verifiedMapping{}
	for _, f := range fetched {
		v, why := jr.verifyMapping(f)
		switch {
		case why != "":
			jr.in.Records.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterGroupMapping, f.Manifest)
			jr.noteCluster(noteMappingsSkipped, uid)
		case v.Record.Hub != jr.in.Hub || v.Record.Cluster != uid:
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
func (jr *joinRead) decideMapping(ctx context.Context, uid string, vs []verifiedMapping) *verifiedMapping {
	slices.SortStableFunc(vs, func(a, b verifiedMapping) int {
		return cmp.Or(b.SignedAt.Compare(a.SignedAt), cmp.Compare(len(a.Record.Rules), len(b.Record.Rules)))
	})
	for i, v := range vs {
		allowed, err := jr.in.Authorize(ctx, v.User, uid)
		switch {
		case jr.in.Forbidden(err):
			jr.noteCluster(noteReviewForbidden, uid)
			return nil
		case err != nil:
			jr.noteCluster(noteReviewFailed, uid)
			return nil
		case allowed:
			for _, old := range vs[i+1:] {
				jr.in.Records.Reject(delivery.ClusterJoinStream(uid), delivery.PredicateClusterGroupMapping, old.Ref)
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

// verifyMapping opens f, a group mapping record, as verifyRecord does, and
// says why it does not verify, "" when it does.
func (jr *joinRead) verifyMapping(f Fetched) (verifiedMapping, string) {
	v := verifiedMapping{Ref: f.Manifest}
	var why string
	v.User, v.SignedAt, why = jr.in.VerifyPerson(jr.in.Policy, "group mapping", func(verify func([]byte) error) (string, error) {
		st, err := delivery.OpenClusterGroupMapping(f.Bundle, verify)
		v.Record = st.Predicate
		return st.Predicate.Approver, err
	})
	return v, why
}
