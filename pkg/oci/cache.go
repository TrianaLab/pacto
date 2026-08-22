package oci

import (
	"compress/gzip"
	"container/list"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/trianalab/pacto/v3/internal/cachehook"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/logging"
)

// pullCacheMaxEntries bounds the in-memory pulled-bundle cache. Long-running
// callers (the dashboard prefetches every version of every repo on a loop)
// would otherwise retain every bundle — with its decompressed FS — forever,
// scaling memory with repo×version count and eventually OOMing. Evicted entries
// still serve from the disk cache. Package var so tests can shrink it.
//
// ponytail: count-based LRU. Entries vary in size (SBOMs dwarf tiny contracts),
// so this bounds count, not bytes. Switch to a byte-budget LRU if a few huge
// bundles ever blow the ceiling on their own.
var pullCacheMaxEntries = 512

// CachedStore wraps a BundleStore with in-memory and disk caching. Pulled
// bundles are kept in memory (fastest) and persisted to disk under
// ~/.cache/pacto/oci/ so they survive across process invocations. ListTags
// results are cached in memory for the lifetime of the process.
type CachedStore struct {
	inner    BundleStore
	cacheDir string

	// skipDiskReads disables loading from disk cache (cold-start mode).
	// Disk writes remain enabled so same-session pulls are persisted.
	skipDiskReads bool

	// materialized records that THIS process has committed an entry into the disk
	// cache. It is a different fact from what the cache held at startup and from
	// what happens to be discoverable now, and the only one that says the
	// directory has content this run put there.
	materialized atomic.Bool

	// pullCache is a bounded LRU: map ref -> list element, with pullLRU ordering
	// entries most-recently-used at the front. Guarded by pullMu.
	pullMu    sync.Mutex
	pullCache map[string]*list.Element
	pullLRU   *list.List

	tagsMu    sync.Mutex
	tagsCache map[string][]string
}

// pullEntry is the value stored in each pullLRU list element. ref is the LOOKUP
// KEY — what a caller asked for — and rec is what the entry that answered says
// these bytes ARE: the reference they were pulled under and the manifest digest
// of the artifact that served them (empty when nothing recorded one). The two
// are different facts, and they differ whenever one entry directory answers to
// more than one reference, so rec travels with the bundle instead of being
// re-derived from the key or from a mutable tag.
type pullEntry struct {
	ref    string
	rec    CachedRef
	bundle *contract.Bundle
}

// NewCachedStore creates a BundleStore that caches pulled bundles on disk.
// If the cache directory cannot be determined, caching is silently disabled.
func NewCachedStore(inner BundleStore) *CachedStore {
	dir, err := pactoCacheDir()
	if err != nil {
		dir = ""
	}
	return &CachedStore{
		inner:     inner,
		cacheDir:  dir,
		pullCache: map[string]*list.Element{},
		pullLRU:   list.New(),
		tagsCache: map[string][]string{},
	}
}

// DisableCache skips reading from the disk cache (cold-start mode) and clears
// the in-memory caches (both pulled bundles and listed tags). Disk writes
// remain enabled so that same-session pulls (e.g. fetch-all-versions) are still
// persisted and available for enrichment.
func (c *CachedStore) DisableCache() {
	c.skipDiskReads = true
	c.pullMu.Lock()
	c.pullCache = map[string]*list.Element{}
	c.pullLRU = list.New()
	c.pullMu.Unlock()
	c.tagsMu.Lock()
	c.tagsCache = map[string][]string{}
	c.tagsMu.Unlock()
}

// CacheDir returns the resolved on-disk cache directory (e.g. ~/.cache/pacto/oci).
// Returns empty string if no cache directory could be determined at creation time.
func (c *CachedStore) CacheDir() string {
	return c.cacheDir
}

func pactoCacheDir() (string, error) {
	if os.Getenv("XDG_CACHE_HOME") != "" {
		return CacheDirFor(""), nil
	}
	home, err := userHomeDirFn()
	if err != nil {
		return "", err
	}
	return CacheDirFor(home), nil
}

