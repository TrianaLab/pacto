/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

type fakeIndex struct {
	entries   map[string]orgindex.Entry
	images    map[string][]publish.ImagesRecord
	cut       map[string]bool
	entryErr  map[string]bool
	imagesErr map[string]bool
	reads     []string
}

func newFake() *fakeIndex {
	return &fakeIndex{entries: map[string]orgindex.Entry{}, images: map[string][]publish.ImagesRecord{},
		cut: map[string]bool{}, entryErr: map[string]bool{}, imagesErr: map[string]bool{}}
}

func (f *fakeIndex) Entry(_ context.Context, tag string) (orgindex.Entry, error) {
	f.reads = append(f.reads, tag)
	if f.entryErr[tag] {
		return orgindex.Entry{}, errors.New("refused")
	}
	return f.entries[tag], nil
}

func (f *fakeIndex) Images(_ context.Context, repo, digest string) ([]publish.ImagesRecord, bool, error) {
	b := repo + "@" + digest
	f.reads = append(f.reads, b)
	if f.imagesErr[b] {
		return nil, false, errors.New("unreachable")
	}
	return f.images[b], f.cut[b], nil
}

// publishVersion adds service's version, bundled at repo, whose signed images
// record names imageRepo@digest; it returns the tag.
func (f *fakeIndex) publishVersion(t *testing.T, service, repo, version, imageRepo, digest string) string {
	t.Helper()
	tag, err := orgindex.Tag(fleet.ServiceKey(service), version)
	if err != nil {
		t.Fatal(err)
	}
	bundle := repo + "@sha256:" + fmt.Sprintf("%064x", len(f.entries)+1)
	f.entries[tag] = orgindex.Entry{Service: service, Name: path.Base(service), Version: version, Bundle: bundle}
	f.images[bundle] = []publish.ImagesRecord{{Records: []publish.ImageRecord{{Repository: imageRepo, Digest: digest}}, Signed: true}}
	return tag
}

func readVersions(f *fakeIndex) []string {
	var out []string
	for _, r := range f.reads {
		if e, ok := f.entries[r]; ok {
			out = append(out, e.Version)
		}
	}
	return out
}

func TestIndexReadsNewestAndRunningTags(t *testing.T) {
	f := newFake()
	tags := make([]string, 0, 11)
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0", "1.6.0", "1.7.0", "2.0.0-rc.1"} {
		tags = append(tags, f.publishVersion(t, "acme/payments", "reg.io/c/payments", v, "reg.io/payments", dA))
	}
	tags = append(tags, "latest", tags[0][:16]+"-notsemver")
	var x Index
	_, unread, partial := x.Candidates(context.Background(), tags, []string{"1.1.0"}, f)
	got := readVersions(f)
	slices.Sort(got)
	want := []string{"1.1.0", "1.3.0", "1.4.0", "1.5.0", "1.6.0", "1.7.0"}
	if !slices.Equal(got, want) || unread != 0 || partial {
		t.Fatalf("read %v (unread %d, partial %v), want %v", got, unread, partial, want)
	}
	if !x.Indexed("reg.io/c/payments") || x.Indexed("reg.io/c/other") {
		t.Fatal("Indexed does not follow the read entries")
	}
}

func TestIndexLinksOlderVersionByDigest(t *testing.T) {
	f := newFake()
	tags := make([]string, 0, 3)
	for i, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		d := dB
		if i == 0 {
			d = dA
		}
		tags = append(tags, f.publishVersion(t, "acme/payments", "reg.io/c/payments", v, "reg.io/payments", d))
	}
	var x Index
	byDigest, _, _ := x.Candidates(context.Background(), tags, nil, f)
	l, _ := Match(payments(img("reg.io/payments", dA)), byDigest)
	if l.Signal != fleet.LinkSignalImageDigest || l.Ref != f.entries[tags[0]].Bundle {
		t.Fatalf("the third-newest version did not link: %+v", l)
	}
}

