package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const goldenPathYAML = `pactoVersion: "2.4"
service:
  name: http-service
  version: 1.0.0
  owner: {team: platform}
  summary: An HTTP API
  template: {}
`

// writeGoldenPath writes a golden path directory with extra appended to its
// contract, plus a file its .pactoignore keeps out of the bundle.
func writeGoldenPath(t *testing.T, extra string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "paths", "http-service")
	_ = os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	for name, data := range map[string]string{
		"pacto.yaml":          goldenPathYAML + extra,
		"docs/runbook.md":     "# Runbook\n",
		ignore.IgnoreFileName: "secret.txt\n",
		"secret.txt":          "not in the bundle\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func dirHash(t *testing.T, dir string) string {
	t.Helper()
	d := os.DirFS(dir)
	m, _ := ignore.Load(d)
	h, err := lock.HashFS(ignore.FS(d, m))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestResolveTemplate_Directory: a directory gives the content hash of what a
// bundle of it holds, an unknown signature and an origin ref relative to the
// created contract, or its published coordinate.
func TestResolveTemplate_Directory(t *testing.T) {
	from := writeGoldenPath(t, "")
	target := filepath.Join(t.TempDir(), "gift-cards")
	tpl, err := (&Service{}).resolveTemplate(t.Context(), from, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Digest != dirHash(t, from) {
		t.Errorf("digest %s, want the hash of the ignore-filtered dir", tpl.Digest)
	}
	if _, err := fs.Stat(tpl.FS, "secret.txt"); err == nil {
		t.Error("an ignored file is in the path's files")
	}
	if tpl.Signature.Status != fleet.StepStatusUnknown || tpl.Signature.Detail != "a directory carries no signature" {
		t.Errorf("signature %+v", tpl.Signature)
	}
	if want, _ := filepath.Rel(target, from); tpl.Ref != filepath.ToSlash(want) || strings.HasPrefix(tpl.Ref, "/") || strings.Contains(tpl.Ref, from) {
		t.Errorf("ref %q is not relative to the target", tpl.Ref)
	}

	published := writeGoldenPath(t, "publish:\n  bundle: oci://ghcr.io/acme/paths/http-service\n")
	if tpl, err = (&Service{}).resolveTemplate(t.Context(), published, target, nil); err != nil || tpl.Ref != "oci://ghcr.io/acme/paths/http-service:1.0.0" {
		t.Fatalf("published coordinate: %+v %v", tpl, err)
	}
}

// TestDirTemplateErrors: a directory with no contract, an unreadable
// .pactoignore or an unreadable file is refused.
func TestDirTemplateErrors(t *testing.T) {
	target := filepath.Join(t.TempDir(), "gift-cards")
	missing := t.TempDir()
	if _, err := dirTemplate(missing, missing, target); err == nil || !strings.Contains(err.Error(), "reading the golden path") {
		t.Errorf("no pacto.yaml: %v", err)
	}
	badIgnore := t.TempDir()
	_ = os.WriteFile(filepath.Join(badIgnore, ignore.IgnoreFileName), nil, 0)
	if _, err := dirTemplate(badIgnore, badIgnore, target); err == nil {
		t.Error("an unreadable .pactoignore was accepted")
	}
	unreadable := writeGoldenPath(t, "")
	_ = os.Chmod(filepath.Join(unreadable, "docs", "runbook.md"), 0)
	if _, err := dirTemplate(unreadable, unreadable, target); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("an unreadable file: %v", err)
	}
}

// TestResolveTemplate_NotAGoldenPath: a contract with no service.template is
// refused, from a directory or a registry.
func TestResolveTemplate_NotAGoldenPath(t *testing.T) {
	dir := t.TempDir()
	plain := "pactoVersion: \"2.1\"\nservice: {name: payments, version: 1.0.0}\n"
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(plain), 0o644)
	if _, err := (&Service{}).resolveTemplate(t.Context(), dir, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "not a golden path") {
		t.Errorf("directory: %v", err)
	}
	store := &mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) {
		return &contract.Bundle{RawYAML: []byte(plain)}, nil
	}}
	if _, err := (&Service{BundleStore: store}).resolveTemplate(t.Context(), "oci://ghcr.io/acme/payments:1.0.0", "", nil); err == nil || !strings.Contains(err.Error(), "not a golden path") {
		t.Errorf("registry: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("service: ["), 0o644)
	if _, err := (&Service{}).resolveTemplate(t.Context(), dir, t.TempDir(), nil); err == nil {
		t.Error("an unparsable contract was accepted")
	}
}

func goldenBundle() *contract.Bundle {
	return &contract.Bundle{RawYAML: []byte(goldenPathYAML), FS: fstest.MapFS{"pacto.yaml": {Data: []byte(goldenPathYAML)}}}
}

const pathDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// TestResolveTemplate_OCIPullsByDigest: the tag is resolved once and the path
// pulled by that manifest digest; the origin keeps the ref as given and the
// content hash.
func TestResolveTemplate_OCIPullsByDigest(t *testing.T) {
	var pulled []string
	store := &mockBundleStore{
		ResolveFn: func(context.Context, string) (string, error) { return pathDigest, nil },
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			pulled = append(pulled, ref)
			return goldenBundle(), nil
		},
	}
	svc := &Service{BundleStore: store}
	tpl, err := svc.resolveTemplate(t.Context(), "oci://ghcr.io/acme/paths/http-service:1.0.0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pulled) != 1 || pulled[0] != "ghcr.io/acme/paths/http-service@"+pathDigest {
		t.Errorf("pulled %v", pulled)
	}
	if tpl.Ref != "oci://ghcr.io/acme/paths/http-service:1.0.0" || tpl.Signature.Detail != noTemplateKey {
		t.Errorf("template %+v", tpl)
	}
	// An untagged ref records the tag it resolved to.
	if tpl, err = svc.resolveTemplate(t.Context(), "oci://ghcr.io/acme/paths/http-service", "", nil); err != nil || tpl.Ref != "oci://ghcr.io/acme/paths/http-service:1.0.0" {
		t.Errorf("untagged: %+v %v", tpl, err)
	}
	// A digest-pinned ref is pulled as given, with no resolve.
	pulled = nil
	store.ResolveFn = func(context.Context, string) (string, error) { return "", errors.New("resolved") }
	pinned := "oci://ghcr.io/acme/paths/http-service@" + pathDigest
	if tpl, err = svc.resolveTemplate(t.Context(), pinned, "", nil); err != nil || tpl.Ref != pinned || pulled[0] != contract.TrimOCIScheme(pinned) {
		t.Errorf("pinned: %+v %v %v", tpl, err, pulled)
	}
}

// TestResolveTemplate_OCIErrors: a registry that does not answer, or answers
// with a bundle that cannot be read, creates nothing.
func TestResolveTemplate_OCIErrors(t *testing.T) {
	for name, c := range map[string]struct {
		store *mockBundleStore
		want  string
	}{
		"resolve": {&mockBundleStore{ResolveFn: func(context.Context, string) (string, error) { return "", errors.New("down") }}, "resolving"},
		"tags":    {&mockBundleStore{ListTagsFn: func(context.Context, string) ([]string, error) { return nil, errors.New("down") }}, "down"},
		"pull":    {&mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) { return nil, errors.New("down") }}, "pulling"},
		"raw": {&mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) {
			return &contract.Bundle{}, nil
		}}, "reading"},
		"hash": {&mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) {
			return &contract.Bundle{RawYAML: []byte(goldenPathYAML), FS: brokenFS{}}, nil
		}}, "hashing"},
	} {
		ref := "oci://ghcr.io/acme/paths/http-service:1.0.0"
		if name == "tags" {
			ref = "oci://ghcr.io/acme/paths/http-service"
		}
		if _, err := (&Service{BundleStore: c.store}).resolveTemplate(t.Context(), ref, "", nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := (&Service{}).resolveTemplate(t.Context(), "oci://ghcr.io/acme/paths/http-service:1.0.0", "", nil); err == nil {
		t.Error("no bundle store: pulled anyway")
	}
}

// brokenFS lists a file it cannot read.
type brokenFS struct{}

func (brokenFS) Open(name string) (fs.File, error) {
	if name == "." {
		return fstest.MapFS{"broken": {}}.Open(".")
	}
	return nil, errors.New("unreadable")
}

// signedRegistry is an in-process registry holding the golden path, signed by
// key unless key is nil.
type signedRegistry struct {
	*provRegistry
	repo, digest string
	svc          *Service
}

func pushGoldenPath(t *testing.T, key ed25519.PrivateKey) *signedRegistry {
	t.Helper()
	reg := provenanceTestRegistry(t)
	client := oci.NewClient(anonymous, oci.WithInsecureRegistries(reg.host))
	c, err := contract.Parse(strings.NewReader(goldenPathYAML))
	if err != nil {
		t.Fatal(err)
	}
	b := goldenBundle()
	b.Contract = c
	repo := reg.host + "/paths/http-service"
	digest, err := client.Push(t.Context(), repo+":1.0.0", b)
	if err != nil {
		t.Fatal(err)
	}
	if key != nil {
		kp, _ := signing.FromPrivateKey(key)
		if err := publish.SignDigest(t.Context(), reg.client, signing.Keyed(kp, ""), repo, digest); err != nil {
			t.Fatal(err)
		}
	}
	return &signedRegistry{reg, repo, digest, &Service{BundleStore: oci.NewCachedStore(client),
		sys: sysDeps{publishRegistry: func() publish.Registry { return reg.client }}}}
}

func templateKey(t *testing.T, name string, pub any, repos ...string) delivery.TrustedKey {
	t.Helper()
	pem, err := delivery.PublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	return delivery.TrustedKey{Name: name, PublicKey: pem, Role: delivery.RoleTemplate, Streams: repos}
}

// TestVerifyTemplateSignature: only a template key whose repository pattern
// matches and that verifies the signature makes the step done; with template
// keys configured anything else refuses; with none the step is unknown.
func TestVerifyTemplateSignature(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	signed := pushGoldenPath(t, key)
	unsigned := pushGoldenPath(t, nil)
	recordKey := templateKey(t, "recorder", pub)
	recordKey.Role = ""
	for _, c := range []struct {
		name   string
		reg    *signedRegistry
		keys   []delivery.TrustedKey
		status string
		err    string
	}{
		{"no trust", signed, nil, fleet.StepStatusUnknown, ""},
		{"record-only trust", signed, []delivery.TrustedKey{recordKey}, fleet.StepStatusUnknown, ""},
		{"template key, valid signature", signed, []delivery.TrustedKey{templateKey(t, "paths", other), templateKey(t, "platform", pub)}, fleet.StepStatusDone, ""},
		{"template key, no signature", unsigned, []delivery.TrustedKey{templateKey(t, "platform", pub)}, "", "no signature"},
		{"template key, another signer", signed, []delivery.TrustedKey{templateKey(t, "paths", other)}, "", "no signature verifies"},
		{"template key, repository pattern misses", signed, []delivery.TrustedKey{templateKey(t, "platform", pub, "ghcr.io/acme/*")}, "", "no template key"},
		{"template key that does not open", signed, []delivery.TrustedKey{{Name: "broken", PublicKey: "x", Role: delivery.RoleTemplate}}, "", "broken"},
	} {
		var trust *delivery.TrustConfig
		if c.keys != nil {
			trust = &delivery.TrustConfig{Keys: c.keys}
		}
		step, err := c.reg.svc.verifyTemplate(t.Context(), c.reg.repo, c.reg.digest, trust)
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: err %v, want %q", c.name, err, c.err)
		case c.err == "" && (err != nil || step.Status != c.status):
			t.Errorf("%s: %+v %v", c.name, step, err)
		case c.status == fleet.StepStatusDone && step.Detail != "signed by platform, a template key of the trust config":
			t.Errorf("%s: detail %q", c.name, step.Detail)
		}
	}
	signed.failWhen(func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") })
	if _, err := signed.svc.verifyTemplate(t.Context(), signed.repo, signed.digest, &delivery.TrustConfig{Keys: []delivery.TrustedKey{templateKey(t, "platform", pub)}}); err == nil {
		t.Error("a referrers error verified")
	}
}

// TestKeyName: a key is named by its name, its keyless identity or both.
func TestKeyName(t *testing.T) {
	for want, k := range map[string]delivery.TrustedKey{
		"platform":                         {Name: "platform"},
		"ci@acme from https://issuer":      {Identity: "ci@acme", Issuer: "https://issuer"},
		"ci (ci@acme from https://issuer)": {Name: "ci", Identity: "ci@acme", Issuer: "https://issuer"},
		"the template key":                 {},
	} {
		if got := keyName(k); got != want {
			t.Errorf("keyName = %q, want %q", got, want)
		}
	}
}

// cachedStore serves poisoned bytes from its cache and the registry's from the
// store it wraps.
type cachedStore struct {
	oci.BundleStore
	inner oci.BundleStore
}

func (c cachedStore) Pull(context.Context, string) (*contract.Bundle, error) {
	y := []byte(goldenPathYAML + "metadata: {poisoned: \"yes\"}\n")
	return &contract.Bundle{RawYAML: y, FS: fstest.MapFS{"pacto.yaml": {Data: y}}}, nil
}

func (c cachedStore) Uncached() oci.BundleStore { return c.inner }

// pinnedStore answers a pinned pull with another digest.
type pinnedStore struct{ oci.BundleStore }

func (pinnedStore) PullPinned(context.Context, string) (*contract.Bundle, string, error) {
	return goldenBundle(), pathDigest, nil
}

// TestResolveTemplate_TrustBypassesCache: with template keys the path is read
// from the registry past the cache, and a digest other than the resolved one
// refuses; without them the cache answers.
func TestResolveTemplate_TrustBypassesCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	reg := pushGoldenPath(t, key)
	trust := &delivery.TrustConfig{Keys: []delivery.TrustedKey{templateKey(t, "platform", pub)}}
	client := reg.svc.BundleStore.(*oci.CachedStore).Uncached()
	reg.svc.BundleStore = cachedStore{client, client}
	ref := "oci://" + reg.repo + ":1.0.0"
	tpl, err := reg.svc.resolveTemplate(t.Context(), ref, "", trust)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := fs.ReadFile(tpl.FS, "pacto.yaml"); strings.Contains(string(raw), "poisoned") || tpl.Signature.Status != fleet.StepStatusDone {
		t.Fatalf("the cache answered a verified pull: %s %+v", raw, tpl.Signature)
	}
	if tpl, err = reg.svc.resolveTemplate(t.Context(), ref, "", nil); err != nil {
		t.Fatal(err)
	} else if raw, _ := fs.ReadFile(tpl.FS, "pacto.yaml"); !strings.Contains(string(raw), "poisoned") {
		t.Error("without trust the cache was bypassed")
	}
	stranger, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := reg.svc.resolveTemplate(t.Context(), ref, "", &delivery.TrustConfig{Keys: []delivery.TrustedKey{templateKey(t, "paths", stranger)}}); err == nil {
		t.Error("a path no template key signed was read")
	}
	reg.svc.BundleStore = cachedStore{client, pinnedStore{client}}
	if _, err := reg.svc.resolveTemplate(t.Context(), ref, "", trust); err == nil || !strings.Contains(err.Error(), "answered") {
		t.Errorf("another digest: %v", err)
	}
	reg.svc.BundleStore = pinnedStore{client} // no cache to read past
	if _, err := reg.svc.resolveTemplate(t.Context(), ref, "", trust); err == nil {
		t.Error("another digest from an uncached store was accepted")
	}
}