// CacheDirFor returns the on-disk OCI cache directory for the given home
// directory, honoring XDG_CACHE_HOME. Exported so other components (e.g. the
// dashboard diagnostics) resolve the exact same path.
func CacheDirFor(home string) string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "pacto", "oci")
	}
	return filepath.Join(home, ".cache", "pacto", "oci")
}

// Push uploads the bundle to the registry via the wrapped store. It does not
// populate the cache; pushed bundles are only cached when later pulled.
func (c *CachedStore) Push(ctx context.Context, ref string, bundle *contract.Bundle) (string, error) {
	return c.inner.Push(ctx, ref, bundle)
}

// Resolve delegates to the wrapped store to resolve a ref to a digest; it is
// not cached and always contacts the underlying store.
func (c *CachedStore) Resolve(ctx context.Context, ref string) (string, error) {
	return c.inner.Resolve(ctx, ref)
}

// ListTags returns the tags for repo, serving from the in-memory tags cache on
// a hit and otherwise querying the wrapped store and caching the result for the
// process lifetime.
func (c *CachedStore) ListTags(ctx context.Context, repo string) ([]string, error) {
	c.tagsMu.Lock()
	if cached, ok := c.tagsCache[repo]; ok {
		c.tagsMu.Unlock()
		logging.LoggerFromContext(ctx).Debug("tags cache hit", "repo", repo)
		return cached, nil
	}
	c.tagsMu.Unlock()

	tags, err := c.inner.ListTags(ctx, repo)
	if err != nil {
		return nil, err
	}

	c.tagsMu.Lock()
	c.tagsCache[repo] = tags
	c.tagsMu.Unlock()

	return tags, nil
}

// PullCached returns the bundle for ref from the in-memory cache, then the disk
// cache (skipped when DisableCache is active), and reports whether it was found.
// It NEVER contacts the registry, so it is the offline read path: [Resolver] in
// [LocalOnly] mode uses it, which is what keeps "local only" from silently
// becoming a per-ref network pull on a cache miss.
func (c *CachedStore) PullCached(ctx context.Context, ref string) (*contract.Bundle, bool) {
	bundle, _, hit := c.pullCached(ctx, ref)
	return bundle, hit
}

// PullCachedPinned is [CachedStore.PullCached] plus the identity RECORDED with
// the bundle it serves — the reference it was pulled under AND the manifest
// digest of the artifact that answered, read from the same cache generation as
// the bytes. So the offline reader reports what it actually got and never has to
// ask a registry the mode forbids.
//
// The record is the entry's own statement, not the lookup key echoed back, and a
// hit is only reported when the two AGREE (see [CachedStore.pullCached]). A zero
// record means the entry predates the sidecar and states no identity at all.
func (c *CachedStore) PullCachedPinned(ctx context.Context, ref string) (*contract.Bundle, CachedRef, bool) {
	return c.pullCached(ctx, ref)
}

// Materialized reports whether this store has written a bundle into the disk
// cache during this process. A dashboard uses it to tell "the cache is empty
// because nothing has been pulled yet" from "the cache holds what this session
// pulled" — the pod's cache is an emptyDir, so the first is where every
// operator-managed run starts.
func (c *CachedStore) Materialized() bool { return c.materialized.Load() }

// pullCached is [CachedStore.PullCached] plus the identity recorded for the
// served bundle, so a hit reports what the pull observed rather than what the
// lookup key spells.
//
// A hit requires the two to AGREE. An entry that states a DIFFERENT reference is
// some other artifact's, and serving it here would publish its bytes and its
// digest under the reference this call named — the mixed revision that the
// injective key ([CachedStore.entryDir]) prevents for entries written by this
// version and that a legacy entry, written when the key still aliased, can still
// offer. Disagreement is therefore a MISS: online the registry is asked for the
// artifact actually wanted, offline the caller is told it is not cached. An entry
// that states NOTHING (written before the sidecar existed) contradicts nothing
// and is served with no identity, as it always was.
func (c *CachedStore) pullCached(ctx context.Context, ref string) (*contract.Bundle, CachedRef, bool) {
	bundle, rec, ok := c.cachedEntry(ctx, ref)
	if !ok {
		return nil, CachedRef{}, false
	}
	if rec.Ref != "" && rec.Ref != ref {
		logging.LoggerFromContext(ctx).Debug("cache entry names another reference",
			"ref", ref, "entry", rec.Ref)
		return nil, CachedRef{}, false
	}
	return bundle, rec, true
}

