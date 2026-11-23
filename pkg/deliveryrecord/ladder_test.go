package deliveryrecord

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// TestKindOfLadder: ladder records are written under the ladder kind.
func TestKindOfLadder(t *testing.T) {
	if got := kindOf(delivery.PredicateEnvironmentLadder); got != KindLadder || KindLadder != "ladder" {
		t.Fatalf("kind %q", got)
	}
}

// TestListLadderBoundedWithForks: in either form, ladder records of joinUID
// read back under the ladder kind, a join record on another stream is never
// one, past the bound the listing is cut, and a moved write-once tag is a
// fork.
func TestListLadderBoundedWithForks(t *testing.T) {
	stream := delivery.LadderStream(joinUID)
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			_, path := setup(t, testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags})
			repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
			if _, err := EnsureAnchor(t.Context(), repo, stream); err != nil {
				t.Fatal(err)
			}
			push := func(b string) {
				if _, err := Push(t.Context(), repo, stream, mode, delivery.PredicateEnvironmentLadder, []byte(b), nil); err != nil {
					t.Fatal(err)
				}
			}
			push(`{"r":0}`)
			pushJoin(t, repo, mode, []byte(`{"join":1}`), nil)
			r := &Reader{}
			got, cut, forks, err := r.ListLadder(t.Context(), repo, joinUID)
			if err != nil || cut || len(got) != 1 || string(got[0].Bundle) != `{"r":0}` || len(forks) != 0 {
				t.Fatalf("got %d, cut %v, forks %v, %v", len(got), cut, forks, err)
			}
			for i := range delivery.MaxLadderRecords {
				push(fmt.Sprintf(`{"r":%d}`, i+1))
			}
			if got, cut, _, err = r.ListLadder(t.Context(), repo, joinUID); err != nil || !cut || len(got) != delivery.MaxLadderRecords {
				t.Fatalf("got %d, cut %v, %v", len(got), cut, err)
			}
		})
	}
}

// TestMovedLadderTagRejected: a -ladder-<12 hex> tag moved onto another
// manifest is not read as a record and is named in forks.
func TestMovedLadderTagRejected(t *testing.T) {
	stream := delivery.LadderStream(joinUID)
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	if _, err := EnsureAnchor(t.Context(), repo, stream); err != nil {
		t.Fatal(err)
	}
	moved := KindTag(stream, KindLadder, digest.FromString("another manifest"))
	tagAs(t, path, Anchor(stream).ManifestDesc, []byte(`{"moved":1}`), delivery.PredicateEnvironmentLadder, func(digest.Digest) string { return moved })
	got, _, forks, err := (&Reader{}).ListLadder(t.Context(), repo, joinUID)
	if err != nil || len(got) != 0 || !slices.ContainsFunc(forks, func(f string) bool { return strings.Contains(f, moved) }) {
		t.Fatalf("got %d, forks %v, %v", len(got), forks, err)
	}
}
