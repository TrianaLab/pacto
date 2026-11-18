package deliveryrecord

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

func approvalAt(t *testing.T, key ed25519.PrivateKey, at string) []byte {
	t.Helper()
	st := delivery.NewApprovalRecord(delivery.RecordStage{}, Anchor(stream).ManifestDesc.Digest.String(), delivery.OperationObject{},
		delivery.ApprovalSpec{Decision: delivery.Approve}, delivery.ApprovalSigner{Kind: delivery.SignerPerson}, at)
	b, _, err := delivery.SignApproval(context.Background(), edSigner(key), st)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var modes = []capabilities.ReferrersMode{capabilities.ReferrersNative, capabilities.ReferrersWriteOnceTags}

// predicateFixture is a stream in mode holding three approvals of u1 (on
// the 11th to 13th), one of u2 (the 18th), an intent and an operation
// record, an approval both a referrer and a tag (the 10th, no uid), one
// attached elsewhere and a vanished tag.
func predicateFixture(t *testing.T, mode capabilities.ReferrersMode) (*faulty, *remote.Repository) {
	t.Helper()
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags})
	repo, anchor := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	push := func(predicate string, b []byte, uid string) {
		if _, err := Push(t.Context(), repo, stream, mode, predicate, b, map[string]string{AnnUID: uid}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		push(delivery.PredicateApproval, approvalAt(t, key, fmt.Sprintf("2026-11-1%dT00:00:00Z", i)), "u1")
	}
	push(delivery.PredicateApproval, approvalAt(t, key, "2026-11-18T00:00:00Z"), "u2")
	push(delivery.PredicateIntent, []byte("intent"), "")
	if err := PutRecord(t.Context(), repo, anchor, stream, 1, ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType,
		signed(t, key, 1), &anchor, map[string]string{AnnPredicateType: delivery.PredicateOperation}), mode); err != nil {
		t.Fatal(err)
	}
	kindTag := func(d digest.Digest) string { return KindTag(stream, KindApproval, d) }
	tagAs(t, path, anchor, approvalAt(t, key, "2026-11-10T00:00:00Z"), delivery.PredicateApproval, kindTag)
	elsewhere := Anchor("elsewhere").ManifestDesc
	tagAs(t, path, elsewhere, approvalAt(t, key, "2026-11-19T00:00:00Z"), delivery.PredicateApproval, kindTag)
	gone := KindTag(stream, KindApproval, digest.FromString("gone"))
	tagAs(t, path, elsewhere, []byte("x"), delivery.PredicateApproval, func(digest.Digest) string { return gone })
	f.tagStatus = map[string]int{gone: http.StatusNotFound}
	return f, repo
}

// TestListPredicateBounded: in either form, the approval records on a
// stream's anchor come back newest first; past max it returns max and says
// it was cut. Intent and operation records, a record attached elsewhere, one
// for another operation and a vanished tag are not read as its approvals.
func TestListPredicateBounded(t *testing.T) {
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			_, repo := predicateFixture(t, mode)
			r := &Reader{}
			all, cut, err := r.ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "", 10)
			if err != nil || cut || len(all) != 5 || claimedTime(all[0].Bundle) != "2026-11-18T00:00:00Z" || claimedTime(all[4].Bundle) != "2026-11-10T00:00:00Z" {
				t.Fatalf("all = %d %v %v", len(all), cut, err)
			}
			mine, cut, err := r.ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "u1", 2)
			if err != nil || !cut || len(mine) != 2 || claimedTime(mine[0].Bundle) < claimedTime(mine[1].Bundle) || !strings.HasPrefix(mine[0].Manifest, "sha256:") {
				t.Fatalf("bounded = %d %v %v", len(mine), cut, err)
			}
			intents, _, err := r.ListPredicate(t.Context(), repo, stream, delivery.PredicateIntent, "", 10)
			if err != nil || len(intents) != 1 || string(intents[0].Bundle) != "intent" {
				t.Fatalf("intents = %+v %v", intents, err)
			}
		})
	}
}

