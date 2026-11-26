/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package offeringpublish

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/xrdoffering"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

var now = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

const clusterA = "6f1c0000-0000-4000-8000-000000000001"

var (
	gvrXRDv2 = schema.GroupVersionResource{Group: crossplaneGroup, Version: "v2", Resource: "compositeresourcedefinitions"}
	gvrXRDv1 = schema.GroupVersionResource{Group: crossplaneGroup, Version: "v1", Resource: "compositeresourcedefinitions"}
	gvrComp  = schema.GroupVersionResource{Group: crossplaneGroup, Version: "v1", Resource: "compositions"}
)

// testHost is an in-process OCI registry.
func testHost(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// xrd is an XRD at version (v1 or v2) of kind in group storage.acme.io, at
// generation gen, whose spec schema has a bounded size.
func xrd(version, kind string, gen int64, maxSize int64) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": crossplaneGroup + "/" + version, "kind": "CompositeResourceDefinition",
		"metadata": map[string]any{"name": strings.ToLower(kind) + "s.storage.acme.io", "generation": gen},
		"spec": map[string]any{"group": "storage.acme.io", "names": map[string]any{"kind": kind, "plural": strings.ToLower(kind) + "s"},
			"versions": []any{map[string]any{"name": "v1", "served": true, "referenceable": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{"type": "object", "properties": map[string]any{
					"spec": map[string]any{"type": "object", "properties": map[string]any{
						"size": map[string]any{"type": "integer", "maximum": maxSize}}}}}}}}},
	}}
	return u
}

// composition selects kind in storage.acme.io.
func composition(n, kind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": crossplaneGroup + "/v1", "kind": "Composition", "metadata": map[string]any{"name": n},
		"spec": map[string]any{"compositeTypeRef": map[string]any{"apiVersion": "storage.acme.io/v1", "kind": kind}},
	}}
}

func cluster(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		gvrXRDv2: "CompositeResourceDefinitionList", gvrXRDv1: "CompositeResourceDefinitionList", gvrComp: "CompositionList"}, objs...)
}

func serves(versions ...string) func(string) (bool, error) {
	return func(gv string) (bool, error) {
		return slices.Contains(versions, strings.TrimPrefix(gv, crossplaneGroup+"/")), nil
	}
}

// logs collects what a publisher logs.
type logs struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *logs) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

// publisher is a publisher of cluster id for host/acme over dyn.
func publisher(t *testing.T, host, id string, dyn *dynamicfake.FakeDynamicClient, served func(string) (bool, error)) (*Publisher, ed25519.PublicKey, *logs) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	l := &logs{}
	p := &Publisher{cfg: Config{Registry: host + "/acme", ClusterID: id, Key: key, Keychain: authn.DefaultKeychain, Dynamic: dyn,
		Served: served, Pacto: "6.5.0"}, now: func() time.Time { return now },
		log: funcr.New(func(prefix, args string) { l.mu.Lock(); defer l.mu.Unlock(); l.buf.WriteString(args + "\n") }, funcr.Options{})}
	return p, pub, l
}

func client() *publish.Client { return publish.NewClient(authn.DefaultKeychain) }

// offeringTags are repo's offering tags, sorted.
func offeringTags(t *testing.T, repo string) []string {
	t.Helper()
	got, err := client().Tags(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range got {
		if strings.Contains(g, "_0.0.0-inferred.") {
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return out
}

func TestOfferingPublisherPushesChangedXRD(t *testing.T) {
	host := testHost(t)
	p, pub, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500), composition("small", "XBucket")), serves("v2", "v1"))
	p.run(context.Background())
	repo := host + "/acme/pacto-inferred/crossplane-" + clusterA + "/services"
	if p.repository() != repo {
		t.Fatalf("repository = %s", p.repository())
	}
	if got := offeringTags(t, repo); !slices.Equal(got, []string{"xbucket_0.0.0-inferred.3.0"}) {
		t.Fatalf("tags = %v", got)
	}
	h, err := client().Head(context.Background(), repo+":xbucket_0.0.0-inferred.3.0")
	if err != nil || h.Annotations[publish.AnnotationCreated] != now.Format(time.RFC3339) || h.Annotations[oci.AnnotationKind] != oci.KindOffering {
		t.Fatalf("head = %+v, %v", h, err)
	}
	if err := publish.VerifyDigest(context.Background(), client(), publish.PolicyVerifier(signing.Policy{Key: pub}), repo, h.Digest); err != nil {
		t.Errorf("signature: %v", err)
	}
	b, err := client().Pull(context.Background(), repo+"@"+h.Digest)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || contract.ContributionRefusal(b, rec) != "" || b.Contract.Service.Offering != "xbucket" {
		t.Fatalf("bundle = %+v, %v", b.Contract.Service, err)
	}
	if p, ok := contract.ContributionRef(repo + ":x_1.0.0"); !ok || p != "crossplane-"+clusterA {
		t.Errorf("producer = %q, %v", p, ok)
	}
}