// cachedEntry finds what the cache holds under ref — memory first, then disk —
// without judging whether it is ref's. Both legs return the pair the entry
// itself supplied, so [CachedStore.pullCached] can apply ONE rule to both and a
// warm read can never answer differently from the cold read that filled it.
func (c *CachedStore) cachedEntry(ctx context.Context, ref string) (*contract.Bundle, CachedRef, bool) {
	// 1. In-memory cache (fastest). The pair is copied out under the mutex: a
	// concurrent storePull rewrites the entry in place.
	c.pullMu.Lock()
	if el, ok := c.pullCache[ref]; ok {
		c.pullLRU.MoveToFront(el)
		e := el.Value.(*pullEntry)
		bundle, rec := e.bundle, e.rec
		c.pullMu.Unlock()
		logging.LoggerFromContext(ctx).Debug("cache hit (memory)", "ref", ref)
		return bundle, rec, true
	}
	c.pullMu.Unlock()

	// 2. Disk cache (skipped when --no-cache / DisableCache is active).
	if c.cacheDir == "" || c.skipDiskReads {
		return nil, CachedRef{}, false
	}
	for _, dir := range c.entryDirs(ref) {
		if bundle, rec, ok := ReadCacheEntry(dir); ok {
			logging.LoggerFromContext(ctx).Debug("cache hit (disk)", "ref", ref)
			// The sidecar was committed with these exact bytes, so it is the
			// identity of what was just loaded, not a fresh guess about a tag.
			c.storePull(ref, bundle, rec)
			return bundle, rec, true
		}
	}
	return nil, CachedRef{}, false
}

// Pull returns the bundle for ref, checking the in-memory cache, then the disk
// cache (skipped when DisableCache is active), then the wrapped store. Registry
// pulls are stored in memory and persisted to disk for future lookups.
func (c *CachedStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	bundle, _, err := c.PullPinned(ctx, ref)
	return bundle, err
}

// PullPinned is [CachedStore.Pull] plus the manifest digest of the artifact the
// returned bundle actually came from (see [resolveAndPull] for why the two must
// come from one observation). The digest is empty only when nothing ever told us
// — never a different artifact's.
//
// The originally requested ref is preserved as provenance — it is the cache key
// and the sidecar's Ref — so pinning changes what is fetched, never what this
// entry is called. A cache hit reports the digest RECORDED at pull time, so the
// answer never depends on where the tag points now.
func (c *CachedStore) PullPinned(ctx context.Context, ref string) (*contract.Bundle, string, error) {
	if bundle, rec, ok := c.pullCached(ctx, ref); ok {
		return bundle, rec.Digest, nil
	}

	// 3. Registry (slowest).
	logging.LoggerFromContext(ctx).Debug("cache miss, pulling from registry", "ref", ref)
	bundle, digest, err := resolveAndPull(ctx, c.inner, ref)
	if err != nil {
		return nil, "", err
	}

	// One record for the three places this pull is remembered: what the caller is
	// told, what memory serves next, and what is committed beside the bytes.
	rec := CachedRef{Ref: ref, Digest: digest}
	c.storePull(ref, bundle, rec)
	if c.cacheDir != "" {
		entry := c.entryDir(ref)
		if err := c.writeCacheEntry(entry, rec, bundle); err != nil {
			// The pull SUCCEEDED; only its persistence did not. Say so instead of
			// dropping it silently — a cache that never fills is a performance
			// mystery, and the failure that motivated this path was invisible.
			logging.LoggerFromContext(ctx).Warn("could not cache the pulled bundle", "ref", ref, "error", err)
		} else {
			c.materialized.Store(true)
		}
	}

	return bundle, digest, nil
}

