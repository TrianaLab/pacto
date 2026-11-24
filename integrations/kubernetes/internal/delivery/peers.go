/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"sync"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// peerListTTL is how long the promoter reuses a join list and the members
// it read: one list per period.
// ponytail: one list per 30 s; watch the anchor tags if promotes need fresher.
const peerListTTL = 30 * time.Second

// peerVerdictTTL is how long an approve review is remembered.
const peerVerdictTTL = time.Minute

// maxPeerVerdicts bounds the remembered reviews.
const maxPeerVerdicts = 1024

// clusterJoinResource is the virtual resource a person needs approve on to
// approve a cluster's join: no CRD serves it.
var clusterJoinResource = authorizationv1.ResourceAttributes{Group: "pacto.trianalab.io", Resource: "clusterjoins", Verb: "approve"}

// PeerOptions say how the promoter reads the clusters that joined its
// cluster, the hub.
type PeerOptions struct {
	// Repository is the <registry>/pacto-targets repository members publish
	// to. Empty reads none: the promoter projects onto its own Pactos.
	Repository string
	// ClusterID is this cluster's UID, the hub every counted record names.
	ClusterID string
	// Member says this cluster is a member of a hub (--member): it reads no
	// joins and counts other clusters as unread.
	Member bool
	// Policy holds the approver signers join records verify under.
	Policy delivery.ClassPolicy
	// Client asks the SubjectAccessReviews.
	Client client.Client
	// Registry reaches the repository.
	Registry ocireferrer.Options
	Now      func() time.Time
}

// PeerMembers is what a promote planned in environment reads beyond the
// promoter's own Pactos (Cluster.Members), computed as every hub surface
// computes it: deliveryrecord.ReadJoins and delivery.MembersOf.
func PeerMembers(o PeerOptions) func(ctx context.Context, environment string) delivery.Members {
	return (&peerReader{o: o}).members
}

// peerReader reads the join list and the members of this hub, through one
// review function and one result cache.
type peerReader struct {
	o       PeerOptions
	records deliveryrecord.Reader

	mu    sync.Mutex
	list  delivery.ClusterJoinList
	repo  *remote.Repository
	until time.Time
	byEnv map[string]delivery.Members
	// vmu guards verdicts: ReadJoins reviews clusters at once.
	vmu      sync.Mutex
	verdicts map[string]peerVerdict
}

type peerVerdict struct {
	allowed bool
	until   time.Time
}

// members are the members a promote in environment reads: none without a
// repository, one unread cluster on a member, else MembersOf the join list,
// each approved member read under its approved key.
func (p *peerReader) members(ctx context.Context, environment string) delivery.Members {
	switch {
	case p.o.Member:
		return delivery.MemberOnlyUnread()
	case p.o.Repository == "":
		return delivery.Members{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	list, repo := p.joinsLocked(ctx)
	if m, ok := p.byEnv[environment]; ok {
		return m
	}
	trust := delivery.JoinTrust(list.Clusters)
	read := func(uid string) (delivery.TargetSnapshot, error) {
		b, _, err := p.fetch(ctx, repo, delivery.TargetSnapshotTagPrefix+uid)
		if err != nil {
			return delivery.TargetSnapshot{}, err
		}
		return deliveryrecord.OpenMember(b, trust, signing.OpenKey, uid)
	}
	m := delivery.MembersOf(list, read, environment, p.o.Now(), &fleet.Cluster{ID: p.o.ClusterID})
	p.byEnv[environment] = m
	return m
}

// joins is the join list of this hub, read at most once per peerListTTL.
func (p *peerReader) joins(ctx context.Context) delivery.ClusterJoinList {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, _ := p.joinsLocked(ctx)
	return l
}

// joinsLocked is joins with p.mu held, and the repository it listed.
func (p *peerReader) joinsLocked(ctx context.Context) (delivery.ClusterJoinList, *remote.Repository) {
	now := p.o.Now()
	if now.Before(p.until) {
		return p.list, p.repo
	}
	ctx, cancel := context.WithTimeout(ctx, delivery.TargetSnapshotReadTimeout)
	defer cancel()
	why := ""
	if len(p.o.Policy.Signers) == 0 {
		why = "this hub accepts no signed approvals: set delivery.identities.signers on the hub's operator"
	}
	p.list, p.repo = deliveryrecord.ReadJoins(ctx, deliveryrecord.JoinRead{Repository: p.o.Repository, Hub: p.o.ClusterID,
		Policy: p.o.Policy, PolicyWhy: why, Records: &p.records, Authorize: p.review, Fetch: p.fetch,
		Forbidden: apierrors.IsForbidden, VerifyPerson: signing.VerifyPersonRecord, OpenKey: signing.OpenKey,
		Open: func(path string) (*remote.Repository, error) { return ocireferrer.Open(path, p.o.Registry) }, Now: now})
	p.until, p.byEnv = now.Add(peerListTTL), map[string]delivery.Members{}
	return p.list, p.repo
}

// fetch reads the snapshot bundle at reference and the ref it resolved to.
func (p *peerReader) fetch(ctx context.Context, repo *remote.Repository, reference string) ([]byte, string, error) {
	b, desc, err := deliveryrecord.FetchTargetSnapshot(ctx, repo, reference)
	if err != nil {
		return nil, "", err
	}
	return b, p.o.Repository + "@" + desc.Digest.String(), nil
}

// review asks the apiserver whether user, with no groups (a certificate
// names none), may approve clusterjoins named uid. A review the authorizers
// could not evaluate is an error, never a refusal or a grant. An answer is
// remembered for a minute; an error never is.
func (p *peerReader) review(ctx context.Context, user, uid string) (bool, error) {
	key, now := user+"\x00"+uid, p.o.Now()
	p.vmu.Lock()
	v, ok := p.verdicts[key]
	p.vmu.Unlock()
	if ok && now.Before(v.until) {
		return v.allowed, nil
	}
	res := clusterJoinResource
	res.Name = uid
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user, ResourceAttributes: &res}}
	ctx, cancel := context.WithTimeout(ctx, accessTimeout)
	defer cancel()
	if err := p.o.Client.Create(ctx, sar); err != nil {
		return false, err
	}
	if !sar.Status.Allowed && sar.Status.EvaluationError != "" {
		return false, errors.New("the review could not be evaluated: " + sar.Status.EvaluationError)
	}
	p.vmu.Lock()
	defer p.vmu.Unlock()
	// ponytail: cleared whole when full; an LRU if a hub ever reviews more.
	if p.verdicts == nil || len(p.verdicts) >= maxPeerVerdicts {
		p.verdicts = map[string]peerVerdict{}
	}
	p.verdicts[key] = peerVerdict{allowed: sar.Status.Allowed, until: now.Add(peerVerdictTTL)}
	return sar.Status.Allowed, nil
}