func TestOfferingPublisherUnchangedPushesNothing(t *testing.T) {
	host := testHost(t)
	p, _, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500)), serves("v2"))
	p.run(context.Background())
	p.run(context.Background())
	if got := offeringTags(t, p.repository()); len(got) != 1 {
		t.Fatalf("tags = %v", got)
	}
}

func TestOfferingPublisherRenewsAgingRecord(t *testing.T) {
	host := testHost(t)
	p, _, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500)), serves("v2"))
	p.run(context.Background())
	repo := p.repository()
	first, _ := client().Head(context.Background(), repo+":xbucket_0.0.0-inferred.3.0")
	later := now.Add(fleet.RecentEvidenceWindow)
	p.now = func() time.Time { return later }
	p.run(context.Background())
	if got := offeringTags(t, repo); len(got) != 1 {
		t.Fatalf("tags = %v", got)
	}
	h, _ := client().Head(context.Background(), repo+":xbucket_0.0.0-inferred.3.0")
	if h.Annotations[publish.AnnotationCreated] != later.Format(time.RFC3339) {
		t.Fatalf("created = %s, the aging record was not renewed", h.Annotations[publish.AnnotationCreated])
	}
	// The renewal names the first record, so an acceptance of it is not drift,
	// and a second renewal still names that one.
	if h.Annotations[contract.AnnotationRenews] != first.Digest || h.Digest == first.Digest {
		t.Fatalf("renewal %s names %q, want %s", h.Digest, h.Annotations[contract.AnnotationRenews], first.Digest)
	}
	p.now = func() time.Time { return later.Add(fleet.RecentEvidenceWindow) }
	p.run(context.Background())
	if h2, _ := client().Head(context.Background(), repo+":xbucket_0.0.0-inferred.3.0"); h2.Digest == h.Digest || h2.Annotations[contract.AnnotationRenews] != first.Digest {
		t.Fatalf("second renewal names %q, want %s", h2.Annotations[contract.AnnotationRenews], first.Digest)
	}
}

func TestOfferingPublisherCompositionChangeBumpsK(t *testing.T) {
	host := testHost(t)
	dyn := cluster(xrd("v2", "XBucket", 3, 500), composition("small", "XBucket"))
	p, _, _ := publisher(t, host, clusterA, dyn, serves("v2"))
	p.run(context.Background())
	if err := dyn.Tracker().Add(composition("large", "XBucket")); err != nil {
		t.Fatal(err)
	}
	p.run(context.Background())
	if got := offeringTags(t, p.repository()); !slices.Equal(got, []string{"xbucket_0.0.0-inferred.3.0", "xbucket_0.0.0-inferred.3.1"}) {
		t.Fatalf("tags = %v", got)
	}
}

func TestOfferingPublisherGenerationResetsK(t *testing.T) {
	host := testHost(t)
	dyn := cluster(xrd("v2", "XBucket", 3, 500), composition("small", "XBucket"))
	p, _, _ := publisher(t, host, clusterA, dyn, serves("v2"))
	p.run(context.Background())
	_ = dyn.Tracker().Add(composition("large", "XBucket"))
	p.run(context.Background())
	if err := dyn.Tracker().Update(gvrXRDv2, xrd("v2", "XBucket", 4, 100), ""); err != nil {
		t.Fatal(err)
	}
	p.run(context.Background())
	got := offeringTags(t, p.repository())
	if !slices.Contains(got, "xbucket_0.0.0-inferred.4.0") || len(got) != 3 {
		t.Fatalf("tags = %v", got)
	}
	// A recreated XRD whose generation went back goes above the newest.
	_ = dyn.Tracker().Update(gvrXRDv2, xrd("v2", "XBucket", 1, 100), "")
	p.run(context.Background())
	if got := offeringTags(t, p.repository()); !slices.Contains(got, "xbucket_0.0.0-inferred.4.1") {
		t.Fatalf("tags = %v", got)
	}
}

