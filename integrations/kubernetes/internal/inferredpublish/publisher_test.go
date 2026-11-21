/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package inferredpublish

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

var now = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// testHost is an in-process OCI registry.
func testHost(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// pacto is a reconciled Pacto of service in ns whose workload names stripe at host.
func pacto(ns, service, host string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": service},
		"status": map[string]any{
			"statusFormat": int64(3), "contractStatus": "Compliant", "lastReconciledAt": now.Format(time.RFC3339),
			"contract": map[string]any{"serviceName": service, "version": "1.0.0", "owner": map[string]any{"team": "t"},
				"resolvedRef": "oci://reg.example/pactos/" + service + "@sha256:" + service},
			"externals": []any{map[string]any{"identity": "stripe", "class": "vendor-api", "confidence": "likely", "kind": "env",
				"where": "deployment/" + service + " env STRIPE_API_BASE", "host": host, "entry": "stripe"}},
			"externalsCoverage": map[string]any{"state": "complete", "refreshSeconds": int64(300)},
			"externalCatalog":   externalcatalog.DefaultDigest() + " (embedded)",
		},
	}}
	u.SetGroupVersionKind(gvkPactoList.GroupVersion().WithKind("Pacto"))
	return u
}

// reader serves objs; fail, when set, answers the list in a namespace.
func reader(fail func(ns string) error, objs ...client.Object) client.Reader {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(gvkPactoList.GroupVersion().WithKind("Pacto"), &unstructured.Unstructured{})
	s.AddKnownTypeWithName(gvkPactoList, &unstructured.UnstructuredList{})
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if fail != nil {
				o := &client.ListOptions{}
				o.ApplyOptions(opts)
				if err := fail(o.Namespace); err != nil {
					return err
				}
			}
			return c.List(ctx, list, opts...)
		}}).Build()
}

// logs collects what a publisher logs.
type logs struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *logs) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

// publisher is ForOperator's publisher for host/acme over r, logging to l.
func publisher(t *testing.T, host string, r client.Reader, l *logs, watched ...string) (*InferredPublisher, ed25519.PublicKey) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	run, err := ForOperator(Config{Registry: host + "/acme", Key: key, Keychain: authn.DefaultKeychain, Reader: r, Watched: watched, Pacto: "6.5.0"})
	if err != nil {
		t.Fatal(err)
	}
	p := run.(*InferredPublisher)
	p.Deps.Now = func() time.Time { return now }
	p.Log = funcr.New(func(prefix, args string) { l.mu.Lock(); defer l.mu.Unlock(); l.buf.WriteString(args + "\n") }, funcr.Options{})
	return p, pub
}

func tags(t *testing.T, repo string) []string {
	t.Helper()
	got, _ := publish.NewClient(authn.DefaultKeychain).Tags(context.Background(), repo)
	return got
}

func TestInferredPublisherRunsPublish(t *testing.T) {
	host := testHost(t)
	p, pub := publisher(t, host, reader(nil, pacto("shop", "checkout", "api.stripe.com"), pacto("shop", "pay-agent", "api.stripe.com")), &logs{})
	if p.Root != host+"/acme/pacto-inferred" || p.Interval != InferredInterval {
		t.Fatalf("publisher = %+v", p)
	}
	p.run(context.Background())
	repo := p.Root + "/stripe"
	if got := tags(t, repo); !slices.Equal(got, []string{"0.0.1"}) {
		t.Fatalf("tags = %v", got)
	}
	reg := publish.NewClient(authn.DefaultKeychain)
	h, err := reg.Head(context.Background(), repo+":0.0.1")
	if err != nil || h.Annotations[contract.AnnotationProvenance] != contract.ProvenanceInferred {
		t.Fatalf("head = %+v, %v", h, err)
	}
	if err := publish.VerifyDigest(context.Background(), reg, publish.PolicyVerifier(signing.Policy{Key: pub}), repo, h.Digest); err != nil {
		t.Errorf("signature: %v", err)
	}
	p.run(context.Background()) // a second caller adds no value: nothing new
	if got := tags(t, repo); len(got) != 1 {
		t.Errorf("tags = %v", got)
	}
}

