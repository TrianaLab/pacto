package deliveryrecord

import (
	"fmt"
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