// CachedRefFile is the sidecar written beside every cached bundle.
const CachedRefFile = "ref.json"

// CachedBundleFile is the bundle archive of a cache entry. Cache walkers key on
// it, so it is also what makes an entry VISIBLE.
const CachedBundleFile = "bundle.tar.gz"

// CachedRef is what a cached bundle IS: the reference it was pulled under,
// spelled exactly as the registry was asked, plus the manifest digest of the
// artifact that answered (empty when the registry would not say).
type CachedRef struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
}

// ReadCachedRef reads ONE file: the sidecar beside a cached bundle. The second
// result is false when the entry predates the sidecar or the file is unusable.
//
// It is NOT an entry read, and it is not how a reader learns what a bundle is.
// Pairing what it returns with bytes read separately by pathname is exactly the
// splice [ReadCacheEntry] exists to prevent: the two observations can straddle a
// competing writer's commit, and the reader then publishes one generation's
// content under the next generation's identity. Anything that needs both facts
// must call [ReadCacheEntry]; this reports what a sidecar says, for callers
// checking what a WRITER wrote.
func ReadCachedRef(dir string) (CachedRef, bool) {
	b, err := os.ReadFile(filepath.Join(dir, CachedRefFile))
	if err != nil {
		return CachedRef{}, false
	}
	return parseCachedRef(b)
}

// readCachedRefFrom reads the sidecar through an already-open entry handle, so
// the identity comes from the same generation as the bundle read beside it.
func readCachedRefFrom(root *os.Root) (CachedRef, bool) {
	b, err := root.ReadFile(CachedRefFile)
	if err != nil {
		return CachedRef{}, false
	}
	return parseCachedRef(b)
}

func parseCachedRef(b []byte) (CachedRef, bool) {
	var rec CachedRef
	if err := json.Unmarshal(b, &rec); err != nil || rec.Ref == "" {
		return CachedRef{}, false
	}
	return rec, true
}

// writeCacheEntry commits a bundle and its identity as ONE cache entry.
//
// An entry is two files a reader must agree about: a walker keys on
// bundle.tar.gz and asks the ref.json beside it what that bundle IS. The cache
// PATH cannot answer — it spells a registry port and a tag with the same
// separator, and escapes what it must ([CachedStore.entryDir]) — so a reader
// that finds no usable sidecar GUESSES an identity from the path, and the same
// published artifact enters the fleet a second time under a derived content
// identity.
//
// Written in place the two files can disagree: a sidecar write that fails
// (ref.json already exists as a directory) still leaves the bundle to be
// published, and an interruption between the two pairs a fresh sidecar with the
// previous pull's bytes. So the pair is built in a staging directory OUTSIDE the
// tree cache walkers scan and swapped in whole. A reader sees the old entry or
// the new entry, never a mixture, and any failure leaves the entry ABSENT — an
// ordinary cache miss the next pull repairs, not a corrupt identity.
func (c *CachedStore) writeCacheEntry(dir string, rec CachedRef, bundle *contract.Bundle) error {
	// The entry's parent first: it is an ancestor of the staging root too, so one
	// MkdirAll makes both usable.
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	// The staging root is the cache's PARENT: a half-written bundle.tar.gz inside
	// the walked tree is exactly the incoherent entry this function exists to
	// prevent. Same filesystem, so the commit below is a rename, not a copy.
	staging, err := os.MkdirTemp(filepath.Dir(c.cacheDir), ".oci-staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if err := stageCacheEntry(staging, rec, bundle); err != nil {
		return err
	}
	// A directory rename cannot overwrite a directory, so the old entry goes
	// first. Absence is coherent; a new bundle beside a stale sidecar is not. A
	// RemoveAll that fails needs no report of its own: the rename then fails and
	// says so, and what survives is the coherent entry that was already there.
	_ = os.RemoveAll(dir)
	return os.Rename(staging, dir)
}

