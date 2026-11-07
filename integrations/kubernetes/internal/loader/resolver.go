/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"container/list"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// errLocalConfigRef refuses a configuration ref that is not an OCI reference: a contract the
// operator loaded cannot name a path on the operator's filesystem.
var errLocalConfigRef = errors.New("configuration references resolve only from a registry")

// errInternalConfigRef refuses a configuration ref on an internal address (loopback, private,
// link-local, a cluster or single-label name) other than the contract's own registry, and any dial
// such a pull makes to an address that resolves internal: a provider bundle's author must not make
// the operator send requests into the cluster network. List the host in PACTO_INSECURE_REGISTRIES
// to allow it.
var errInternalConfigRef = errors.New("configuration references to an internal registry host resolve only when it is listed in " + InsecureRegistriesEnvVar)

// maxPullErrorBytes bounds the registry error text a resolve reports, so a registry's response
// body never reaches status.
const maxPullErrorBytes = 200

// CredIDDefault and CredIDAnonymous name the credentials a provider bundle was pulled with when
// no pull secret applies: the operator's default keychain, or none at all.
const (
	CredIDDefault   = "default"
	CredIDAnonymous = "anonymous"
)

// RefResolver resolves the configuration refs of one contract to their provider bundles
// ([validation.BundleResolver]). Credentials are scoped by registry host: a ref on ContractHost,
// the host of spec.contractRef.oci, is pulled with exactly what that ref is pulled with (Auth, or
// the loader's default keychain when Auth is nil); a ref on any other host is pulled anonymously,
// so a contract author cannot send the namespace's pull secret, or the operator's own
// credentials, to a host of their choosing. CredID names the credentials behind Auth
// (`<namespace>/<secret>@<resourceVersion>`, or [CredIDDefault]) and keys the pull cache.
type RefResolver struct {
	L            *Loader
	ContractHost string
	Auth         *authn.AuthConfig
	CredID       string
}

var _ validation.BundleResolver = (*RefResolver)(nil)

// ConfigRefResolver returns the resolver for the configuration refs of a contract loaded from
// contractRef with auth, whose credentials credID names. An inline contract (empty contractRef)
// has no registry host, so every ref it names is pulled anonymously.
func (l *Loader) ConfigRefResolver(contractRef string, auth *authn.AuthConfig, credID string) validation.BundleResolver {
	host := ""
	if contractRef != "" {
		host = registryHost(normalizeOCIRef(contractRef))
	}
	return &RefResolver{L: l, ContractHost: host, Auth: auth, CredID: credID}
}

// ResolveBundle pulls the bundle ref names, from the pull cache when it holds it.
func (r *RefResolver) ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error) {
	if !strings.HasPrefix(ref, "oci://") {
		return nil, errLocalConfigRef
	}
	ociRef := normalizeOCIRef(ref)
	host := registryHost(ociRef)
	var opts []oci.ClientOption
	if host != r.ContractHost {
		allowed := oci.EnvInsecureRegistries()
		if internalHost(host) && !slices.Contains(allowed, host) {
			return nil, errInternalConfigRef
		}
		opts = []oci.ClientOption{oci.WithTransport(publicOnlyTransport(allowed))}
	}
	kc, credID := r.keychain(host)
	key, pinned := refCacheKey(ociRef, credID)
	if b, err, ok := providerCache.get(key); ok {
		return b, err
	}
	var b *contract.Bundle
	res, err := r.L.oci.pullWith(ctx, ociRef, kc, opts...)
	if err == nil {
		b = &contract.Bundle{Contract: res.Contract, RawYAML: res.RawYAML, FS: res.BundleFS}
	} else {
		err = errors.New(boundPullError(err.Error()))
	}
	expires := time.Now().Add(r.L.cacheTTL)
	if pinned && err == nil {
		expires = time.Time{} // a digest names immutable content
	}
	providerCache.put(key, b, err, expires)
	return b, err
}

// keychain picks the credentials for a ref on host, and the identity the cache keys them by.
func (r *RefResolver) keychain(host string) (authn.Keychain, string) {
	if host == "" || host != r.ContractHost {
		return anonymousKeychain{}, CredIDAnonymous
	}
	if r.Auth == nil {
		return r.L.oci.keychain, CredIDDefault
	}
	return r.L.oci.effectiveKeychain(r.Auth), r.CredID
}

