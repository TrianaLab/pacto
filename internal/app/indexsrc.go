package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/catalog"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

// Index read bounds. Vars so tests shorten or lower them.
var (
	// indexReadTimeout bounds one organization index read.
	indexReadTimeout = 30 * time.Second
	// indexListingTTL is how long a process reuses one index read.
	indexListingTTL = time.Minute
	// indexMaxServices bounds the entries one read fetches.
	indexMaxServices = orgindex.MaxServices
	// indexMaxRoots bounds the consumer bundles the closure resolves.
	indexMaxRoots = catalog.CeilingMaxRoots
)

// indexSource is the organization index as a fleet source: the bundles of
// the services whose newest entry depends on the provider, pinned by digest,
// with their closure. Its registry problems are its state and limitations,
// never an error, so "no index" never reads as "no consumers".
type indexSource struct {
	svc        *Service
	candidates []string
	provider   orgindex.Provider
	once       sync.Once
	coll       *fleet.Collection
	summary    impact.ConsumerIndex
	// listed holds the prefixes of valid, confirmed entries bound to their
	// service's name.
	listed map[string]bool
	// unread holds the prefixes the listing names that a bound or the
	// timeout kept from being read: the index may well hold them.
	unread map[string]bool
}

// ID implements [fleet.Source].
func (s *indexSource) ID() string { return "index" }

// Kind implements [fleet.Source].
func (s *indexSource) Kind() string { return "index" }

// Collect implements [fleet.Source]. It reads once per instance, so a
// rebuild that carries the same source makes no request.
func (s *indexSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	s.once.Do(func() { s.coll = s.collect(ctx) })
	return s.coll, nil
}

func (s *indexSource) collect(ctx context.Context) *fleet.Collection {
	ctx, cancel := context.WithTimeout(ctx, indexReadTimeout)
	defer cancel()
	rd := s.svc.IndexEntries(ctx, s.candidates)
	s.unread = rd.unread
	roots, pending := s.selectRoots(rd.entries)
	coll := s.closure(ctx, roots)
	s.confirmNames(coll, pending)
	s.withoutProviderByName(coll)
	coll.Limitations = append(slices.Clone(rd.lims), coll.Limitations...)
	s.summary = impact.ConsumerIndex{Repository: rd.repo, Status: rd.status, Services: rd.services,
		Consumers: len(roots), Completeness: fleet.CompletenessComplete, Reason: rd.note}
	// The read's own time, so a memoized read never reads as fresh.
	at := rd.at
	coll.State = &fleet.SourceState{Status: fleet.SourceAvailable, ObservedAt: &at, LastSuccessfulSync: &at}
	switch {
	case rd.unavailable:
		coll.State = &fleet.SourceState{Status: fleet.SourceUnavailable, ObservedAt: &at}
	case len(coll.Limitations) > 0:
		coll.State.Status = fleet.SourcePartial
	}
	if len(coll.Limitations) > 0 {
		s.summary.Completeness = fleet.CompletenessPartial
	}
	return coll
}

// selectRoots records which services the index lists and returns the
// bundles of those that depend on the provider. An entry's name is the
// writer's claim: it counts as listed when its bundle repository ends in
// that name, and a selected entry whose repository does not is pending until
// its pulled bundle shows the name (confirmNames), so an entry pointing at
// another service's bundle never speaks for the service it names.
//
// ponytail: an unselected entry whose repository does not end in its name
// is never listed, so a consumer of that name the fleet holds reads
// CONSUMER_NOT_INDEXED (partial, never positive); pull its bundle too if
// such repositories turn out common.
func (s *indexSource) selectRoots(entries []orgindex.Entry) (roots []string, pending map[string]orgindex.Entry) {
	s.listed = map[string]bool{}
	pending = map[string]orgindex.Entry{}
	for _, e := range entries {
		repo, _ := contract.SplitOCIDigest(e.Bundle)
		named := contract.DependencyName(repo) == e.Name
		if named {
			s.listed[orgindex.Prefix(fleet.ServiceKey(e.Service))] = true
		}
		if e.Names(s.provider) {
			root := contract.OCIScheme + e.Bundle
			roots = append(roots, root)
			if !named {
				pending[root] = e
			}
		}
	}
	return roots, pending
}

// confirmNames lists each pending entry whose pulled root bundle is the
// service the entry names.
func (s *indexSource) confirmNames(coll *fleet.Collection, pending map[string]orgindex.Entry) {
	for _, r := range coll.Revisions {
		e, ok := pending[r.RequestedRef]
		if ok && r.Bundle != nil && r.Bundle.Contract != nil &&
			string(fleet.NewServiceKeyDomain(r.Domain, r.Bundle.Contract.Service.Name)) == e.Service {
			s.listed[orgindex.Prefix(fleet.ServiceKey(e.Service))] = true
		}
	}
}

