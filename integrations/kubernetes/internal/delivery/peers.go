/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const (
	// peersTTL is how long one read of the members is used.
	peersTTL = 30 * time.Second
	// peerVerdictTTL is how long an approve review is remembered.
	peerVerdictTTL = time.Minute
	// maxPeerCache bounds the reviews and verified join records remembered.
	maxPeerCache = 4096
)

// clusterJoinResource is the virtual resource an approver of a member must
// be allowed to approve, in this cluster, for that member to be a peer here.
var clusterJoinResource = authorizationv1.ResourceAttributes{Group: "pacto.trianalab.io", Resource: "clusterjoins", Verb: "approve"}

// Peers finds the member that runs the upstream rung of a rung this cluster
// hosts, and the one Stage stream of it to follow. A member is a peer only
// when the hub that approved this cluster's own key (delivery.OwnHub)
// approved it by a record this cluster's approver signers verify, and this
// cluster lets that approver approve clusterjoins named it. Its recorder key
// then verifies that one stream and nothing else (Key).
type Peers struct {
	// Client asks this cluster's apiserver the approve reviews.
	Client client.Client
	// Targets is the <registry>/pacto-targets repository, read with Options.
	Targets string
	Options ocireferrer.Options
	// ClusterID is this cluster's uid; Policy names the approver signers.
	ClusterID string
	Policy    delivery.ClassPolicy
	Now       func() time.Time

	// openJoin verifies a join record; tests replace it.
	openJoin func(delivery.ClassPolicy, []byte) delivery.VerifiedJoin

	mu       sync.Mutex
	reader   deliveryrecord.Reader
	view     peerView
	viewAt   time.Time
	verdicts map[string]peerVerdict
	joins    map[string]delivery.VerifiedJoin
	// bound is, by service and rung, the key that verifies the one stream
	// each placement follows; bmu guards it apart from a read.
	bmu   sync.Mutex
	bound map[string]delivery.TrustedKey
}

// peerView is one read of the members: the peers, or why none can be.
type peerView struct {
	why     string
	members []delivery.ClusterJoinState
}

type peerVerdict struct {
	allowed bool
	until   time.Time
}

// noTrustedPeer is why a rung whose upstream runs on no peer stays unplaced.
func noTrustedPeer(rung string) string {
	return "upstream " + rung + " runs on no member this cluster trusts: ask an approver who may approve clusterjoins here to approve the member that runs it"
}

// upstream is the Stage stream to follow for svc in rung, whose upstream
// rung this cluster does not host, or nil and why the rung stays unplaced.
// A rule naming clusters for (svc, rung) limits the members considered.
// Pacto never falls back to the registry.
func (p *Peers) upstream(ctx context.Context, svc delivery.ServiceRef, rung string, rules []delivery.PlacementRule) (*delivery.RemoteStage, string) {
	if p == nil {
		return nil, noTrustedPeer(rung)
	}
	bind := svc.Domain + "/" + svc.Name + "\x00" + rung
	p.bind(bind, nil)
	p.mu.Lock()
	v := p.peers(ctx)
	p.mu.Unlock()
	if v.why != "" {
		return nil, "upstream " + rung + " runs on another member, and " + v.why
	}
	var named []string
	for _, r := range rules {
		if r.Places(svc.Name, rung) {
			named = append(named, r.ClusterUIDs...)
		}
	}
	var hosts []delivery.ClusterJoinState
	var streams [][]delivery.JoinStream
	for _, m := range v.members {
		if len(named) > 0 && !slices.Contains(named, m.UID) {
			continue
		}
		ss := slices.DeleteFunc(slices.Clone(m.Streams), func(s delivery.JoinStream) bool { return s.Service != svc || s.Environment != rung })
		if len(ss) > 0 || len(m.Environments) > 0 && len(delivery.HostedRungs([]delivery.LadderRung{{Name: rung}}, m.Environments)) == 1 {
			hosts, streams = append(hosts, m), append(streams, ss)
		}
	}
	switch {
	case len(hosts) == 0:
		return nil, noTrustedPeer(rung)
	case len(hosts) > 1:
		names := make([]string, len(hosts))
		for i, h := range hosts {
			names[i] = h.Name
		}
		return nil, "upstream " + rung + " runs on " + strings.Join(names, " and ") + ": name one in a placement rule (clusters, by uid)"
	case len(streams[0]) == 0:
		return nil, "upstream " + rung + " on " + hosts[0].Name + " records no Stage stream yet"
	case len(streams[0]) > 1:
		return nil, fmt.Sprintf("upstream %s on %s records %d Stage streams of %s, so Pacto cannot tell which one to follow", rung, hosts[0].Name, len(streams[0]), svc.Name)
	case hosts[0].RecorderKey == nil:
		return nil, "upstream " + rung + " on " + hosts[0].Name + " advertises no recorder key: set delivery.recorder.signing.publicKey on " + hosts[0].Name
	}
	m, stream := hosts[0], streams[0][0].Stream
	key := *m.RecorderKey
	key.Name, key.Role, key.Streams, key.UntilSeq, key.UntilRecord = "member-recorder:"+m.Name, "", []string{stream}, 0, ""
	p.bind(bind, &key)
	return &delivery.RemoteStage{Repository: m.StreamsRepository, Stream: stream}, ""
}

