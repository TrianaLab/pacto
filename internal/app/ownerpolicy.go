package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// OwnerPolicy is the owner policy that decides who may set the owner of a
// service whose bundle is ref (repository path), and when there is none, why.
// A server with OwnerPolicyPath reads its mount. Anyone else reads the
// ownerPolicy section of the signed target snapshots beside ref, verified
// with the trust config at trust (else $PACTO_DELIVERY_TRUST): only
// snapshots a key scoped to their cluster signed, complete and fresh, count,
// and only when every one carries the same policy. Anything less is no
// policy: unknown, never an empty one.
func (s *Service) OwnerPolicy(ctx context.Context, ref, trust string) (*delivery.OwnerPolicy, string) {
	if s.OwnerPolicyPath != "" {
		return delivery.ReadOwnerPolicy(s.OwnerPolicyPath)
	}
	tc, err := DeliveryTrust(trust)
	switch {
	case err != nil:
		return nil, "trust unreadable: " + err.Error()
	case tc == nil:
		return nil, "trust missing: no delivery trust config verifies the target snapshots that carry it"
	}
	found, _, failing := s.findTargetSnapshot(ctx, "oci://"+ref)
	switch {
	case failing != nil:
		return nil, "the target snapshots beside " + ref + " could not be read"
	case found == "":
		return nil, "no target snapshot was found beside " + ref
	}
	return s.snapshotOwnerPolicy(ctx, contract.TrimOCIScheme(found), tc)
}

// snapshotOwnerPolicy is the one owner policy every cluster's snapshot in the
// pacto-targets repository path carries.
func (s *Service) snapshotOwnerPolicy(ctx context.Context, path string, trust *delivery.TrustConfig) (*delivery.OwnerPolicy, string) {
	repo, err := ocireferrer.Open(path, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
	var tags []string
	truncated := false
	if err == nil {
		tags, truncated, err = deliveryrecord.ListTargetSnapshotTags(ctx, repo)
	}
	if err != nil || truncated {
		return nil, "the clusters in " + path + " could not all be listed"
	}
	r := &snapshotReader{cache: &s.snapshots, trust: trust, now: s.env().now()}
	var policy *delivery.OwnerPolicy
	var canonical []byte
	for _, tag := range tags {
		p, why := r.ownerPolicy(ctx, repo, path, tag)
		if p == nil {
			return nil, why
		}
		c, _ := json.Marshal(p) // ponytail: encoding/json sorts map keys, so equal policies marshal equal
		if canonical != nil && !bytes.Equal(c, canonical) {
			return nil, "clusters disagree on the owner policy"
		}
		policy, canonical = p, c
	}
	return policy, ""
}

// ownerPolicy is the owner policy of the snapshot tagged tag, when it counts:
// signed by a key scoped to its cluster, named for the cluster of its tag,
// complete and fresh.
func (r *snapshotReader) ownerPolicy(ctx context.Context, repo *remote.Repository, path, tag string) (*delivery.OwnerPolicy, string) {
	cluster := strings.TrimPrefix(tag, delivery.TargetSnapshotTagPrefix)
	b, _, err := r.fetch(ctx, repo, path, tag)
	if err != nil {
		return nil, "the target snapshot of cluster " + cluster + " could not be read: " + err.Error()
	}
	st, v, err := delivery.OpenTargetSnapshot(b, r.trust, signing.OpenKey)
	snap := st.Predicate
	asOf, _ := time.Parse(time.RFC3339, snap.AsOf)
	switch {
	case err != nil:
		return nil, "the target snapshot of cluster " + cluster + " does not verify: " + err.Error()
	case !v.Scoped:
		return nil, "unscoped: the target snapshot of cluster " + cluster + " is signed by a key not scoped to it"
	case snap.Cluster != cluster:
		return nil, "the target snapshot under the tag of cluster " + cluster + " names cluster " + snap.Cluster
	case snap.Completeness != fleet.CompletenessComplete:
		return nil, "partial: the target snapshot of cluster " + cluster + " is " + string(snap.Completeness)
	case r.now.Sub(asOf) > delivery.TargetSnapshotFreshness || asOf.After(r.now.Add(targetSnapshotSkew)):
		return nil, "stale: the target snapshot of cluster " + cluster + " is from " + snap.AsOf
	case snap.OwnerPolicy == nil:
		return nil, "the target snapshot of cluster " + cluster + " carries no owner policy"
	}
	return snap.OwnerPolicy, ""
}
