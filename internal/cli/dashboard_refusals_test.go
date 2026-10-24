package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

var gvrStagesTest = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}

func signedRefusal(t *testing.T, key ed25519.PrivateKey, service, version, at string) []byte {
	t.Helper()
	kp, _ := signing.FromPrivateKey(key)
	b, _, err := delivery.SignRefusal(t.Context(), signing.Keyed(kp, ""), delivery.NewRefusalStatement("sha256:"+strings.Repeat("a", 64),
		delivery.PublishRefusal{Service: service, Version: version, Baseline: "2.4.2", Classification: "BREAKING", RequiredBump: "major",
			Message: "BREAKING since 2.4.2 needs a major bump", At: at}))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// refusalsFixture is a registry holding two payments refusals and one of
// orders on the payments anchor, and a dashboard whose cluster adds a payments
// Stage that knows no version and a fraud Stage whose version names no contract.
func refusalsFixture(t *testing.T) (*httptest.Server, *remote.Repository, *dashboardIdentity, func(*rest.Config) (dynamic.Interface, error)) {
	t.Helper()
	ctx := context.Background()
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	path := strings.TrimPrefix(srv.URL, "http://") + "/pactos/payments"
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	for _, b := range [][]byte{signedRefusal(t, key, "payments", "2.4.3", "2026-10-22T10:00:00Z"), signedRefusal(t, key, "payments", "2.4.4", "2026-10-22T11:00:00Z")} {
		if err := deliveryrecord.PushRefusal(ctx, repo, b); err != nil {
			t.Fatal(err)
		}
	}
	// A refusal of orders attached to the payments anchor is unreadable.
	anchor := deliveryrecord.RefusalAnchor("payments")
	if err := ocireferrer.Push(ctx, repo, ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, signedRefusal(t, key, "orders", "1.0.1", "2026-10-22T12:00:00Z"),
		&anchor.ManifestDesc, map[string]string{deliveryrecord.AnnBundleContent: "dsse-envelope", deliveryrecord.AnnPredicateType: delivery.PredicatePublishRefusal})); err != nil {
		t.Fatal(err)
	}
	id, _ := localAPI(t)
	// A second payments Stage that knows no version, and a fraud Stage whose
	// only version names no contract.
	next := id.newClient
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		c, err := next(cfg)
		for _, st := range []map[string]any{
			{"spec": map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"}}},
			{"spec": map[string]any{"service": map[string]any{"name": "fraud"}, "applyRef": map[string]any{"name": "missing"}},
				"status": map[string]any{"pointer": cliA}},
		} {
			name := "a-" + st["spec"].(map[string]any)["service"].(map[string]any)["name"].(string)
			_, _ = c.Resource(gvrStagesTest).Namespace("prod").Create(ctx, u("delivery.pacto.trianalab.io/v1alpha1", "Stage", name, st), metav1.CreateOptions{})
		}
		return c, err
	}
	id.records = func(string, ...string) (*remote.Repository, error) { return repo, nil }
	return srv, repo, id, next
}

// TestDashDeliveryPublishRefusals: the refusals pacto publish kept in the
// contract bundle repository the Stage's versions name reach the dashboard;
// what cannot be found or read makes the answer partial, never empty proof.
func TestDashDeliveryPublishRefusals(t *testing.T) {
	ctx := context.Background()
	_, repo, id, _ := refusalsFixture(t)
	var opened []string
	id.records = func(p string, _ ...string) (*remote.Repository, error) {
		opened = append(opened, p)
		return repo, nil
	}
	api, err := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err == nil, "delivery: %v", err)
	r, err := api.PublishRefusals(ctx, "prod")
	expect(t, err == nil && r != nil, "refusals: %v %v", r, err)
	expect(t, len(r.Refusals) == 2 && r.Refusals[0].Version == "2.4.4" && r.Refusals[0].RequiredBump == "major" && r.Refusals[0].Baseline == "2.4.2" &&
		r.Refusals[0].Check == "version" && !r.Refusals[0].Verified, "refusals: %+v", r.Refusals)
	all := strings.Join(r.Unreadable, "|")
	expect(t, r.Completeness == "partial" && len(r.Unreadable) == 2 && strings.Contains(all, `payments: sha256:`) &&
		strings.Contains(all, "fraud: no version its Stages know names a contract"), "partial: %+v", r)
	expect(t, len(opened) == 1 && opened[0] == "reg/pactos/payments", "opened %v", opened)
	expect(t, strings.Contains(r.Note, "not checked"), "note: %q", r.Note)
}

// TestDashDeliveryPublishRefusalsFailures: a repository that cannot be opened
// or listed is said, no record repository means no record, and a Stage list
// that fails is refused.
func TestDashDeliveryPublishRefusalsFailures(t *testing.T) {
	ctx := context.Background()
	srv, repo, id, next := refusalsFixture(t)
	var (
		r   *dashboard.PublishRefusals
		api dashboard.DeliveryAPI
		err error
	)
	// A repository that cannot be opened or listed is said.
	refusalsWith := func(records func(string, ...string) (*remote.Repository, error)) *dashboard.PublishRefusals {
		t.Helper()
		id.records = records
		api, _ := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
		r, _ := api.PublishRefusals(ctx, "prod")
		return r
	}
	r = refusalsWith(func(string, ...string) (*remote.Repository, error) { return nil, errors.New("no repo") })
	expect(t, strings.Contains(strings.Join(r.Unreadable, "|"), "payments: no repo"), "unopened: %+v", r)
	srv.Close()
	r = refusalsWith(func(string, ...string) (*remote.Repository, error) { return repo, nil })
	expect(t, strings.Contains(strings.Join(r.Unreadable, "|"), "payments: reading reg/pactos/payments: "), "unlisted: %+v", r)

	// No record repository: this host keeps no record. A Stage list that fails is refused.
	id.records = nil
	api, _ = id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	r, err = api.PublishRefusals(ctx, "prod")
	expect(t, r == nil && err == nil, "no records: %v %v", r, err)
	id.records = func(string, ...string) (*remote.Repository, error) { return repo, nil }
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		c, err := next(cfg)
		c.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("boom")
		})
		return c, err
	}
	api, _ = id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	_, err = api.PublishRefusals(ctx, "prod")
	expect(t, isRefusal(err, http.StatusUnprocessableEntity), "a failed Stage list: %v", err)
}
