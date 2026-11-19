package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const orgCatalog = "apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n  - identity: corp/mq\n    class: legacy\n    label: Corporate MQ\n    aliases: [legacy-mq]\n"

// ghcrRegistry serves ghcr.io repositories from a test registry, counts the
// referrer listings and holds each one until release is closed.
type ghcrRegistry struct {
	*publish.Client
	host    string
	lists   atomic.Int64
	release chan struct{}
}

func (g *ghcrRegistry) repo(r string) string {
	return strings.Replace(r, "ghcr.io/", g.host+"/", 1)
}

func (g *ghcrRegistry) ReferrerDescriptors(ctx context.Context, repo, digest string) ([]v1.Descriptor, error) {
	g.lists.Add(1)
	if g.release != nil {
		<-g.release
	}
	return g.Client.ReferrerDescriptors(ctx, g.repo(repo), digest)
}

func (g *ghcrRegistry) Referrers(ctx context.Context, repo, digest, artifactType string) ([]string, error) {
	return g.Client.Referrers(ctx, g.repo(repo), digest, artifactType)
}

func (g *ghcrRegistry) Mode(ctx context.Context, repo, digest string) (capabilities.ReferrersMode, error) {
	return g.Client.Mode(ctx, g.repo(repo), digest)
}

func (g *ghcrRegistry) Layer(ctx context.Context, repo, digest string) ([]byte, map[string]string, error) {
	return g.Client.Layer(ctx, g.repo(repo), digest)
}

// ghcrOptions configures a build whose OCI source reads ghcr.io. No store is
// configured, so the source fails without reaching the network.
func ghcrOptions(p *ProvenanceChecker) FleetOptions {
	return FleetOptions{OCIRefs: []string{"oci://ghcr.io/acme/orders:1.0.0"}, Provenance: p, LongRunning: true}
}

// defaultRef builds a fleet with opts and returns its default catalog ref.
func defaultRef(t *testing.T, s *Service, opts FleetOptions) fleet.ExternalCatalogRef {
	t.Helper()
	snap, err := s.Fleet(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.ExternalCatalogs) == 0 || snap.ExternalCatalogs[0].Origin != externalcatalog.OriginDefault {
		t.Fatalf("catalogs = %+v", snap.ExternalCatalogs)
	}
	return snap.ExternalCatalogs[0]
}