// closure is the selected bundles and their dependency closure, bounded by
// the catalog's root ceiling.
func (s *indexSource) closure(ctx context.Context, roots []string) *fleet.Collection {
	if len(roots) == 0 {
		return &fleet.Collection{}
	}
	rec := newBundleRecorder()
	// Build fails only with no resolver or no roots, and both are set.
	cat, _ := catalog.Build(ctx, catalog.Request{Roots: roots, Resolver: s.svc.recordingCatalogResolver(rec),
		Bounds: catalog.Bounds{MaxRoots: indexMaxRoots}})
	return catalogCollection(s.ID(), cat, rec)
}

// withoutProviderByName drops the DEPENDENCY_BY_NAME gap the closure
// reports for the provider's own name when every resolved revision naming it
// is in the provider's domain: the fleet resolves that name to the provider,
// which the answer already holds, so nothing is unknown. A by-name
// dependency on any other service, or on that name from another domain,
// stays a gap.
func (s *indexSource) withoutProviderByName(coll *fleet.Collection) {
	domain, name := fleet.ParseServiceKey(s.provider.Key)
	for _, r := range coll.Revisions {
		if r.Bundle == nil || r.Bundle.Contract == nil || r.Domain == domain {
			continue
		}
		for _, d := range r.Bundle.Contract.Dependencies {
			if d.Ref == "" && !d.External && d.Name == name {
				return
			}
		}
	}
	coll.Limitations = slices.DeleteFunc(coll.Limitations, func(l fleet.Limitation) bool {
		return l.Code == catalog.LimitationDependencyByName && strings.HasSuffix(l.Message, " ("+name+")")
	})
}

// result is the index summary and listing impact checks the answer with: a
// nil source gives skipped when it has a status (else nothing) and no check.
// Only a finished listing is checked against: an index that was not read
// cannot say a consumer is missing from it.
func (s *indexSource) result(skipped impact.ConsumerIndex) (*impact.ConsumerIndex, func(fleet.ServiceKey) bool) {
	if s == nil {
		if skipped.Status == "" {
			return nil, nil
		}
		return &skipped, nil
	}
	sum := s.summary
	if sum.Status != impact.IndexRead {
		return &sum, nil
	}
	return &sum, s.indexed
}

// indexed reports whether the index lists k, or may: a prefix the listing
// names but a bound or the timeout kept from being read is never reported
// missing.
func (s *indexSource) indexed(k fleet.ServiceKey) bool {
	p := orgindex.Prefix(k)
	return s.listed[p] || s.unread[p]
}

// indexRead is one index read before selection: what every changed service
// selects its consumers from.
type indexRead struct {
	at       time.Time
	repo     string
	note     string
	status   string
	services int
	// entries are the valid, confirmed newest entries, one per service.
	entries []orgindex.Entry
	// unread are the listed prefixes a bound or the timeout kept unread.
	unread      map[string]bool
	lims        []fleet.Limitation
	unavailable bool
}

// indexMemo keeps index reads per candidate list for indexListingTTL, and
// the entry cache when the bundle store has no cache directory.
type indexMemo struct {
	mu      sync.Mutex
	reads   map[string]*indexRead
	entries map[string]indexCached
}

// IndexEntries is the read of the first organization index among
// candidates: its repository and the valid, confirmed newest entry of each
// service, reused for indexListingTTL. It is the one index reader: the
// consumer source and pacto ci's ref proposals both read through it. Only a
// finished read is kept.
//
// ponytail: one fleet rebuild per changed service in pacto ci; give CIPlan
// one index source that selects for every changed service if a large
// monorepo makes it slow.
func (s *Service) IndexEntries(ctx context.Context, candidates []string) *indexRead {
	key := strings.Join(candidates, " ")
	now := s.env().now()
	m := &s.indexListings
	m.mu.Lock()
	rd := m.reads[key]
	m.mu.Unlock()
	if rd != nil && now.Sub(rd.at) < indexListingTTL {
		return rd
	}
	rd = s.readIndex(ctx, candidates)
	rd.at = now
	if rd.status == impact.IndexRead && ctx.Err() == nil {
		m.mu.Lock()
		if m.reads == nil {
			m.reads = map[string]*indexRead{}
		}
		m.reads[key] = rd
		m.mu.Unlock()
	}
	return rd
}