// internalHost reports whether a registry host names an internal address: a loopback, private,
// link-local or unspecified IP, localhost, a single-label name or a cluster-local suffix.
func internalHost(host string) bool {
	h := host
	if hp, _, err := net.SplitHostPort(host); err == nil {
		h = hp
	}
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	if ip := net.ParseIP(h); ip != nil {
		return internalIP(ip)
	}
	if !strings.Contains(h, ".") {
		return true // localhost, or a short in-cluster service name
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".svc", ".cluster.local"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return false
}

// internalIP reports whether ip is a loopback, private, link-local or unspecified address.
func internalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// publicOnlyTransport dials public addresses only, so a name that resolves to an internal address,
// a registry's token realm and a redirect all stay out of the cluster network. It dials the hosts
// allowed lists (PACTO_INSECURE_REGISTRIES) and the proxy the environment names as they are.
func publicOnlyTransport(allowed []string) http.RoundTripper {
	open := append(slices.Clone(allowed), proxyHosts()...)
	guarded := &net.Dialer{Timeout: 30 * time.Second, Control: refuseInternalIP}
	plain := &net.Dialer{Timeout: 30 * time.Second}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true // ponytail: one transport per pull, so no idle connection outlives it
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil && (slices.Contains(open, addr) || slices.Contains(open, host)) {
			return plain.DialContext(ctx, network, addr)
		}
		return guarded.DialContext(ctx, network, addr)
	}
	return t
}

// refuseInternalIP is a [net.Dialer] Control func: it runs after name resolution, on the address
// about to be dialed, and refuses an internal one.
func refuseInternalIP(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if ip := net.ParseIP(host); err != nil || ip == nil || internalIP(ip) {
		return errInternalConfigRef
	}
	return nil
}

// proxyHosts are the proxy hosts the environment configures for registry requests.
func proxyHosts() []string {
	var hosts []string
	for _, scheme := range []string{"http", "https"} {
		u, err := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: scheme, Host: "registry.invalid"}})
		if err == nil && u != nil {
			hosts = append(hosts, u.Hostname())
		}
	}
	return hosts
}

// boundPullError is a registry error's first line, at most maxPullErrorBytes long.
func boundPullError(msg string) string {
	msg, _, _ = strings.Cut(msg, "\n")
	if len(msg) > maxPullErrorBytes {
		msg = strings.ToValidUTF8(msg[:maxPullErrorBytes], "") + "..."
	}
	return msg
}

// registryHost is the registry host of an OCI reference, or "" when it does not parse.
func registryHost(ref string) string {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return ""
	}
	return parsed.Context().RegistryStr()
}

// refCacheKey keys a pull by reference and credential identity, and reports whether the ref is
// pinned by digest.
func refCacheKey(ref, credID string) (string, bool) {
	return ref + "\x00" + credID, strings.Contains(ref, "@sha256:")
}

// anonymousKeychain resolves every registry to no credentials.
type anonymousKeychain struct{}

func (anonymousKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	return authn.Anonymous, nil
}

// providerCacheSize bounds how many provider pulls the cache holds.
const providerCacheSize = 256

// providerCache is shared by every reconcile, so two reconciles of one contract pull a provider once.
var providerCache = newRefCache(providerCacheSize)

// refCache is a bounded LRU of provider pulls. An entry with a zero expiry never expires.
type refCache struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[string]*list.Element
}

type refCacheEntry struct {
	key     string
	bundle  *contract.Bundle
	err     error
	expires time.Time
}

func newRefCache(maxEntries int) *refCache {
	return &refCache{max: maxEntries, order: list.New(), items: map[string]*list.Element{}}
}

func (c *refCache) get(key string) (*contract.Bundle, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, nil, false
	}
	e := el.Value.(*refCacheEntry)
	if !e.expires.IsZero() && time.Now().After(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		return nil, nil, false
	}
	c.order.MoveToFront(el)
	return e.bundle, e.err, true
}

func (c *refCache) put(key string, b *contract.Bundle, err error, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.order.Remove(el)
	}
	c.items[key] = c.order.PushFront(&refCacheEntry{key: key, bundle: b, err: err, expires: expires})
	if c.order.Len() > c.max {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*refCacheEntry).key)
	}
}