// settledRef builds until the default check is no longer running.
func settledRef(t *testing.T, s *Service, opts FleetOptions) fleet.ExternalCatalogRef {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		ref := defaultRef(t, s, opts)
		if ref.Reason != reasonCatalogChecking {
			return ref
		}
		if time.Now().After(deadline) {
			t.Fatal("the default catalog check never completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDefaultNotCheckedWithoutRegistry(t *testing.T) {
	ref := defaultRef(t, NewService(nil, nil), FleetOptions{LocalRoots: []string{t.TempDir()}, Provenance: NewProvenanceChecker()})
	if ref.Copy != externalcatalog.CopyEmbedded || ref.Signature != fleet.SignatureUnknown || !strings.Contains(ref.Reason, "ghcr.io") || ref.Digest != externalcatalog.DefaultDigest() {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestDefaultNotCheckedForPrivateRegistryOnly(t *testing.T) {
	reg := &ghcrRegistry{Client: publish.NewClient(anonymous), host: "unused.invalid"}
	s := NewService(nil, nil)
	opts := FleetOptions{OCIRefs: []string{"oci://r.example/acme/orders:1.0.0"}, CatalogRoots: []string{"oci://r.example/acme/pay:1.0.0", "./local"}, Provenance: newProvenanceChecker(reg)}
	defaultRef(t, s, opts)
	ref := defaultRef(t, s, opts)
	if reg.lists.Load() != 0 || ref.Copy != externalcatalog.CopyEmbedded || !strings.Contains(ref.Reason, "ghcr.io") {
		t.Fatalf("lists %d, ref %+v", reg.lists.Load(), ref)
	}
}

func TestReadsGHCR(t *testing.T) {
	for _, c := range []struct {
		opts FleetOptions
		want bool
	}{
		{FleetOptions{OCIRefs: []string{"ghcr.io/a/b:1"}}, true},
		{FleetOptions{OCIRefs: []string{"oci://GHCR.io/a/b@sha256:x"}}, true},
		{FleetOptions{CatalogRoots: []string{"oci://ghcr.io/a/b:1"}}, true},
		{FleetOptions{CatalogRoots: []string{"ghcr.io/a/b"}}, false}, // a local path
		{FleetOptions{OCIRefs: []string{"ghcr.io.example/a:1", "ghcr.io"}}, false},
		{FleetOptions{}, false},
	} {
		if got := readsGHCR(c.opts); got != c.want {
			t.Errorf("readsGHCR(%+v) = %v", c.opts, got)
		}
	}
}

// signDefault pushes the embedded default catalog to the test registry and
// signs it keyless as the release workflow.
func signDefault(t *testing.T, reg *provRegistry) {
	t.Helper()
	ss := sigtest.New(t)
	oldRoot, oldR := publicGoodRoot, rekorURL
	t.Cleanup(func() { publicGoodRoot, rekorURL = oldRoot, oldR })
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return ss.Root, nil }
	rekorURL = ""
	t.Setenv("SIGSTORE_ID_TOKEN", sigtest.Token(externalcatalog.DefaultSigner, externalcatalog.DefaultIssuer))
	signer, err := signing.Keyless(ss.Fulcio, ss.TSA, "", signing.IDToken(""), ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	repo := reg.host + "/trianalab/pacto/external-catalog"
	digest, err := externalcatalog.Write(t.Context(), oci.NewClient(anonymous), repo, externalcatalog.DefaultBytes())
	if err != nil || digest != externalcatalog.DefaultDigest() {
		t.Fatalf("push = %s, %v", digest, err)
	}
	if err := publish.SignDigest(t.Context(), reg.client, signer, repo, digest); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultCheckedThroughProvenance(t *testing.T) {
	reg := provenanceTestRegistry(t)
	signDefault(t, reg)
	fake := &ghcrRegistry{Client: reg.client, host: reg.host}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	checker := newProvenanceChecker(fake)
	checker.now = clock
	s := NewService(nil, nil)
	s.sys.now = clock
	opts := ghcrOptions(checker)

	ref := settledRef(t, s, opts)
	if ref.Copy != externalcatalog.CopyRegistry || ref.Signature != fleet.SignatureVerified || ref.Signer != externalcatalog.DefaultSigner || ref.Reason == "" {
		t.Fatalf("ref = %+v", ref)
	}
	if strings.Contains(ref.Reason, "contract") {
		t.Errorf("reason talks about a contract: %q", ref.Reason)
	}
	defaultRef(t, s, opts)
	if n := fake.lists.Load(); n != 1 {
		t.Fatalf("referrers listed %d times within the TTL, want 1", n)
	}
	now = now.Add(provenanceTTL + time.Second)
	settledRef(t, s, opts)
	deadline := time.Now().Add(20 * time.Second)
	for fake.lists.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := fake.lists.Load(); n != 2 {
		t.Fatalf("referrers listed %d times after the TTL, want 2", n)
	}
	// Let the check finish before the cleanups restore what it reads.
	for checking(s) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

func checking(s *Service) bool {
	s.catalogs.mu.Lock()
	defer s.catalogs.mu.Unlock()
	return s.catalogs.checking
}

// TestDefaultUnsignedKeepsEmbedded: a default catalog digest the registry
// does not hold cannot be probed, so its signature is unknown, never
// unsigned or verified, and the embedded copy stays.
func TestDefaultUnsignedKeepsEmbedded(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := settledRef(t, NewService(nil, nil), ghcrOptions(newProvenanceChecker(&ghcrRegistry{Client: reg.client, host: reg.host})))
	if ref.Copy != externalcatalog.CopyEmbedded || ref.Signature != fleet.SignatureUnknown || ref.Reason == "" {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestDefaultCheckDoesNotDelayBuild(t *testing.T) {
	reg := provenanceTestRegistry(t)
	fake := &ghcrRegistry{Client: reg.client, host: reg.host, release: make(chan struct{})}
	t.Cleanup(func() { close(fake.release) })
	s := NewService(nil, nil)
	opts := ghcrOptions(newProvenanceChecker(fake))
	start := time.Now()
	defaultRef(t, s, opts)
	ref := defaultRef(t, s, opts)
	if time.Since(start) > 10*time.Second || ref.Signature != fleet.SignatureUnknown || ref.Reason != reasonCatalogChecking || ref.Copy != externalcatalog.CopyEmbedded {
		t.Fatalf("ref = %+v after %s", ref, time.Since(start))
	}
}

func TestDefaultNotCheckedByOneShotCommand(t *testing.T) {
	reg := provenanceTestRegistry(t)
	fake := &ghcrRegistry{Client: reg.client, host: reg.host}
	s := NewService(nil, nil)
	opts := ghcrOptions(newProvenanceChecker(fake))
	opts.LongRunning = false
	ref := defaultRef(t, s, opts)
	if ref.Signature != fleet.SignatureUnknown || ref.Reason != reasonCatalogOneShot || checking(s) || fake.lists.Load() != 0 {
		t.Fatalf("a one-shot build started the check: ref = %+v, lists %d", ref, fake.lists.Load())
	}
}

func TestExtensionCancelledPullNotRemembered(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgCatalog)
	s := NewService(nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.orgCatalog(ctx, ref); err == nil {
		t.Fatal("a cancelled pull succeeded")
	}
	if _, err := s.orgCatalog(t.Context(), ref); err != nil {
		t.Fatalf("a cancelled caller's error was remembered: %v", err)
	}
}

func TestDefaultNilProvenanceIsUnknown(t *testing.T) {
	ref := defaultRef(t, NewService(nil, nil), ghcrOptions(nil))
	if ref.Copy != externalcatalog.CopyEmbedded || ref.Signature != fleet.SignatureUnknown || !strings.Contains(ref.Reason, "No provenance checker") {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestDefaultRegistryErrorKeepsEmbedded(t *testing.T) {
	reg := provenanceTestRegistry(t)
	reg.failWhen(func(*http.Request) bool { return true })
	ref := settledRef(t, NewService(nil, nil), ghcrOptions(newProvenanceChecker(&ghcrRegistry{Client: reg.client, host: reg.host})))
	if ref.Copy != externalcatalog.CopyEmbedded || ref.Signature != fleet.SignatureUnknown || !strings.Contains(ref.Reason, "could not list") {
		t.Fatalf("ref = %+v", ref)
	}
}

// pushOrgCatalog pushes body to the test registry and returns its pinned ref.
func pushOrgCatalog(t *testing.T, reg *provRegistry, body string) string {
	t.Helper()
	repo := reg.host + "/acme/external-catalog"
	digest, err := externalcatalog.Write(t.Context(), oci.NewClient(anonymous), repo, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return contract.OCIScheme + repo + "@" + digest
}

// legacyConsumer writes a local bundle that declares legacy-mq.
func legacyConsumer(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "orders")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: orders\n  version: \"1.0.0\"\ndependencies:\n  - name: legacy-mq\n    external: true\n    required: true\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func orgRef(t *testing.T, snap *fleet.FleetSnapshot) fleet.ExternalCatalogRef {
	t.Helper()
	if len(snap.ExternalCatalogs) != 2 {
		t.Fatalf("catalogs = %+v", snap.ExternalCatalogs)
	}
	return snap.ExternalCatalogs[1]
}

func TestExtensionRefNeedsDigest(t *testing.T) {
	for _, ref := range []string{"oci://r.example/c:1", "r.example/c", "oci://r.example/c@sha256:short", "oci://r.example/c@md5:" + strings.Repeat("a", 32)} {
		_, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{ExternalCatalogRef: ref})
		if err == nil || !strings.Contains(err.Error(), "@sha256:") {
			t.Errorf("%s: err = %v", ref, err)
		}
		if err := CheckExternalCatalogRef(ref); err == nil {
			t.Errorf("CheckExternalCatalogRef(%s) accepted it", ref)
		}
	}
	if err := CheckExternalCatalogRef(""); err != nil {
		t.Errorf("an unset ref is refused: %v", err)
	}
}

func TestExtensionMergedOverDefault(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgCatalog)
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{LocalRoots: []string{legacyConsumer(t)}, ExternalCatalogRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	org := orgRef(t, snap)
	repo, digest, _ := strings.Cut(contract.TrimOCIScheme(ref), "@")
	if org.Origin != externalcatalog.OriginOrganization || org.Repository != repo || org.Digest != digest || org.Copy != externalcatalog.CopyRegistry {
		t.Fatalf("org = %+v", org)
	}
	rec := snap.Externals["corp/mq"]
	if rec == nil || rec.Catalog == nil || rec.Catalog.Origin != externalcatalog.OriginOrganization || rec.Catalog.Digest != digest {
		t.Fatalf("corp/mq = %+v", rec)
	}
	if snap.Completeness == fleet.CompletenessPartial {
		t.Errorf("limitations %+v", snap.Limitations)
	}
}

func wantCatalogUnavailable(t *testing.T, snap *fleet.FleetSnapshot, err error, why string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != fleet.CompletenessPartial || len(snap.ExternalCatalogs) != 1 {
		t.Fatalf("completeness %q, catalogs %+v", snap.Completeness, snap.ExternalCatalogs)
	}
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationExternalCatalogUnavailable && strings.Contains(l.Message, why) {
			return
		}
	}
	t.Fatalf("limitations %+v, want %s naming %q", snap.Limitations, fleet.LimitationExternalCatalogUnavailable, why)
}

func TestExtensionPullFailurePartial(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgCatalog)
	reg.failWhen(func(*http.Request) bool { return true })
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{LocalRoots: []string{legacyConsumer(t)}, ExternalCatalogRef: ref})
	wantCatalogUnavailable(t, snap, err, "could not be read")
	if snap.Externals["corp/mq"] != nil || snap.Externals["legacy-mq"] == nil {
		t.Errorf("externals %v", snap.Externals)
	}
}

func TestExtensionInvalidFilePartial(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, "apiVersion: v0\n")
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{ExternalCatalogRef: ref})
	wantCatalogUnavailable(t, snap, err, "apiVersion")
}

func TestExtensionDigestMismatchPartial(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgCatalog)
	other := pushOrgCatalog(t, reg, strings.Replace(orgCatalog, "Corporate MQ", "Other MQ", 1))
	_, pinned, _ := strings.Cut(ref, "@")
	_, served, _ := strings.Cut(other, "@")
	// The registry answers the pinned digest with another manifest.
	reg.failWhen(func(r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/manifests/"+pinned) {
			r.URL.Path = strings.Replace(r.URL.Path, pinned, served, 1)
		}
		return false
	})
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{ExternalCatalogRef: ref})
	wantCatalogUnavailable(t, snap, err, "could not be read")
}

func TestExtensionSignatureNeverGatesLoading(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgCatalog)
	attach(t, reg, ref, delivery.BundleMediaType, nil) // a junk Sigstore bundle
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{LocalRoots: []string{legacyConsumer(t)}, ExternalCatalogRef: ref, Provenance: newProvenanceChecker(reg.client)})
	if err != nil {
		t.Fatal(err)
	}
	if org := orgRef(t, snap); org.Signature != fleet.SignatureUnknown || snap.Externals["corp/mq"] == nil {
		t.Fatalf("org %+v, externals %v", org, snap.Externals)
	}
}

