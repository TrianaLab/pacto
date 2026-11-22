//go:build registryconformance

package registryconformance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// TestRegistryConformanceEvidence proves subject prefixes on real registries:
// an Evidence Server started with only a prefix accepts a signed
// ResourceProvisioned report on a revision published after it started, reads
// it Ready through the fleet source, and a second server with no shared state
// lists it and refuses its replay. On zot the record is a referrer of the
// revision; on distribution it is a write-once tag in <prefix>pacto-evidence,
// and no sha256-<hex> referrers-tag index is ever written.
func TestRegistryConformanceEvidence(t *testing.T) {
	for _, tc := range []struct{ name, env string }{
		{"zot", "PACTO_CONFORMANCE_NATIVE_REGISTRY"},
		{"distribution", "PACTO_CONFORMANCE_TAGS_REGISTRY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := os.Getenv(tc.env)
			if host == "" {
				t.Skipf("%s is not set: no %s registry to test against", tc.env, tc.name)
			}
			conformEvidence(t, host, tc.name == "zot")
		})
	}
}

func conformEvidence(t *testing.T, host string, native bool) {
	ctx := t.Context()
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	root := host + "/conformance/ev-" + hex.EncodeToString(suffix) + "/"

	client := oci.NewClient(authn.NewMultiKeychain())
	svc := &app.Service{BundleStore: client}
	dir := t.TempDir()
	kp, err := svc.GenerateKey(dir, "db-adapter", "db-adapter-key", false)
	if err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(dir, "trust.yaml")
	if err := os.WriteFile(trust, []byte("apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n  - keyId: db-adapter-key\n    producerId: db-adapter\n    publicKeyFile: "+
		filepath.Base(kp.PublicKeyPath)+"\n    allowedContractRepos: ["+root+"invoices]\n    allowedObservationKinds: [ResourceProvisioned]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := app.ServeOptions{TrustPath: trust, SubjectPrefixes: []string{"oci://" + root}, Producers: []string{"db-adapter"}}
	base := serve(t, svc, opts)

	// Published after the server started: no Helm edit, no restart.
	digest, err := client.Push(ctx, root+"invoices:0.1.0", invoicesBundle(t, root))
	if err != nil {
		t.Fatalf("pushing the revision: %v", err)
	}
	ref := "oci://" + root + "invoices@" + digest
	set, err := svc.ProvisionedEvidence(app.ProvisionedOptions{ContractRef: ref, Configuration: "database", Environment: "production", Provider: "acme", Phase: "Ready"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := svc.SignEvidenceSet(set, app.SignOptions{KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: "db-adapter", Sequence: 1, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if res := post(t, svc, base, env); res.StatusCode != http.StatusAccepted {
		t.Fatalf("report on a revision published after start = %d %s, want 202", res.StatusCode, res.Body)
	}
	if n := provisioned(t, base); n != 1 {
		t.Fatalf("/targets lists %d provisioning reports, want 1", n)
	}
	col, err := fleetsrc.NewEvidenceHTTPSource("evidence-http", base).Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := col.Provisioning; len(got) != 1 || got[0].Phase != "Ready" || got[0].Outcome != "Observed" || !got[0].Verified {
		t.Fatalf("fleet provisioning = %+v, want one verified observed Ready record", got)
	}

	// A second server over the same registry, sharing nothing in process,
	// reads the record back from the ledger and refuses its replay.
	second := serve(t, svc, opts)
	if n := provisioned(t, second); n != 1 {
		t.Fatalf("a second server lists %d provisioning reports, want 1", n)
	}
	if res := post(t, svc, second, env); res.StatusCode != http.StatusConflict {
		t.Fatalf("replay to a second server = %d %s, want 409", res.StatusCode, res.Body)
	}

	subjectRepo := open(t, root+"invoices")
	ledger := open(t, root+"pacto-evidence")
	anchor := anchorTag(ref)
	ledgerTags := tags(t, ledger)
	if native {
		desc, err := subjectRepo.Resolve(ctx, "0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		var referrers []ocispec.Descriptor
		if err := subjectRepo.Referrers(ctx, desc, "", func(d []ocispec.Descriptor) error { referrers = append(referrers, d...); return nil }); err != nil {
			t.Fatalf("listing referrers: %v", err)
		}
		if len(referrers) != 1 {
			t.Fatalf("revision has %d referrers, want the one record", len(referrers))
		}
		if len(ledgerTags) != 1 || ledgerTags[0] != anchor {
			t.Fatalf("ledger tags = %v, want only the anchor %s", ledgerTags, anchor)
		}
	} else {
		record := regexp.MustCompile("^" + anchor + "-[0-9a-f]{12}$")
		if len(ledgerTags) != 2 || ledgerTags[0] != anchor || !record.MatchString(ledgerTags[1]) {
			t.Fatalf("ledger tags = %v, want the anchor %s and one record tag under it", ledgerTags, anchor)
		}
		if got := tags(t, subjectRepo); len(got) != 1 || got[0] != "0.1.0" {
			t.Fatalf("subject repository tags = %v, want only 0.1.0", got)
		}
	}
	for _, repo := range []*remote.Repository{subjectRepo, ledger} {
		for _, tag := range tags(t, repo) {
			if strings.HasPrefix(tag, "sha256-") {
				t.Fatalf("%s holds the referrers-tag index %s", repo.Reference.Repository, tag)
			}
		}
	}
}

// serve starts an Evidence Server on a free port and returns its base URL.
func serve(t *testing.T, svc *app.Service, opts app.ServeOptions) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.ServeEvidenceOnListener(ctx, ln, opts) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeEvidenceOnListener = %v", err)
		}
	})
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		resp, err := http.Get(base + "/api/evidence/v1/ready")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("evidence server at %s never became ready (last error %v)", base, err)
		}
	}
}

func post(t *testing.T, svc *app.Service, base string, env evidenceenvelope.Envelope) app.SendResult {
	t.Helper()
	res, err := svc.PostEnvelope(context.Background(), base+"/api/evidence/v1/envelopes", env)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// provisioned counts the provisioning reports base lists on /targets.
func provisioned(t *testing.T, base string) int {
	t.Helper()
	resp, err := http.Get(base + "/api/evidence/v1/targets")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var targets evidenceingest.TargetsResponse
	if resp.StatusCode != http.StatusOK || json.Unmarshal(b, &targets) != nil {
		t.Fatalf("/targets = %d %s", resp.StatusCode, b)
	}
	return len(targets.Provisioning)
}

func open(t *testing.T, path string) *remote.Repository {
	t.Helper()
	repo, err := ocireferrer.Open(path, ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// tags lists every tag of repo, sorted: distribution lists an anchor's record
// tags before the anchor (it sorts its tag directories, and '-' < '/').
func tags(t *testing.T, repo *remote.Repository) []string {
	t.Helper()
	got, err := ocireferrer.Tags(t.Context(), repo, "")
	if err != nil {
		t.Fatalf("listing %s tags: %v", repo.Reference.Repository, err)
	}
	slices.Sort(got)
	return got
}

// anchorTag is the ledger anchor of ref: evidence-<hex sha256(ref)>.
func anchorTag(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return "evidence-" + hex.EncodeToString(sum[:])
}

// invoicesBundle is a consumer that takes a database configuration in the
// production environment.
func invoicesBundle(t *testing.T, root string) *contract.Bundle {
	t.Helper()
	y := []byte(`pactoVersion: '2.4'
service:
  name: invoices
  version: 0.1.0
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
    visibility: public
configurations:
  - name: database
    ref: oci://` + root + `platform/postgresql:16.1.0
environments:
  - name: production
    file: environments/production.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
	c, err := contract.Parse(bytes.NewReader(y))
	if err != nil {
		t.Fatalf("contract.Parse: %v", err)
	}
	return &contract.Bundle{Contract: c, RawYAML: y, FS: fstest.MapFS{
		"pacto.yaml":                   &fstest.MapFile{Data: y},
		"openapi.yaml":                 &fstest.MapFile{Data: []byte("openapi: '3.0.0'\ninfo:\n  title: T\n  version: '1.0.0'\npaths: {}\n")},
		"environments/production.yaml": &fstest.MapFile{Data: []byte("database: {}\n")},
	}}
}
