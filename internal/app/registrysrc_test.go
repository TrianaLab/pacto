package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	ggcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// tplYAML is the golden path name at version.
func tplYAML(name, version string) string {
	return "pactoVersion: \"2.4\"\nservice:\n  name: " + name + "\n  version: " + version +
		"\n  owner: {team: paths}\n  template:\n    plugins:\n      - name: dockerfile\n"
}

// offeringYAML is the platform offering name at version.
func offeringYAML(name, version string) string {
	return "pactoVersion: \"2.4\"\nservice:\n  name: " + name + "\n  version: " + version +
		"\n  owner: {team: data}\n  offering: " + name + "\n  summary: Primary database\n"
}

// svcYAML is the plain service name at version.
func svcYAML(name, version string) string {
	return "pactoVersion: \"2.4\"\nservice: {name: " + name + ", version: " + version + "}\n"
}

// pushAt pushes yaml to <host>/<repo>, writing its entry to index.
func (e *idxEnv) pushAt(t *testing.T, repo, yaml, index string) *PushResult {
	t.Helper()
	res, err := e.svc.Push(t.Context(), PushOptions{Ref: "oci://" + e.ir.host + "/" + repo, Path: writeIdxBundle(t, yaml), Index: index})
	if err != nil {
		t.Fatalf("push %s: %v", repo, err)
	}
	e.ir.reset()
	return res
}

// bundleOf is the bundle yaml describes.
func (e *idxEnv) bundleOf(t *testing.T, yaml string) *contract.Bundle {
	t.Helper()
	b, err := e.svc.ResolveBundle(t.Context(), writeIdxBundle(t, yaml))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// writeImage writes img to <host>/<repo>:<tag>.
func (e *idxEnv) writeImage(t *testing.T, repo, tag string, img v1.Image) {
	t.Helper()
	ref, _ := name.ParseReference(e.ir.host+"/"+repo+":"+tag, name.Insecure)
	if err := ggcrremote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	e.ir.reset()
}

// pushRaw pushes a manifest of artifactType with annotations, config and
// one bundle layer to <host>/<repo>:<tag>.
func (e *idxEnv) pushRaw(t *testing.T, repo, tag, artifactType string, annotations map[string]string, config []byte) {
	t.Helper()
	r, _ := anonRepo(e.ir.host + "/" + repo)
	push := func(mediaType string, b []byte) ocispec.Descriptor {
		d := ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(b), Size: int64(len(b))}
		if err := r.Push(t.Context(), d, bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
		return d
	}
	m := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, ArtifactType: artifactType,
		Config: push(ocispec.MediaTypeImageConfig, config), Layers: []ocispec.Descriptor{push(string(oci.LayerMediaType), []byte("layer"))}, Annotations: annotations}
	raw, _ := json.Marshal(m)
	d := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(raw), Size: int64(len(raw))}
	if err := r.PushReference(t.Context(), d, bytes.NewReader(raw), tag); err != nil {
		t.Fatal(err)
	}
	e.ir.reset()
}

// labels is an image config holding labels.
func labels(l map[string]string) []byte {
	b, _ := json.Marshal(map[string]any{"architecture": "amd64", "os": "linux", "config": map[string]any{"Labels": l},
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{}}})
	return b
}

// prefix is the discovery prefix of e.
func (e *idxEnv) prefix() string { return e.ir.host + "/acme" }

// discover is one discovery of e's prefix by svc with opts.
func (e *idxEnv) discover(t *testing.T, svc *Service, opts FleetOptions) *fleet.Collection {
	t.Helper()
	coll, err := (&registrySource{id: "registry", prefix: e.prefix(), svc: svc, opts: opts}).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return coll
}

// hook sets e's request hook.
func (e *idxEnv) hook(h func(w http.ResponseWriter, r *http.Request) bool) {
	e.ir.mu.Lock()
	defer e.ir.mu.Unlock()
	e.ir.hook = h
}

// answer is a hook that answers status to the requests whose method and
// path match.
func answer(status int, method, part string) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != method || !strings.Contains(r.URL.Path, part) {
			return false
		}
		http.Error(w, `{"errors":[{"code":"DENIED"}]}`, status)
		return true
	}
}

// either answers with the first hook that does.
func either(hs ...func(w http.ResponseWriter, r *http.Request) bool) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		return slices.ContainsFunc(hs, func(h func(w http.ResponseWriter, r *http.Request) bool) bool { return h(w, r) })
	}
}

// catalog404 is a registry that serves no _catalog.
var catalog404 = answer(http.StatusNotFound, http.MethodGet, "/v2/_catalog")

// setVar sets *p to v for the test.
func setVar[T any](t *testing.T, p *T, v T) {
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// discoveredNames are the service names coll's revisions hold, sorted.
func discoveredNames(coll *fleet.Collection) []string {
	var out []string
	for _, r := range coll.Revisions {
		out = append(out, r.Bundle.Contract.Service.Name+"@"+r.Bundle.Contract.Service.Version)
	}
	slices.Sort(out)
	return out
}

func TestCheckDiscover(t *testing.T) {
	for in, want := range map[string]string{
		"oci://ghcr.io/acme/": "ghcr.io/acme",
		" ghcr.io ":           "ghcr.io",
		"127.0.0.1:5000/acme": "127.0.0.1:5000/acme",
		"127.0.0.1:5000":      "127.0.0.1:5000",
		"ghcr.io/acme:1.0.0":  "",
		"ghcr.io/a/b:latest":  "",
		"ghcr.io/acme@sha256:" + strings.Repeat("a", 64): "",
		"":                     "",
		"https://ghcr.io/acme": "",
		"/etc/passwd":          "",
		"Ghcr.io/ACME":         "",
	} {
		got, err := CheckDiscover(in)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("%q: %q %v", in, got, err)
		}
		if err != nil && !strings.Contains(err.Error(), "--discover takes <registry>[/<path>] with no tag or digest") {
			t.Errorf("%q: %v", in, err)
		}
	}
	if _, err := NewService(fakeFleetStore{}, nil).Fleet(t.Context(), FleetOptions{Discover: []string{"ghcr.io/acme:1"}}); err == nil {
		t.Fatal("Fleet took a tagged prefix")
	}
}