func TestInferredPublisherListErrorPublishesNothingRemoved(t *testing.T) {
	host := testHost(t)
	objs := []client.Object{pacto("shop", "checkout", "api.stripe.com"), pacto("pay", "pay-agent", "files.stripe.com")}
	failPay := false
	r := reader(func(ns string) error {
		if failPay && ns == "pay" {
			return errors.New("forbidden")
		}
		return nil
	}, objs...)
	l := &logs{}
	p, _ := publisher(t, host, r, l, "shop", "pay")
	p.run(context.Background())
	failPay = true
	p.run(context.Background())
	repo := p.Root + "/stripe"
	if got := tags(t, repo); !slices.Equal(got, []string{"0.0.1"}) {
		t.Fatalf("a partial read removed a host: tags = %v\n%s", got, l)
	}
	if !strings.Contains(l.String(), `"listError"=true`) {
		t.Errorf("logs = %s", l)
	}
	failPay = false
	objs[1].(*unstructured.Unstructured).Object["status"].(map[string]any)["externals"] = []any{}
	_ = r.(client.Client).Update(context.Background(), objs[1])
	p.run(context.Background()) // a watched-namespaces read never removes it either
	if got := tags(t, repo); len(got) != 1 {
		t.Errorf("a scoped read removed a host: tags = %v", got)
	}
	cw, _ := publisher(t, host, r, l)
	cw.Deps.Signer, cw.Deps.Verify = p.Deps.Signer, p.Deps.Verify
	cw.run(context.Background()) // a complete cluster-wide read removes it
	if got := tags(t, repo); len(got) != 2 {
		t.Errorf("tags = %v", got)
	}
}

// TestInferredPublisherScopedIsPartial: an operator that watches only some
// namespaces cannot see the rest, so every surface it writes says partial
// with TARGET_SNAPSHOT_SCOPED.
func TestInferredPublisherScopedIsPartial(t *testing.T) {
	host := testHost(t)
	p, _ := publisher(t, host, reader(nil, pacto("shop", "checkout", "api.stripe.com")), &logs{}, "shop")
	p.run(context.Background())
	b, err := publish.NewClient(authn.DefaultKeychain).Pull(context.Background(), p.Root+"/stripe:0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec.Surface == nil || rec.Surface.Completeness.State != contract.CoveragePartial ||
		!slices.Contains(rec.Surface.Completeness.Limitations, "TARGET_SNAPSHOT_SCOPED") {
		t.Fatalf("record = %+v, %v", rec, err)
	}
}

func TestInferredPublisherMetrics(t *testing.T) {
	host := testHost(t)
	p, _ := publisher(t, host, reader(nil, pacto("shop", "checkout", "api.stripe.com")), &logs{})
	published, failed := testutil.ToFloat64(Published), testutil.ToFloat64(PublishErrors)
	p.run(context.Background())
	if testutil.ToFloat64(Published) != published+1 || testutil.ToFloat64(PublishErrors) != failed {
		t.Fatalf("published %v, errors %v", testutil.ToFloat64(Published), testutil.ToFloat64(PublishErrors))
	}
	p.Deps.Signer = nil // never unsigned: the run publishes nothing and counts one error
	p.run(context.Background())
	if testutil.ToFloat64(PublishErrors) != failed+1 {
		t.Errorf("errors %v", testutil.ToFloat64(PublishErrors))
	}
}

func TestForOperatorNilWithoutRegistry(t *testing.T) {
	if r, err := ForOperator(Config{}); r != nil || err != nil {
		t.Fatalf("publisher %v, %v", r, err)
	}
	if _, err := ForOperator(Config{Registry: "ghcr.io/acme"}); err == nil || !strings.Contains(err.Error(), "installation key") {
		t.Fatalf("no key: %v", err)
	}
}

func TestForOperatorPushesThroughScope(t *testing.T) {
	host := testHost(t)
	l := &logs{}
	p, _ := publisher(t, host, reader(nil, pacto("shop", "checkout", "api.stripe.com")), l)
	p.Root = host + "/other/pacto-inferred"
	failed := testutil.ToFloat64(PublishErrors)
	p.run(context.Background())
	if got := tags(t, p.Root+"/stripe"); len(got) != 0 {
		t.Fatalf("pushed outside the scope: %v", got)
	}
	if !strings.Contains(l.String(), "outside the installation push scope") || testutil.ToFloat64(PublishErrors) != failed+1 {
		t.Errorf("logs = %s", l)
	}
}

func TestInferredPublisherStartPublishesOnEveryTick(t *testing.T) {
	host := testHost(t)
	p, _ := publisher(t, host, reader(nil, pacto("shop", "checkout", "api.stripe.com")), &logs{})
	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	p.Tick = func(time.Duration) <-chan time.Time { return tick }
	done := make(chan error)
	go func() { done <- p.Start(ctx) }()
	tick <- now // read only after the first run
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := tags(t, p.Root+"/stripe"); len(got) != 1 {
		t.Errorf("tags = %v", got)
	}
	p.Tick = nil // the real ticker: a cancelled context stops after one run
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
}
