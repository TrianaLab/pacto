package publish

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
)

func TestDecodeImagesStrict(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	got, err := DecodeImages([]byte(`{"images":[{"name":"api","repository":"r.io/api","digest":"` + d + `","source":"built"}]}`))
	expect(t, err == nil && len(got) == 1 && got[0].Digest == d && got[0].Repository == "r.io/api", "decode %+v %v", got, err)
	for name, body := range map[string]string{
		"unknown field": `{"images":[],"extra":1}`,
		"not json":      `{`,
		"short digest":  `{"images":[{"name":"api","digest":"sha256:abc"}]}`,
		"other algo":    `{"images":[{"name":"api","digest":"sha512:` + strings.Repeat("a", 64) + `"}]}`,
		"over bound":    `{"images":[{"name":"` + strings.Repeat("x", maxImagesRecordBytes) + `"}]}`,
	} {
		if _, err := DecodeImages([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// publishImages publishes payments with one --image on reg and returns the
// bundle repository, the bundle digest and the images record digest.
func publishImages(t *testing.T, reg *Client, host string) (string, string, string) {
	t.Helper()
	img := ImageRecord{Name: "image", Repository: "r.io/api", Digest: "sha256:" + strings.Repeat("a", 64), Source: "flag:--image"}
	res, err := Apply(t.Context(), reg, newTools(), Input{Bundle: imagesBundle(host), Revision: "abc123", Images: []ImageRecord{img}})
	expect(t, err == nil && res.ImagesRecord != "", "publish: %v %+v", err, res)
	return host + "/pacto/payments", res.Digests[BundleItem], res.ImagesRecord
}

// TestReadImagesFromPublishedBundle: the images record a publish attached
// reads back through either referrer mode, unsigned until a Sigstore
// signature referrer is attached to it, then signed.
func TestReadImagesFromPublishedBundle(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	host, plain := newRegistry(t)
	for mode, c := range map[string]struct {
		host string
		reg  *Client
	}{"referrers": {host, plain}, "write-once tags": {tr.host, tr.c}} {
		repo, bundle, record := publishImages(t, c.reg, c.host)
		recs, partial, err := c.reg.ReadImages(ctx, repo, bundle)
		expect(t, err == nil && !partial && len(recs) == 1 && !recs[0].Signed && recs[0].Records[0].Repository == "r.io/api",
			"%s: unsigned read %+v %v %v", mode, recs, partial, err)
		expect(t, SignDigest(ctx, c.reg, echoSigner{}, repo, record) == nil, "%s: sign", mode)
		recs, partial, err = c.reg.ReadImages(ctx, repo, bundle)
		expect(t, err == nil && !partial && len(recs) == 1 && recs[0].Signed, "%s: signed read %+v %v %v", mode, recs, partial, err)
		none, partial, err := c.reg.ReadImages(ctx, repo, record) // the record has no images record of its own
		expect(t, err == nil && !partial && none == nil, "%s: none %+v %v %v", mode, none, partial, err)
	}
}

// faultyRegistry serves a referrers registry and fails, or paginates, the
// requests whose path ends in a suffix set after the publish.
type faultyRegistry struct {
	host           string
	reg            *Client
	deny, paginate atomic.Pointer[string]
}

func newFaultyRegistry(t *testing.T) *faultyRegistry {
	t.Helper()
	f := &faultyRegistry{}
	h := registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := f.deny.Load(); s != nil && strings.HasSuffix(r.URL.Path, *s) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if s := f.paginate.Load(); s != nil && strings.HasSuffix(r.URL.Path, *s) {
			w.Header().Set("Link", `<`+r.URL.Path+`?last=x>; rel="next"`)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	f.host = strings.TrimPrefix(srv.URL, "http://")
	f.reg = NewClient(anonymous, f.host)
	return f
}

func ptr(s string) *string { return &s }

// TestReadImagesPartialListingIsPartial: a paginated listing of the bundle's
// referrers, or of an unsigned record's, reads as partial, never as "no
// record" or "unsigned"; a signature found on a paginated page is signed.
func TestReadImagesPartialListingIsPartial(t *testing.T) {
	ctx := t.Context()
	f := newFaultyRegistry(t)
	repo, bundle, record := publishImages(t, f.reg, f.host)
	f.paginate.Store(ptr("/referrers/" + bundle))
	recs, partial, err := f.reg.ReadImages(ctx, repo, bundle)
	expect(t, err == nil && partial && len(recs) == 1, "bundle page: %+v %v %v", recs, partial, err)
	f.paginate.Store(ptr("/referrers/" + record))
	recs, partial, err = f.reg.ReadImages(ctx, repo, bundle)
	expect(t, err == nil && partial && len(recs) == 1 && !recs[0].Signed, "unsigned record page: %+v %v %v", recs, partial, err)
	f.paginate.Store(nil)
	expect(t, SignDigest(ctx, f.reg, echoSigner{}, repo, record) == nil, "sign")
	f.paginate.Store(ptr("/referrers/" + record))
	recs, partial, err = f.reg.ReadImages(ctx, repo, bundle)
	expect(t, err == nil && !partial && len(recs) == 1 && recs[0].Signed, "signed record page: %+v %v %v", recs, partial, err)
}

// TestReadImagesFailures: a listing, record or signature read that fails is
// an error, never an empty answer; a record that does not decode names no
// image.
func TestReadImagesFailures(t *testing.T) {
	ctx := t.Context()
	f := newFaultyRegistry(t)
	repo, bundle, record := publishImages(t, f.reg, f.host)
	for _, suffix := range []string{"/referrers/" + bundle, "/manifests/" + record, "/referrers/" + record} {
		f.deny.Store(ptr(suffix))
		if _, _, err := f.reg.ReadImages(ctx, repo, bundle); err == nil {
			t.Errorf("%s denied: no error", suffix)
		}
	}
	f.deny.Store(nil)
	bad := attachBadImages(t, f.reg, repo, bundle)
	recs, _, err := f.reg.ReadImages(ctx, repo, bundle)
	expect(t, err == nil && len(recs) == 1 && bad != record, "malformed record read: %+v %v", recs, err)
}

// attachBadImages attaches an images record that does not decode.
func attachBadImages(t *testing.T, reg *Client, repo, bundle string) string {
	t.Helper()
	d, err := attachReferrer(context.Background(), reg, newTools(), SignOf(nil), referrer{ImagesType, "a bad record", func() ([]byte, error) { return []byte(`{"images":[{"digest":"x"}]}`), nil }},
		repo, bundle, "1.0.0", "abc", &Result{})
	expect(t, err == nil, "attach: %v", err)
	return d
}