func TestExtensionNeverReportsVerifiedWithoutTrustedSigner(t *testing.T) {
	_, signer := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgCatalog)
	repo, digest, _ := strings.Cut(contract.TrimOCIScheme(ref), "@")
	if err := publish.SignDigest(t.Context(), reg.client, signer, repo, digest); err != nil {
		t.Fatal(err)
	}
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{ExternalCatalogRef: ref, Provenance: newProvenanceChecker(reg.client)})
	if err != nil {
		t.Fatal(err)
	}
	org := orgRef(t, snap)
	if org.Signature != fleet.SignatureUnknown || org.Signer != "" || !strings.Contains(org.Reason, "digest pin") {
		t.Fatalf("org = %+v", org)
	}
}

func TestExtensionMemoised(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := pushOrgCatalog(t, reg, orgCatalog)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	s := NewService(nil, nil)
	s.sys.now = func() time.Time { return now }
	opts := FleetOptions{ExternalCatalogRef: ref}
	if _, err := s.Fleet(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	pulls := reg.requests.Load()
	snap, err := s.Fleet(t.Context(), opts)
	if err != nil || reg.requests.Load() != pulls || len(snap.ExternalCatalogs) != 2 {
		t.Fatalf("second build asked the registry: %d -> %d requests, %v", pulls, reg.requests.Load(), err)
	}
	now = now.Add(provenanceTTL + time.Second)
	if _, err := s.Fleet(t.Context(), opts); err != nil || reg.requests.Load() == pulls {
		t.Fatalf("an expired memo did not pull again: %v", err)
	}
	// A failure is remembered for the shorter TTL.
	reg.failWhen(func(*http.Request) bool { return true })
	now = now.Add(provenanceTTL + time.Second)
	if _, err := s.Fleet(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	failed := reg.requests.Load()
	snap, _ = s.Fleet(t.Context(), opts)
	if reg.requests.Load() != failed || snap.Completeness != fleet.CompletenessPartial {
		t.Fatalf("a failed pull was retried within its TTL")
	}
}

// TestExternalCatalogReleaseSource: the default catalog is checked against
// the Pacto release signer, and a match says so in its source.
func TestExternalCatalogReleaseSource(t *testing.T) {
	reg := provenanceTestRegistry(t)
	signDefault(t, reg)
	ref := contract.OCIScheme + reg.host + "/trianalab/pacto/external-catalog@" + externalcatalog.DefaultDigest()
	p := newProvenanceChecker(reg.client).CheckSigner(t.Context(), externalCatalogSource, ref, externalcatalog.DefaultSigner, externalcatalog.DefaultIssuer)
	if p.Signature.Status != fleet.SignatureVerified || p.Signature.Source != "release" || p.Signature.Signer != externalcatalog.DefaultSigner {
		t.Fatalf("signature %+v", p.Signature)
	}
}