// bind sets the key of placement b, or removes it when k is nil.
func (p *Peers) bind(b string, k *delivery.TrustedKey) {
	p.bmu.Lock()
	defer p.bmu.Unlock()
	if k == nil {
		delete(p.bound, b)
		return
	}
	if p.bound == nil {
		p.bound = map[string]delivery.TrustedKey{}
	}
	p.bound[b] = *k
}

// Key is the recorder key of the peer whose stream a placement follows,
// scoped to that stream, or nil when no placement follows stream.
func (p *Peers) Key(stream string) *delivery.TrustedKey {
	p.bmu.Lock()
	defer p.bmu.Unlock()
	for _, k := range p.bound {
		if k.Streams[0] == stream {
			return &k
		}
	}
	return nil
}

// peers is the last read of the members, read again after peersTTL.
func (p *Peers) peers(ctx context.Context) peerView {
	if now := p.Now(); p.viewAt.IsZero() || !now.Before(p.viewAt.Add(peersTTL)) {
		p.view, p.viewAt = p.read(ctx), now
	}
	return p.view
}

// read reads this cluster's hub and the members it approved that are peers
// here, under one read timeout.
func (p *Peers) read(ctx context.Context) peerView {
	ctx, cancel := context.WithTimeout(ctx, delivery.TargetSnapshotReadTimeout)
	defer cancel()
	repo, err := ocireferrer.Open(p.Targets, p.Options)
	var tags []string
	if err == nil {
		tags, _, err = deliveryrecord.ListClusterTags(ctx, repo)
	}
	if err != nil {
		return peerView{why: "the members in " + p.Targets + " could not be read (" + err.Error() + ")"}
	}
	own := p.candidate(ctx, repo, p.ClusterID)
	keyID := ""
	if own.SelfSigned {
		keyID = own.Join.KeyID
	}
	hub, why := delivery.OwnHub(p.ClusterID, keyID, p.records(ctx, repo, p.ClusterID, "").Verified)
	if hub == "" {
		return peerView{why: "no hub approved this cluster's own key (" + why + "): approve this cluster on its hub first"}
	}
	var cands []delivery.JoinCandidate
	recs := map[string]delivery.JoinRecords{}
	// ponytail: the first MaxTargetSnapshots members by uid; a member past
	// them is never a peer. Page them if an installation joins more.
	for _, t := range tags[:min(len(tags), delivery.MaxTargetSnapshots)] {
		if uid := strings.TrimPrefix(t, delivery.TargetSnapshotTagPrefix); uid != p.ClusterID {
			recs[uid] = p.records(ctx, repo, uid, hub)
			cands = append(cands, p.candidate(ctx, repo, uid))
		}
	}
	states, _ := delivery.JoinStates(hub, cands, recs)
	v := peerView{}
	for _, st := range states {
		if st.State == delivery.JoinApproved {
			v.members = append(v.members, st)
		}
	}
	return v
}

// records reads and verifies the join records of uid. Those naming hub
// count only when this cluster lets their approver approve uid; with no
// hub, none is reviewed.
func (p *Peers) records(ctx context.Context, repo *remote.Repository, uid, hub string) delivery.JoinRecords {
	fetched, cut, _, err := p.reader.ListClusterJoins(ctx, repo, uid)
	rs := delivery.JoinRecords{Cut: cut}
	if err != nil {
		rs.Err = err.Error()
	}
	for _, f := range fetched {
		v := p.verify(f)
		if hub != "" && v.Verified && v.Record.Hub == hub {
			allowed, err := p.review(ctx, v.User, uid)
			switch {
			case err != nil:
				v.Counted, v.Why = false, "the approval by "+v.User+" could not be checked here: "+err.Error()
			case !allowed:
				v.Counted, v.Why = false, v.User+" may not approve clusterjoins in this cluster"
			}
		}
		rs.Verified = append(rs.Verified, v)
	}
	return rs
}

// verify is f verified under the approver signers, remembered by manifest.
// ponytail: cleared whole when full; it only saves signature checks.
func (p *Peers) verify(f deliveryrecord.Fetched) delivery.VerifiedJoin {
	if v, ok := p.joins[f.Manifest]; ok {
		return v
	}
	open := p.openJoin
	if open == nil {
		open = signing.VerifyJoinRecord
	}
	v := open(p.Policy, f.Bundle)
	v.Ref, v.Counted = f.Manifest, v.Verified
	if p.joins == nil || len(p.joins) >= maxPeerCache {
		p.joins = map[string]delivery.VerifiedJoin{}
	}
	p.joins[f.Manifest] = v
	return v
}