// pushForeign pushes o at version to repo signed by key, or unsigned when key is nil.
func pushForeign(t *testing.T, host, repo string, o xrdoffering.Offering, version string, key ed25519.PrivateKey) {
	t.Helper()
	other := &Publisher{cfg: Config{Key: key}, now: func() time.Time { return now }}
	if key == nil {
		_, other.cfg.Key, _ = ed25519.GenerateKey(rand.Reader)
		other.reg = unsigned{client()}
	} else {
		other.reg = client()
	}
	if err := other.push(context.Background(), other.reg, repo, o, version, ""); err != nil && key != nil {
		t.Fatal(err)
	}
}

// unsigned is a registry that refuses signature writes.
type unsigned struct{ publish.Registry }

func (unsigned) Referrers(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("no signatures here")
}

func TestOfferingPublisherSignsWithInstallationKey(t *testing.T) {
	for _, foreign := range []string{"other key", "unsigned"} {
		t.Run(foreign, func(t *testing.T) {
			host := testHost(t)
			x := xrd("v2", "XBucket", 3, 500)
			p, _, _ := publisher(t, host, clusterA, cluster(x), serves("v2"))
			o, _ := xrdoffering.Map(x, nil, "6.5.0")
			var key ed25519.PrivateKey
			if foreign == "other key" {
				_, key, _ = ed25519.GenerateKey(rand.Reader)
			}
			// The same bytes under the newest tag, signed by another key or
			// not at all: never compared, the next version goes above it.
			pushForeign(t, host, p.repository(), o, "0.0.0-inferred.3.0", key)
			p.run(context.Background())
			if got := offeringTags(t, p.repository()); !slices.Equal(got, []string{"xbucket_0.0.0-inferred.3.0", "xbucket_0.0.0-inferred.3.1"}) {
				t.Fatalf("tags = %v", got)
			}
		})
	}
}

func TestOfferingPublisherPushScope(t *testing.T) {
	host := testHost(t)
	p, _, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500), xrd("v2", "XQueue", 1, 10)), serves("v2"))
	p.run(context.Background())
	reg, _ := name.NewRegistry(host)
	repos, err := remote.Catalog(context.Background(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(repos, []string{"acme/pacto-inferred/crossplane-" + clusterA + "/services"}) {
		t.Fatalf("repositories written = %v", repos)
	}
}

func TestOfferingPublisherBoundsCharged(t *testing.T) {
	host := testHost(t)
	objs := make([]runtime.Object, 0, maxXRDs+maxCompositions+2)
	for i := range maxXRDs + 1 {
		objs = append(objs, xrd("v2", fmt.Sprintf("X%03d", i), 1, 5))
	}
	for i := range maxCompositions + 1 {
		objs = append(objs, composition(fmt.Sprintf("c%04d", i), "Xnone"))
	}
	p, _, _ := publisher(t, host, clusterA, cluster(objs...), serves("v2"))
	// Every write fails fast: the failure count is how many XRDs were handled.
	p.reg = faulty{client(), func(m, _ string) bool { return m == "write" }}
	p.run(context.Background())
	for _, want := range []string{"more than 200 XRDs", "more than 1000 Compositions", "200 offerings could not be published"} {
		if !strings.Contains(p.ad.Partial, want) {
			t.Errorf("partial = %q, want %q", p.ad.Partial, want)
		}
	}
	if p.ad.Observed != nil {
		t.Fatalf("a partial run observed %v", p.ad.Observed)
	}
}

func TestOfferingPublisherListErrorKeepsTags(t *testing.T) {
	for _, resource := range []string{"compositeresourcedefinitions", "compositions"} {
		t.Run(resource, func(t *testing.T) {
			host := testHost(t)
			dyn := cluster(xrd("v2", "XBucket", 3, 500))
			p, _, _ := publisher(t, host, clusterA, dyn, serves("v2"))
			p.run(context.Background())
			asOf := p.ad.AsOf
			_ = dyn.Tracker().Add(xrd("v2", "XQueue", 1, 10))
			dyn.PrependReactor("list", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("forbidden")
			})
			p.now = func() time.Time { return now.Add(time.Hour) }
			p.run(context.Background())
			if got := offeringTags(t, p.repository()); len(got) != 1 {
				t.Fatalf("tags = %v", got)
			}
			if !strings.HasPrefix(p.ad.Partial, "listing failed: ") || !slices.Equal(p.ad.Observed, []string{"xbucket"}) || p.ad.AsOf != asOf {
				t.Fatalf("advertisement = %+v", p.ad)
			}
		})
	}
}

