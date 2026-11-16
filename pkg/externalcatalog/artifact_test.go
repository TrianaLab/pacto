package externalcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// testRegistry is an in-process registry; reject, when set, answers a request
// with 400 (never retried) instead of serving it; alter, when it returns
// true, has written its own answer.
type testRegistry struct {
	host   string
	reject func(r *http.Request) bool
	alter  func(w http.ResponseWriter, r *http.Request) bool
}

func newTestRegistry(t *testing.T) *testRegistry {
	t.Helper()
	tr := &testRegistry{}
	reg := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tr.reject != nil && tr.reject(r) {
			http.Error(w, "rejected", http.StatusBadRequest)
			return
		}
		if tr.alter != nil && tr.alter(w, r) {
			return
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	tr.host = strings.TrimPrefix(srv.URL, "http://")
	return tr
}

func testClient() *oci.Client { return oci.NewClient(authn.NewMultiKeychain()) }

// putManifest writes raw manifest bytes to ref with no blob checks.
func putManifest(t *testing.T, ref string, m []byte) {
	t.Helper()
	r, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Put(r, rawManifest(m)); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDigestIsDeterministic(t *testing.T) {
	b := DefaultBytes()
	first, second := Digest(b), Digest(append([]byte(nil), b...))
	if first != second || first != DefaultDigest() {
		t.Fatal("digest is not deterministic")
	}
	sum := sha256.Sum256(Manifest(b))
	if DefaultDigest() != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatal("digest is not the sha256 of the manifest")
	}
	b[0] ^= 1
	if Digest(b) == DefaultDigest() {
		t.Fatal("a one-byte change kept the digest")
	}
}

func TestManifestShape(t *testing.T) {
	sum := sha256.Sum256([]byte("{}"))
	if EmptyDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatal("EmptyDigest is not the digest of {}")
	}
	var m map[string]any
	if err := json.Unmarshal(Manifest([]byte("x")), &m); err != nil {
		t.Fatal(err)
	}
	if m["artifactType"] != ArtifactType || m["mediaType"] != "application/vnd.oci.image.manifest.v1+json" || m["schemaVersion"] != float64(2) {
		t.Fatalf("manifest = %v", m)
	}
	if _, ok := m["annotations"]; ok {
		t.Fatal("manifest carries annotations")
	}
	if len(m) != 5 {
		t.Fatalf("manifest has unexpected keys: %v", m)
	}
	cfg := m["config"].(map[string]any)
	if cfg["digest"] != EmptyDigest || cfg["mediaType"] != EmptyMediaType || cfg["size"] != float64(2) {
		t.Fatalf("config = %v", cfg)
	}
	layers := m["layers"].([]any)
	l := layers[0].(map[string]any)
	if len(layers) != 1 || l["mediaType"] != LayerMediaType || l["size"] != float64(1) || l["digest"] != sha256Digest([]byte("x")) {
		t.Fatalf("layers = %v", layers)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	reg := newTestRegistry(t)
	ref := reg.host + "/pacto/external-catalog:1.0.0"
	d, err := Write(context.Background(), testClient(), ref, DefaultBytes())
	if err != nil || d != DefaultDigest() {
		t.Fatalf("Write = %q, %v", d, err)
	}
	got, err := Read(context.Background(), testClient(), ref)
	if err != nil || string(got) != string(DefaultBytes()) {
		t.Fatalf("Read = %d bytes, %v", len(got), err)
	}
	byDigest, err := Read(context.Background(), testClient(), reg.host+"/pacto/external-catalog@"+d)
	if err != nil || string(byDigest) != string(got) {
		t.Fatalf("Read by digest: %v", err)
	}
}

func TestWriteStripsOCIScheme(t *testing.T) {
	reg := newTestRegistry(t)
	ref := "oci://" + reg.host + "/c:1"
	if _, err := Write(context.Background(), testClient(), ref, []byte("b")); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(context.Background(), testClient(), ref); err != nil || string(got) != "b" {
		t.Fatalf("Read = %q, %v", got, err)
	}
}

func TestWriteErrors(t *testing.T) {
	reg := newTestRegistry(t)
	if _, err := Write(context.Background(), testClient(), "UPPER CASE", nil); err == nil {
		t.Fatal("invalid ref accepted")
	}
	reg.reject = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/blobs/") }
	if _, err := Write(context.Background(), testClient(), reg.host+"/c:1", []byte("b")); err == nil {
		t.Fatal("blob failure ignored")
	}
	reg.reject = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/manifests/") }
	if _, err := Write(context.Background(), testClient(), reg.host+"/c:1", []byte("b")); err == nil {
		t.Fatal("manifest failure ignored")
	}
}

func TestReadMissing(t *testing.T) {
	reg := newTestRegistry(t)
	if _, err := Read(context.Background(), testClient(), reg.host+"/c:none"); err == nil {
		t.Fatal("missing artifact read")
	}
}

func manifestWith(mut func(*manifest)) []byte {
	var m manifest
	_ = json.Unmarshal(Manifest([]byte("b")), &m)
	mut(&m)
	out, _ := json.Marshal(m)
	return out
}

func TestReadRejectsWrongMediaType(t *testing.T) {
	reg := newTestRegistry(t)
	putManifest(t, reg.host+"/c:1", manifestWith(func(m *manifest) { m.Layers[0].MediaType = "application/yaml" }))
	_, err := Read(context.Background(), testClient(), reg.host+"/c:1")
	if err == nil || !strings.Contains(err.Error(), "layer media type") {
		t.Fatalf("Read = %v", err)
	}
}

func TestReadRejectsTwoLayers(t *testing.T) {
	reg := newTestRegistry(t)
	putManifest(t, reg.host+"/c:1", manifestWith(func(m *manifest) { m.Layers = append(m.Layers, m.Layers[0]) }))
	_, err := Read(context.Background(), testClient(), reg.host+"/c:1")
	if err == nil || !strings.Contains(err.Error(), "2 layers") {
		t.Fatalf("Read = %v", err)
	}
}

func TestCheckManifestRejects(t *testing.T) {
	for want, raw := range map[string][]byte{
		"decode manifest": []byte("{"),
		"artifactType":    manifestWith(func(m *manifest) { m.ArtifactType = "x" }),
		"empty":           manifestWith(func(m *manifest) { m.Config.Digest = "sha256:00" }),
		"over the":        manifestWith(func(m *manifest) { m.Layers[0].Size = MaxFileBytes + 1 }),
	} {
		if _, err := checkManifest(raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("checkManifest error = %v, want %q", err, want)
		}
	}
}

func TestReadLayerFailures(t *testing.T) {
	reg := newTestRegistry(t)
	ctx := context.Background()

	// An unparsable layer digest fails before any blob request.
	putManifest(t, reg.host+"/c:bad", manifestWith(func(m *manifest) { m.Layers[0].Digest = "sha256:zz" }))
	if _, err := Read(ctx, testClient(), reg.host+"/c:bad"); err == nil {
		t.Fatal("bad layer digest read")
	}

	// A layer blob the registry does not hold.
	putManifest(t, reg.host+"/c:gone", manifestWith(func(m *manifest) {}))
	if _, err := Read(ctx, testClient(), reg.host+"/c:gone"); err == nil {
		t.Fatal("missing layer read")
	}

	// A blob whose bytes do not match its digest.
	if _, err := Write(ctx, testClient(), reg.host+"/c:ok", []byte("b")); err != nil {
		t.Fatal(err)
	}
	reg.alter = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/blobs/"+sha256Digest([]byte("b"))) {
			_, _ = w.Write([]byte("tampered"))
			return true
		}
		return false
	}
	if _, err := Read(ctx, testClient(), reg.host+"/c:ok"); err == nil {
		t.Fatal("tampered layer read")
	}
	reg.alter = nil

	// A blob larger than its manifest says, over the limit.
	big := make([]byte, MaxFileBytes+10)
	repo, _ := name.NewRepository(reg.host + "/c")
	if err := remote.WriteLayer(repo, static.NewLayer(big, LayerMediaType)); err != nil {
		t.Fatal(err)
	}
	putManifest(t, reg.host+"/c:big", manifestWith(func(m *manifest) { m.Layers[0].Digest = sha256Digest(big) }))
	if _, err := Read(ctx, testClient(), reg.host+"/c:big"); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("oversize layer: %v", err)
	}
}
