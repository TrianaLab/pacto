package oci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

// The installation repositories: fixed names under the one registry a platform
// engineer sets with the chart's `registry` value.
const (
	// TargetsRepository holds the cluster's signed target snapshot. The operator writes it.
	TargetsRepository = "pacto-targets"
	// IndexRepository holds the organization index. The CLI (`pacto publish --index`) writes it.
	IndexRepository = "pacto-index"
	// InferredRepository holds the inferred provider contracts. The operator writes it.
	InferredRepository = "pacto-inferred"
)

// maxManifestBytes bounds how much of a manifest body the push scope reads to
// find its subject.
const maxManifestBytes = 4 << 20

// ErrOutsidePushScope is returned for a cluster-side write the push scope refuses.
var ErrOutsidePushScope = errors.New("outside the installation push scope")

// InstallationRepository returns the installation repository name under registry.
func InstallationRepository(registry, name string) string {
	return strings.TrimSuffix(registry, "/") + "/" + name
}

// PushScope is the set of repositories a cluster-side process may tag, delete or
// write subject-less manifests in. Outside it, only a referrer pushed by digest
// onto an existing manifest is written. Attach it to a repository's transport
// with [PushScope.Guard].
type PushScope struct{ prefixes []string }

// NewPushScope holds <registry>/pacto-targets, <registry>/pacto-index and
// <registry>/pacto-inferred when registry is set, plus every non-empty repos
// entry. An empty registry and no repos is a referrer-only scope.
func NewPushScope(registry string, repos ...string) *PushScope {
	var prefixes []string
	if registry != "" {
		for _, name := range []string{TargetsRepository, IndexRepository, InferredRepository} {
			prefixes = append(prefixes, normalizeRepo(InstallationRepository(registry, name)))
		}
	}
	for _, r := range repos {
		if r != "" {
			prefixes = append(prefixes, normalizeRepo(r))
		}
	}
	slices.Sort(prefixes)
	return &PushScope{prefixes: slices.Compact(prefixes)}
}

// Prefixes returns the repositories inside the scope, sorted.
func (s *PushScope) Prefixes() []string { return slices.Clone(s.prefixes) }

// Allows reports whether repo (host plus path) equals a prefix or sits under one.
func (s *PushScope) Allows(repo string) bool {
	repo = normalizeRepo(repo)
	for _, p := range s.prefixes {
		if repo == p || strings.HasPrefix(repo, p+"/") {
			return true
		}
	}
	return false
}

// Guard wraps base (nil is [http.DefaultTransport]) so that every manifest PUT
// or DELETE and every blob DELETE outside the scope is refused, except a
// manifest PUT by digest whose body carries a subject. Every other request,
// blob uploads and their continuations on any host included, passes untouched.
func (s *PushScope) Guard(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return guardTransport{scope: s, base: base}
}

type guardTransport struct {
	scope *PushScope
	base  http.RoundTripper
}

func (g guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.scope.check(req); err != nil {
		if req.Body != nil {
			_ = req.Body.Close() // a RoundTripper closes the body, even on error
		}
		return nil, err
	}
	return g.base.RoundTrip(req)
}

// check applies the scope to one request.
func (s *PushScope) check(req *http.Request) error {
	repo, kind, ref, ok := nameBearingWrite(req)
	if !ok || s.Allows(repo) {
		return nil
	}
	if kind == "manifests" && req.Method == http.MethodPut && strings.Contains(ref, ":") && hasSubject(req) {
		return nil // a referrer by digest: tags never contain ':'
	}
	allowed := "none"
	if len(s.prefixes) > 0 {
		allowed = strings.Join(s.prefixes, ", ")
	}
	return fmt.Errorf("%w: %s %s (allowed: %s)", ErrOutsidePushScope, req.Method, repo, allowed)
}

// nameBearingWrite recognises PUT/DELETE /v2/<name>/manifests/<ref> and
// DELETE /v2/<name>/blobs/<digest>, returning <host>/<name>.
func nameBearingWrite(req *http.Request) (repo, kind, ref string, ok bool) {
	if req.Method != http.MethodPut && req.Method != http.MethodDelete {
		return "", "", "", false
	}
	rest, found := strings.CutPrefix(req.URL.Path, "/v2/")
	if !found {
		return "", "", "", false
	}
	for _, k := range []string{"manifests", "blobs"} {
		if k == "blobs" && req.Method != http.MethodDelete {
			continue
		}
		i := strings.LastIndex(rest, "/"+k+"/")
		if i <= 0 {
			continue
		}
		ref = rest[i+len(k)+2:]
		if ref == "" || strings.Contains(ref, "/") {
			continue // e.g. blobs/uploads/<id>: an upload, which names nothing
		}
		return req.URL.Host + "/" + rest[:i], k, ref, true
	}
	return "", "", "", false
}

// hasSubject reports whether the manifest body names a subject. Without
// GetBody, or past the cap, there is nothing to prove it, so it is false.
func hasSubject(req *http.Request) bool {
	if req.GetBody == nil {
		return false
	}
	body, err := req.GetBody()
	if err != nil {
		return false
	}
	defer func() { _ = body.Close() }()
	// ponytail: 4 MiB manifest cap, the OCI distribution limit most registries enforce; raise it with the spec
	raw, err := io.ReadAll(io.LimitReader(body, maxManifestBytes+1))
	if err != nil || len(raw) > maxManifestBytes {
		return false
	}
	var m struct {
		Subject *struct {
			Digest string `json:"digest"`
		} `json:"subject"`
	}
	return json.Unmarshal(raw, &m) == nil && m.Subject != nil && m.Subject.Digest != ""
}

// normalizeRepo lowercases the host and folds index.docker.io and
// registry-1.docker.io, the host ORAS sends a docker.io request to, into
// docker.io.
func normalizeRepo(repo string) string {
	host, path, _ := strings.Cut(repo, "/")
	host = strings.ToLower(host)
	if host == "index.docker.io" || host == "registry-1.docker.io" {
		host = "docker.io"
	}
	return host + "/" + path
}