func TestOfferingPublisherAPIUnservedTickPushesNothing(t *testing.T) {
	host := testHost(t)
	p, _, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500)), serves())
	p.run(context.Background())
	if got := offeringTags(t, p.repository()); len(got) != 0 {
		t.Fatalf("tags = %v", got)
	}
	if p.ad.Partial != notServed || p.ad.Producer != "crossplane-"+clusterA || p.ad.Repository != p.repository() {
		t.Fatalf("advertisement = %+v", p.ad)
	}
	p.cfg.Served = func(string) (bool, error) { return false, errors.New("connection refused") }
	p.run(context.Background())
	if !strings.HasPrefix(p.ad.Partial, "reading the API failed: ") {
		t.Fatalf("partial = %q", p.ad.Partial)
	}
}

func TestOfferingPublisherServedV1ListsV1(t *testing.T) {
	host := testHost(t)
	p, _, _ := publisher(t, host, clusterA, cluster(xrd("v1", "XBucket", 2, 500)), serves("v1"))
	p.run(context.Background())
	if got := offeringTags(t, p.repository()); !slices.Equal(got, []string{"xbucket_0.0.0-inferred.2.0"}) {
		t.Fatalf("tags = %v", got)
	}
}

func TestOfferingPublisherCrossplaneInstalledLaterPublishes(t *testing.T) {
	host := testHost(t)
	calls := 0
	p, _, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500)), func(gv string) (bool, error) {
		calls++
		return calls > 2, nil // the first tick asks v2 and v1, both unserved
	})
	ticks := make(chan time.Time)
	p.tick = func(d time.Duration) <-chan time.Time {
		if d != OfferingInterval {
			t.Errorf("interval = %v", d)
		}
		return ticks
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- p.Start(ctx) }()
	ticks <- now // received after the first run
	ticks <- now // received after the second run, which pushed
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := offeringTags(t, p.repository()); !slices.Equal(got, []string{"xbucket_0.0.0-inferred.3.0"}) {
		t.Fatalf("tags = %v", got)
	}
}

func TestOfferingPublisherStartStopsWithContext(t *testing.T) {
	ctrl.SetLogger(logr.Discard())
	p := &Publisher{cfg: Config{Served: serves()}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if p.ad.Partial != notServed || p.clock().IsZero() {
		t.Fatalf("advertisement = %+v", p.ad)
	}
}

func TestOfferingPublisherRecordsObserved(t *testing.T) {
	host := testHost(t)
	p, _, l := publisher(t, host, clusterA, cluster(xrd("v2", "XQueue", 1, 10), xrd("v2", "XBucket", 3, 500)), serves("v2"))
	p.run(context.Background())
	if !slices.Equal(p.ad.Observed, []string{"xbucket", "xqueue"}) || !p.ad.AsOf.Equal(now) || p.ad.Partial != "" ||
		p.ad.Producer != "crossplane-"+clusterA || p.ad.Repository != p.repository() {
		t.Fatalf("advertisement = %+v", p.ad)
	}
	if !strings.Contains(l.String(), `"observed"=2`) {
		t.Errorf("log = %s", l)
	}
}

func TestOfferingPublisherSkipsUnmappableAndDuplicateSlugs(t *testing.T) {
	host := testHost(t)
	dup := xrd("v2", "XBucket", 1, 5)
	dup.SetName("xbuckets.other.acme.io")
	bad := xrd("v2", "X_Bad", 1, 5)
	p, _, l := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500), dup, bad), serves("v2"))
	p.run(context.Background())
	if got := offeringTags(t, p.repository()); !slices.Equal(got, []string{"xbucket_0.0.0-inferred.1.0"}) {
		t.Fatalf("tags = %v", got)
	}
	// The XRD that lost its slug leaves the run partial; the unmappable one
	// is no offering, only counted.
	if p.ad.Observed != nil || p.ad.Partial != "1 XRDs share a slug with another and were left out" ||
		!strings.Contains(l.String(), "is another XRD's") || !strings.Contains(l.String(), "no DNS label") {
		t.Fatalf("advertisement = %+v\n%s", p.ad, l)
	}
}