func TestIndexPartialListingIsPartial(t *testing.T) {
	f := newFake()
	cut := f.publishVersion(t, "acme/a", "reg.io/c/a", "1.0.0", "reg.io/a", dA)
	bad := f.publishVersion(t, "acme/b", "reg.io/c/b", "1.0.0", "reg.io/b", dA)
	gone := f.publishVersion(t, "acme/c", "reg.io/c/c", "1.0.0", "reg.io/c", dA)
	f.cut[f.entries[cut].Bundle], f.entryErr[bad], f.imagesErr[f.entries[gone].Bundle] = true, true, true
	var x Index
	byDigest, unread, partial := x.Candidates(context.Background(), []string{cut, bad, gone}, nil, f)
	if !partial || unread != 0 || len(byDigest[dA]) != 1 {
		t.Fatalf("partial %v unread %d candidates %+v", partial, unread, byDigest)
	}
	f.reads = nil
	x.Candidates(context.Background(), []string{cut}, nil, f)
	if !slices.Equal(f.reads, []string{f.entries[cut].Bundle}) {
		t.Fatalf("a cut listing was cached: reads %v", f.reads)
	}
}

func TestIndexCachesForever(t *testing.T) {
	f := newFake()
	tag := f.publishVersion(t, "acme/payments", "reg.io/c/payments", "1.0.0", "reg.io/payments", dA)
	var x Index
	x.Candidates(context.Background(), []string{tag}, nil, f)
	f.reads = nil
	byDigest, _, partial := x.Candidates(context.Background(), []string{tag}, nil, f)
	if len(f.reads) != 0 || partial || len(byDigest[dA]) != 1 {
		t.Fatalf("second cycle read %v (partial %v, %+v)", f.reads, partial, byDigest)
	}
}

func TestIndexBoundsReadsPerCycle(t *testing.T) {
	f := newFake()
	tags := make([]string, 0, 300)
	for i := range 300 {
		tags = append(tags, f.publishVersion(t, fmt.Sprintf("acme/s%d", i), fmt.Sprintf("reg.io/c/s%d", i), "1.0.0", "reg.io/s", dA))
	}
	var x Index
	byDigest, unread, partial := x.Candidates(context.Background(), tags, nil, f)
	if len(f.reads) != maxIndexReadsPerCycle || unread != 200 || !partial || len(byDigest[dA]) != 100 {
		t.Fatalf("reads %d unread %d partial %v candidates %d", len(f.reads), unread, partial, len(byDigest[dA]))
	}
	f.reads = nil
	byDigest, unread, _ = x.Candidates(context.Background(), tags, nil, f)
	if unread != 100 || len(byDigest[dA]) != 200 {
		t.Fatalf("the next cycle did not progress: unread %d candidates %d", unread, len(byDigest[dA]))
	}
	// Entries already cached: the bound stops the images reads too.
	cached := Index{entries: map[string]orgindex.Entry{}, images: map[string][]publish.ImagesRecord{}}
	for _, tag := range tags[:maxIndexReadsPerCycle+1] {
		cached.entries[tag] = f.entries[tag]
	}
	f.reads = nil
	if _, unread, _ = cached.Candidates(context.Background(), tags[:maxIndexReadsPerCycle+1], nil, f); unread != 1 {
		t.Fatalf("images reads past the bound: unread %d", unread)
	}
}

func TestIndexClearsOnOverflow(t *testing.T) {
	m := map[string]int{}
	for i := range maxCached {
		keep(m, fmt.Sprint(i), i)
	}
	if len(m) != maxCached {
		t.Fatalf("len %d before overflow", len(m))
	}
	keep(m, "next", 1)
	if len(m) != 1 || m["next"] != 1 {
		t.Fatalf("overflow kept %d entries", len(m))
	}
}

func TestEntryServiceIsNeverTrusted(t *testing.T) {
	f := newFake()
	real := f.publishVersion(t, "acme/payments", "reg.io/c/payments", "1.0.0", "reg.io/payments", dA)
	forged := f.publishVersion(t, "acme/payments", "reg.io/c/impostor", "1.1.0", "reg.io/payments", dA)
	var x Index
	byDigest, _, _ := x.Candidates(context.Background(), []string{real, forged}, nil, f)
	for _, c := range byDigest[dA] {
		if c.Service == "acme/payments" {
			t.Fatalf("a candidate took the entry's claimed service: %+v", c)
		}
	}
	l, lim := Match(payments(img("reg.io/payments", dA)), byDigest)
	if l.Signal != "" || lim.Code != fleet.LimitationInferredReplacementAmbiguous {
		t.Fatalf("two bundles claiming one service merged: %+v %+v", l, lim)
	}
}
