package deliveryrecord

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const joinUID = "9f1c4e2a"

// joinRepo is a pacto-targets repository in mode with the join stream of
// joinUID anchored.
func joinRepo(t *testing.T, mode capabilities.ReferrersMode) (*remote.Repository, string) {
	t.Helper()
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(delivery.ClusterJoinStream(joinUID))
	if err := ocireferrer.Push(t.Context(), repo, anchor); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(t.Context(), anchor.ManifestDesc, AnchorTag(delivery.ClusterJoinStream(joinUID))); err != nil {
		t.Fatal(err)
	}
	return repo, path
}

func pushJoin(t *testing.T, repo *remote.Repository, mode capabilities.ReferrersMode, bundle []byte, ann map[string]string) {
	t.Helper()
	if _, err := Push(t.Context(), repo, delivery.ClusterJoinStream(joinUID), mode, delivery.PredicateClusterJoin, bundle, ann); err != nil {
		t.Fatal(err)
	}
}

// TestListClusterJoinsDedupesByBundle: in either form, 25 copies of one
// record and one other are two records, under the join kind's tags; past
// the bound the listing is cut.
func TestListClusterJoinsDedupesByBundle(t *testing.T) {
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			repo, _ := joinRepo(t, mode)
			for i := range 25 {
				pushJoin(t, repo, mode, []byte(`{"copy":1}`), map[string]string{"copy": fmt.Sprint(i)})
			}
			pushJoin(t, repo, mode, []byte(`{"other":1}`), nil)
			r := &Reader{}
			got, cut, forks, err := r.ListClusterJoins(t.Context(), repo, joinUID)
			if err != nil || cut || len(got) != 2 || len(forks) != 0 {
				t.Fatalf("got %d, cut %v, forks %v, %v", len(got), cut, forks, err)
			}
			for i := range delivery.MaxJoinRecords {
				pushJoin(t, repo, mode, fmt.Appendf(nil, `{"n":%d}`, i), nil)
			}
			if got, cut, _, err = r.ListClusterJoins(t.Context(), repo, joinUID); err != nil || !cut || len(got) != delivery.MaxJoinRecords {
				t.Fatalf("got %d, cut %v, %v", len(got), cut, err)
			}
		})
	}
}

// TestMovedJoinTagRejected: a -join-<12 hex> tag moved onto another manifest
// is not read as a record and is named in forks.
func TestMovedJoinTagRejected(t *testing.T) {
	mode := capabilities.ReferrersWriteOnceTags
	repo, path := joinRepo(t, mode)
	pushJoin(t, repo, mode, []byte(`{"kept":1}`), nil)
	stream := delivery.ClusterJoinStream(joinUID)
	if !strings.HasPrefix(KindTag(stream, KindJoin, digest.FromString("x")), AnchorTag(stream)+"-join-") {
		t.Fatal("join records are not tagged as the join kind")
	}
	moved := KindTag(stream, KindJoin, digest.FromString("another manifest"))
	tagAs(t, path, Anchor(stream).ManifestDesc, []byte(`{"moved":1}`), delivery.PredicateClusterJoin, func(digest.Digest) string { return moved })
	got, _, forks, err := (&Reader{}).ListClusterJoins(t.Context(), repo, joinUID)
	if err != nil || len(got) != 1 || string(got[0].Bundle) != `{"kept":1}` || !slices.ContainsFunc(forks, func(f string) bool { return strings.Contains(f, moved) }) {
		t.Fatalf("got %d, forks %v, %v", len(got), forks, err)
	}
}

// TestListClusterTagsUncapped: every cluster tag within the page bound,
// past the 32 a snapshot read takes, and never a join or anchor tag.
func TestListClusterTagsUncapped(t *testing.T) {
	repo, _ := joinRepo(t, capabilities.ReferrersWriteOnceTags)
	pushJoin(t, repo, capabilities.ReferrersWriteOnceTags, []byte(`{"r":1}`), nil)
	for i := range delivery.MaxTargetSnapshots + 8 {
		tag := fmt.Sprintf("cluster-%02d", i)
		tagManifest(t, repo, []byte(`{"schemaVersion":2,"mediaType":"`+ocispec.MediaTypeImageManifest+`","annotations":{"t":"`+tag+`"}}`), tag)
	}
	tags, truncated, err := ListClusterTags(t.Context(), repo)
	if err != nil || truncated || len(tags) != delivery.MaxTargetSnapshots+8 || tags[0] != "cluster-00" {
		t.Fatalf("tags %d, %v, %v", len(tags), truncated, err)
	}
	capped, truncated, err := ListTargetSnapshotTags(t.Context(), repo)
	if err != nil || !truncated || len(capped) != delivery.MaxTargetSnapshots {
		t.Fatalf("capped %d, %v, %v", len(capped), truncated, err)
	}
}