func TestDiscoveryCompleteness(t *testing.T) {
	tpl := func(t *testing.T, e *idxEnv, index string) {
		e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), index)
	}
	for _, c := range []struct {
		name   string
		setup  func(t *testing.T, e *idxEnv)
		status fleet.SourceStatus
		code   string
		msg    string
	}{
		{"index and catalog", func(t *testing.T, e *idxEnv) { tpl(t, e, "") }, fleet.SourceAvailable, "", ""},
		{"index without catalog", func(t *testing.T, e *idxEnv) { tpl(t, e, ""); e.hook(catalog404) },
			fleet.SourcePartial, fleet.LimitationRegistryListingIndexOnly, "HOST serves no _catalog; listed from HOST/acme/pacto-index"},
		{"index with a refused catalog", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			e.hook(answer(http.StatusUnauthorized, http.MethodGet, "/v2/_catalog"))
		}, fleet.SourcePartial, fleet.LimitationRegistryListingIndexOnly, "HOST refused _catalog to this credential; listed from HOST/acme/pacto-index"},
		{"catalog without index", func(t *testing.T, e *idxEnv) { tpl(t, e, IndexNone) }, fleet.SourceAvailable, "", ""},
		{"neither", func(t *testing.T, e *idxEnv) { tpl(t, e, IndexNone); e.hook(catalog404) }, fleet.SourcePartial, fleet.LimitationRegistryListingNone,
			"no HOST/acme/pacto-index and HOST serves no _catalog: the golden paths and offerings under HOST/acme are unknown"},
		{"index refused", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			e.hook(answer(http.StatusForbidden, http.MethodGet, "/pacto-index/tags/list"))
		}, fleet.SourcePartial, fleet.LimitationRegistryListingUnavailable, "HOST/acme/pacto-index could not be listed: "},
		{"catalog failed", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			e.hook(answer(http.StatusBadRequest, http.MethodGet, "/v2/_catalog"))
		}, fleet.SourcePartial, fleet.LimitationRegistryListingUnavailable, "the _catalog of HOST could not be listed: "},
		{"both failed", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			e.svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("closed") }
		}, fleet.SourceUnavailable, fleet.LimitationRegistryListingUnavailable, "HOST/acme/pacto-index could not be listed: "},
		{"index past its page bound", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			var n atomic.Int64
			e.hook(func(w http.ResponseWriter, r *http.Request) bool {
				if !strings.HasSuffix(r.URL.Path, "/pacto-index/tags/list") {
					return false
				}
				i := n.Add(1)
				w.Header().Set("Link", fmt.Sprintf(`<%s?n=100&last=t%07d>; rel="next"`, r.URL.Path, i))
				_, _ = fmt.Fprintf(w, `{"name":"acme/pacto-index","tags":["t%07d"]}`, i)
				return true
			})
		}, fleet.SourcePartial, fleet.LimitationRegistryListingTruncated, "the listing of HOST/acme stopped at a bound or its timeout"},
		{"index read past the timeout", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			setVar(t, &indexReadTimeout, 200*time.Millisecond)
			e.hook(func(_ http.ResponseWriter, r *http.Request) bool {
				if strings.Contains(r.URL.Path, "/pacto-index/blobs/") || strings.Contains(r.URL.Path, "/pacto-index/manifests/") {
					time.Sleep(300 * time.Millisecond)
				}
				return false
			})
		}, fleet.SourcePartial, fleet.LimitationRegistryListingTruncated, "the listing of HOST/acme stopped at a bound or its timeout"},
		{"index over its service bound", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			e.pushAt(t, "acme/node-worker", tplYAML("node-worker", "0.4.0"), "")
			setVar(t, &indexMaxServices, 1)
			e.hook(catalog404)
		}, fleet.SourcePartial, fleet.LimitationRegistryListingTruncated, "the listing of HOST/acme stopped at a bound or its timeout"},
		{"a refused entry", func(t *testing.T, e *idxEnv) {
			tpl(t, e, "")
			e.forgeEntry(t, orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(e.key("x")), Name: "x", Version: "1.0.0",
				Bundle: e.ir.host + "/acme/x@sha256:" + strings.Repeat("a", 64)})
		}, fleet.SourcePartial, fleet.LimitationRegistryListingEntryUnreadable, "1 bundles listed under HOST/acme could not be read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newIdxEnv(t)
			c.setup(t, e)
			coll := e.discover(t, e.svc, FleetOptions{})
			if coll.State.Status != c.status || coll.State.ObservedAt == nil {
				t.Fatalf("state %+v, lims %+v", coll.State, coll.Limitations)
			}
			msg := strings.ReplaceAll(c.msg, "HOST", e.ir.host)
			if c.code == "" && len(coll.Limitations) != 0 || c.code != "" && !strings.HasPrefix(limMessage(coll.Limitations, c.code), msg) {
				t.Fatalf("want %s %q, got %+v", c.code, msg, coll.Limitations)
			}
			for _, l := range slices.Concat(coll.Limitations, coll.Notes) {
				if strings.Contains(l.Code, "INDEX_NOT_FOUND") || strings.Contains(l.Message, "listed from") && c.code != fleet.LimitationRegistryListingIndexOnly {
					t.Fatalf("limitation %+v", l)
				}
			}
		})
	}
}

