package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"oras.land/oras-go/v2/registry/remote"

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
	// joinVerdictTTL is how long an approve review is remembered.
	joinVerdictTTL = time.Minute
	// maxJoinVerdicts bounds the reviews remembered.
	maxJoinVerdicts = 256
	// clusterJoinsID names the source that says the joins were read partly.
	clusterJoinsID = deliveryrecord.JoinsSourceID
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
		switch {
		case err != nil:
			l := unresolvedJoins(now, err)
			l.CandidatesCompleteness = fleet.CompletenessPartial
			l.Limitations = append(l.Limitations, fleet.Limitation{Code: fleet.LimitationSourceUnavailable, Source: delivery.CandidatesID,
				Message: "the candidate clusters were not read: " + err.Error()})
			return l
		case r.Repository == "" && r.Candidates != nil:
			// No member can join, yet the candidates the operator wrote show.
			return withCandidates(ctx, unresolvedJoins(now, nil), r.Candidates, now)
		case r.Repository == "":
			return unresolvedJoins(now, nil)
		}
		opts = r
	}
	p, why, unread := readPolicy(ctx, opts.Delivery)
	r := &snapshotReader{cache: &s.snapshots, now: now}
	list, _ := deliveryrecord.ReadJoins(ctx, deliveryrecord.JoinRead{Repository: opts.Repository, Hub: opts.Hub, Include: opts.Include,
		Policy: p, PolicyWhy: why, PolicyUnread: unread, Records: &s.joinRecords,
		Authorize: func(ctx context.Context, user, uid string) (bool, error) {
			return s.authorizeRecord(ctx, opts.Authorize, user, uid, now)
		},
		Fetch: func(ctx context.Context, repo *remote.Repository, reference string) ([]byte, string, error) {
			return r.fetch(ctx, repo, opts.Repository, reference)
		},
		Forbidden: apierrors.IsForbidden, VerifyPerson: verifyPerson, OpenKey: signing.OpenKey, Open: openTargets, Now: now})
	if opts.Candidates == nil {
		return list
	}
	return withCandidates(ctx, list, opts.Candidates, now)
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

// readPolicy reads the hub's approver signers, why no record counts ("" when
// some may) and whether they could not be read.
func readPolicy(ctx context.Context, d *Delivery) (delivery.ClassPolicy, string, bool) {
	p, err := d.classPolicy(ctx)
	switch {
	case err != nil:
		return p, "the hub's approver signers could not be read, so no approval counts: " + err.Error(), true
	case len(p.Signers) == 0:
		return p, noSigners, false
	}
	return p, "", false
}

// openTargets opens the pacto-targets repository path with the ambient
// registry credentials.
func openTargets(path string) (*remote.Repository, error) {
	return ocireferrer.Open(path, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
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

// clusterJoinSources are the sources of the members this hub approved, read
// through r, the build's one snapshot reader, each verified only under its
// approved key scoped to its UID. A member r already read is skipped. A list
// read partly adds one partial source that says what was not read. Once a
// person approved or removed a member, pending clusters add one count for
// gates ([delivery.UnreadClusters]), never a source state.
func (s *Service) clusterJoinSources(ctx context.Context, r *snapshotReader, opts JoinOptions) []fleet.Source {
	list := s.ClusterJoins(ctx, membersOnly(opts))
	var out []fleet.Source
	if list.Completeness != fleet.CompletenessComplete { // its limitations name no cluster
		id := r.claim(clusterJoinsID)
		out = append(out, fleet.NewMemorySource(id, fleet.SourceKindTargetSnapshot, &fleet.Collection{
			State: &fleet.SourceState{Status: fleet.SourcePartial}, Limitations: withSource(id, list.Limitations)}))
	}
	if unread := delivery.UnreadClusters(list); unread != nil { // a count for gates, never a source state
		out = append(out, unread)
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
	case !deliveryrecord.SelfSigned(bundle, rec.Cluster, *st.Predicate.Join, signing.OpenKey):
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
	res, why := readDetection(ctx, dyn, ref, now)
	switch {
	case why != "":
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

// AdvertisedChartVersion is the chart version the operator's detection at
// ref advertises (installation.chartVersion), read as AdvertisedTargets
// reads it; "" when it names none.
func AdvertisedChartVersion(ctx context.Context, dyn dynamic.Interface, ref string, now func() time.Time) (string, error) {
	res, why := readDetection(ctx, dyn, ref, now)
	switch {
	case why != "":
		return "", fmt.Errorf("the detection %s names no chart version: %s", ref, why)
	case res.Installation == nil:
		return "", nil
	}
	return res.Installation.ChartVersion, nil
}

// readDetection reads the operator's detection at ref (k8s:<namespace>/<name>
// with dyn, or a file); why says why it is not available.
func readDetection(ctx context.Context, dyn dynamic.Interface, ref string, now func() time.Time) (detect.Result, string) {
	src := fleetsrc.NewDetectionFileSource("detection", ref, now)
	if rest, ok := strings.CutPrefix(ref, "k8s:"); ok {
		ns, name, _ := strings.Cut(rest, "/")
		src = fleetsrc.NewDetectionK8sSource("detection", dyn, ns, name, now)
	}
	res, st := src.Read(ctx)
	if st.Status == fleet.SourceAvailable {
		return res, ""
	}
	if st.Error != nil {
		return res, st.Error.Message
	}
	return res, string(st.Status)
}

// PlanMembers reads, for each promote, the members of the hub joins
// resolves to, as the fleet reads them: the join list and each approved
// member's snapshot through the shared snapshot cache, verified only under
// its approved key scoped to its UID. The hub's own Pactos sit in the hub's
// cluster. nil joins gives nil: no members.
func (s *Service) PlanMembers(joins *JoinOptions) func(ctx context.Context, environment string) delivery.Members {
	if joins == nil {
		return nil
	}
	return func(ctx context.Context, environment string) delivery.Members {
		list := s.ClusterJoins(ctx, membersOnly(*joins))
		trust := delivery.JoinTrust(list.Clusters)
		r := &snapshotReader{cache: &s.snapshots, now: s.env().now()}
		repo, _ := openTargets(list.Repository) // read only for an approved member: the list was read
		read := func(uid string) (delivery.TargetSnapshot, error) {
			return r.member(ctx, repo, list.Repository, trust, uid)
		}
		var home *fleet.Cluster
		if list.Hub.UID != "" {
			home = &fleet.Cluster{ID: list.Hub.UID, Name: list.Hub.Name}
		}
		return delivery.MembersOf(list, read, environment, r.now, home)
	}
}

// member reads the snapshot of member uid from repo (at path), verified
// under trust, the approved keys, each covering only its own member's
// stream. ClusterJoins read it a moment before; a tag that moved since is
// an error, so the member's consumers read unknown.
func (r *snapshotReader) member(ctx context.Context, repo *remote.Repository, path string, trust *delivery.TrustConfig, uid string) (delivery.TargetSnapshot, error) {
	b, _, err := r.fetch(ctx, repo, path, delivery.TargetSnapshotTagPrefix+uid)
	if err != nil {
		return delivery.TargetSnapshot{}, err
	}
	st, _, err := delivery.OpenTargetSnapshot(b, trust, signing.OpenKey)
	if err == nil && st.Predicate.Cluster != uid {
		err = errors.New("its tag holds the snapshot of cluster " + st.Predicate.Cluster)
	}
	return st.Predicate, err
}