// stageCacheEntry writes both files of an entry into an uncommitted directory.
func stageCacheEntry(dir string, rec CachedRef, bundle *contract.Bundle) error {
	if err := writeBundleFile(filepath.Join(dir, CachedBundleFile), bundle); err != nil {
		return err
	}
	b, _ := json.Marshal(rec) // two string fields; marshalling cannot fail
	return os.WriteFile(filepath.Join(dir, CachedRefFile), b, 0o600)
}

// digestFromRef returns the manifest digest a reference already pins, else "".
func digestFromRef(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// PinRefToDigest rewrites a reference to its immutable digest-pinned form
// "<repository>@<digest>", dropping any scheme, tag or digest it already
// carries. A tag is a ':' after the last '/', so a registry port
// (localhost:5000/...) is never mistaken for a tag separator.
func PinRefToDigest(ref, digest string) string {
	r := strings.TrimPrefix(ref, "oci://")
	if i := strings.Index(r, "@"); i >= 0 {
		r = r[:i]
	}
	slash := strings.LastIndex(r, "/")
	if colon := strings.LastIndex(r, ":"); colon > slash {
		r = r[:colon]
	}
	return r + "@" + digest
}

// storePull remembers a bundle under the key it was looked up by, together with
// the record of what that bundle IS. The two are stored as a pair: a warm hit
// must answer with the identity the cold read observed, never with one
// reconstructed from the key.
func (c *CachedStore) storePull(ref string, bundle *contract.Bundle, rec CachedRef) {
	c.pullMu.Lock()
	defer c.pullMu.Unlock()
	if el, ok := c.pullCache[ref]; ok {
		e := el.Value.(*pullEntry)
		e.bundle, e.rec = bundle, rec
		c.pullLRU.MoveToFront(el)
		return
	}
	c.pullCache[ref] = c.pullLRU.PushFront(&pullEntry{ref: ref, rec: rec, bundle: bundle})
	// Evict least-recently-used entries beyond the cap. Len > cap ≥ 0 guarantees
	// a non-nil Back() each iteration.
	for c.pullLRU.Len() > pullCacheMaxEntries {
		oldest := c.pullLRU.Back()
		c.pullLRU.Remove(oldest)
		delete(c.pullCache, oldest.Value.(*pullEntry).ref)
	}
}

// untaggedSegment stands in for the tag of a reference that names none, so that
// "a" and "a:b" cannot spell to one directory. Escaping never produces it: a
// literal "%00" tag escapes to "%2500".
const untaggedSegment = "%00"

// entryNamespace is the reserved directory every entry this version writes lives
// under. It is what makes the new layout DISJOINT from the legacy one, which is
// a strictly stronger property than the injectivity below and the one an upgrade
// actually needs.
//
// A legacy key is the reference itself with ':' spelled '/', so its first path
// segment is the reference's first component — and an OCI reference component
// must begin with an alphanumeric. No valid reference can therefore produce a
// legacy key under a segment starting with '_', so nothing this version commits
// can ever land on a baseline an earlier one left. (The same reservation already
// backs the "_invalid" sentinel in [CachedStore.contained].)
const entryNamespace = "_v2"

// entryDir returns the directory this version writes ref's cache entry to. The
// mapping is INJECTIVE — distinct references never name one directory — and its
// whole range is disjoint from [CachedStore.legacyEntryDir]'s.
//
// The old key mapped every ':' to '/', which is not injective:
// "localhost:5000/demo/svc:1.0.0" and "localhost/5000/demo/svc:1.0.0" named ONE
// entry, so pulling either overwrote the other's offline baseline and a lookup
// by either was answered with whichever had been installed last — bundle B
// published under reference A.
//
// So only the TAG's ':' is still spelled as a separator, and every other ':'
// (a registry port, a digest algorithm) is escaped inside its path segment along
// with the '%' that escaping uses. The tag always gets a segment of its own,
// [untaggedSegment] when there is none. Unescape each segment, rejoin with '/'
// and put the ':' back before the last, and the reference comes back exactly.
//
// Fixing the encoding alone was not enough. The repaired key stayed inside the
// legacy NAMESPACE, where it is injective among new keys and still collides with
// other references' old ones: entryDir("localhost/5000/demo/svc:1.0.0") is
// exactly where a legacy entry for "localhost:5000/demo/svc:1.0.0" lives, so the
// first pull after an upgrade destroyed a baseline it had no way to see. The
// reserved namespace is what closes that; compatibility comes from reading the
// legacy path ([CachedStore.entryDirs]), never from writing to it.
//
// ponytail: a reserved segment, not a per-entry version marker or a manifest —
// the layout is derivable from the reference, so nothing needs to be recorded to
// find it. A '.' or '..' segment would still leave the cache directory; those
// are not valid OCI references and [CachedStore.contained] rejects them rather
// than the encoding growing cases for them.
func (c *CachedStore) entryDir(ref string) string {
	repo, tag, tagged := splitRefTag(ref)
	parts := append([]string{entryNamespace}, strings.Split(repo, "/")...)
	for i, p := range parts[1:] {
		parts[i+1] = escapeRefSegment(p)
	}
	if tagged {
		parts = append(parts, escapeRefSegment(tag))
	} else {
		parts = append(parts, untaggedSegment)
	}
	return c.contained(filepath.Join(parts...))
}

// legacyEntryDir returns the directory ref's entry lived in before the key was
// injective. READ-ONLY: nothing is written here any more, and an entry found
// here is only served when its sidecar names the reference asked for (see
// [CachedStore.pullCached]) — that is what keeps two references that still
// collide under this spelling from crossing.
func (c *CachedStore) legacyEntryDir(ref string) string {
	return c.contained(strings.ReplaceAll(ref, ":", "/"))
}

// entryDirs lists the directories a read must consider, in order, without
// naming the same one twice — for a reference with neither a port nor a digest
// the two spellings are identical.
func (c *CachedStore) entryDirs(ref string) []string {
	dir := c.entryDir(ref)
	if legacy := c.legacyEntryDir(ref); legacy != dir {
		return []string{dir, legacy}
	}
	return []string{dir}
}

// contained resolves rel under the cache directory, refusing to leave it and
// refusing to BE it.
//
// [filepath.IsLocal] is the containment test rather than a string comparison on
// a joined path: it is the standard library's own answer to "does this stay
// inside", and it rejects an escaping "..", an absolute path, an empty path and
// a Windows reserved name lexically, before anything is joined. It answers yes
// for "." though, and a path that cleans to "." names the CACHE DIRECTORY
// itself — not an entry in it, but every entry at once — so that is ruled out
// beside it. A degenerate reference is thus a miss, never the whole cache.
func (c *CachedStore) contained(rel string) string {
	if !filepath.IsLocal(rel) || filepath.Clean(rel) == "." {
		return filepath.Join(c.cacheDir, "_invalid")
	}
	return filepath.Join(c.cacheDir, rel)
}

// splitRefTag splits a reference into its repository and its tag. A tag is a
// ':' after the last '/', so a registry port is never mistaken for one; the last
// result reports whether there was a tag at all, which "" alone cannot.
func splitRefTag(ref string) (repo, tag string, tagged bool) {
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon], ref[colon+1:], true
	}
	return ref, "", false
}

