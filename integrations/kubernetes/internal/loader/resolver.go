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
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// errLocalConfigRef refuses a configuration ref that is not an OCI reference: a contract the
// operator loaded cannot name a path on the operator's filesystem.
var errLocalConfigRef = errors.New("configuration references resolve only from a registry")

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
	kc, credID := r.keychain(registryHost(ociRef))
	key, pinned := refCacheKey(ociRef, credID)
	if b, err, ok := providerCache.get(key); ok {
		return b, err
	}
	var b *contract.Bundle
	res, err := r.L.oci.pullWith(ctx, ociRef, kc)
	if err == nil {
		b = &contract.Bundle{Contract: res.Contract, RawYAML: res.RawYAML, FS: res.BundleFS}
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
