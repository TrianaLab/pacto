package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// applyRefusalsFixture adds to refusalsFixture a streams repository holding
// 20 apply refusals of payments on the Stage prod/b-payments, which names its
// record stream there, and one on a Stage in staging the caller cannot list.
// It returns the dashboard identity, the recorder's public key, the streams
// repository path and a function that pushes one more refusal.
func applyRefusalsFixture(t *testing.T) (*dashboardIdentity, ed25519.PublicKey, string, func(svc delivery.ServiceRef, ns, stage string, i int)) {
	t.Helper()
	ctx := context.Background()
	srv, repo, id, _ := refusalsFixture(t)
	streams := strings.TrimPrefix(srv.URL, "http://") + "/delivery/streams"
	streamsRepo, _ := ocireferrer.Open(streams, ocireferrer.Options{})
	svc := delivery.ServiceRef{Name: "payments"}
	if err := ocireferrer.Push(ctx, streamsRepo, deliveryrecord.Anchor(delivery.ApplyRefusalStream(svc))); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	push := func(svc delivery.ServiceRef, ns, stage string, i int) {
		if err := ocireferrer.Push(ctx, streamsRepo, deliveryrecord.Anchor(delivery.ApplyRefusalStream(svc))); err != nil {
			t.Fatal(err)
		}
		r := delivery.ApplyRefusal{Service: svc, Namespace: ns, Stage: stage, Version: fmt.Sprintf("2.5.%d", i),
			Digest: fmt.Sprintf("sha256:%064d", i), Refused: []string{"ClusterRoleBinding orders-admin is cluster-scoped"},
			Message: "the version reaches outside " + ns, Record: fmt.Sprintf("rec-%d", i), Operation: "p", At: fmt.Sprintf("2026-11-21T10:%02d:00Z", i)}
		b, _, err := delivery.SignApplyRefusal(ctx, edSigner(key), delivery.NewApplyRefusalStatement(r.Digest, r))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := deliveryrecord.PushApplyRefusal(ctx, streamsRepo, capabilities.ReferrersNative, b); err != nil {
			t.Fatal(err)
		}
	}
	push(svc, "staging", "b-payments", 0)
	for i := 1; i <= deliveryrecord.MaxApplyRefusals-1; i++ {
		push(svc, "prod", "b-payments", i)
	}
	next := id.newClient
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		c, err := next(cfg)
		_, _ = c.Resource(gvrStagesTest).Namespace("prod").Create(ctx, u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "b-payments", map[string]any{
			"spec":   map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"}},
			"status": map[string]any{"stream": map[string]any{"id": "payments-prod", "anchor": deliveryrecord.AnchorRef(streams, "payments-prod")}},
		}), metav1.CreateOptions{})
		return c, err
	}
	id.records = func(p string, _ ...string) (*remote.Repository, error) {
		if p == streams {
			return streamsRepo, nil
		}
		return repo, nil
	}
	return id, pub, streams, push
}

func refusalsOfKind(r *dashboard.PublishRefusals, check string) []dashboard.PublishRefusal {
	var out []dashboard.PublishRefusal
	for _, x := range r.Refusals {
		if x.Check == check {
			out = append(out, x)
		}
	}
	return out
}

// TestPublishRefusalsListsApplyRefusals: the refused promotes of every Stage
// the caller lists reach the answer beside the refused publishes, every one
// of them (the server caps once), verified with the delivery trust config;
// a refused promote on a Stage the caller cannot list never does.
func TestPublishRefusalsListsApplyRefusals(t *testing.T) {
	id, pub, _, _ := applyRefusalsFixture(t)
	trust, err := app.DeliveryTrust(writeTrust(t, pub))
	expect(t, err == nil, "trust: %v", err)
	id.trust = trust
	api, err := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, err == nil, "delivery: %v", err)
	r, err := api.PublishRefusals(context.Background(), "prod")
	expect(t, err == nil && r != nil, "refusals: %v %v", r, err)
	applied := refusalsOfKind(r, "manifests")
	expect(t, len(refusalsOfKind(r, "version")) == 2 && len(applied) == deliveryrecord.MaxApplyRefusals-1 && len(r.Refusals) > 20,
		"every row, uncapped: %+v", r.Refusals)
	for _, a := range applied {
		expect(t, a.Service == "payments" && a.Namespace == "prod" && a.Stage == "b-payments" && strings.HasPrefix(a.Record, "rec-") &&
			a.Version != "" && strings.HasPrefix(a.Digest, "sha256:") &&
			a.Reason == "the version reaches outside prod" && a.At != "" && a.Verified, "a refused promote: %+v", a)
	}
	expect(t, strings.HasPrefix(r.Note, "read from each service's") && strings.Contains(r.Note, "refused promotes, verified with the delivery trust config"),
		"note: %q", r.Note)
}