func TestDiscoveryNoListingIsPartialNeverEmpty(t *testing.T) {
	e := newIdxEnv(t)
	e.hook(catalog404)
	snap, err := e.svc.Fleet(t.Context(), FleetOptions{Discover: []string{"oci://" + e.prefix()}})
	if err != nil {
		t.Fatal(err)
	}
	if st := sourceOf(t, snap, "registry"); st.Kind != fleet.SourceKindRegistryListing || st.Status != fleet.SourcePartial || st.RevisionCount != 0 || snap.Completeness == fleet.CompletenessComplete {
		t.Fatalf("source %+v, completeness %s", st, snap.Completeness)
	}
}

func TestDiscoveryResolvesOnlyTemplatesAndOfferings(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/orders", svcYAML("orders", "1.0.0"), "")
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	e.pushAt(t, "acme/postgresql", offeringYAML("postgresql", "16.0.0"), IndexNone)
	coll := e.discover(t, e.svc, FleetOptions{})
	if got := discoveredNames(coll); !slices.Equal(got, []string{"go-http-postgres@1.0.0", "postgresql@16.0.0"}) || coll.State.Status != fleet.SourceAvailable {
		t.Fatalf("%v %+v %+v", got, coll.State, coll.Limitations)
	}
	if n := e.ir.count("GET", "/acme/orders/blobs/"); n != 0 {
		t.Fatalf("the service was pulled: %d blob reads", n)
	}
}

func TestDiscoveredOfferingCounts(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/postgresql", offeringYAML("postgresql", "16.0.0"), "")
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	consumer := writeIdxBundle(t, "pactoVersion: \"2.4\"\nservice:\n  name: billing\n  version: 1.0.0\n  owner: {team: billing}\n"+
		"dependencies:\n  - ref: oci://"+e.prefix()+"/postgresql\n    compatibility: \"^16.0.0\"\n")
	snap, err := e.svc.Fleet(t.Context(), FleetOptions{LocalRoots: []string{consumer}, Discover: []string{e.prefix()}})
	if err != nil {
		t.Fatal(err)
	}
	offering, tpl := snap.Services[e.key("postgresql")], snap.Services[e.key("go-http-postgres")]
	if !targetlessDeclared(offering) || tpl == nil || !tpl.Template {
		t.Fatalf("offering %+v, template %+v", offering, tpl)
	}
	q := fleet.NewQuery(snap)
	if sum := q.Overview().Summary; sum.Services != 2 || sum.GoldenPaths != 1 {
		t.Fatalf("overview %+v", sum)
	}
	if dep := relationshipTo(snap, e.key("postgresql")); dep == nil || !dep.Resolved || dep.RangeStatus != "satisfied" || dep.Provenance != fleet.ProvenanceDeclared {
		t.Fatalf("dependency %+v", dep)
	}
	if data, paths := ownedServices(t, q, "team:data"), ownedServices(t, q, "team:paths"); data != 1 || paths != 0 {
		t.Fatalf("owners count %d offering and %d golden path services", data, paths)
	}
	if it := attentionOn(t, q, e.key("go-http-postgres")); it != nil {
		t.Fatalf("the golden path raised %+v", it)
	}
}

// targetlessDeclared reports whether s is a declared service, no golden
// path, that runs nowhere and so is never compliant.
func targetlessDeclared(s *fleet.ServiceRecord) bool {
	return s != nil && !s.Template && len(s.Targets) == 0 && s.Status != fleet.StatusCompliant
}

// ownedServices counts the services, golden paths left out, owner owns in q.
func ownedServices(t *testing.T, q *fleet.Query, owner string) int {
	t.Helper()
	list, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindService}, OwnerKey: owner, Template: fleet.TemplateExclude})
	if err != nil {
		t.Fatal(err)
	}
	return list.Total
}

// relationshipTo is the relationship of snap that resolves to k.
func relationshipTo(snap *fleet.FleetSnapshot, k fleet.ServiceKey) *fleet.Relationship {
	for i, r := range snap.Relationships {
		if r.ToService == k {
			return &snap.Relationships[i]
		}
	}
	return nil
}