func TestOfferingPublisherAdvertisesNothingWithoutCrossplane(t *testing.T) {
	host := testHost(t)
	p, _, _ := publisher(t, host, clusterA, cluster(), serves())
	p.run(context.Background())
	if ad := p.Advertisement(); ad != nil {
		t.Fatalf("advertisement = %+v, want none while Crossplane was never served", ad)
	}
}

func TestTwoInstallationsSharingRegistryDoNotCollide(t *testing.T) {
	host := testHost(t)
	const clusterB = "6f1c0000-0000-4000-8000-000000000002"
	a, pubA, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500)), serves("v2"))
	b, pubB, _ := publisher(t, host, clusterB, cluster(xrd("v2", "XBucket", 3, 500)), serves("v2"))
	a.run(context.Background())
	b.run(context.Background())
	a.run(context.Background())
	b.run(context.Background())
	if a.repository() == b.repository() {
		t.Fatal("two installations share one repository")
	}
	for _, c := range []struct {
		p        *Publisher
		own, oth ed25519.PublicKey
	}{{a, pubA, pubB}, {b, pubB, pubA}} {
		repo := c.p.repository()
		if got := offeringTags(t, repo); !slices.Equal(got, []string{"xbucket_0.0.0-inferred.3.0"}) {
			t.Fatalf("%s tags = %v", repo, got)
		}
		h, _ := client().Head(context.Background(), repo+":xbucket_0.0.0-inferred.3.0")
		if err := publish.VerifyDigest(context.Background(), client(), publish.PolicyVerifier(signing.Policy{Key: c.own}), repo, h.Digest); err != nil {
			t.Errorf("%s own key: %v", repo, err)
		}
		if err := publish.VerifyDigest(context.Background(), client(), publish.PolicyVerifier(signing.Policy{Key: c.oth}), repo, h.Digest); err == nil {
			t.Errorf("%s verifies with the other installation's key", repo)
		}
	}
}

// faulty fails the registry calls fail names.
type faulty struct {
	publish.Registry
	fail func(method, arg string) bool
}

var errInjected = errors.New("injected")

func (f faulty) Tags(ctx context.Context, repo string) ([]string, error) {
	if f.fail("tags", repo) {
		return nil, errInjected
	}
	return f.Registry.Tags(ctx, repo)
}

func (f faulty) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	if f.fail("head", ref) {
		return nil, errInjected
	}
	return f.Registry.Head(ctx, ref)
}

func (f faulty) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if f.fail("pull", ref) {
		return nil, errInjected
	}
	return f.Registry.Pull(ctx, ref)
}

func (f faulty) Write(ctx context.Context, repo string, a publish.Artifact) (string, error) {
	if f.fail("write", repo) {
		return "", errInjected
	}
	return f.Registry.Write(ctx, repo, a)
}

func (f faulty) Tag(ctx context.Context, repo, digest, tag string) error {
	if f.fail("tag", tag) {
		return errInjected
	}
	return f.Registry.Tag(ctx, repo, digest, tag)
}

func (f faulty) Mode(ctx context.Context, repo, digest string) (capabilities.ReferrersMode, error) {
	if f.fail("mode", digest) {
		return "", errInjected
	}
	return f.Registry.Mode(ctx, repo, digest)
}

