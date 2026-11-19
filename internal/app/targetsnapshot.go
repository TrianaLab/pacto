package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// TrustEnv names the delivery trust config file when no flag does.
const TrustEnv = "PACTO_DELIVERY_TRUST"

// DeliveryTrust reads the trust config delivery records and target snapshots
// are verified with: the file path names, or $PACTO_DELIVERY_TRUST. None is
// nil: they are read unverified, and every answer says so.
func DeliveryTrust(path string) (*delivery.TrustConfig, error) {
	path = cmp.Or(path, os.Getenv(TrustEnv))
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the delivery trust config: %w", err)
	}
	t, err := delivery.ParseTrust(raw)
	if err == nil {
		_, err = signing.TrustVerifier(t)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

const (
	// targetSnapshotsID names the source of a whole pacto-targets repository:
	// one that cannot be listed, or a listing cut at its bound.
	targetSnapshotsID = "targets"
	// targetSnapshotSkew is how far past now a snapshot's asOf may be before
	// it reads as forged.
	targetSnapshotSkew = 2 * time.Minute
	// maxTargetSnapshotCache bounds the snapshots a process keeps.
	maxTargetSnapshotCache = 64
)

// targetSnapshotCache keeps the digest-verified bundle of each snapshot
// manifest a process read. It keeps no verdict: verification and staleness
// run on every read, so a removed key or an aging snapshot takes effect at once.
type targetSnapshotCache struct {
	mu      sync.Mutex
	bundles map[digest.Digest][]byte
}

func (c *targetSnapshotCache) get(d digest.Digest) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.bundles[d]
	return b, ok
}

// put remembers b under d.
// ponytail: cleared whole when full (64 snapshots, two full repositories); an
// LRU when one process reads more clusters than that.
func (c *targetSnapshotCache) put(d digest.Digest, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bundles == nil || len(c.bundles) >= maxTargetSnapshotCache {
		c.bundles = map[digest.Digest][]byte{}
	}
	c.bundles[d] = b
}

// snapshotRef is one --target-snapshot value: a repository path, and the
// cluster-<uid> tag or digest it pins (empty for every cluster tag).
type snapshotRef struct{ repo, reference string }

// parseSnapshotRefs reads the --target-snapshot values: one repository, or
// one or more pinned cluster-<uid> tags or digests. A repo with another
// value, or a tag other than cluster-<uid>, is a usage error.
func parseSnapshotRefs(values []string) ([]snapshotRef, error) {
	var out []snapshotRef
	repos := 0
	for _, v := range values {
		p := contract.TrimOCIScheme(v)
		ref := snapshotRef{repo: p}
		if repo, d, ok := strings.Cut(p, "@"); ok {
			ref = snapshotRef{repo, d}
		} else if i := strings.LastIndex(p, ":"); i > strings.LastIndex(p, "/") {
			ref = snapshotRef{p[:i], p[i+1:]}
			if !strings.HasPrefix(ref.reference, delivery.TargetSnapshotTagPrefix) {
				return nil, fmt.Errorf("--target-snapshot %q: pin a %s<uid> tag or a digest", v, delivery.TargetSnapshotTagPrefix)
			}
		} else {
			repos++
		}
		if ref.repo == "" || !strings.Contains(ref.repo, "/") {
			return nil, fmt.Errorf("--target-snapshot %q: want oci://<registry>/%s", v, delivery.TargetSnapshotRepository)
		}
		out = append(out, ref)
	}
	if repos > 0 && len(out) > 1 {
		return nil, errors.New("--target-snapshot takes one repository, or one or more pinned cluster tags or digests")
	}
	return out, nil
}

// targetSnapshotSources reads the target snapshots opts.TargetSnapshot names
// as fleet sources, all within delivery.TargetSnapshotReadTimeout: per
// snapshot one target-snapshot source of its Pactos and one workload source
// of its inferred services, both on the snapshot's cluster. A repository that
// cannot be listed is one failing source. A ref or trust config that does
// not parse is an error. No ref reads nothing.
func (s *Service) targetSnapshotSources(ctx context.Context, opts FleetOptions) ([]fleet.Source, error) {
	if len(opts.TargetSnapshot) == 0 {
		return nil, nil
	}
	refs, err := parseSnapshotRefs(opts.TargetSnapshot)
	trust, terr := DeliveryTrust(opts.TargetSnapshotTrustFile)
	if err := errors.Join(err, terr); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(s.sys.snapshotTimeout, delivery.TargetSnapshotReadTimeout))
	defer cancel()
	now := s.env().now
	if opts.Now != nil {
		now = opts.Now
	}
	r := &snapshotReader{cache: &s.snapshots, trust: trust, now: now(), seen: map[string]int{}}
	var out []fleet.Source
	for _, ref := range refs {
		out = append(out, r.resolveTargetSnapshot(ctx, ref)...)
	}
	return out, nil
}