// attentionOn is the first attention item q raises about k.
func attentionOn(t *testing.T, q *fleet.Query, k fleet.ServiceKey) *fleet.AttentionItem {
	t.Helper()
	att, err := q.Attention(fleet.AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for i, it := range att.Items {
		if it.Service == string(k) {
			return &att.Items[i]
		}
	}
	return nil
}

func TestDiscoveryClassifiesUntypedBundles(t *testing.T) {
	e := newIdxEnv(t)
	old, _ := oci.LegacyBundleImage(e.bundleOf(t, "pactoVersion: \"2.3\"\nservice: {name: orders, version: 1.0.0}\n"))
	e.writeImage(t, "acme/orders", "1.0.0", old)
	tpl, _ := oci.LegacyBundleImage(e.bundleOf(t, tplYAML("go-http-postgres", "1.0.0")))
	e.writeImage(t, "acme/go-http-postgres", "1.0.0", tpl)
	l := e.svc.registryListing(t.Context(), e.prefix())
	if len(l.refs) != 1 || !strings.Contains(l.refs[0], "/acme/go-http-postgres@") || l.unreadable != 0 {
		t.Fatalf("%+v", l)
	}
	// The 2.3 service: its manifest and its config, never its layer.
	if n := e.ir.count("GET", "/acme/orders/blobs/"); n != 1 {
		t.Fatalf("%d blob reads of the 2.3 service", n)
	}
	if got := discoveredNames(e.discover(t, e.svc, FleetOptions{})); !slices.Equal(got, []string{"go-http-postgres@1.0.0"}) {
		t.Fatal(got)
	}
}

func TestDiscoveryDropsMislabelledService(t *testing.T) {
	e := newIdxEnv(t)
	img, _ := oci.BundleImage(e.bundleOf(t, svcYAML("orders", "1.0.0")))
	e.writeImage(t, "acme/orders", "1.0.0", mutate.Annotations(img, map[string]string{oci.AnnotationKind: oci.KindTemplate}).(v1.Image))
	coll := e.discover(t, e.svc, FleetOptions{RegistryProbe: probeAll})
	if len(coll.Revisions) != 0 || !strings.HasPrefix(limMessage(coll.Limitations, fleet.LimitationRegistryListingEntryUnreadable), "1 bundles") {
		t.Fatalf("%v %+v", discoveredNames(coll), coll.Limitations)
	}
	for _, p := range coll.Providers {
		if slices.Contains(p.Services, e.key("orders")) {
			t.Fatalf("row %+v names the dropped service", p)
		}
	}
}

func TestDiscoveryRepositoryNameBindsService(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	// A higher version under another repository, listed by _catalog and by
	// the index entry its push writes.
	e.pushAt(t, "acme/anything", tplYAML("go-http-postgres", "2.0.0"), "")
	coll := e.discover(t, e.svc, FleetOptions{})
	if got := discoveredNames(coll); !slices.Equal(got, []string{"go-http-postgres@1.0.0"}) ||
		limMessage(coll.Limitations, fleet.LimitationRegistryListingEntryUnreadable) == "" {
		t.Fatalf("%v %+v", got, coll.Limitations)
	}
	snap, err := e.svc.Fleet(t.Context(), FleetOptions{Discover: []string{e.prefix()}})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := fleet.NewQuery(snap).Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindService}, Template: fleet.TemplateOnly})
	if list.Total != 1 || len(snap.Revisions) != 1 {
		t.Fatalf("%+v %d revisions", list.Entities, len(snap.Revisions))
	}
}

func TestForgedIndexEntryDoesNotHideRealPath(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	evil := e.pushAt(t, "acme/evil", tplYAML("go-http-postgres", "9.0.0"), IndexNone)
	e.forgeEntry(t, orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(e.key("go-http-postgres")), Name: "go-http-postgres",
		Version: "9.0.0", Bundle: e.ir.host + "/acme/evil@" + evil.Digest})
	if got := discoveredNames(e.discover(t, e.svc, FleetOptions{})); !slices.Equal(got, []string{"go-http-postgres@1.0.0"}) {
		t.Fatal(got)
	}
	if e.ir.count("HEAD", "/acme/go-http-postgres/manifests/1.0.0") == 0 {
		t.Fatal("the walk never visited the real repository")
	}
}

// TestDiscoveryReadsPastALaggingIndex: a version pushed with no index entry
// (or whose entry write failed) is still found by _catalog in a repository
// the index already names, so a stale entry never reads as the newest path.
func TestDiscoveryReadsPastALaggingIndex(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.2.0"), "")
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.3.0"), IndexNone)
	coll := e.discover(t, e.svc, FleetOptions{})
	if got := discoveredNames(coll); !slices.Contains(got, "go-http-postgres@1.3.0") || coll.State.Status != fleet.SourceAvailable {
		t.Fatalf("%v %+v %+v", got, coll.State, coll.Limitations)
	}
}

// TestDiscoveredForgeryNeverJoinsOwnerHistory: the owner pass sees only the
// revisions whose name is their repository's, so a bundle pushed to another
// repository under the prefix cannot steer the verdict of the service it
// names before keepDiscovered drops it.
func TestDiscoveredForgeryNeverJoinsOwnerHistory(t *testing.T) {
	var seen []string
	pass := discoveredOwnerRights(func(_ context.Context, _ string, revs []fleet.ContractRevision) {
		for _, r := range revs {
			seen = append(seen, r.Version)
		}
	})
	revs := []fleet.ContractRevision{
		{Service: "go-http-postgres", Version: "0.0.1", ResolvedRef: "ghcr.io/acme/evil@sha256:" + strings.Repeat("e", 64)},
		{Service: "go-http-postgres", Version: "1.2.0", ResolvedRef: "oci://ghcr.io/acme/go-http-postgres@sha256:" + strings.Repeat("a", 64)},
	}
	pass(t.Context(), "registry", revs)
	if !slices.Equal(seen, []string{"1.2.0"}) || len(revs) != 2 {
		t.Fatalf("owner pass saw %v", seen)
	}
	if discoveredOwnerRights(nil) != nil {
		t.Fatal("no owner pass became one")
	}
}

func TestDiscoveryUsesOnlyConfirmedEntries(t *testing.T) {
	e := newIdxEnv(t)
	e.hook(catalog404)
	elsewhere := newIndexReg(t)
	y := e.pushAt(t, "acme/y", tplYAML("y", "1.0.0"), IndexNone)
	z := e.pushAt(t, "other/z", tplYAML("z", "1.0.0"), IndexNone)
	entry := func(n, bundle string) orgindex.Entry {
		return orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(e.key(n)), Name: n, Version: "1.0.0", Bundle: bundle}
	}
	e.forgeEntry(t, entry("a", elsewhere.host+"/acme/a@"+y.Digest))                  // another host
	e.forgeEntry(t, entry("z", e.ir.host+"/other/z@"+z.Digest))                      // outside the index root
	e.forgeEntry(t, entry("b", e.ir.host+"/acme/y@sha256:"+strings.Repeat("b", 64))) // unconfirmed
	e.forgeEntry(t, entry("x", e.ir.host+"/acme/y@"+y.Digest))                       // names x, points at y
	coll := e.discover(t, e.svc, FleetOptions{})
	if got := discoveredNames(coll); !slices.Equal(got, []string{"y@1.0.0"}) || coll.Revisions[0].RequestedRef != "oci://"+e.ir.host+"/acme/y@"+y.Digest {
		t.Fatalf("%v %+v", got, coll.Revisions)
	}
}

