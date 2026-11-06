/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

const providerYAML = `pactoVersion: "2.6"
service: {name: pg, version: 16.1.0}
configurations:
  - {name: db, schema: configuration/schema.json, required: true}
`

// recordingRegistry is a real OCI registry that records the Authorization header of every request
// and counts manifest reads. With challenge set it answers 401 to any request without credentials.
type recordingRegistry struct {
	host      string
	mu        sync.Mutex
	auths     []string
	manifests int
}

func newRecordingRegistry(t *testing.T, challenge bool) *recordingRegistry {
	t.Helper()
	rr := &recordingRegistry{}
	reg := registry.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth := req.Header.Get("Authorization")
		rr.mu.Lock()
		rr.auths = append(rr.auths, auth)
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/manifests/") {
			rr.manifests++
		}
		rr.mu.Unlock()
		if challenge && auth == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reg.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	rr.host = strings.TrimPrefix(srv.URL, "http://")
	return rr
}

// push publishes the provider bundle at repo:tag and returns its digest, then forgets the requests.
func (rr *recordingRegistry) push(t *testing.T, repoTag string) string {
	t.Helper()
	c, err := contract.Parse(strings.NewReader(providerYAML))
	if err != nil {
		t.Fatal(err)
	}
	b := &contract.Bundle{Contract: c, FS: fstest.MapFS{
		"pacto.yaml":                &fstest.MapFile{Data: []byte(providerYAML)},
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	}}
	pusher := staticKeychain{auth: &authn.AuthConfig{Username: "pusher", Password: "push"}}
	digest, err := oci.NewClient(pusher).Push(t.Context(), rr.host+"/"+repoTag, b)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	rr.mu.Lock()
	rr.auths, rr.manifests = nil, 0
	rr.mu.Unlock()
	return digest
}

func (rr *recordingRegistry) seen() ([]string, int) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]string(nil), rr.auths...), rr.manifests
}

// testLoader is a loader whose default keychain holds known credentials, so a test can tell
// whether they were sent.
func testLoader() *Loader {
	return &Loader{cacheTTL: 30 * time.Second, oci: &OCIPuller{
		keychain: staticKeychain{auth: &authn.AuthConfig{Username: "operator", Password: "default-keychain"}},
	}}
}

// basic is the Authorization header the given credentials produce.
func basic(user, pass string) string {
	req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	req.SetBasicAuth(user, pass)
	return req.Header.Get("Authorization")
}

func resetProviderCache(t *testing.T) {
	t.Helper()
	providerCache = newRefCache(providerCacheSize)
	t.Cleanup(func() { providerCache = newRefCache(providerCacheSize) })
}

func TestResolver_PullsAnOCIRef(t *testing.T) {
	resetProviderCache(t)
	reg := newRecordingRegistry(t, false)
	reg.push(t, "acme/pg:16.1.0")
	t.Setenv(InsecureRegistriesEnvVar, reg.host) // an inline contract has no host of its own
	r := testLoader().ConfigRefResolver("", nil, "")

	b, err := r.ResolveBundle(t.Context(), "oci://"+reg.host+"/acme/pg:16.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if b.Contract.Service.Name != "pg" || b.FS == nil {
		t.Fatalf("bundle = %+v, want the pg provider with its files", b)
	}
}

func TestResolver_RefusesALocalRef(t *testing.T) {
	r := testLoader().ConfigRefResolver("ghcr.io/acme/app:1.0.0", nil, "")
	for _, ref := range []string{"./platform-config", "file:///etc/passwd", "ghcr.io/acme/pg:1.0.0"} {
		if _, err := r.ResolveBundle(t.Context(), ref); !errors.Is(err, errLocalConfigRef) {
			t.Errorf("ResolveBundle(%q) error = %v, want %v", ref, err, errLocalConfigRef)
		}
	}
}

// A ref on another host than spec.contractRef never receives the pull secret nor the operator's
// default keychain: the registry challenges, and every request it saw carried no credentials.
func TestResolver_OtherHostIsAnonymous(t *testing.T) {
	resetProviderCache(t)
	contractReg := newRecordingRegistry(t, false)
	other := newRecordingRegistry(t, true)
	other.push(t, "acme/pg:16.1.0")
	t.Setenv(InsecureRegistriesEnvVar, "elsewhere:5000, "+other.host)
	override := &authn.AuthConfig{Username: "secret-user", Password: "secret-pass"}

	for _, auth := range []*authn.AuthConfig{override, nil} {
		resetProviderCache(t)
		r := testLoader().ConfigRefResolver("oci://"+contractReg.host+"/acme/app:1.0.0", auth, "ns/pull@1")
		if _, err := r.ResolveBundle(t.Context(), "oci://"+other.host+"/acme/pg:16.1.0"); err == nil {
			t.Fatal("pull from a challenging registry succeeded without credentials")
		}
	}
	auths, _ := other.seen()
	if len(auths) == 0 {
		t.Fatal("the other registry saw no request")
	}
	for _, a := range auths {
		if a != "" {
			t.Errorf("the other registry received credentials %q", a)
		}
	}
}