// escapeRefSegment percent-escapes the two characters that would otherwise make
// a path segment ambiguous: ':' (which the layout spells as a separator) and the
// '%' this escaping introduces.
func escapeRefSegment(s string) string {
	if !strings.ContainsAny(s, "%:") {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		switch s[i] {
		case '%':
			b.WriteString("%25")
		case ':':
			b.WriteString("%3A")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Nothing retires a legacy entry. Retirement read a sidecar BY PATHNAME and
// later removed that pathname, and the disk cache is shared: between the two
// operations another process installs a whole new generation, and the removal
// takes a foreign one the read never saw. A sidecar-less legacy entry is worse —
// its owner is unknowable, so "it must be ours" is a guess that costs a stranger
// their only offline baseline.
//
// The duplicate a surviving legacy entry would otherwise cause is a WALKER
// problem, and it is solved where it appears: both cache walkers skip an entry
// whose complete recorded identity they have already indexed. Stale bytes stay
// on disk until the cache is cleared, which is what a cache is for.

// cacheEntryAttempts bounds the re-reads below. Each retry observes the
// generation that displaced the last one, so a competing writer would have to
// win the race repeatedly to exhaust them — and exhaustion is a cache MISS, not
// an incoherent answer.
const cacheEntryAttempts = 3

// ReadCacheEntry reads a cache entry — the bundle and the identity beside it —
// from ONE installed generation. It is THE definition of a cached generation:
// every reader of the disk cache, in this package or outside it, gets both facts
// here or gets neither.
//
// A walker's business is DISCOVERY — which entry directories exist — and nothing
// more. The pathname it found an entry by cannot identify the entry ([entryDir]
// spells a registry port, a tag and a missing tag with characters a path also
// uses), and an identity read separately from the bytes describes whatever was
// installed at the moment of THAT read. So a walker hands the directory to this
// function and publishes what comes back, together.
//
// [CachedStore.writeCacheEntry] commits a generation whole, but that only makes
// each generation coherent; it does not make a READER coherent. Two files opened
// by pathname are two observations, and another process (or another CachedStore
// over the same directory — the disk cache is shared, so its mutex proves
// nothing) can commit a new generation between them. The reader then pairs
// bundle A with identity B and publishes content under a digest it does not
// have, which is the same lie the in-place writer used to tell.
//
// So both files come from one directory HANDLE. A generation swapped out from
// under it is unlinked, not rewritten: its files keep answering this handle, and
// once RemoveAll has taken them the reader sees them ABSENT rather than
// replaced. Absent is coherent — retry and read the generation that displaced
// it, or report a miss the next pull repairs.
func ReadCacheEntry(dir string) (*contract.Bundle, CachedRef, bool) {
	for range cacheEntryAttempts {
		bundle, rec, swapped := readCacheGeneration(dir)
		if !swapped {
			return bundle, rec, bundle != nil
		}
	}
	return nil, CachedRef{}, false
}

// readCacheGeneration reads one generation through a single directory handle.
// The last result reports that the generation went away mid-read, so the caller
// can read its successor instead of returning half of each.
func readCacheGeneration(dir string) (*contract.Bundle, CachedRef, bool) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, CachedRef{}, false // no entry here at all: an ordinary miss
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(CachedBundleFile)
	if err != nil {
		return nil, CachedRef{}, !heldGenerationIsInstalled(root, dir)
	}
	defer func() { _ = f.Close() }()
	bundle, err := loadBundle(f)
	if err != nil {
		return nil, CachedRef{}, false
	}

	cachehook.AfterBundleRead()

	rec, ok := readCachedRefFrom(root)
	// No sidecar has two meanings: an entry written before sidecars existed —
	// compatible, identity simply unknown — or a generation that has since been
	// swapped away, whose successor does have one.
	if !ok && !heldGenerationIsInstalled(root, dir) {
		return nil, CachedRef{}, true
	}
	return bundle, rec, false
}

// heldGenerationIsInstalled reports whether the directory this handle holds is
// still the one installed at dir. A commit replaces the directory, so a handle
// whose identity no longer matches names a generation nobody can reach.
func heldGenerationIsInstalled(root *os.Root, dir string) bool {
	held, herr := root.Stat(".")
	installed, ierr := os.Stat(dir)
	return herr == nil && ierr == nil && os.SameFile(held, installed)
}

func loadBundle(r io.Reader) (*contract.Bundle, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	fsys, err := extractTar(gr)
	if err != nil {
		return nil, err
	}
	return bundleFromFS(fsys)
}

// writeBundleFile writes the bundle archive to path. Close is reported, not
// deferred away: a gzip flush that fails on close would otherwise commit a
// truncated archive under a valid identity.
func writeBundleFile(path string, bundle *contract.Bundle) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := writeBundleTarGz(f, bundle.FS); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