// readIndex lists the index, then reads and confirms each service's newest
// entry.
func (s *Service) readIndex(ctx context.Context, candidates []string) *indexRead {
	repo, note, tags, err := s.indexRepository(ctx, candidates)
	rd := &indexRead{repo: repo, note: note, status: impact.IndexRead}
	switch {
	case errors.Is(err, errIndexNotFound):
		rd.repo, rd.status, rd.note = "", impact.IndexNotFound, err.Error()
		rd.lims = indexLim(orgindex.LimitationNotFound, err.Error()+": the consumers it would name are unknown")
		return rd
	case errors.Is(err, ocireferrer.ErrTagPagesExceeded):
		rd.status, rd.note = impact.IndexUnavailable, "the listing passed its page bound"
		rd.lims = indexLim(orgindex.LimitationTruncated, "the organization index at "+repo+" was not read: "+redactDigests(err.Error()))
		return rd
	case err != nil:
		rd.status, rd.note, rd.unavailable = impact.IndexUnavailable, redactDigests(err.Error()), true
		rd.lims = indexLim(orgindex.LimitationUnavailable, "the organization index at "+repo+" could not be listed: "+rd.note)
		return rd
	}
	newest, ignored := orgindex.Newest(tags)
	rd.services = len(newest)
	if ignored > 0 {
		rd.note = strings.TrimSpace(rd.note + " " + strconv.Itoa(ignored) + " tags outside the entry scheme ignored")
	}
	rd.entries, rd.unread, rd.lims = s.readEntries(ctx, repo, newest)
	return rd
}

// readEntries reads and confirms the newest entry of at most
// indexMaxServices services, Workers at a time. unread are the listed
// prefixes the service bound or the timeout kept from being read.
func (s *Service) readEntries(ctx context.Context, repo string, newest map[string]string) (valid []orgindex.Entry, unread map[string]bool, lims []fleet.Limitation) {
	prefixes := slices.Sorted(maps.Keys(newest))
	unread = map[string]bool{}
	if len(prefixes) > indexMaxServices {
		lims = indexLim(orgindex.LimitationTruncated, fmt.Sprintf("the index lists %d services; only the first %d were read", len(prefixes), indexMaxServices))
		for _, p := range prefixes[indexMaxServices:] {
			unread[p] = true
		}
		prefixes = prefixes[:indexMaxServices]
	}
	// indexRepository just listed repo, so it opens.
	r, _ := s.env().refusalRepo(repo)
	entries := make([]orgindex.Entry, len(prefixes))
	errs := make([]error, len(prefixes))
	var g errgroup.Group
	g.SetLimit(orgindex.Workers)
	for i, p := range prefixes {
		g.Go(func() error {
			e, err := s.readEntry(ctx, r, repo, newest[p])
			if err == nil {
				err = s.confirmEntry(ctx, e)
			}
			entries[i], errs[i] = e, err
			return nil
		})
	}
	_ = g.Wait() // every goroutine returns nil
	if ctx.Err() != nil {
		for _, p := range prefixes {
			unread[p] = true
		}
		return nil, unread, append(lims, indexLim(orgindex.LimitationTruncated, "the index read passed its "+indexReadTimeout.String()+" timeout; no entry was used")...)
	}
	var invalid []string
	for i, err := range errs {
		if err != nil {
			invalid = append(invalid, newest[prefixes[i]]+": "+entryReason(err))
			continue
		}
		valid = append(valid, entries[i])
	}
	if n := len(invalid); n > 0 {
		more := ""
		if n > maxInvalidReasons {
			invalid, more = invalid[:maxInvalidReasons], fmt.Sprintf(" (and %d more)", n-maxInvalidReasons)
		}
		lims = append(lims, indexLim(orgindex.LimitationEntryInvalid, fmt.Sprintf("%d index entries not used: %s%s",
			n, strings.Join(invalid, "; "), more))...)
	}
	return valid, unread, lims
}

// maxInvalidReasons bounds how many refused entries a limitation names.
const maxInvalidReasons = 10

// maxReasonLen bounds the text of one refused entry.
const maxReasonLen = 200

// entryReason is why an entry was refused, with no entry content: a Validate
// or Decode refusal is its fixed reason, and registry error text loses its
// digests and non-printing characters and is cut to maxReasonLen, so a
// forged entry can neither flood an answer nor steer the agent reading it.
func entryReason(err error) string {
	if r := orgindex.Reason(err); r != "" {
		return r
	}
	msg := strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, redactDigests(err.Error()))
	if runes := []rune(msg); len(runes) > maxReasonLen {
		msg = string(runes[:maxReasonLen]) + "…"
	}
	return msg
}