// A ref on the host of spec.contractRef receives the same credentials that ref is pulled with.
func TestResolver_SameHostReceivesTheOverride(t *testing.T) {
	resetProviderCache(t)
	reg := newRecordingRegistry(t, true)
	reg.push(t, "acme/pg:16.1.0")
	override := &authn.AuthConfig{Username: "secret-user", Password: "secret-pass"}
	r := testLoader().ConfigRefResolver("oci://"+reg.host+"/acme/app:1.0.0", override, "ns/pull@1")

	if _, err := r.ResolveBundle(t.Context(), "oci://"+reg.host+"/acme/pg:16.1.0"); err != nil {
		t.Fatal(err)
	}
	auths, _ := reg.seen()
	want := basic("secret-user", "secret-pass")
	for _, a := range auths {
		if a != "" && a != want {
			t.Errorf("received %q, want only the override %q", a, want)
		}
	}
	if !strings.Contains(strings.Join(auths, "\n"), want) {
		t.Errorf("the override was never sent; saw %q", auths)
	}
}

// Without a pull secret, a ref on the contract's host uses the operator's default keychain, as
// spec.contractRef does.
func TestResolver_SameHostWithoutSecretUsesTheDefaultKeychain(t *testing.T) {
	resetProviderCache(t)
	reg := newRecordingRegistry(t, true)
	reg.push(t, "acme/pg:16.1.0")
	r := testLoader().ConfigRefResolver("oci://"+reg.host+"/acme/app:1.0.0", nil, "")

	if _, err := r.ResolveBundle(t.Context(), "oci://"+reg.host+"/acme/pg:16.1.0"); err != nil {
		t.Fatal(err)
	}
	auths, _ := reg.seen()
	if !strings.Contains(strings.Join(auths, "\n"), basic("operator", "default-keychain")) {
		t.Errorf("the default keychain was never sent; saw %q", auths)
	}
}

// Two reconciles with a pull secret and a digest-pinned ref pull the provider once.
func TestResolver_DigestPinnedRefPullsOnce(t *testing.T) {
	resetProviderCache(t)
	reg := newRecordingRegistry(t, false)
	digest := reg.push(t, "acme/pg:16.1.0")
	ref := "oci://" + reg.host + "/acme/pg@" + digest
	auth := &authn.AuthConfig{Username: "u", Password: "p"}

	for range 2 {
		r := testLoader().ConfigRefResolver("oci://"+reg.host+"/acme/app:1.0.0", auth, "ns/pull@7")
		if _, err := r.ResolveBundle(t.Context(), ref); err != nil {
			t.Fatal(err)
		}
	}
	if _, manifests := reg.seen(); manifests != 1 {
		t.Errorf("manifest reads = %d, want 1", manifests)
	}
}

// A tag ref within the TTL pulls once per credential identity.
func TestResolver_TagRefPullsOncePerCredentialIdentity(t *testing.T) {
	resetProviderCache(t)
	reg := newRecordingRegistry(t, false)
	reg.push(t, "acme/pg:16.1.0")
	ref := "oci://" + reg.host + "/acme/pg:16.1.0"
	contractRef := "oci://" + reg.host + "/acme/app:1.0.0"
	auth := &authn.AuthConfig{Username: "u", Password: "p"}

	pulls := func() int { _, m := reg.seen(); return m }
	resolve := func(credID string) {
		t.Helper()
		if _, err := testLoader().ConfigRefResolver(contractRef, auth, credID).ResolveBundle(t.Context(), ref); err != nil {
			t.Fatal(err)
		}
	}
	resolve("ns/a@1")
	first := pulls()
	resolve("ns/a@1")
	if pulls() != first {
		t.Fatalf("the same credential identity pulled again within the TTL")
	}
	resolve("ns/b@1")
	if pulls() == first {
		t.Fatalf("another credential identity was served from the first one's pull")
	}
}

func TestRefCache_ExpiresAndEvicts(t *testing.T) {
	c := newRefCache(2)
	past := time.Now().Add(-time.Second)
	c.put("expired", nil, nil, past)
	if _, _, ok := c.get("expired"); ok {
		t.Error("an expired entry was served")
	}
	c.put("a", nil, nil, time.Time{})
	c.put("b", nil, nil, time.Time{})
	_, _, _ = c.get("a") // a is now the most recent
	c.put("c", nil, nil, time.Time{})
	if _, _, ok := c.get("b"); ok {
		t.Error("the least recently used entry survived eviction")
	}
	c.put("a", nil, errors.New("replaced"), time.Time{})
	if _, err, ok := c.get("a"); !ok || err == nil {
		t.Error("a replaced entry did not keep its new value")
	}
}