// TestRefusalsUnverifiedWithoutTrust: with no trust config nothing checks the
// recorder's signature, so no refused promote reads verified.
func TestRefusalsUnverifiedWithoutTrust(t *testing.T) {
	id, _, _, _ := applyRefusalsFixture(t)
	api, _ := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	r, err := api.PublishRefusals(context.Background(), "prod")
	expect(t, err == nil && r != nil, "refusals: %v %v", r, err)
	applied := refusalsOfKind(r, "manifests")
	expect(t, len(applied) == deliveryrecord.MaxApplyRefusals-1, "listed: %+v", applied)
	for _, a := range applied {
		expect(t, !a.Verified, "unverified without trust: %+v", a)
	}
}

// TestPublishRefusalsPartialOnUnreadableStream: a streams repository that
// cannot be opened or listed and a refusal whose signature does not verify each make the answer partial and are named,
// never "none".
func TestPublishRefusalsPartialOnUnreadableStream(t *testing.T) {
	ctx := context.Background()
	id, _, streams, _ := applyRefusalsFixture(t)
	refusals := func() *dashboard.PublishRefusals {
		t.Helper()
		api, _ := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
		r, err := api.PublishRefusals(ctx, "prod")
		expect(t, err == nil && r != nil, "refusals: %v %v", r, err)
		return r
	}
	records := id.records
	id.records = func(p string, insecure ...string) (*remote.Repository, error) {
		if p == streams {
			return nil, errors.New("no streams")
		}
		return records(p, insecure...)
	}
	r := refusals()
	expect(t, r.Completeness == "partial" && strings.Contains(strings.Join(r.Unreadable, "|"), "payments: no streams"), "unopened: %+v", r)

	// A trust config the recorder's key does not match: every refusal is unreadable.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	trust, _ := app.DeliveryTrust(writeTrust(t, other))
	id.records, id.trust = records, trust
	r = refusals()
	expect(t, len(refusalsOfKind(r, "manifests")) == 0 && strings.Contains(strings.Join(r.Unreadable, "|"), "payments: sha256:"), "unverified: %+v", r)

	// A streams repository that cannot be listed.
	id.trust = nil
	id.records = func(p string, insecure ...string) (*remote.Repository, error) {
		if p == streams {
			return ocireferrer.Open(strings.TrimSuffix(streams, "/streams")+"/missing", ocireferrer.Options{})
		}
		return records(p, insecure...)
	}
	r = refusals()
	expect(t, strings.Contains(strings.Join(r.Unreadable, "|"), "payments: reading the refused promotes in "+streams), "unlisted: %+v", r)
}

// TestPublishRefusalsReadsEveryDomainOfAService: a service name two domains
// share keeps one apply refusal stream per domain, and both are read.
func TestPublishRefusalsReadsEveryDomainOfAService(t *testing.T) {
	ctx := context.Background()
	id, _, streams, push := applyRefusalsFixture(t)
	billing := delivery.ServiceRef{Domain: "billing", Name: "payments"}
	push(billing, "prod", "c-payments", 40)
	next := id.newClient
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		c, err := next(cfg)
		_, _ = c.Resource(gvrStagesTest).Namespace("prod").Create(ctx, u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "c-payments", map[string]any{
			"spec":   map[string]any{"service": map[string]any{"name": "payments", "domain": "billing"}, "applyRef": map[string]any{"name": "payments"}},
			"status": map[string]any{"stream": map[string]any{"id": "payments-billing", "anchor": deliveryrecord.AnchorRef(streams, "payments-billing")}},
		}), metav1.CreateOptions{})
		return c, err
	}
	api, _ := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	r, err := api.PublishRefusals(ctx, "prod")
	expect(t, err == nil && r != nil, "refusals: %v %v", r, err)
	stages := map[string]int{}
	for _, a := range refusalsOfKind(r, "manifests") {
		stages[a.Stage]++
	}
	expect(t, stages["c-payments"] == 1 && stages["b-payments"] == deliveryrecord.MaxApplyRefusals-1,
		"both domains' streams read: %v %+v", stages, r)
}

// TestPublishRefusalsPartialWhenTheStreamIsCut: refusals on Stages the caller
// cannot list share the stream, so more than the bound makes the answer
// partial: the caller's own refusal may be among those cut.
func TestPublishRefusalsPartialWhenTheStreamIsCut(t *testing.T) {
	id, _, _, push := applyRefusalsFixture(t)
	push(delivery.ServiceRef{Name: "payments"}, "staging", "b-payments", 50)
	api, _ := id.delivery(httptest.NewRequest(http.MethodGet, "/", nil))
	r, err := api.PublishRefusals(context.Background(), "prod")
	expect(t, err == nil && r != nil, "refusals: %v %v", r, err)
	expect(t, r.Completeness == "partial" && strings.Contains(strings.Join(r.Unreadable, "|"), "payments: more than 20 refused promotes; older ones were not read"),
		"cut: %+v", r)
}