// snapshotReader reads the snapshots of one fleet build.
type snapshotReader struct {
	cache *targetSnapshotCache
	trust *delivery.TrustConfig
	now   time.Time
	// seen counts the snapshots read per cluster, so two refs of one cluster
	// keep distinct source ids.
	seen map[string]int
}

// resolveTargetSnapshot reads the snapshot ref pins, or every cluster tag of
// its repository.
func (r *snapshotReader) resolveTargetSnapshot(ctx context.Context, ref snapshotRef) []fleet.Source {
	repo, err := ocireferrer.Open(ref.repo, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
	if err != nil {
		return []fleet.Source{fleet.NewFailingSource(targetSnapshotsID, fleet.SourceKindTargetSnapshot, err)}
	}
	if ref.reference != "" {
		return r.snapshot(ctx, repo, ref.repo, ref.reference)
	}
	tags, truncated, err := deliveryrecord.ListTargetSnapshotTags(ctx, repo)
	if err != nil {
		return []fleet.Source{fleet.NewFailingSource(targetSnapshotsID, fleet.SourceKindTargetSnapshot, err)}
	}
	var out []fleet.Source
	if truncated {
		out = append(out, unusedSnapshot(targetSnapshotsID, nil, fleet.SourcePartial, fleet.LimitationTargetSnapshotBound,
			fmt.Sprintf("%s holds more cluster tags than the %d a read takes; the clusters past them are unknown", ref.repo, delivery.MaxTargetSnapshots)))
	}
	for _, tag := range tags {
		out = append(out, r.snapshot(ctx, repo, ref.repo, tag)...)
	}
	return out
}

// snapshot reads the snapshot at reference in repo (at path) and classifies it.
func (r *snapshotReader) snapshot(ctx context.Context, repo *remote.Repository, path, reference string) []fleet.Source {
	tagCluster, isTag := strings.CutPrefix(reference, delivery.TargetSnapshotTagPrefix)
	id := targetSnapshotsID + "-" + tagCluster
	var cl *fleet.Cluster
	if isTag {
		cl = snapshotCluster(tagCluster)
	} else {
		d := strings.TrimPrefix(reference, "sha256:")
		tagCluster, id = "", targetSnapshotsID+"-"+d[:min(12, len(d))]
	}
	bundle, ref, err := r.fetch(ctx, repo, path, reference)
	switch {
	case errors.Is(err, deliveryrecord.ErrTargetSnapshotBound) || errors.Is(err, context.DeadlineExceeded):
		return []fleet.Source{unusedSnapshot(id, cl, fleet.SourceUnavailable, fleet.LimitationTargetSnapshotBound,
			"the target snapshot "+reference+" is over its bound, or its read took longer than the read timeout")}
	case errors.Is(err, errdef.ErrNotFound):
		return []fleet.Source{unusedSnapshot(id, cl, fleet.SourceUnavailable, fleet.LimitationTargetSnapshotInvalid,
			"the target snapshot "+path+":"+reference+" was not found")}
	case err != nil:
		return []fleet.Source{fleet.NewFailingSource(id, fleet.SourceKindTargetSnapshot, err)}
	}
	return r.classify(id, tagCluster, ref, bundle)
}

// fetch reads the bundle at reference through the cache, keyed by the
// manifest digest it resolves to, and returns it with <path>@<digest>.
func (r *snapshotReader) fetch(ctx context.Context, repo *remote.Repository, path, reference string) ([]byte, string, error) {
	desc, err := repo.Resolve(ctx, reference)
	if err != nil {
		return nil, "", err
	}
	ref := path + "@" + desc.Digest.String()
	if b, ok := r.cache.get(desc.Digest); ok {
		return b, ref, nil
	}
	b, _, err := deliveryrecord.FetchTargetSnapshot(ctx, repo, desc.Digest.String())
	if err != nil {
		return nil, "", err
	}
	r.cache.put(desc.Digest, b)
	return b, ref, nil
}

// classify verifies a snapshot bundle and returns its two sources, or one
// unused source when it does not verify, names another cluster than its tag
// (tagCluster, empty for a digest) or claims an asOf in the future.
func (r *snapshotReader) classify(id, tagCluster, ref string, bundle []byte) []fleet.Source {
	st, verdict, err := delivery.OpenTargetSnapshot(bundle, r.trust, signing.OpenKey)
	if err != nil {
		return []fleet.Source{unusedSnapshot(id, snapshotCluster(tagCluster), fleet.SourceUnavailable, fleet.LimitationTargetSnapshotInvalid,
			"the target snapshot "+ref+" was not used: "+err.Error())}
	}
	snap := st.Predicate
	asOf, _ := time.Parse(time.RFC3339, snap.AsOf) // OpenTargetSnapshot checked it
	switch {
	case tagCluster != "" && snap.Cluster != tagCluster:
		return []fleet.Source{unusedSnapshot(id, snapshotCluster(tagCluster), fleet.SourceUnavailable, fleet.LimitationTargetSnapshotInvalid,
			"the target snapshot "+ref+" names cluster "+snap.Cluster+" under the tag of cluster "+tagCluster+"; it was not used")}
	case asOf.After(r.now.Add(targetSnapshotSkew)):
		return []fleet.Source{unusedSnapshot(id, snapshotCluster(snap.Cluster), fleet.SourceUnavailable, fleet.LimitationTargetSnapshotInvalid,
			"the target snapshot "+ref+" claims asOf "+snap.AsOf+", in the future; it was not used")}
	}
	id = targetSnapshotsID + "-" + snap.Cluster
	if r.seen[snap.Cluster]++; r.seen[snap.Cluster] > 1 {
		id = fmt.Sprintf("%s-%d", id, r.seen[snap.Cluster])
	}
	cl := snapshotCluster(snap.Cluster)
	status, lims := classifyTargetSnapshot(id, snap, verdict, asOf, r.now)
	state := fleet.SourceState{Status: status, Ref: ref, Signer: snapshotSigner(verdict), Publisher: snap.Publisher, Cluster: cl, LastSuccessfulSync: &asOf}
	col := delivery.PactoCollection(id, snap.Pactos, nil, r.now)
	col.Cluster, col.State, col.Limitations = cl, &state, append(col.Limitations, lims...)
	// The workload source shares the status and cluster; the ref and signer
	// stay on the snapshot source, which carries the verdict's limitations.
	workloadState := fleet.SourceState{Status: status, Cluster: cl, LastSuccessfulSync: &asOf}
	return []fleet.Source{
		fleet.NewMemorySource(id, fleet.SourceKindTargetSnapshot, col),
		fleet.NewMemorySource(id+workloadsSuffix, fleet.SourceKindKubernetesWorkloads,
			&fleet.Collection{Cluster: cl, State: &workloadState, InferredServices: snap.Workloads}),
	}
}

// classifyTargetSnapshot is the status of a snapshot that verified or was read
// unverified, and its limitations: the publisher's own, unverified or not
// scoped to its cluster (partial) and older than the freshness bound (stale).
func classifyTargetSnapshot(id string, snap delivery.TargetSnapshot, v delivery.TargetSnapshotVerdict, asOf, now time.Time) (fleet.SourceStatus, []fleet.Limitation) {
	var lims []fleet.Limitation
	for _, l := range snap.Limitations {
		l.Source = id
		lims = append(lims, l)
	}
	switch {
	case !v.Verified:
		lims = append(lims, fleet.Limitation{Code: fleet.LimitationTargetSnapshotUnverified, Source: id,
			Message: "the target snapshot of cluster " + snap.Cluster + " was read with no delivery trust config; nothing verifies it"})
	case !v.Scoped:
		lims = append(lims, fleet.Limitation{Code: fleet.LimitationTargetSnapshotUnscoped, Source: id,
			Message: "the target snapshot of cluster " + snap.Cluster + " is signed by " + snapshotSigner(v) + ", a key not scoped to this cluster"})
	}
	status := fleet.SourceAvailable
	if snap.Completeness != fleet.CompletenessComplete || len(lims) > 0 {
		status = fleet.SourcePartial
	}
	if now.Sub(asOf) > delivery.TargetSnapshotFreshness {
		status = fleet.SourceStale
		lims = append(lims, fleet.Limitation{Code: fleet.LimitationTargetSnapshotStale, Source: id,
			Message: fmt.Sprintf("the target snapshot of cluster %s is from %s, older than %s", snap.Cluster, snap.AsOf, delivery.TargetSnapshotFreshness)})
	}
	return status, lims
}

// snapshotSigner names the trust entry that verified a snapshot: its name,
// else key:<fingerprint> of its public key, else its keyless identity; empty
// when unverified. It never reads the snapshot's claimed publisher.
func snapshotSigner(v delivery.TargetSnapshotVerdict) string {
	switch {
	case !v.Verified:
		return ""
	case v.Key.Name != "":
		return v.Key.Name
	case v.Key.PublicKey != "":
		p, _ := signing.PolicyOf(v.Key) // it verified, so it opens
		return "key:" + signing.Hint(p.Key)
	}
	return v.Key.Identity
}

// snapshotCluster is the cluster of uid, named by its first characters; nil
// for no uid.
func snapshotCluster(uid string) *fleet.Cluster {
	if uid == "" {
		return nil
	}
	return &fleet.Cluster{ID: uid, Name: uid[:min(8, len(uid))], NameSource: fleet.ClusterNameFromUID}
}

// unusedSnapshot is a target-snapshot source that contributes nothing, with
// status and the one limitation that says why.
func unusedSnapshot(id string, cl *fleet.Cluster, status fleet.SourceStatus, code, message string) fleet.Source {
	return fleet.NewMemorySource(id, fleet.SourceKindTargetSnapshot, &fleet.Collection{Cluster: cl,
		State:       &fleet.SourceState{Status: status, Cluster: cl},
		Limitations: []fleet.Limitation{{Code: code, Source: id, Message: message}}})
}

// registryRoots are the installation registries a provider ref may live
// under, in the order D8 tries them: <host>/<first path segment>, then
// <host>. ghcr.io/acme/contracts/ledger:2.3.0 gives ghcr.io/acme and ghcr.io;
// a ref one segment below its host gives the host alone.
func registryRoots(ref string) []string {
	host, path, _ := strings.Cut(contract.TrimOCIScheme(ref), "/")
	first, rest, nested := strings.Cut(path, "/")
	if !nested || first == "" || rest == "" {
		return []string{host}
	}
	return []string{host + "/" + first, host}
}

// findTargetSnapshot looks for the pacto-targets repository beside the first
// oci:// ref of refs (D8) and returns it as an oci:// ref, with where it
// looked. Only a 401, a 403, a 404 and a repository with no cluster tag mean
// "not here": the walk goes on and records the outcome. Any other error
// stops it and returns a failing target-snapshot source, so the answer is
// partial rather than the answer of a registry with no snapshot. No oci://
// ref looks nowhere and returns a nil lookup.
func (s *Service) findTargetSnapshot(ctx context.Context, refs ...string) (string, *impact.RegistryLookup, fleet.Source) {
	i := slices.IndexFunc(refs, func(r string) bool { return graph.ParseDependencyRef(r).IsOCI() })
	if i < 0 {
		return "", nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(s.sys.snapshotTimeout, delivery.TargetSnapshotReadTimeout))
	defer cancel()
	kc := oci.NewKeychain(oci.EnvCredentialOptions())
	look := &impact.RegistryLookup{Tried: []impact.RegistryRoot{}}
	for _, root := range registryRoots(refs[i]) {
		path := root + "/" + delivery.TargetSnapshotRepository
		repo, err := ocireferrer.Open(path, ocireferrer.Options{Keychain: kc, Insecure: oci.EnvInsecureRegistries()})
		var tags []string
		if err == nil {
			tags, _, err = deliveryrecord.ListTargetSnapshotTags(ctx, repo)
		}
		outcome := lookupOutcome(err, len(tags), credentialed(kc, root))
		if outcome == "" {
			return "", nil, fleet.NewFailingSource(targetSnapshotsID, fleet.SourceKindTargetSnapshot, fmt.Errorf("looking for target snapshots at %s: %w", path, err))
		}
		look.Tried = append(look.Tried, impact.RegistryRoot{Repository: path, Outcome: outcome})
		if outcome == impact.RegistryFound {
			look.Found, look.Clusters = path, len(tags)
			return "oci://" + path, look, nil
		}
	}
	return "", look, nil
}

// lookupOutcome is what listing a pacto-targets repository answered: found
// with tags, noClusterTag without, notFound for a 404 (or NAME_UNKNOWN) and
// for a 401 or 403 to an anonymous read, denied for a 401 or 403 to a read
// with credentials. Any other error is "": it is no answer.
func lookupOutcome(err error, tags int, credentialed bool) string {
	var resp *errcode.ErrorResponse
	switch {
	case err == nil && tags > 0:
		return impact.RegistryFound
	case err == nil:
		return impact.RegistryNoClusterTag
	case !errors.As(err, &resp):
		return ""
	case (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && credentialed:
		return impact.RegistryDenied
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound,
		slices.ContainsFunc(resp.Errors, func(e errcode.Error) bool { return e.Code == errcode.ErrorCodeNameUnknown }):
		return impact.RegistryNotFound
	}
	return ""
}

// credentialed reports whether kc holds credentials for root's registry.
func credentialed(kc authn.Keychain, root string) bool {
	host, _, _ := strings.Cut(root, "/")
	reg, err := name.NewRegistry(host)
	if err != nil {
		return false
	}
	a, err := kc.Resolve(reg)
	return err == nil && a != authn.Anonymous
}

// targetSnapshotStates are the states of snap's target-snapshot sources, in
// snap's order (by id).
func targetSnapshotStates(snap *fleet.FleetSnapshot) []fleet.SourceState {
	var out []fleet.SourceState
	for _, st := range snap.Sources {
		if st.Kind == fleet.SourceKindTargetSnapshot {
			out = append(out, st)
		}
	}
	return out
}