func (f faulty) Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error) {
	if f.fail("referrers", digest) {
		return nil, errInjected
	}
	return f.Registry.Referrers(ctx, repo, digest, artifactType)
}

func TestOfferingPublisherRegistryFailures(t *testing.T) {
	for _, c := range []struct {
		name, method string
		first        bool // fail on the first run, before any tag exists
		partial      string
	}{
		{"tags", "tags", true, "reading "},
		{"write", "write", true, "1 offerings could not be published"},
		{"tag", "tag", true, "1 offerings could not be published"},
		{"sign", "mode", true, "1 offerings could not be published"},
		{"head", "head", false, "1 offerings could not be published"},
		{"verify", "referrers", false, "1 offerings could not be published"},
		{"pull", "pull", false, "1 offerings could not be published"},
	} {
		t.Run(c.name, func(t *testing.T) {
			host := testHost(t)
			p, _, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500)), serves("v2"))
			on := c.first
			p.reg = faulty{client(), func(m, _ string) bool { return on && m == c.method }}
			if !c.first {
				p.run(context.Background())
				on = true
			}
			before := len(offeringTags(t, p.repository()))
			p.run(context.Background())
			if !strings.HasPrefix(p.ad.Partial, c.partial) || strings.HasPrefix(c.partial, "1") != strings.Contains(p.ad.Partial, "offerings") {
				t.Fatalf("partial = %q", p.ad.Partial)
			}
			if c.method != "tag" && c.method != "mode" && len(offeringTags(t, p.repository())) != before {
				t.Errorf("a failed run pushed a tag")
			}
		})
	}
}

func TestOfferingPublisherScopedOperatorRunsNothing(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	for _, cfg := range []Config{
		{ClusterID: clusterA, Key: key},
		{Registry: "ghcr.io/acme", ClusterID: clusterA, Key: key, Watched: []string{"team-a"}},
	} {
		if p, err := ForOperator(cfg); p != nil || err != nil {
			t.Fatalf("ForOperator(%+v) = %v, %v", cfg, p, err)
		}
	}
}

func TestOfferingPublisherNeedsKeyAndClusterID(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	for want, cfg := range map[string]Config{
		"installation key": {Registry: "ghcr.io/acme", ClusterID: clusterA},
		"cluster id":       {Registry: "ghcr.io/acme", Key: key},
	} {
		if _, err := ForOperator(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
	}
}

func TestOfferingPublisherBuiltWhenAPIUnserved(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	p, err := ForOperator(Config{Registry: "ghcr.io/acme/", ClusterID: clusterA, Key: key,
		Served: func(string) (bool, error) { t.Fatal("ForOperator asked whether the API is served"); return false, nil }})
	if err != nil || p == nil {
		t.Fatalf("ForOperator = %v, %v", p, err)
	}
	ad := p.Advertisement()
	if ad.Producer != "crossplane-"+clusterA || ad.Repository != "ghcr.io/acme/pacto-inferred/crossplane-"+clusterA+"/services" ||
		ad.Partial != "the first run has not finished" || ad.Observed != nil || ad.AsOf != nil {
		t.Fatalf("advertisement = %+v", ad)
	}
}

func TestOfferingPublisherAdvertisementIsACopy(t *testing.T) {
	host := testHost(t)
	p, _, _ := publisher(t, host, clusterA, cluster(xrd("v2", "XBucket", 3, 500)), serves("v2"))
	p.run(context.Background())
	ad := p.Advertisement()
	if !slices.Equal(ad.Observed, []string{"xbucket"}) || ad.Partial != "" || !ad.AsOf.Equal(now) {
		t.Fatalf("advertisement = %+v", ad)
	}
	ad.Observed[0] = "changed"
	if p.ad.Observed[0] != "xbucket" {
		t.Fatal("the advertisement shares the publisher's slice")
	}
	p.cfg.Served = serves()
	p.run(context.Background())
	if ad := p.Advertisement(); ad.Partial != notServed || !slices.Equal(ad.Observed, []string{"xbucket"}) {
		t.Fatalf("advertisement = %+v", ad)
	}
}