func TestCatalogOutsidePrefixDropped(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), IndexNone)
	e.pushAt(t, "acmeco/node-worker", tplYAML("node-worker", "1.0.0"), IndexNone)
	e.pushAt(t, "other/node-worker", tplYAML("node-worker", "1.0.0"), IndexNone)
	if got := discoveredNames(e.discover(t, e.svc, FleetOptions{})); !slices.Equal(got, []string{"go-http-postgres@1.0.0"}) {
		t.Fatal(got)
	}
	if n := e.ir.count("HEAD", "/acmeco/") + e.ir.count("HEAD", "/other/"); n != 0 {
		t.Fatalf("%d requests outside the prefix", n)
	}
}

func TestCatalogSkipsPactoOwnedRepositories(t *testing.T) {
	e := newIdxEnv(t)
	for _, repo := range []string{"acme/pacto-inferred/stripe", "acme/team/pacto-inferred/x", "acme/pacto-evidence", "acme/pacto-targets"} {
		e.pushAt(t, repo, offeringYAML("stripe", "1.0.0"), IndexNone)
	}
	coll := e.discover(t, e.svc, FleetOptions{})
	if len(coll.Revisions) != 0 || coll.State.Status != fleet.SourceAvailable {
		t.Fatalf("%v %+v", discoveredNames(coll), coll.Limitations)
	}
	if n := e.ir.count("GET", "/tags/list") - e.ir.count("GET", "/pacto-index/tags/list"); n != 0 {
		t.Fatalf("%d tag listings of installation repositories", n)
	}
}

func TestCatalogBoundTruncates(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/a-path", tplYAML("a-path", "1.0.0"), IndexNone)
	e.pushAt(t, "acme/b-path", tplYAML("b-path", "1.0.0"), IndexNone)
	setVar(t, &indexMaxServices, 1)
	coll := e.discover(t, e.svc, FleetOptions{})
	if got := discoveredNames(coll); !slices.Equal(got, []string{"a-path@1.0.0"}) || limMessage(coll.Limitations, fleet.LimitationRegistryListingTruncated) == "" {
		t.Fatalf("%v %+v", got, coll.Limitations)
	}
}

func TestDiscoveryTimeoutIsTruncatedNotEmpty(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), IndexNone)
	setVar(t, &indexReadTimeout, 200*time.Millisecond)
	e.hook(func(_ http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/acme/go-http-postgres/") {
			time.Sleep(300 * time.Millisecond)
		}
		return false
	})
	coll := e.discover(t, e.svc, FleetOptions{})
	if coll.State.Status != fleet.SourcePartial || limMessage(coll.Limitations, fleet.LimitationRegistryListingTruncated) == "" ||
		limMessage(coll.Limitations, fleet.LimitationRegistryListingEntryUnreadable) != "" {
		t.Fatalf("%+v %+v", coll.State, coll.Limitations)
	}
}

func TestDiscoveryFetchBudget(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/typed", tplYAML("typed", "1.0.0"), IndexNone)
	old, _ := oci.LegacyBundleImage(e.bundleOf(t, "pactoVersion: \"2.3\"\nservice: {name: old, version: 1.0.0}\n"))
	e.writeImage(t, "acme/old", "1.0.0", old)
	untyped, _ := oci.LegacyBundleImage(e.bundleOf(t, tplYAML("untyped", "1.0.0")))
	e.writeImage(t, "acme/untyped", "1.0.0", untyped)
	if l := e.svc.registryListing(t.Context(), e.prefix()); len(l.refs) != 2 {
		t.Fatalf("%+v", l)
	}
	for repo, want := range map[string][2]int{"typed": {1, 0}, "old": {1, 1}, "untyped": {2, 2}} {
		// The untyped 2.4 bundle is pulled: its manifest again and its layer.
		if m, b := e.ir.count("GET", "/acme/"+repo+"/manifests/"), e.ir.count("GET", "/acme/"+repo+"/blobs/"); m != want[0] || b != want[1] {
			t.Errorf("%s: %d manifest reads, %d blob reads", repo, m, b)
		}
	}
}

func TestDiscoveryRefusesOversizedDescriptorBeforeFetch(t *testing.T) {
	e := newIdxEnv(t)
	e.pushRaw(t, "acme/big-manifest", "1.0.0", oci.BundleArtifactType, map[string]string{"pad": strings.Repeat("a", oci.MaxBundleManifestBytes)},
		labels(map[string]string{"io.pacto.name": "big-manifest"}))
	e.pushRaw(t, "acme/big-config", "1.0.0", "", nil,
		labels(map[string]string{"io.pacto.name": "big-config", "pad": strings.Repeat("a", oci.MaxBundleConfigBytes)}))
	coll := e.discover(t, e.svc, FleetOptions{})
	if !strings.HasPrefix(limMessage(coll.Limitations, fleet.LimitationRegistryListingEntryUnreadable), "2 bundles") {
		t.Fatalf("%+v", coll.Limitations)
	}
	if m, b := e.ir.count("GET", "/acme/big-manifest/manifests/"), e.ir.count("GET", "/acme/big-config/blobs/"); m != 0 || b != 0 {
		t.Fatalf("%d manifest reads, %d config reads", m, b)
	}
}

