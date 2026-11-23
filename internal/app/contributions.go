package app

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Masterminds/semver/v3"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// Bounds of one read of the contributions an Evidence Server advertises,
// charged before the work.
const (
	// maxContributedServices is how many services one producer contributes,
	// counted after the newest version of each is picked.
	maxContributedServices = 200
	// maxVersionsTried is how many versions of one service are tried, newest
	// first, when a newer one is refused. ponytail: a holder of push rights
	// without the key can hide a service behind this many refused tags; the
	// row then reads partial and counts them.
	maxVersionsTried = 5
	// contributionDeadline bounds the read of one producer.
	contributionDeadline = 10 * time.Second
)

// contributionReader reads the records producers contribute: per producer, the
// newest version of each service in its contribution repository, verified
// against the keys the Evidence Server advertises for it. It reads only a
// repository it rebuilt itself under one of Roots, so whoever answers
// /producers never chooses what Pacto lists and pulls with its credentials.
type contributionReader struct {
	Store    oci.BundleStore
	Registry publish.Registry
	// Roots are the registry paths Pacto is configured to read, such as
	// ghcr.io/acme.
	Roots []string
	Now   func() time.Time
	// Memo remembers verified digests across reads; nil keeps none.
	Memo *verifiedMemo
}

// verifiedMemo remembers which digest a key verified. The zero value is ready.
type verifiedMemo struct {
	mu sync.Mutex
	m  map[string]string
}