// readEntry reads the entry under tag, checking each bound before the read
// it guards, then binds it to repo and tag.
func (s *Service) readEntry(ctx context.Context, r *remote.Repository, repo, tag string) (orgindex.Entry, error) {
	desc, err := r.Resolve(ctx, tag)
	if err != nil {
		return orgindex.Entry{}, err
	}
	if desc.Size > orgindex.MaxManifestBytes {
		return orgindex.Entry{}, fmt.Errorf("the manifest is %d bytes, over the %d-byte bound", desc.Size, orgindex.MaxManifestBytes)
	}
	layer, err := s.entryLayer(ctx, r, repo, desc)
	if err != nil {
		return orgindex.Entry{}, err
	}
	e, err := orgindex.Decode(layer)
	if err == nil {
		err = e.Validate(repo, tag)
	}
	return e, err
}

// entryLayer is the layer of the entry manifest desc, from the cache when
// the cached manifest hashes to desc and the cached layer to the digest that
// manifest names.
func (s *Service) entryLayer(ctx context.Context, r *remote.Repository, repo string, desc ocispec.Descriptor) ([]byte, error) {
	c, hit := s.cachedEntry(repo, desc.Digest)
	fresh := !hit || digest.FromBytes(c.Manifest) != desc.Digest
	if fresh {
		raw, err := content.FetchAll(ctx, r, desc)
		if err != nil {
			return nil, err
		}
		c = indexCached{Manifest: raw}
	}
	layer, err := orgindex.EntryLayer(c.Manifest)
	if err != nil {
		return nil, err
	}
	if fresh || digest.FromBytes(c.Layer) != layer.Digest {
		if c.Layer, err = content.FetchAll(ctx, r, layer); err != nil {
			return nil, err
		}
		s.keepCachedEntry(repo, desc.Digest, c)
	}
	return c.Layer, nil
}

// confirmEntry requires the version tag in the service's own bundle
// repository, which only its publisher writes, to name the entry's bundle:
// a higher-version copy written by another publisher never shadows the
// service's real newest entry.
func (s *Service) confirmEntry(ctx context.Context, e orgindex.Entry) error {
	repo, want := contract.SplitOCIDigest(e.Bundle)
	r, err := s.env().refusalRepo(repo)
	if err != nil {
		return fmt.Errorf("its bundle repository does not open: %w", err)
	}
	desc, err := r.Resolve(ctx, e.Version)
	switch {
	case err != nil:
		return fmt.Errorf("its version is not confirmed by its bundle repository: %w", err)
	case desc.Digest.String() != want:
		return errors.New("its bundle repository names another bundle for its version")
	}
	return nil
}

// indexCached is one cached entry: its manifest and its layer.
type indexCached struct {
	Manifest []byte `json:"manifest"`
	Layer    []byte `json:"layer"`
}

// indexCachePath is where the entry with manifest d of repo is cached on
// disk, "" when the bundle store has no cache directory.
func (s *Service) indexCachePath(repo string, d digest.Digest) string {
	dir := bundleStoreCacheDir(s.BundleStore)
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(repo))
	return filepath.Join(dir, "index", hex.EncodeToString(sum[:])[:16], d.Encoded()+".json")
}

// cachedEntry is the cached entry with manifest d of repo. The caller checks
// it against the digests: a cache file is never trusted on its own.
func (s *Service) cachedEntry(repo string, d digest.Digest) (indexCached, bool) {
	var c indexCached
	if p := s.indexCachePath(repo, d); p != "" {
		b, err := os.ReadFile(p)
		if err == nil {
			err = json.Unmarshal(b, &c)
		}
		return c, err == nil
	}
	s.indexListings.mu.Lock()
	defer s.indexListings.mu.Unlock()
	c, ok := s.indexListings.entries[repo+"@"+d.String()]
	return c, ok
}

// keepCachedEntry caches c, best effort: a failed write only costs a read.
func (s *Service) keepCachedEntry(repo string, d digest.Digest, c indexCached) {
	if p := s.indexCachePath(repo, d); p != "" {
		b, _ := json.Marshal(c) // two byte slices
		if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
			_ = os.WriteFile(p, b, 0o644)
		}
		return
	}
	s.indexListings.mu.Lock()
	defer s.indexListings.mu.Unlock()
	if s.indexListings.entries == nil {
		s.indexListings.entries = map[string]indexCached{}
	}
	s.indexListings.entries[repo+"@"+d.String()] = c
}

// indexLim is one index limitation.
func indexLim(code, msg string) []fleet.Limitation {
	return []fleet.Limitation{{Code: code, Source: "index", Message: msg}}
}

// redactDigests replaces every digest in registry error text: index text
// never carries one.
func redactDigests(s string) string { return digestPattern.ReplaceAllString(s, "sha256:…") }