// joinBundle is a join record of joinUID signed with a fresh keyed signer.
func joinBundle(t *testing.T) []byte {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	j := delivery.ClusterJoin{Cluster: joinUID, Hub: "hub", KeyID: signing.Hint(pub), PublicKey: base64.StdEncoding.EncodeToString(der),
		Decision: delivery.JoinApprove, Approver: "oidc:alice@acme.com", At: "2026-11-20T10:00:00Z"}
	b, err := delivery.SignClusterJoin(t.Context(), edSigner(priv), delivery.NewClusterJoinStatement(j))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// joinServer is an empty pacto-targets repository in mode that refuses the
// requests fails picks, with a fresh prober for its host.
func joinServer(t *testing.T, mode capabilities.ReferrersMode, fails func(*http.Request) bool) *remote.Repository {
	t.Helper()
	old := ocireferrer.Probes
	ocireferrer.Probes = &ocireferrer.Prober{}
	t.Cleanup(func() { ocireferrer.Probes = old })
	inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fails != nil && fails(r) {
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusForbidden)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	repo, _ := ocireferrer.Open(strings.TrimPrefix(srv.URL, "http://")+"/acme/"+delivery.TargetSnapshotRepository, ocireferrer.Options{})
	return repo
}

// TestJoinRecordRoundTripsNativeAndWriteOnce: in either form a pushed join
// record anchors its stream, takes the form the registry probes to and reads
// back through ListClusterJoins.
func TestJoinRecordRoundTripsNativeAndWriteOnce(t *testing.T) {
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			repo := joinServer(t, mode, nil)
			b := joinBundle(t)
			ref, got, err := PushClusterJoin(t.Context(), repo, b)
			if err != nil || got != mode || !strings.HasPrefix(ref, repo.Reference.Registry+"/acme/pacto-targets@sha256:") {
				t.Fatalf("%q %q %v", ref, got, err)
			}
			recs, cut, forks, err := (&Reader{}).ListClusterJoins(t.Context(), repo, joinUID)
			if err != nil || cut || len(forks) != 0 || len(recs) != 1 || string(recs[0].Bundle) != string(b) {
				t.Fatalf("%d records, %v %v %v", len(recs), cut, forks, err)
			}
		})
	}
}

// TestJoinAnchorTagIsNotAClusterTag: a repository holding a snapshot and
// join records in either form lists only the snapshot's cluster tag.
func TestJoinAnchorTagIsNotAClusterTag(t *testing.T) {
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			repo := joinServer(t, mode, nil)
			if _, _, err := PushClusterJoin(t.Context(), repo, joinBundle(t)); err != nil {
				t.Fatal(err)
			}
			tagManifest(t, repo, []byte(`{"schemaVersion":2,"mediaType":"`+ocispec.MediaTypeImageManifest+`"}`), "cluster-"+joinUID)
			for _, list := range []func() ([]string, bool, error){
				func() ([]string, bool, error) { return ListTargetSnapshotTags(t.Context(), repo) },
				func() ([]string, bool, error) { return ListClusterTags(t.Context(), repo) },
			} {
				if tags, _, err := list(); err != nil || !slices.Equal(tags, []string{"cluster-" + joinUID}) {
					t.Fatalf("%v %v", tags, err)
				}
			}
			if strings.HasPrefix(AnchorTag(delivery.ClusterJoinStream(joinUID)), delivery.TargetSnapshotTagPrefix) {
				t.Fatal("the join anchor tag is a cluster tag")
			}
		})
	}
}

// TestEnsureAnchorIdempotent: the second call pushes nothing and probes to
// the same form.
func TestEnsureAnchorIdempotent(t *testing.T) {
	pushes := 0
	repo := joinServer(t, capabilities.ReferrersNative, func(r *http.Request) bool {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/") {
			pushes++
		}
		return false
	})
	stream := delivery.ClusterJoinStream(joinUID)
	first, err := EnsureAnchor(t.Context(), repo, stream)
	if err != nil || first.Referrers != capabilities.ReferrersNative {
		t.Fatalf("%+v %v", first, err)
	}
	n := pushes
	if second, err := EnsureAnchor(t.Context(), repo, stream); err != nil || second.Referrers != first.Referrers || pushes != n || n == 0 {
		t.Fatalf("%+v %v, %d then %d pushes", second, err, n, pushes)
	}
}

// TestPushClusterJoinFailures: a bundle that is no join record, an anchor
// that cannot be pushed or tagged and a probe that fails write nothing.
func TestPushClusterJoinFailures(t *testing.T) {
	if _, _, err := PushClusterJoin(t.Context(), joinServer(t, capabilities.ReferrersNative, nil), []byte("{}")); err == nil {
		t.Fatal("pushed a bundle that is no join record")
	}
	put := func(r *http.Request) bool { return r.Method == http.MethodPut }
	tag := func(r *http.Request) bool {
		return r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/stream-")
	}
	probe := func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") }
	for name, fails := range map[string]func(*http.Request) bool{"push": put, "tag": tag, "probe": probe} {
		if _, _, err := PushClusterJoin(t.Context(), joinServer(t, capabilities.ReferrersNative, fails), joinBundle(t)); err == nil {
			t.Fatalf("%s: no error", name)
		}
	}
}