func TestRegistryHost(t *testing.T) {
	if got := registryHost("ghcr.io/acme/pg:1.0.0"); got != "ghcr.io" {
		t.Errorf("registryHost = %q, want ghcr.io", got)
	}
	if got := registryHost("UPPER/bad ref"); got != "" {
		t.Errorf("registryHost of an unparsable ref = %q, want empty", got)
	}
}

// A provider's ref to an internal address other than the contract's registry is refused before
// any request, unless PACTO_INSECURE_REGISTRIES lists it.
func TestResolver_RefusesAnInternalHost(t *testing.T) {
	resetProviderCache(t)
	reg := newRecordingRegistry(t, false)
	reg.push(t, "acme/pg:16.1.0")
	r := testLoader().ConfigRefResolver("oci://ghcr.io/acme/app:1.0.0", nil, "")
	for _, ref := range []string{
		"oci://" + reg.host + "/acme/pg:16.1.0", "oci://10.0.0.12:2379/x:1", "oci://kubernetes.default.svc/x:1",
		"oci://registry:5000/x:1", "oci://[fe80::1]:5000/x:1", "oci://db.cluster.local/x:1", "oci://169.254.169.254/x:1",
	} {
		if _, err := r.ResolveBundle(t.Context(), ref); !errors.Is(err, errInternalConfigRef) {
			t.Errorf("ResolveBundle(%q) error = %v, want %v", ref, err, errInternalConfigRef)
		}
	}
	if auths, _ := reg.seen(); len(auths) != 0 {
		t.Errorf("the internal registry saw %d requests", len(auths))
	}
	if internalHost("ghcr.io") || internalHost("registry.example.com:443") || internalHost("8.8.8.8") {
		t.Error("a public host reads as internal")
	}
}

// A registry's error body reaches the caller as one bounded line.
func TestBoundPullError(t *testing.T) {
	long := strings.Repeat("x", 500)
	if got := boundPullError("unexpected status 500: " + long + "\nsecret"); len(got) != maxPullErrorBytes+3 || strings.Contains(got, "secret") {
		t.Errorf("bounded = %q", got)
	}
	if got := boundPullError("short"); got != "short" {
		t.Errorf("short = %q", got)
	}
}

// A name that resolves to an internal address passes the host check, so the dialer refuses it
// after resolution; a host PACTO_INSECURE_REGISTRIES lists is dialed as it is.
func TestPublicOnlyTransport_RefusesAResolvedInternalAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	named := "localhost:" + port // a name, resolved by the dialer to a loopback address

	get := func(rt http.RoundTripper) error {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+named+"/v2/", nil)
		resp, err := rt.RoundTrip(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	if err := get(publicOnlyTransport(nil)); err == nil || !strings.Contains(err.Error(), errInternalConfigRef.Error()) {
		t.Errorf("dial of a name resolving to loopback: error = %v, want %v", err, errInternalConfigRef)
	}
	if err := get(publicOnlyTransport([]string{named})); err != nil {
		t.Errorf("dial of a listed host: %v", err)
	}
}

// A registry's token realm whose name resolves to an internal address is never fetched: the realm
// request goes through the same dialer as the registry's own. (go-containerregistry already refuses
// a realm that is a literal internal IP.)
func TestResolver_RefusesAnInternalTokenRealm(t *testing.T) {
	resetProviderCache(t)
	var realmHits int
	var mu sync.Mutex
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		realmHits++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"token":"t"}`))
	}))
	t.Cleanup(realm.Close)
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+strings.Replace(realm.URL, "127.0.0.1", "localhost", 1)+`/token",service="reg"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(reg.Close)
	host := strings.TrimPrefix(reg.URL, "http://")
	t.Setenv(InsecureRegistriesEnvVar, host) // the registry is listed; its realm is not

	r := testLoader().ConfigRefResolver("oci://ghcr.io/acme/app:1.0.0", nil, "")
	if _, err := r.ResolveBundle(t.Context(), "oci://"+host+"/acme/pg:16.1.0"); err == nil {
		t.Fatal("pull through an unreachable token realm succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if realmHits != 0 {
		t.Errorf("the internal token realm saw %d requests", realmHits)
	}
}

func TestRefuseInternalIP(t *testing.T) {
	for addr, refused := range map[string]bool{"127.0.0.1:443": true, "[::1]:443": true, "169.254.169.254:80": true, "8.8.8.8:443": false, "garbage": true} {
		if err := refuseInternalIP("tcp", addr, nil); (err != nil) != refused {
			t.Errorf("refuseInternalIP(%q) = %v, want refused %v", addr, err, refused)
		}
	}
}