func TestDiscoveryCountsUnreadableCandidates(t *testing.T) {
	cfg := func(version string) []byte {
		return labels(map[string]string{"io.pacto.name": "x", "io.pacto.pactoVersion": version})
	}
	for _, c := range []struct {
		name       string
		setup      func(t *testing.T, e *idxEnv)
		unreadable int
	}{
		{"no version tag", func(t *testing.T, e *idxEnv) {
			img, _ := random.Image(16, 1)
			e.writeImage(t, "acme/img", "latest", img)
		}, 0},
		{"an image", func(t *testing.T, e *idxEnv) {
			img, _ := random.Image(16, 1)
			e.writeImage(t, "acme/img", "1.0.0", img)
		}, 0},
		{"an image index", func(t *testing.T, e *idxEnv) {
			idx, _ := random.Index(16, 1, 1)
			ref, _ := name.ParseReference(e.ir.host+"/acme/idx:1.0.0", name.Insecure)
			if err := ggcrremote.WriteIndex(ref, idx); err != nil {
				t.Fatal(err)
			}
		}, 0},
		{"another artifact", func(t *testing.T, e *idxEnv) { e.pushRaw(t, "acme/x", "1.0.0", "application/x-other", nil, cfg("2.4")) }, 0},
		{"no bundle label", func(t *testing.T, e *idxEnv) { e.pushRaw(t, "acme/x", "1.0.0", "", nil, labels(nil)) }, 0},
		{"an unreadable label", func(t *testing.T, e *idxEnv) { e.pushRaw(t, "acme/x", "1.0.0", "", nil, cfg("x")) }, 1},
		{"a tag listing that fails", func(t *testing.T, e *idxEnv) {
			e.pushRaw(t, "acme/x", "1.0.0", "", nil, cfg("2.3"))
			e.hook(answer(http.StatusBadRequest, http.MethodGet, "/acme/x/tags/list"))
		}, 1},
		{"a manifest that does not resolve", func(t *testing.T, e *idxEnv) {
			e.pushRaw(t, "acme/x", "1.0.0", "", nil, cfg("2.3"))
			e.hook(answer(http.StatusBadRequest, http.MethodHead, "/acme/x/manifests/"))
		}, 1},
		{"a manifest that does not read", func(t *testing.T, e *idxEnv) {
			e.pushRaw(t, "acme/x", "1.0.0", "", nil, cfg("2.3"))
			e.hook(answer(http.StatusBadRequest, http.MethodGet, "/acme/x/manifests/"))
		}, 1},
		{"a config that does not read", func(t *testing.T, e *idxEnv) {
			e.pushRaw(t, "acme/x", "1.0.0", "", nil, cfg("2.3"))
			e.hook(answer(http.StatusBadRequest, http.MethodGet, "/acme/x/blobs/"))
		}, 1},
		{"an untyped bundle that does not pull", func(t *testing.T, e *idxEnv) { e.pushRaw(t, "acme/x", "1.0.0", "", nil, cfg("2.4")) }, 1},
		{"a repository that does not open", func(t *testing.T, e *idxEnv) {
			e.pushRaw(t, "acme/x", "1.0.0", "", nil, cfg("2.3"))
			e.svc.sys.refusalRepo = func(p string) (*remote.Repository, error) {
				if strings.HasSuffix(p, "/acme/x") {
					return nil, errors.New("closed")
				}
				return anonRepo(p)
			}
		}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newIdxEnv(t)
			c.setup(t, e)
			l := e.svc.registryListing(t.Context(), e.prefix())
			if len(l.refs) != 0 || l.unreadable != c.unreadable {
				t.Fatalf("%+v", l)
			}
		})
	}
}

func TestDiscoveryLimitationsNameNoRepository(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	e.pushAt(t, "acme/broken", tplYAML("broken", "1.0.0"), "")
	e.pushAt(t, "acme/anything", tplYAML("go-http-postgres", "2.0.0"), "")
	img, _ := oci.BundleImage(e.bundleOf(t, svcYAML("orders", "1.0.0")))
	e.writeImage(t, "acme/orders", "1.0.0", mutate.Annotations(img, map[string]string{oci.AnnotationKind: oci.KindTemplate}).(v1.Image))
	e.forgeEntry(t, orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(e.key("x")), Name: "x", Version: "1.0.0",
		Bundle: e.ir.host + "/acme/x@sha256:" + strings.Repeat("a", 64)})
	// The broken template classifies from its manifest; its pull fails.
	e.hook(either(catalog404, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/acme/broken/blobs/") {
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		return true
	}))
	coll := e.discover(t, e.svc, FleetOptions{})
	if !strings.HasPrefix(limMessage(coll.Limitations, fleet.LimitationRegistryListingEntryUnreadable), "3 bundles") {
		t.Fatalf("%+v", coll.Limitations)
	}
	idx := e.prefix() + "/pacto-index"
	for _, l := range slices.Concat(coll.Limitations, coll.Notes) {
		msg := strings.ReplaceAll(l.Message, idx, "")
		if strings.Contains(msg, e.prefix()+"/") || strings.Contains(msg, "sha256") {
			t.Errorf("%s names a repository or a digest: %s", l.Code, l.Message)
		}
	}
}

