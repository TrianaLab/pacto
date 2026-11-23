package inferredcontract

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

var now = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

// svc is one Kubernetes service of a fixture: its name, the namespace it
// runs in and its bundle files beside pacto.yaml.
type svc struct {
	name, ns string
	fsys     fstest.MapFS
}

// fixture is a fleet: its services, the external uses its source reports
// and the limitations every target carries.
type fixture struct {
	svcs []svc
	uses []fleet.RawExternalUse
	lims []fleet.Limitation
	// cat classifies the externals; nil is the default catalog.
	cat *externalcatalog.Catalog
	// catLims are the limitations of loading cat.
	catLims []fleet.Limitation
}

func (f fixture) build(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	col := &fleet.Collection{ExternalUses: f.uses}
	for _, s := range f.svcs {
		c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: s.name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
		digest := "sha256:" + s.name
		col.Revisions = append(col.Revisions, fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: s.fsys}, Digest: digest})
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: s.ns, Kind: fleet.SourceKindKubernetes, Name: s.name, Service: s.name,
			Digest: digest, Compliance: fleet.StatusCompliant, Limitations: f.lims})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }, ExternalCatalog: f.cat, ExternalCatalogLimitations: f.catLims}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// use is service's use of identity through host, read at the minute before
// now minus ago minutes.
func use(service, identity, host string, ago int) fleet.RawExternalUse {
	at := now.Add(-time.Duration(ago+1) * time.Minute)
	return fleet.RawExternalUse{Service: service, Namespace: "shop", Identity: identity, Class: "vendor-api", Confidence: "likely", Kind: "env",
		Where: "deployment/" + service + " env API_BASE", Host: host, Entry: "stripe", Catalog: externalcatalog.DefaultDigest(), ObservedAt: &at}
}

// stripeFleet is checkout calling stripe at hosts, plus every extra service.
func stripeFleet(t *testing.T, hosts []string, extra ...string) *fleet.FleetSnapshot {
	t.Helper()
	f := fixture{svcs: []svc{{name: "checkout", ns: "shop"}}}
	for _, h := range hosts {
		f.uses = append(f.uses, use("checkout", "stripe", h, 0))
	}
	for _, s := range extra {
		f.svcs = append(f.svcs, svc{name: s, ns: "shop"})
		for _, h := range hosts {
			f.uses = append(f.uses, use(s, "stripe", h, 5))
		}
	}
	return f.build(t)
}

// sdkRecord is a bundle whose inferred record names stripe through a Go
// module requirement with major.
func sdkRecord(major *int) fstest.MapFS {
	data := "require github.com/stripe/stripe-go/v76 v76.0.0\n"
	sum := sha256.Sum256([]byte(data))
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Catalog: []string{"default " + externalcatalog.DefaultDigest()},
			Sources: []contract.InferredSourceFile{{Path: "go.mod", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[stripe]", Value: "stripe", Source: "go.mod", Confidence: "likely",
			External: &contract.InferredExternal{Identity: "stripe", Catalog: "default",
				Sources: []contract.InferredExternalSource{{Kind: "sdk", Path: "go.mod"}},
				Client:  &contract.InferredClient{SDK: "stripe-go", Major: major}}}}}
	return fstest.MapFS{"go.mod": {Data: []byte(data)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
}

// anonymous never asks a credential helper.
var anonymous = authn.NewMultiKeychain()

// testRegistry is an in-memory registry with the Referrers API.
func testRegistry(t *testing.T) (string, *publish.Client) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return host, publish.NewClient(anonymous, host)
}

// testKey is an installation key: its signer and the verifier of its own key.
func testKey(t *testing.T) (publish.Signer, publish.BundleVerifier) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, err := signing.FromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signing.Keyed(kp, ""), publish.PolicyVerifier(signing.Policy{Key: pub})
}

func deps(t *testing.T, reg publish.Registry) Deps {
	t.Helper()
	s, v := testKey(t)
	return Deps{Registry: reg, Signer: s, Verify: v, Pacto: "4.7.0", Now: func() time.Time { return now }}
}

// failSigner signs nothing.
type failSigner struct{}

func (failSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("the signer is unreachable")
}

// hooked is a registry whose listed tags, head reads and writes a test may
// replace or fail.
type hooked struct {
	publish.Registry
	tags  func(repo string) []string
	head  func(ref string) error
	pull  error
	write error
	tag   error
}

func (h hooked) Tags(ctx context.Context, repo string) ([]string, error) {
	if h.tags != nil {
		return h.tags(repo), nil
	}
	return h.Registry.Tags(ctx, repo)
}

func (h hooked) Head(ctx context.Context, ref string) (*publish.Remote, error) {
	if h.head != nil {
		if err := h.head(ref); err != nil {
			return nil, err
		}
	}
	return h.Registry.Head(ctx, ref)
}

func (h hooked) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if h.pull != nil {
		return nil, h.pull
	}
	return h.Registry.Pull(ctx, ref)
}

func (h hooked) Write(ctx context.Context, repo string, a publish.Artifact) (string, error) {
	if h.write != nil {
		return "", h.write
	}
	return h.Registry.Write(ctx, repo, a)
}

func (h hooked) Tag(ctx context.Context, repo, digest, tag string) error {
	if h.tag != nil {
		return h.tag
	}
	return h.Registry.Tag(ctx, repo, digest, tag)
}

// pushUnsigned writes b to repo as tag with no signature, as anyone with
// push access may.
func pushUnsigned(t *testing.T, reg publish.Registry, repo, tag string, b *contract.Bundle) string {
	t.Helper()
	d := digestOf(b)
	h, _ := v1.NewHash(d)
	if _, err := reg.Write(context.Background(), repo, withDigest{annotated(b), h}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Tag(context.Background(), repo, d, tag); err != nil {
		t.Fatal(err)
	}
	return d
}

// surfaceAt is the surface of the newest tag of repo.
func surfaceAt(t *testing.T, reg publish.Registry, ref string) *contract.InferredSurface {
	t.Helper()
	b, err := reg.Pull(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec == nil {
		t.Fatalf("record of %s: %v", ref, err)
	}
	return rec.Surface
}

func hostValues(s *contract.InferredSurface) string {
	var out []string
	for _, h := range s.Hosts {
		out = append(out, h.Value)
	}
	return strings.Join(out, ",")
}