// TestOriginDigestIsContentHash: the origin digest is the content hash of the
// pulled path, the identity a local fleet source gives it, not its manifest
// digest.
func TestOriginDigestIsContentHash(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := pushGoldenPath(t, nil)
	tpl, err := reg.svc.resolveTemplate(t.Context(), "oci://"+reg.repo+":1.0.0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := lock.HashFS(tpl.FS)
	if tpl.Digest != want || tpl.Digest == reg.digest {
		t.Errorf("origin digest %s, content hash %s, manifest %s", tpl.Digest, want, reg.digest)
	}
}

// TestInitFrom: a service is created from a directory with an Unknown line
// for its local origin, and refused before anything is read when an option
// cannot go with a golden path.
func TestInitFrom(t *testing.T) {
	from := writeGoldenPath(t, "")
	target := filepath.Join(t.TempDir(), "gift-cards")
	r, err := (&Service{}).InitFrom(t.Context(), InitOptions{Dir: target, Owner: "checkout"}, InitFrom{From: from})
	if err != nil {
		t.Fatal(err)
	}
	if r.Unknown[len(r.Unknown)-1] != localOrigin {
		t.Errorf("unknown: %v", r.Unknown)
	}
	if _, err := os.Stat(filepath.Join(target, "docs", "runbook.md")); err != nil {
		t.Error(err)
	}
	if _, err := (&Service{}).InitFrom(t.Context(), InitOptions{Dir: target}, InitFrom{From: "oci://unreachable/x:1"}); err == nil || !strings.Contains(err.Error(), "--owner") {
		t.Errorf("no owner: %v", err)
	}
	// With a bundle store the created contract is validated with the resolver.
	stored := filepath.Join(t.TempDir(), "gift-cards")
	if r, err = (&Service{BundleStore: &mockBundleStore{}}).InitFrom(t.Context(), InitOptions{Dir: stored, Owner: "checkout", Plan: true}, InitFrom{From: from}); err != nil || r.Steps[2].Status != fleet.StepStatusDone {
		t.Errorf("with a resolver: %+v %v", r, err)
	}
	if _, err := (&Service{sys: sysDeps{absPath: func(p string) (string, error) {
		if p == from {
			return "", errors.New("no working directory")
		}
		return filepath.Abs(p)
	}}}).InitFrom(t.Context(), InitOptions{Dir: stored, Owner: "checkout"}, InitFrom{From: from}); err == nil {
		t.Error("an unresolvable golden path folder was accepted")
	}
	broken := &Service{sys: sysDeps{absPath: func(string) (string, error) { return "", errors.New("no working directory") }}}
	if _, err := broken.InitFrom(t.Context(), InitOptions{Name: "x", Owner: "checkout"}, InitFrom{From: from}); err == nil {
		t.Error("an unresolvable folder was accepted")
	}
	if _, err := (&Service{}).InitFrom(t.Context(), InitOptions{Dir: t.TempDir(), Owner: "checkout"}, InitFrom{From: t.TempDir()}); err == nil {
		t.Error("a folder with no golden path was accepted")
	}
}

// TestTemplateStrictValidation_UnresolvedRef: with no bundle store a
// configuration ref cannot resolve, and creation is refused with nothing
// written.
func TestTemplateStrictValidation_UnresolvedRef(t *testing.T) {
	from := writeGoldenPath(t, "configurations:\n  - name: database\n    ref: oci://ghcr.io/acme/postgresql:1.0.0\n    required: true\n")
	target := filepath.Join(t.TempDir(), "gift-cards")
	_, err := (&Service{}).InitFrom(t.Context(), InitOptions{Dir: target, Owner: "checkout"}, InitFrom{From: from})
	if err == nil || !strings.Contains(err.Error(), "CONFIG_REF_UNRESOLVED") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "pacto.yaml")); err == nil {
		t.Error("a refused creation wrote pacto.yaml")
	}
}