func TestDiscoveryListingMemoized(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	e.pushAt(t, "acme/node-worker", tplYAML("node-worker", "0.4.0"), IndexNone)
	now := time.Now()
	e.svc.sys.now = func() time.Time { return now }
	fo := FleetOptions{Discover: []string{e.prefix()}}
	for range 2 {
		if _, err := e.svc.Fleet(t.Context(), fo); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.ir.count("GET", "/v2/_catalog"); n != 1 {
		t.Fatalf("%d catalog listings", n)
	}
	// Past the TTL the listing is read again; the kinds stay known.
	now = now.Add(indexListingTTL)
	e.ir.reset()
	if l := e.svc.registryListing(t.Context(), e.prefix()); len(l.refs) != 2 || e.ir.count("GET", "/manifests/") != 0 || e.ir.count("GET", "/v2/_catalog") != 1 {
		t.Fatalf("%+v, requests %v", l, e.ir.reqs)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	coll, _ := (&registrySource{id: "registry", prefix: e.prefix(), svc: e.svc}).Collect(ctx)
	if coll.State.Status != fleet.SourceUnavailable || coll.State.Error == nil {
		t.Fatalf("%+v", coll.State)
	}
}

func TestTruncatedWalkIsMemoized(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/a-path", tplYAML("a-path", "1.0.0"), IndexNone)
	e.pushAt(t, "acme/b-path", tplYAML("b-path", "1.0.0"), IndexNone)
	setVar(t, &indexMaxServices, 1)
	first := time.Now()
	e.svc.sys.now = func() time.Time { return first }
	e.discover(t, e.svc, FleetOptions{})
	e.svc.sys.now = func() time.Time { return first.Add(time.Second) }
	e.ir.reset()
	coll := e.discover(t, e.svc, FleetOptions{})
	if e.ir.count("GET", "/v2/_catalog") != 0 || limMessage(coll.Limitations, fleet.LimitationRegistryListingTruncated) == "" || !coll.State.ObservedAt.Equal(first) {
		t.Fatalf("%v %+v %+v", e.ir.reqs, coll.State, coll.Limitations)
	}
	// A listing the caller cancelled is never kept, though it reads truncated.
	e.svc.sys.now = func() time.Time { return first.Add(indexListingTTL) }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if l := e.svc.registryListing(ctx, e.prefix()); l.index.outcome != listTruncated && l.catalog.outcome != listTruncated && !l.cut {
		t.Fatalf("cancelled listing %+v", l)
	}
	e.ir.reset()
	e.svc.registryListing(t.Context(), e.prefix())
	if e.ir.count("GET", "/v2/_catalog") == 0 {
		t.Fatal("a cancelled listing was reused")
	}
	// A failed walk is never kept.
	e.svc.registryListings.listings = nil
	e.hook(answer(http.StatusBadRequest, http.MethodGet, "/v2/_catalog"))
	e.svc.sys.now = func() time.Time { return first.Add(indexListingTTL) }
	e.discover(t, e.svc, FleetOptions{})
	e.svc.sys.now = func() time.Time { return first.Add(indexListingTTL + time.Second) }
	e.ir.reset()
	e.discover(t, e.svc, FleetOptions{})
	if e.ir.count("GET", "/v2/_catalog") != 1 {
		t.Fatal("a failed walk was reused")
	}
}

func TestDiscoveryAndRepositoriesKeyToOneRevision(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	snap, err := e.svc.Fleet(t.Context(), FleetOptions{OCIRefs: []string{"oci://" + e.prefix() + "/go-http-postgres:1.0.0"}, Discover: []string{e.prefix()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Revisions) != 1 || len(snap.Services[e.key("go-http-postgres")].Sources) != 2 {
		t.Fatalf("%d revisions, %+v", len(snap.Revisions), snap.Services)
	}
}

func TestDiscoveryAndWholeIndexShareOneListing(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	snap, err := e.svc.Fleet(t.Context(), FleetOptions{ReadIndex: e.index(), Discover: []string{e.prefix()}})
	if err != nil {
		t.Fatal(err)
	}
	if e.listings() != 1 || len(snap.Revisions) != 1 || !slices.Equal(snap.Services[e.key("go-http-postgres")].Sources, []string{"registry", "registry-index"}) {
		t.Fatalf("%d listings, %d revisions, %+v", e.listings(), len(snap.Revisions), snap.Services)
	}
}

func TestNoDiscoveryNoSource(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	snap, err := e.svc.Fleet(t.Context(), FleetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sources) != 0 || e.ir.count("GET", "/v2/_catalog") != 0 {
		t.Fatalf("%+v", snap.Sources)
	}
	noStore := &Service{}
	snap, err = noStore.Fleet(t.Context(), FleetOptions{Discover: []string{e.prefix(), e.ir.host + "/other"}})
	if err != nil {
		t.Fatal(err)
	}
	if st := sourceOf(t, snap, "registry-2"); st.Status != fleet.SourceUnavailable || st.Kind != fleet.SourceKindRegistryListing {
		t.Fatalf("%+v", snap.Sources)
	}
}

func TestDiscoveredTemplateIsGoldenPath(t *testing.T) {
	e := newIdxEnv(t)
	e.pushAt(t, "acme/go-http-postgres", tplYAML("go-http-postgres", "1.0.0"), "")
	snap, err := e.svc.Fleet(t.Context(), FleetOptions{Discover: []string{e.prefix()}})
	if err != nil {
		t.Fatal(err)
	}
	list, err := fleet.NewQuery(snap).Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindService}, Template: fleet.TemplateOnly})
	if err != nil || list.Total != 1 || !snap.Services[e.key("go-http-postgres")].Template {
		t.Fatalf("%v %+v", err, list)
	}
}

func TestDiscoverReadersOfOCIRefs(t *testing.T) {
	if !readsGHCR(FleetOptions{Discover: []string{"ghcr.io/acme"}}) || !readsGHCR(FleetOptions{Discover: []string{"oci://GHCR.io"}}) ||
		readsGHCR(FleetOptions{Discover: []string{"quay.io/acme"}}) {
		t.Fatal("readsGHCR ignores Discover")
	}
	pc := newProvenanceChecker(descRegistry{})
	n := trustingIn(pc, Installation{}, nil)
	NewService(fakeFleetStore{}, nil).declarationSigners(t.Context(), FleetOptions{Provenance: pc, Discover: []string{"ghcr.io/acme"}})
	if *n != 1 {
		t.Fatal("the signer policy was not read with Discover alone")
	}
}

// discoverOther is a discovery prefix on e's host no impact reads: any
// _catalog request is a discovery read.
func (e *idxEnv) discoverOther() (FleetOptions, func() int) {
	return FleetOptions{Discover: []string{e.ir.host + "/other"}}, func() int {
		return e.ir.count("GET", "/v2/_catalog") + e.ir.count("GET", "/other/")
	}
}

func TestImpactIgnoresDiscover(t *testing.T) {
	e := newIdxEnv(t)
	e.seed(t)
	e.hook(catalog404)
	without := e.impact(t, FleetOptions{})
	fo, reads := e.discoverOther()
	with := e.impact(t, fo)
	if with.Completeness != without.Completeness || reads() != 0 {
		t.Fatalf("%s vs %s, %d discovery reads", with.Completeness, without.Completeness, reads())
	}
}

// TestImpactOnDiscoverySnapshotStaysComplete: a dashboard or MCP server
// started with discovery analyses its own snapshot, whose registry source
// reads partial on a registry with no _catalog; the impact answer is as
// complete as it is without discovery.
func TestImpactOnDiscoverySnapshotStaysComplete(t *testing.T) {
	e := newIdxEnv(t)
	e.seed(t)
	e.hook(catalog404)
	snapOf := func(fo FleetOptions) *fleet.FleetSnapshot {
		snap, err := e.svc.Fleet(t.Context(), fo)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	refs := []string{"oci://" + e.prefix() + "/ledger:1.0.0", "oci://" + e.prefix() + "/orders:1.0.0", "oci://" + e.prefix() + "/billing:1.0.0"}
	without := e.onSnapshot(t, snapOf(FleetOptions{OCIRefs: refs}), false)
	snap := snapOf(FleetOptions{OCIRefs: refs, Discover: []string{e.prefix()}})
	if sourceOf(t, snap, "registry").Status != fleet.SourcePartial || snap.Completeness != fleet.CompletenessPartial {
		t.Fatalf("discovery did not read partial: %s %+v", snap.Completeness, snap.Sources)
	}
	if with := e.onSnapshot(t, snap, false); with.Completeness != without.Completeness || with.Completeness == fleet.CompletenessPartial {
		t.Fatalf("%s vs %s %+v", with.Completeness, without.Completeness, with.Limitations)
	}
}

func TestImpactEventIgnoresDiscover(t *testing.T) {
	e := newIdxEnv(t)
	e.seed(t)
	fo, reads := e.discoverOther()
	if _, err := e.svc.ImpactEvent(t.Context(), ImpactEventOptions{Event: impact.Event{Kind: impact.EventUnavailable, Service: "ledger"}, Fleet: fo}); err != nil || reads() != 0 {
		t.Fatalf("%v, %d discovery reads", err, reads())
	}
}

func TestCIIgnoresDiscover(t *testing.T) {
	e := proposalEnv(t)
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: map[string]*contract.Bundle{},
		Fleet: FleetOptions{Discover: []string{e.host + "/acme"}}})
	if err != nil || p.Snapshot.AsOf.IsZero() {
		t.Fatalf("%v %+v", err, p.Snapshot)
	}
	for _, s := range p.Snapshot.Sources {
		if strings.HasPrefix(s.Name, "registry") {
			t.Fatalf("the plan discovered: %+v", p.Snapshot.Sources)
		}
	}
}

