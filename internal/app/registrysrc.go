package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

// CheckDiscover is a --discover prefix as host[/path], without oci:// or a
// trailing slash; a tag or digest is refused.
func CheckDiscover(prefix string) (string, error) {
	p := strings.TrimSuffix(contract.TrimOCIScheme(strings.TrimSpace(prefix)), "/")
	_, last, nested := strings.Cut(p, "/")
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	if p == "" || strings.Contains(p, "@") || nested && strings.Contains(last, ":") {
		return "", fmt.Errorf("--discover %q: --discover takes <registry>[/<path>] with no tag or digest", prefix)
	}
	return p, nil
}

// checkDiscovers is every prefix through CheckDiscover, in a new slice.
func checkDiscovers(prefixes []string) ([]string, error) {
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		var err error
		if out[i], err = CheckDiscover(p); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// registrySources is one discovery source per opts.Discover prefix.
func (s *Service) registrySources(opts FleetOptions) []fleet.Source {
	var out []fleet.Source
	for i, p := range opts.Discover {
		id := sourceID("registry", i, len(opts.Discover))
		if s.BundleStore == nil {
			out = append(out, fleet.NewFailingSource(id, "oci", errNoBundleStore))
			continue
		}
		out = append(out, &registrySource{id: id, prefix: p, svc: s, opts: opts})
	}
	return out
}

// registrySource is the golden paths and offerings published under a
// registry prefix as a fleet source: listed from the organization index,
// then _catalog, classified from their manifests and pulled by digest with
// the --oci checks. What it could not list is its state and limitations,
// so "not listed" never reads as absent.
type registrySource struct {
	id, prefix string
	svc        *Service
	opts       FleetOptions
	once       sync.Once
	coll       *fleet.Collection
}

// ID implements [fleet.Source].
func (s *registrySource) ID() string { return s.id }

// Kind implements [fleet.Source]: its revisions are published bundles, so
// the owner rule and provenance apply as for --oci.
func (s *registrySource) Kind() string { return "oci" }

// Collect implements [fleet.Source]. It reads once per instance, so a
// rebuild that carries the same source makes no request.
func (s *registrySource) Collect(ctx context.Context) (*fleet.Collection, error) {
	s.once.Do(func() { s.coll = s.collect(ctx) })
	return s.coll, nil
}

// collect lists under the read timeout, then pulls under the caller's
// context, as the whole-index source does.
func (s *registrySource) collect(ctx context.Context) *fleet.Collection {
	lctx, cancel := context.WithTimeout(ctx, indexReadTimeout)
	l := s.svc.registryListing(lctx, s.prefix)
	cancel()
	at := l.at
	coll, err := s.svc.ociSourceFor(s.id, l.refs, s.opts).Collect(ctx)
	if err != nil {
		return &fleet.Collection{State: &fleet.SourceState{Status: fleet.SourceUnavailable, ObservedAt: &at,
			Error: &fleet.SourceError{Code: "UNAVAILABLE", Message: redactDigests(err.Error())}}}
	}
	unreadable := l.unreadable + redactRefFailures(coll) + keepDiscovered(coll)
	coll.Limitations = l.limitations(s.id, s.prefix, unreadable)
	coll.State = &fleet.SourceState{Status: fleet.SourceAvailable, ObservedAt: &at, LastSuccessfulSync: &at}
	switch {
	case l.index.outcome == listUnavailable && l.catalog.outcome == listUnavailable:
		coll.State = &fleet.SourceState{Status: fleet.SourceUnavailable, ObservedAt: &at}
	case len(coll.Limitations) > 0:
		coll.State.Status = fleet.SourcePartial
	}
	return coll
}

// redactRefFailures removes every limitation the inner registry source
// wrote, each naming the ref it could not pull, and returns how many: they
// join the one counted REGISTRY_LISTING_ENTRY_UNREADABLE, which names none.
func redactRefFailures(coll *fleet.Collection) int {
	n := len(coll.Limitations)
	coll.Limitations = nil
	return n
}

// keepDiscovered drops every revision the listing cannot vouch for, with
// the services the registry rows name for it, and returns how many: its
// pulled contract is no template or offering (the kind annotation is the
// pusher's word), or its name is not its repository's, so a bundle pushed
// to another repository never speaks for the service it names.
func keepDiscovered(coll *fleet.Collection) int {
	n := len(coll.Revisions)
	kept := map[fleet.ServiceKey]bool{}
	coll.Revisions = slices.DeleteFunc(coll.Revisions, func(r fleet.RawRevision) bool {
		c := r.Bundle.Contract
		repo, _ := contract.SplitOCIDigest(contract.TrimOCIScheme(r.RequestedRef))
		if k := oci.BundleKind(c); k == oci.KindService || contract.DependencyName(repo) != c.Service.Name {
			return true
		}
		kept[fleet.NewServiceKeyDomain(r.Domain, c.Service.Name)] = true
		return false
	})
	for i := range coll.Providers {
		coll.Providers[i].Services = slices.DeleteFunc(coll.Providers[i].Services, func(k fleet.ServiceKey) bool { return !kept[k] })
	}
	return n - len(coll.Revisions)
}

// Listing outcomes.
const (
	listRead        = "read"
	listAbsent      = "absent"
	listTruncated   = "truncated"
	listUnavailable = "unavailable"
	listUnsupported = "unsupported"
	listRefused     = "refused"
)

// listing is what one listing (the index or _catalog) found.
type listing struct {
	outcome string
	// reason is why an unavailable listing failed, with no digest.
	reason string
	cands  []candidate
	// skip holds the repositories of index entries bound to their name.
	skip       map[string]bool
	unreadable int
}

// candidate is a repository and the digest an index entry names, or ""
// for its newest version tag.
type candidate struct{ repo, ref string }

// registryListing is one listing of a prefix: the outcome of each listing,
// the template and offering bundles found, pinned by digest, and the
// candidates that could not be classified. A finished or truncated listing
// is reused for indexListingTTL with its own time; a failed one never is.
type registryListing struct {
	at             time.Time
	index, catalog listing
	// cut says the read timeout stopped classification.
	cut        bool
	refs       []string
	unreadable int
}

// registryMemo keeps listings per prefix and bundle kinds per digest.
type registryMemo struct {
	mu       sync.Mutex
	listings map[string]*registryListing
	kinds    map[string]string
}

// maxKinds bounds the kinds a process remembers.
const maxKinds = 10000

// registryListing is the listing of prefix, reused for indexListingTTL.
func (s *Service) registryListing(ctx context.Context, prefix string) *registryListing {
	now := s.env().now()
	m := &s.registryListings
	m.mu.Lock()
	l := m.listings[prefix]
	m.mu.Unlock()
	if l != nil && now.Sub(l.at) < indexListingTTL {
		return l
	}
	l = s.listRegistry(ctx, prefix)
	l.at = now
	if l.index.outcome != listUnavailable && l.catalog.outcome != listUnavailable {
		m.mu.Lock()
		if m.listings == nil {
			m.listings = map[string]*registryListing{}
		}
		m.listings[prefix] = l
		m.mu.Unlock()
	}
	return l
}

// listRegistry lists the index, then _catalog, then classifies every
// candidate both found.
func (s *Service) listRegistry(ctx context.Context, prefix string) *registryListing {
	idx := oci.InstallationRepository(prefix, oci.IndexRepository)
	l := &registryListing{index: s.listIndex(ctx, idx)}
	l.catalog = s.listCatalog(ctx, prefix, idx, l.index.skip)
	l.refs, l.unreadable = s.classifyAll(ctx, slices.Concat(l.index.cands, l.catalog.cands))
	l.unreadable += l.index.unreadable + l.catalog.unreadable
	l.cut = ctx.Err() != nil
	return l
}

// listIndex lists the organization index idx through the one index
// reader. It maps the read's state to a listing outcome and never copies
// an orgindex limitation, whose messages name entries.
func (s *Service) listIndex(ctx context.Context, idx string) listing {
	rd := s.IndexEntries(ctx, []string{idx})
	l := listing{outcome: listRead, skip: map[string]bool{}}
	switch rd.status {
	case impact.IndexNotFound:
		l.outcome = listAbsent
		return l
	case impact.IndexUnavailable:
		l.outcome, l.reason = listUnavailable, rd.note
		if !rd.unavailable || ctx.Err() != nil {
			l.outcome = listTruncated
		}
		return l
	}
	if len(rd.unread) > 0 || slices.ContainsFunc(rd.lims, func(x fleet.Limitation) bool { return x.Code == orgindex.LimitationTruncated }) {
		l.outcome = listTruncated
	}
	l.unreadable = max(0, rd.services-len(rd.entries)-len(rd.unread))
	for _, e := range rd.entries {
		repo, dg := contract.SplitOCIDigest(e.Bundle)
		if contract.DependencyName(repo) == e.Name {
			l.skip[repo] = true
		}
		l.cands = append(l.cands, candidate{repo: repo, ref: dg})
	}
	return l
}

// listCatalog lists the repositories under prefix through _catalog, minus
// the ones skip holds and every repository a Pacto installation owns:
// pacto-index, pacto-targets and pacto-evidence hold no bundle, and
// pacto-inferred contracts have their own readers and trust rules.
func (s *Service) listCatalog(ctx context.Context, prefix, idx string, skip map[string]bool) listing {
	host, path, _ := strings.Cut(prefix, "/")
	l := listing{outcome: listRead}
	r, err := s.env().refusalRepo(idx)
	var names []string
	if err == nil {
		names, err = ocireferrer.Catalog(ctx, r, path, indexMaxServices)
	}
	switch {
	case errors.Is(err, ocireferrer.ErrCatalogUnsupported):
		l.outcome = listUnsupported
	case errors.Is(err, ocireferrer.ErrCatalogRefused):
		l.outcome = listRefused
	case errors.Is(err, ocireferrer.ErrCatalogTruncated) || err != nil && ctx.Err() != nil:
		l.outcome = listTruncated
	case err != nil:
		l.outcome, l.reason = listUnavailable, redactDigests(err.Error())
	}
	for _, n := range names {
		if repo := host + "/" + n; !skip[repo] && !pactoOwned(n) {
			l.cands = append(l.cands, candidate{repo: repo})
		}
	}
	return l
}

// pactoOwned reports whether the repository name has a segment naming an
// installation repository.
func pactoOwned(name string) bool {
	return slices.ContainsFunc(strings.Split(name, "/"), func(seg string) bool {
		return slices.Contains([]string{oci.IndexRepository, oci.TargetsRepository, oci.InferredRepository, "pacto-evidence"}, seg)
	})
}

// classifyAll classifies cands, Workers at a time, and returns the pinned
// refs of the templates and offerings, deduplicated, and how many could not
// be classified. A candidate the timeout stopped is not counted: the
// listing reads truncated instead.
func (s *Service) classifyAll(ctx context.Context, cands []candidate) (refs []string, unreadable int) {
	type result struct {
		ref, kind string
		err       error
	}
	results := make([]result, len(cands))
	var g errgroup.Group
	g.SetLimit(orgindex.Workers)
	for i, c := range cands {
		g.Go(func() error {
			ref, kind, err := s.classify(ctx, c)
			results[i] = result{ref, kind, err}
			return nil
		})
	}
	_ = g.Wait() // every goroutine returns nil
	for _, r := range results {
		switch {
		case r.err != nil && ctx.Err() == nil:
			unreadable++
		case (r.kind == oci.KindTemplate || r.kind == oci.KindOffering) && !slices.Contains(refs, r.ref):
			refs = append(refs, r.ref)
		}
	}
	slices.Sort(refs)
	return refs, unreadable
}

// classify is c's bundle pinned by digest and its kind, "" with no error
// for a repository that holds no bundle version.
func (s *Service) classify(ctx context.Context, c candidate) (ref, kind string, err error) {
	if k, ok := s.cachedKind(c.ref); ok {
		return contract.OCIScheme + c.repo + "@" + c.ref, k, nil
	}
	r, err := s.env().refusalRepo(c.repo)
	if err != nil {
		return "", "", err
	}
	if c.ref == "" {
		tags, err := ocireferrer.Tags(ctx, r, "")
		if err != nil {
			return "", "", err
		}
		if c.ref = newestTag(tags); c.ref == "" {
			return "", "", nil
		}
	}
	desc, err := r.Resolve(ctx, c.ref)
	if err != nil {
		return "", "", err
	}
	ref = contract.OCIScheme + c.repo + "@" + desc.Digest.String()
	if k, ok := s.cachedKind(desc.Digest.String()); ok {
		return ref, k, nil
	}
	kind, err = s.bundleKind(ctx, r, ref, desc)
	if errors.Is(err, oci.ErrNotBundle) {
		kind, err = "", nil
	}
	if err == nil {
		s.keepKind(desc.Digest.String(), kind)
	}
	return ref, kind, err
}

// bundleKind classifies the manifest desc, cheapest first: the manifest,
// then the config labels, then the pulled contract for an untyped 2.4+
// bundle. Each size is checked before its fetch, since whoever pushed the
// artifact chose it, and each fetch reads at most the checked size.
func (s *Service) bundleKind(ctx context.Context, r *remote.Repository, ref string, desc ocispec.Descriptor) (string, error) {
	switch {
	case desc.MediaType == ocispec.MediaTypeImageIndex || desc.MediaType == "application/vnd.docker.distribution.manifest.list.v2+json":
		return "", oci.ErrNotBundle
	case desc.Size > oci.MaxBundleManifestBytes:
		return "", fmt.Errorf("the manifest is over %d bytes", oci.MaxBundleManifestBytes)
	}
	raw, err := content.FetchAll(ctx, r, desc)
	if err != nil {
		return "", err
	}
	kind, decided, err := oci.ClassifyManifest(raw)
	if err != nil || decided {
		return kind, err
	}
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m) // ClassifyManifest parsed it
	if m.Config.Size > oci.MaxBundleConfigBytes {
		return "", fmt.Errorf("the config is over %d bytes", oci.MaxBundleConfigBytes)
	}
	cfg, err := content.FetchAll(ctx, r, m.Config)
	if err != nil {
		return "", err
	}
	if kind, decided, err = oci.KindFromConfig(cfg); err != nil || decided {
		return kind, err
	}
	b, err := s.BundleStore.Pull(ctx, contract.TrimOCIScheme(ref))
	if err != nil {
		return "", err
	}
	return oci.BundleKind(b.Contract), nil
}