// TestInitFromRunsPlugins: the plugins the caller names run through the
// service's runner, resolved with plugin.Find, and never under a trust config
// whose template key did not verify the path.
func TestInitFromRunsPlugins(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pacto-plugin-test"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	from := writeGoldenPath(t, "")
	_ = os.WriteFile(filepath.Join(from, "pacto.yaml"), []byte(strings.Replace(goldenPathYAML, "template: {}", "template:\n    plugins:\n      - name: test", 1)), 0o644)
	target := filepath.Join(t.TempDir(), "gift-cards")
	svc := &Service{PluginRunner: &mockPluginRunner{}}
	r, err := svc.InitFrom(t.Context(), InitOptions{Dir: target, Owner: "checkout"}, InitFrom{From: from, RunPlugins: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Steps[3]; s.Status != fleet.StepStatusDone || !strings.Contains(s.Detail, filepath.Join(bin, "pacto-plugin-test")) {
		t.Errorf("plugin step: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(target, "out.txt")); err != nil {
		t.Error(err)
	}
	trust := &delivery.TrustConfig{Keys: []delivery.TrustedKey{{Role: delivery.RoleTemplate}}}
	_, err = svc.InitFrom(t.Context(), InitOptions{Dir: filepath.Join(t.TempDir(), "x"), Owner: "checkout"}, InitFrom{From: from, Trust: trust, RunPlugins: []string{"test"}})
	if err == nil || !strings.Contains(err.Error(), "signature a template key verified") {
		t.Errorf("under trust: %v", err)
	}
}

// TestDirTemplateReadsOnlyRegularFiles pins that a directory golden path is
// read like a pulled bundle: a symlink, even to a file outside the folder, is
// refused, and so is a file over a bundle's size limits.
func TestDirTemplateReadsOnlyRegularFiles(t *testing.T) {
	target := filepath.Join(t.TempDir(), "gift-cards")
	secret := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(secret, []byte("token"), 0o600)

	linked := writeGoldenPath(t, "")
	if err := os.Symlink(secret, filepath.Join(linked, "docs", "notes.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := dirTemplate(linked, linked, target); err == nil || !strings.Contains(err.Error(), "docs/notes.md is not a regular file") {
		t.Errorf("symlink: %v", err)
	}

	linkedIgnore := writeGoldenPath(t, "")
	_ = os.Remove(filepath.Join(linkedIgnore, ignore.IgnoreFileName))
	_ = os.Symlink(secret, filepath.Join(linkedIgnore, ignore.IgnoreFileName))
	if _, err := dirTemplate(linkedIgnore, linkedIgnore, target); err == nil || !strings.Contains(err.Error(), ".pactoignore is not a regular file") {
		t.Errorf("symlinked .pactoignore: %v", err)
	}

	for name, sizes := range map[string][]int64{"one file": {oci.MaxFileSize + 1}, "in all": {9 << 20, 9 << 20, 9 << 20, 9 << 20, 9 << 20, 9 << 20}} {
		big := writeGoldenPath(t, "")
		for i, size := range sizes {
			p := filepath.Join(big, "blob"+string(rune('a'+i)))
			if err := os.WriteFile(p, nil, 0o644); err != nil || os.Truncate(p, size) != nil {
				t.Fatal(err)
			}
		}
		if _, err := dirTemplate(big, big, target); err == nil || !strings.Contains(err.Error(), "over the size a bundle may hold") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestLocalRef pins that a directory origin never spells out host folders
// above the home folder, such as /Users/<name>.
func TestLocalRef(t *testing.T) {
	t.Setenv("HOME", "/Users/me")
	for _, tc := range []struct{ from, target, want string }{
		{"/Users/me/tmp-gp/path", "/tmp/out3", "path"},
		{"/Users/other/paths/http", "/Users/me/svc", "http"},
		{"/Users/me/paths/http", "/Users/me/svc/out", "../../paths/http"},
		{"/Users/me/b", "/Users/me/a", "../b"},
		{"/tmp/paths/http", "/tmp/out", "../paths/http"},
	} {
		if got := localRef(filepath.FromSlash(tc.from), filepath.FromSlash(tc.target)); got != tc.want {
			t.Errorf("localRef(%s, %s) = %q, want %q", tc.from, tc.target, got, tc.want)
		}
	}
}

// TestInitFromPublishedDirectory pins that a directory whose contract names
// its published coordinate says that coordinate was never checked.
func TestInitFromPublishedDirectory(t *testing.T) {
	from := writeGoldenPath(t, "publish:\n  bundle: oci://ghcr.io/acme/paths/http-service\n")
	r, err := (&Service{}).InitFrom(t.Context(), InitOptions{Dir: filepath.Join(t.TempDir(), "gift-cards"), Owner: "checkout", Plan: true}, InitFrom{From: from})
	if err != nil {
		t.Fatal(err)
	}
	if r.Unknown[len(r.Unknown)-1] != publishedOrigin {
		t.Errorf("unknown: %v", r.Unknown)
	}
}