func TestPublishInferredIgnoresDiscover(t *testing.T) {
	host, reg := publishRegistry(t)
	ir := newIndexReg(t)
	dir := t.TempDir()
	writeCIFile(t, dir, "checkout/pacto.yaml", "pactoVersion: \"2.0\"\nservice:\n  name: checkout\n  version: \"1.0.0\"\ndependencies:\n  - name: stripe\n    external: true\n")
	svc := &Service{Version: "4.7.0", BundleStore: oci.NewClient(anonymous, oci.WithInsecureRegistries(ir.host)),
		sys: sysDeps{publishRegistry: func() publish.Registry { return reg }, refusalRepo: anonRepo}}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	rep, err := svc.PublishInferred(t.Context(), PublishInferredOptions{Fleet: FleetOptions{LocalRoots: []string{dir}, Discover: []string{ir.host + "/acme"}},
		Registry: "oci://" + host + "/demo", Signer: edSigner(priv), Verify: keyPolicy(pub)})
	if err != nil || len(rep.Published) != 1 || len(ir.reqs) != 0 {
		t.Fatalf("report %+v, %v, %d discovery requests", rep, err, len(ir.reqs))
	}
}

func TestNewestTag(t *testing.T) {
	for want, tags := range map[string][]string{
		"1.2.0":      {"latest", "1.0.0", "1.2.0", "2.0.0-rc.1"},
		"2.0.0-rc.2": {"2.0.0-rc.1", "2.0.0-rc.2", "main"},
		"":           {"latest"},
	} {
		if got := newestTag(tags); got != want {
			t.Errorf("%v: %q", tags, got)
		}
	}
}

func TestKindsBounded(t *testing.T) {
	svc := &Service{}
	for i := range maxKinds + 1 {
		svc.keepKind(fmt.Sprint(i), oci.KindTemplate)
	}
	if k, ok := svc.cachedKind(fmt.Sprint(maxKinds)); len(svc.registryListings.kinds) != 1 || !ok || k != oci.KindTemplate {
		t.Fatalf("%d kinds", len(svc.registryListings.kinds))
	}
}