// review asks this cluster whether user, with no groups, may approve
// clusterjoins named uid. An answer is remembered a minute; an error never.
// ponytail: cleared whole when full.
func (p *Peers) review(ctx context.Context, user, uid string) (bool, error) {
	k, now := user+"\x00"+uid, p.Now()
	if v, ok := p.verdicts[k]; ok && now.Before(v.until) {
		return v.allowed, nil
	}
	res := clusterJoinResource
	res.Name = uid
	allowed, err := reviewUser(ctx, p.Client, user, res)
	if err != nil {
		return false, err
	}
	if p.verdicts == nil || len(p.verdicts) >= maxPeerCache {
		p.verdicts = map[string]peerVerdict{}
	}
	p.verdicts[k] = peerVerdict{allowed: allowed, until: now.Add(peerVerdictTTL)}
	return allowed, nil
}

// candidate reads the snapshot of uid and checks its own key signed it.
func (p *Peers) candidate(ctx context.Context, repo *remote.Repository, uid string) delivery.JoinCandidate {
	c := delivery.JoinCandidate{UID: uid}
	b, _, err := deliveryrecord.FetchTargetSnapshot(ctx, repo, delivery.TargetSnapshotTagPrefix+uid)
	var st delivery.TargetSnapshotStatement
	if err == nil {
		st, _, err = delivery.OpenTargetSnapshot(b, nil, signing.OpenKey)
	}
	switch {
	case err != nil:
		c.Err = "its snapshot could not be used: " + err.Error()
		return c
	case st.Predicate.Cluster != uid:
		c.Err = "its snapshot names cluster " + st.Predicate.Cluster
		return c
	}
	j := st.Predicate.Join
	c.Join, c.AsOf = j, st.Predicate.AsOf
	if j != nil {
		self := delivery.JoinTrust([]delivery.ClusterJoinState{{UID: uid, Name: j.Name, PublicKey: j.PublicKey, State: delivery.JoinApproved}})
		_, v, err := delivery.OpenTargetSnapshot(b, self, signing.OpenKey)
		c.SelfSigned = err == nil && v.Scoped
	}
	return c
}

// upstreamReadTimeout bounds one read of an upstream stream.
const upstreamReadTimeout = 30 * time.Second

// maxMemberReaders bounds the readers kept for member keys. A var so tests
// lower it.
var maxMemberReaders = 256

// UpstreamReader reads what a Stage in another cluster verified from its
// signed records (Cluster.Upstream): verified under static, the promoter's
// upstream trust config (nil: none), and, for a stream member names a key
// for (Peers.Key), under that key too, scoped to that stream alone. Nil
// when both are nil: no upstream key is trusted.
func UpstreamReader(static *delivery.TrustConfig, member func(stream string) *delivery.TrustedKey, opts ocireferrer.Options) (
	func(context.Context, delivery.RemoteStage, delivery.RecordMark, []string) (*delivery.StageStatus, string), error,
) {
	if static == nil && member == nil {
		return nil, nil
	}
	base := delivery.TrustConfig{}
	if static != nil {
		base = *static
	}
	verify, err := base.Verifier(signing.OpenKey)
	if err != nil {
		return nil, err
	}
	// One Reader per trust: every record it verified is fetched once, and a
	// record it could not verify is never read again under the same keys.
	reader := &deliveryrecord.Reader{Verify: verify}
	var mu sync.Mutex
	readers := map[string]*deliveryrecord.Reader{}
	readerFor := func(stream string) (*deliveryrecord.Reader, string) {
		var k *delivery.TrustedKey
		if member != nil {
			k = member(stream)
		}
		switch {
		case k == nil && static == nil:
			return nil, noUpstreamTrust
		case k == nil:
			return reader, ""
		}
		id, _ := json.Marshal(k) // plain data
		mu.Lock()
		defer mu.Unlock()
		if rd := readers[string(id)]; rd != nil {
			return rd, ""
		}
		v, err := delivery.TrustConfig{Keys: append(slices.Clone(base.Keys), *k)}.Verifier(signing.OpenKey)
		if err != nil {
			return nil, "the recorder key " + k.Name + " cannot verify records: " + err.Error()
		}
		// ponytail: cleared whole when full; a member key changes rarely.
		if len(readers) >= maxMemberReaders {
			clear(readers)
		}
		readers[string(id)] = &deliveryrecord.Reader{Verify: v}
		return readers[string(id)], ""
	}
	return func(ctx context.Context, r delivery.RemoteStage, seen delivery.RecordMark, acked []string) (*delivery.StageStatus, string) {
		rd, why := readerFor(r.Stream)
		if rd == nil {
			return nil, why
		}
		repo, err := ocireferrer.Open(r.Repository, opts)
		if err != nil {
			return nil, err.Error()
		}
		ctx, cancel := context.WithTimeout(ctx, upstreamReadTimeout)
		defer cancel()
		return rd.Upstream(ctx, repo, r.Stream, seen, acked)
	}, nil
}