// TestListPredicateRemembersManifests: a manifest read once is not fetched
// again; a manifest or bundle the registry refuses is an error.
func TestListPredicateRemembersManifests(t *testing.T) {
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f, repo := predicateFixture(t, mode)
			r := &Reader{}
			if _, _, err := r.ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "", 10); err != nil {
				t.Fatal(err)
			}
			f.manifest.Store(true)
			if again, _, err := r.ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "u1", 10); err != nil || len(again) != 3 {
				t.Fatalf("remembered = %d %v", len(again), err)
			}
			if _, _, err := (&Reader{}).ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "", 10); err == nil {
				t.Fatal("a manifest that would not fetch listed nothing")
			}
			f.manifest.Store(false)
			f.blobs.Store(true)
			if _, _, err := r.ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "", 10); err == nil {
				t.Fatal("a bundle that would not fetch listed nothing")
			}
		})
	}
}

// TestListPredicateRefusesUnreadable: a tag that will not resolve and a
// registry that cannot be reached are errors, never no record.
func TestListPredicateRefusesUnreadable(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	tag := KindTag(stream, KindApproval, digest.FromString("t"))
	tagAs(t, path, anchor, []byte("x"), delivery.PredicateApproval, func(digest.Digest) string { return tag })
	f.tagStatus = map[string]int{tag: http.StatusForbidden}
	if _, _, err := (&Reader{}).ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "", 10); err == nil {
		t.Fatal("a tag that would not resolve listed nothing")
	}
	unreachable, _ := ocireferrer.Open("127.0.0.1:1/x", ocireferrer.Options{})
	if _, _, err := (&Reader{}).ListPredicate(t.Context(), unreachable, stream, delivery.PredicateApproval, "", 10); err == nil {
		t.Fatal("an unreachable registry listed nothing")
	}
}

// TestListPredicateSkipsOversized: a manifest or bundle layer too large to
// be a record is not read as one.
func TestListPredicateSkipsOversized(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	b := approvalAt(t, key, "2026-11-11T00:00:00Z")
	if _, err := Push(t.Context(), repo, stream, capabilities.ReferrersNative, delivery.PredicateApproval, b, nil); err != nil {
		t.Fatal(err)
	}
	defer func(old int64) { MaxBytes = old }(MaxBytes)
	for _, max := range []int64{int64(len(b)) - 1, 100} {
		MaxBytes = max
		if got, _, err := (&Reader{}).ListPredicate(t.Context(), repo, stream, delivery.PredicateApproval, "", 10); err != nil || len(got) != 0 {
			t.Fatalf("MaxBytes %d: %d %v", max, len(got), err)
		}
	}
}

// TestFetch: the bundle of one record comes back by its manifest digest;
// another predicate, a name that is no digest and an unknown digest do not.
func TestFetch(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	b := approvalAt(t, key, "2026-11-11T00:00:00Z")
	ref, err := Push(t.Context(), repo, stream, capabilities.ReferrersNative, delivery.PredicateApproval, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest := ref[len(path)+1:]
	if got, err := Fetch(t.Context(), repo, manifest, delivery.PredicateApproval); err != nil || string(got) != string(b) {
		t.Fatalf("Fetch = %d bytes, %v", len(got), err)
	}
	if _, err := Fetch(t.Context(), repo, manifest, delivery.PredicateIntent); err == nil {
		t.Fatal("an approval record fetched as an intent")
	}
	if _, err := Fetch(t.Context(), repo, AnchorTag(stream), delivery.PredicateApproval); err == nil {
		t.Fatal("a tag fetched")
	}
	if _, err := Fetch(t.Context(), repo, digest.FromString("none").String(), delivery.PredicateApproval); err == nil {
		t.Fatal("an unknown digest fetched")
	}
}