// newestTag is the highest release tag, else the highest prerelease, ""
// with no version tag: orgindex.Newest's rule.
func newestTag(tags []string) string {
	versions := semver.Filter(tags)
	for _, t := range versions {
		if core, _, _ := strings.Cut(t, "+"); !strings.Contains(core, "-") {
			return t
		}
	}
	if len(versions) > 0 {
		return versions[0]
	}
	return ""
}

// cachedKind is the remembered kind of the bundle with digest d.
func (s *Service) cachedKind(d string) (string, bool) {
	s.registryListings.mu.Lock()
	defer s.registryListings.mu.Unlock()
	k, ok := s.registryListings.kinds[d]
	return k, ok
}

// keepKind remembers the kind of the bundle with digest d: content is
// immutable, so the kind never changes.
//
// ponytail: one map under a mutex, cleared when it passes maxKinds; an LRU
// if a registry ever lists more bundles than that per refresh.
func (s *Service) keepKind(d, kind string) {
	s.registryListings.mu.Lock()
	defer s.registryListings.mu.Unlock()
	if len(s.registryListings.kinds) >= maxKinds {
		s.registryListings.kinds = nil
	}
	if s.registryListings.kinds == nil {
		s.registryListings.kinds = map[string]string{}
	}
	s.registryListings.kinds[d] = kind
}

