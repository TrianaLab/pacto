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
	// listed holds the prefixes of valid, confirmed entries.
	listed map[string]bool
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
	rd := s.svc.indexRead(ctx, s.candidates)
	roots := s.selectRoots(rd.entries)
	coll := s.closure(ctx, roots)
	coll.Limitations = append(slices.Clone(rd.lims), coll.Limitations...)
	s.summary = impact.ConsumerIndex{Repository: rd.repo, Status: rd.status, Services: rd.services,
		Consumers: len(roots), Completeness: fleet.CompletenessComplete, Reason: rd.note}
	switch {
	case rd.unavailable:
		coll.State = &fleet.SourceState{Status: fleet.SourceUnavailable}
	case len(coll.Limitations) > 0:
		coll.State = &fleet.SourceState{Status: fleet.SourcePartial}
	}
	if len(coll.Limitations) > 0 {
		s.summary.Completeness = fleet.CompletenessPartial
	}
	return coll
}

// selectRoots records which services the index lists and returns the
// bundles of those that depend on the provider.
func (s *indexSource) selectRoots(entries []orgindex.Entry) []string {
	s.listed = map[string]bool{}
	var roots []string
	for _, e := range entries {
		s.listed[orgindex.Prefix(fleet.ServiceKey(e.Service))] = true
		if e.Names(s.provider) {
			roots = append(roots, contract.OCIScheme+e.Bundle)
		}
	}
	return roots
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

// indexed reports whether the index lists k.
func (s *indexSource) indexed(k fleet.ServiceKey) bool { return s.listed[orgindex.Prefix(k)] }

// indexRead is one index read before selection: what every changed service
// selects its consumers from.
type indexRead struct {
	at       time.Time
	repo     string
	note     string
	status   string
	services int
	// entries are the valid, confirmed newest entries, one per service.
	entries     []orgindex.Entry
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

// indexRead is the read of the first index among candidates, reused for
// indexListingTTL. Only a finished read is kept.
//
// ponytail: one fleet rebuild per changed service in pacto ci; give CIPlan
// one index source that selects for every changed service if a large
// monorepo makes it slow.
func (s *Service) indexRead(ctx context.Context, candidates []string) *indexRead {
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
	if rd.status == impact.IndexRead && ctx.Err() == nil {
		rd.at = now
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
	rd.entries, rd.lims = s.readEntries(ctx, repo, newest)
	return rd
}

// readEntries reads and confirms the newest entry of at most
// indexMaxServices services, Workers at a time.
func (s *Service) readEntries(ctx context.Context, repo string, newest map[string]string) ([]orgindex.Entry, []fleet.Limitation) {
	prefixes := slices.Sorted(maps.Keys(newest))
	var lims []fleet.Limitation
	if len(prefixes) > indexMaxServices {
		lims = indexLim(orgindex.LimitationTruncated, fmt.Sprintf("the index lists %d services; only the first %d were read", len(prefixes), indexMaxServices))
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
		return nil, append(lims, indexLim(orgindex.LimitationTruncated, "the index read passed its "+indexReadTimeout.String()+" timeout; no entry was used")...)
	}
	var valid []orgindex.Entry
	var invalid []string
	for i, err := range errs {
		if err != nil {
			invalid = append(invalid, redactDigests(err.Error()))
			continue
		}
		valid = append(valid, entries[i])
	}
	if len(invalid) > 0 {
		lims = append(lims, indexLim(orgindex.LimitationEntryInvalid, fmt.Sprintf("%d index entries not used: %s",
			len(invalid), strings.Join(invalid, "; ")))...)
	}
	return valid, lims
}

// readEntry reads the entry under tag, checking each bound before the read
// it guards, then binds it to repo and tag.
func (s *Service) readEntry(ctx context.Context, r *remote.Repository, repo, tag string) (orgindex.Entry, error) {
	desc, err := r.Resolve(ctx, tag)
	if err != nil {
		return orgindex.Entry{}, fmt.Errorf("%s: %w", tag, err)
	}
	if desc.Size > orgindex.MaxManifestBytes {
		return orgindex.Entry{}, fmt.Errorf("%s: the manifest is %d bytes, over the %d-byte bound", tag, desc.Size, orgindex.MaxManifestBytes)
	}
	layer, err := s.entryLayer(ctx, r, repo, desc)
	if err != nil {
		return orgindex.Entry{}, fmt.Errorf("%s: %w", tag, err)
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
	layer, err := entryLayerDesc(c.Manifest)
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

// entryLayerDesc is the one layer of an entry manifest, refused unless the
// manifest is an index entry whose layer fits the entry bound.
func entryLayerDesc(raw []byte) (ocispec.Descriptor, error) {
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m) // a manifest that does not decode has no artifactType
	switch {
	case m.ArtifactType != orgindex.ArtifactType:
		return ocispec.Descriptor{}, fmt.Errorf("the manifest's artifactType is %q, not %s", m.ArtifactType, orgindex.ArtifactType)
	case len(m.Layers) != 1 || m.Layers[0].MediaType != orgindex.MediaType:
		return ocispec.Descriptor{}, fmt.Errorf("the manifest is not one %s layer", orgindex.MediaType)
	case m.Layers[0].Size > orgindex.MaxEntryBytes:
		return ocispec.Descriptor{}, fmt.Errorf("the entry layer is %d bytes, over the %d-byte bound", m.Layers[0].Size, orgindex.MaxEntryBytes)
	}
	return m.Layers[0], nil
}

// confirmEntry requires the version tag in the service's own bundle
// repository, which only its publisher writes, to name the entry's bundle:
// a higher-version copy written by another publisher never shadows the
// service's real newest entry.
func (s *Service) confirmEntry(ctx context.Context, e orgindex.Entry) error {
	repo, want := contract.SplitOCIDigest(e.Bundle)
	r, err := s.env().refusalRepo(repo)
	if err != nil {
		return fmt.Errorf("%s %s: %w", e.Service, e.Version, err)
	}
	desc, err := r.Resolve(ctx, e.Version)
	switch {
	case err != nil:
		return fmt.Errorf("%s %s is not confirmed by %s:%s: %w", e.Service, e.Version, repo, e.Version, err)
	case desc.Digest.String() != want:
		return fmt.Errorf("%s %s is not confirmed: %s:%s names another bundle", e.Service, e.Version, repo, e.Version)
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