func (m *verifiedMemo) get(k string) (string, bool) {
	if m == nil {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.m[k]
	return id, ok
}

// put remembers k. ponytail: cleared whole past one full read's worth of
// entries; an LRU if a fleet ever rotates through more.
func (m *verifiedMemo) put(k, keyID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil || len(m.m) >= fleetsrc.MaxContributors*maxContributedServices {
		m.m = map[string]string{}
	}
	m.m[k] = keyID
}

// contributorKey is one advertised key, decoded.
type contributorKey struct {
	id, encoded string
	pub         ed25519.PublicKey
}

// decodeKeys are the advertised keys that hold an Ed25519 public key.
func decodeKeys(keys []evidenceingest.ContributorKey) []contributorKey {
	var out []contributorKey
	for _, k := range keys {
		if raw, err := base64.StdEncoding.DecodeString(k.PublicKey); err == nil && len(raw) == ed25519.PublicKeySize {
			out = append(out, contributorKey{id: k.KeyID, encoded: k.PublicKey, pub: raw})
		}
	}
	return out
}

// Read implements [fleetsrc.ContributionReader]: it reads c's
// contributions into revisions, filling f and returning the limitations that make source partial: an unread repository or record.
// A refused record (unsigned, signed by another key or tagged with another
// name or version) is counted on f and never makes the source partial.
func (r *contributionReader) Read(ctx context.Context, source string, c evidenceingest.ProducerContributor, f *fleet.ProducerFacts) ([]fleet.RawRevision, []fleet.Limitation) {
	// A producer granted under several registry roots is one row naming
	// every repository.
	f.Repository = strings.TrimPrefix(f.Repository+" and "+c.Repository, " and ")
	unlisted := func(why string) []fleet.Limitation {
		f.Unread = strings.TrimPrefix(f.Unread+"; "+c.Repository+": "+why, "; ")
		return []fleet.Limitation{unreadLimitation(source, c.Producer, why)}
	}
	if !r.rebuilt(c) {
		return nil, unlisted("it is no contribution repository of " + c.Producer + " under a registry Pacto is configured to read")
	}
	ctx, cancel := context.WithTimeout(ctx, contributionDeadline)
	defer cancel()
	tags, err := r.Registry.Tags(ctx, c.Repository)
	if err != nil {
		return nil, unlisted(fleet.SanitizedReason(err))
	}
	services, ignored := newestTags(tags)
	f.Ignored += ignored
	var lims []fleet.Limitation
	if len(services) > maxContributedServices {
		// The services past the bound are unknown, never absent.
		why := strconv.Itoa(len(services)) + " services contributed, at most " + strconv.Itoa(maxContributedServices) + " are read"
		f.Problems = append(f.Problems, why)
		lims = append(lims, unreadLimitation(source, c.Producer, why))
		services = services[:maxContributedServices]
	}
	var (
		revs             []fleet.RawRevision
		refused, unread  int
		mistagged, ahead int
		firstUnreadCause string
	)
	keys := decodeKeys(c.Keys)
	for _, s := range services {
		rev, out := r.readService(ctx, c, keys, s)
		refused, mistagged = refused+out.refused, mistagged+out.mistagged
		if out.err != nil {
			unread++
			firstUnreadCause = cmp.Or(firstUnreadCause, fleet.SanitizedReason(out.err))
		}
		if rev != nil {
			if rev.Contributor.AsOf.After(r.Now().Add(fleet.MaxProvisioningClockSkew)) {
				// A record dated in the future has an unknown age: stale.
				rev.Contributor.AsOf = time.Time{}
				ahead++
			}
			revs = append(revs, *rev)
			f.Read++
			if rev.Contributor.AsOf.IsZero() {
				continue
			}
			f.NewestContribution = laterOf(f.NewestContribution, rev.Contributor.AsOf)
		}
	}
	if refused > 0 {
		f.Problems = append(f.Problems, strconv.Itoa(refused)+" records were unsigned or signed by a key not granted to "+c.Producer)
	}
	if mistagged > 0 {
		f.Problems = append(f.Problems, strconv.Itoa(mistagged)+" tags name another service or version than their record")
	}
	if ahead > 0 {
		f.Problems = append(f.Problems, strconv.Itoa(ahead)+" records are dated in the future, so their age is unknown")
	}
	if unread > 0 {
		why := strconv.Itoa(unread) + " records could not be read: " + firstUnreadCause
		f.Problems = append(f.Problems, why)
		lims = append(lims, unreadLimitation(source, c.Producer, why))
	}
	return revs, lims
}

// laterOf is the later of cur and t.
func laterOf(cur *time.Time, t time.Time) *time.Time {
	if cur != nil && !t.After(*cur) {
		return cur
	}
	return &t
}

// unreadLimitation is the CONTRIBUTION_UNREAD of producer's contributions.
func unreadLimitation(source, producer, why string) fleet.Limitation {
	return fleet.Limitation{Code: fleet.LimitationContributionUnread, Source: source,
		Message: "the contributions of " + producer + " were not read: " + why}
}

// rebuilt reports whether c's repository is the contribution repository of
// c's producer under one of r's roots, rebuilt here rather than taken as
// advertised.
func (r *contributionReader) rebuilt(c evidenceingest.ProducerContributor) bool {
	root, found := strings.CutSuffix(c.Repository, "/"+contract.InferredRepository+"/"+c.Producer+"/"+contract.ContributionsRepository)
	if !found || !slices.Contains(r.Roots, root) {
		return false
	}
	p, ok := contract.ContributionRef(c.Repository)
	return ok && p == c.Producer && contract.ContributionRepository(root, c.Producer) == c.Repository
}

// serviceTags are one service's tags, newest version first.
type serviceTags struct {
	name     string
	versions []string
}

// newestTags groups tags of the form <service>_<version> by service, newest
// version first, services sorted by name, and counts the other tags but
// those naming referrers: Pacto's write-once signature tags
// (publish.SigTagPrefix) and the referrers tag schema's <alg>-<hex>, which
// signing on a registry with no Referrers API may leave beside them.
func newestTags(tags []string) ([]serviceTags, int) {
	type tagged struct {
		version *semver.Version
		raw     string
	}
	by, ignored := map[string][]tagged{}, 0
	for _, t := range tags {
		if _, err := v1.NewHash(strings.Replace(t, "-", ":", 1)); err == nil || strings.HasPrefix(t, "pacto-sig-") {
			continue
		}
		name, ver, _ := strings.Cut(t, "_")
		v, ok := contributionVersion(ver)
		if !ok || name == "" || contract.SanitizeName(name) != name {
			ignored++
			continue
		}
		by[name] = append(by[name], tagged{v, ver})
	}
	out := make([]serviceTags, 0, len(by))
	for name, vs := range by {
		slices.SortFunc(vs, func(a, b tagged) int { return b.version.Compare(a.version) })
		s := serviceTags{name: name}
		for _, v := range vs {
			s.versions = append(s.versions, v.raw)
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b serviceTags) int { return strings.Compare(a.name, b.name) })
	return out, ignored
}

// contributionVersion is v parsed when it is a version a contribution tag
// can carry: strict MAJOR.MINOR.PATCH semver with no build metadata.
func contributionVersion(v string) (*semver.Version, bool) {
	sv, err := semver.StrictNewVersion(v)
	return sv, err == nil && sv.Metadata() == ""
}

// readOutcome is what reading one service cost: the versions refused and the
// error that stopped it, nil when none did.
type readOutcome struct {
	refused, mistagged int
	err                error
}

// readService reads s's newest version that is signed by one of keys and
// holds the name and version of its tag, trying older ones while newer are
// refused; an older one read names the newest refused, so it reads stale. A
// registry error stops it.
func (r *contributionReader) readService(ctx context.Context, c evidenceingest.ProducerContributor, keys []contributorKey, s serviceTags) (*fleet.RawRevision, readOutcome) {
	var out readOutcome
	for _, v := range s.versions[:min(len(s.versions), maxVersionsTried)] {
		rev, err := r.readOne(ctx, c, keys, s.name, v)
		switch {
		case errors.Is(err, publish.ErrNoSignature) || errors.Is(err, publish.ErrSignatureInvalid):
			out.refused++
		case errors.Is(err, errMistagged):
			out.mistagged++
		case err != nil:
			out.err = err
			return nil, out
		default:
			if v != s.versions[0] {
				rev.Contributor.NewerRefused = s.versions[0]
			}
			return rev, out
		}
	}
	return nil, out
}

// errMistagged: a verified record names another service or version than its tag.
var errMistagged = errors.New("the record names another service or version than its tag")

// readOne reads the record tagged name_version: its digest, signature,
// bundle, and the creation time its signed manifest carries.
func (r *contributionReader) readOne(ctx context.Context, c evidenceingest.ProducerContributor, keys []contributorKey, name, version string) (*fleet.RawRevision, error) {
	tag := name + "_" + version
	head, err := r.Registry.Head(ctx, c.Repository+":"+tag)
	if err == nil && head == nil {
		err = errors.New("tag " + tag + " not found")
	}
	if err != nil {
		return nil, err
	}
	keyID, err := r.verifyOne(ctx, c.Repository, head.Digest, keys)
	if err != nil {
		return nil, err
	}
	b, err := r.Store.Pull(ctx, c.Repository+"@"+head.Digest)
	if err != nil {
		return nil, err
	}
	if b.Contract == nil || b.Contract.Service.Name != name || b.Contract.Service.Version != version {
		return nil, errMistagged
	}
	asOf, _ := time.Parse(time.RFC3339, head.Annotations[publish.AnnotationCreated])
	resolved := contract.OCIScheme + c.Repository + "@" + head.Digest
	return &fleet.RawRevision{Bundle: b, Domain: contract.OCIDomain(resolved), RequestedRef: contract.OCIScheme + c.Repository + ":" + tag,
		ResolvedRef: resolved, Digest: head.Digest,
		Contributor: &fleet.Contributor{Producer: c.Producer, KeyID: keyID, ReadAt: r.Now(), AsOf: asOf}}, nil
}

// verifyOne is the id of the key one of repo@digest's signatures verifies
// with, remembered across reads.
func (r *contributionReader) verifyOne(ctx context.Context, repo, digest string, keys []contributorKey) (string, error) {
	for _, k := range keys {
		if id, ok := r.Memo.get(digest + "|" + k.encoded); ok {
			return id, nil
		}
	}
	var matched *contributorKey
	verify := func(b, d []byte) (string, error) {
		err := publish.ErrNoSignature
		for i, k := range keys {
			pt, kerr := publish.PolicyVerifier(signing.Policy{Key: k.pub})(b, d)
			if kerr == nil {
				matched = &keys[i]
				return pt, nil
			}
			err = kerr
		}
		return "", err
	}
	if err := publish.VerifyDigest(ctx, r.Registry, verify, repo, digest); err != nil {
		return "", err
	}
	r.Memo.put(digest+"|"+matched.encoded, matched.id)
	return matched.id, nil
}