// limitations are what the listing of prefix leaves unknown, filed under
// id. Messages name the host, the prefix and the index repository, never
// another repository or a digest, so a restricted viewer learns nothing
// about services it may not see.
func (l *registryListing) limitations(id, prefix string, unreadable int) []fleet.Limitation {
	host, _, _ := strings.Cut(prefix, "/")
	idx := oci.InstallationRepository(prefix, oci.IndexRepository)
	lim := func(code, msg string) fleet.Limitation { return fleet.Limitation{Code: code, Source: id, Message: msg} }
	var out []fleet.Limitation
	if l.index.outcome == listUnavailable {
		out = append(out, lim(fleet.LimitationRegistryListingUnavailable, idx+" could not be listed: "+l.index.reason))
	}
	if l.catalog.outcome == listUnavailable {
		out = append(out, lim(fleet.LimitationRegistryListingUnavailable, "the _catalog of "+host+" could not be listed: "+l.catalog.reason))
	}
	catalog := host + " serves no _catalog"
	if l.catalog.outcome == listRefused {
		catalog = host + " refused _catalog to this credential"
	}
	unlisted := l.catalog.outcome == listUnsupported || l.catalog.outcome == listRefused
	switch {
	case len(out) > 0:
	case l.index.outcome == listTruncated || l.catalog.outcome == listTruncated || l.cut:
		out = append(out, lim(fleet.LimitationRegistryListingTruncated, "the listing of "+prefix+
			" stopped at a bound or its timeout: the golden paths and offerings past it are unknown"))
	case unlisted && l.index.outcome == listRead:
		out = append(out, lim(fleet.LimitationRegistryListingIndexOnly, catalog+"; listed from "+idx))
	case unlisted:
		out = append(out, lim(fleet.LimitationRegistryListingNone, "no "+idx+" and "+catalog+
			": the golden paths and offerings under "+prefix+" are unknown"))
	}
	if unreadable > 0 {
		out = append(out, lim(fleet.LimitationRegistryListingEntryUnreadable, fmt.Sprintf(
			"%d bundles listed under %s could not be read or do not match their repository; they are left out", unreadable, prefix)))
	}
	return out
}
