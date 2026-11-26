/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runstats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-containerregistry/pkg/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	rrregistry "github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

const testUID = "0f6c2a1e-5b7d-4c3a-9e2f-1a2b3c4d5e6f"

// promote is a concluded promote of payments on the prod Stage.
func promote() *dv1.Operation {
	op := &dv1.Operation{
		Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: "prod", Facet: delivery.FacetPointer},
		Status: delivery.OperationStatus{
			Outcome: delivery.Succeeded, Verification: delivery.Verified, ActorClass: delivery.ClassController,
			Times:  delivery.Times{Admitted: "2026-11-13T09:00:00Z", Finished: "2026-11-13T09:05:00Z"},
			Policy: &delivery.StageSpec{Service: delivery.ServiceRef{Name: "payments"}, Environment: "prod"},
		},
	}
	op.UID, op.Namespace = types.UID(testUID), "payments-prod"
	return op
}

func newEmitter(publish func(context.Context, runrecord.Record) error) *Emitter {
	e, err := New("unused.example/stats", nil, nil, nil, "cluster-1", "test")
	if err != nil {
		panic(err) // a constant that parses
	}
	e.publish = publish
	return e
}

func mustNew(t *testing.T, path string) *Emitter {
	t.Helper()
	e, err := New(path, nil, nil, nil, "cluster-1", "test")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// logged returns a context whose logger appends every line to lines.
func logged(lines *[]string) context.Context {
	return logr.NewContext(context.Background(), funcr.New(func(_, args string) { *lines = append(*lines, args) }, funcr.Options{}))
}

// statsRegistry serves an in-process registry; unsupported refuses the
// Referrers API, as GHCR does.
func statsRegistry(t *testing.T, unsupported bool) string {
	t.Helper()
	inner := registry.New(registry.WithReferrersSupport(!unsupported), registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if unsupported && strings.Contains(req.URL.Path, "/referrers/") {
			http.Error(w, `{"errors":[{"code":"UNSUPPORTED"}]}`, http.StatusNotFound)
			return
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://") + "/acme/pacto-stats"
}

// tokenRegistry is statsRegistry behind an anonymous bearer-token exchange,
// counting each token it issues.
func tokenRegistry(t *testing.T, tokens *atomic.Int32) string {
	t.Helper()
	inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/token":
			tokens.Add(1)
			_, _ = io.WriteString(w, `{"token":"tok"}`)
		case req.Header.Get("Authorization") != "Bearer tok":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="test",scope="repository:acme/pacto-stats:pull,push"`)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			inner.ServeHTTP(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://") + "/acme/pacto-stats"
}

// publishAndDrain emits op through a real emitter on path and runs the worker
// until its publish returns.
func publishAndDrain(t *testing.T, path string, ops ...*dv1.Operation) {
	t.Helper()
	e := mustNew(t, path)
	var calls atomic.Int32
	real := e.publish
	e.publish = func(ctx context.Context, r runrecord.Record) error {
		defer calls.Add(1)
		return real(ctx, r)
	}
	for _, op := range ops {
		e.Emit(op)
	}
	runUntilEmpty(t, t.Context(), e, &calls, int32(len(ops)))
}

func referrers(t *testing.T, path string) []ocispec.Descriptor {
	t.Helper()
	repo, err := rrregistry.Open(path, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []ocispec.Descriptor
	if err := repo.Referrers(t.Context(), rrregistry.Anchor("payments").ManifestDesc, "",
		func(p []ocispec.Descriptor) error { out = append(out, p...); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

// fetchRecord reads back the record a referrer manifest carries.
func fetchRecord(t *testing.T, path string, desc ocispec.Descriptor) runrecord.Record {
	t.Helper()
	repo, err := remote.NewRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	repo.PlainHTTP = true
	manifest, err := content.FetchAll(t.Context(), repo, desc)
	if err != nil {
		t.Fatal(err)
	}
	var m ocispec.Manifest
	// The test registry lists a config media type, so the manifest is read.
	if err := json.Unmarshal(manifest, &m); err != nil || m.ArtifactType != runrecord.ArtifactType {
		t.Fatalf("manifest = %s %v", manifest, err)
	}
	layer, err := content.FetchAll(t.Context(), repo.Blobs(), m.Layers[0])
	if err != nil {
		t.Fatal(err)
	}
	rec, err := runrecord.Parse(layer)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func assertPublished(t *testing.T, path string) {
	t.Helper()
	got := referrers(t, path)
	if len(got) != 1 {
		t.Fatalf("referrers = %+v", got)
	}
	rec := fetchRecord(t, path, got[0])
	if rec.Kind != runrecord.KindOperation || rec.ID != testUID || rec.Stage.Cluster != "cluster-1" || rec.PactoVersion != "test" {
		t.Fatalf("record = %+v", rec)
	}
	if v := testutil.ToFloat64(Dropped.WithLabelValues("publish-failed")); v != 0 {
		t.Fatalf("publish-failed = %v", v)
	}
}

func TestEmitPublishesUnderServiceAnchor(t *testing.T) {
	Dropped.Reset()
	path := statsRegistry(t, false)
	publishAndDrain(t, path, promote())
	assertPublished(t, path)
}

func TestEmitPublishesWithTagFallback(t *testing.T) {
	Dropped.Reset()
	path := statsRegistry(t, true)
	publishAndDrain(t, path, promote())
	assertPublished(t, path)
}

// hanging returns a publisher that blocks until release closes, and a channel
// that receives each record it starts on.
func hanging(release chan struct{}) (func(context.Context, runrecord.Record) error, chan runrecord.Record) {
	started := make(chan runrecord.Record, 2*queueSize)
	return func(context.Context, runrecord.Record) error {
		started <- runrecord.Record{}
		<-release
		return nil
	}, started
}

func TestEmitNeverBlocks(t *testing.T) {
	Dropped.Reset()
	release := make(chan struct{})
	defer close(release)
	pub, started := hanging(release)
	e := newEmitter(pub)
	go func() { _ = e.Run(t.Context()) }()
	e.Emit(promote())
	<-started
	done := make(chan struct{})
	go func() {
		for range 1000 {
			e.Emit(promote())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Emit blocked while the publisher hung")
	}
}

func TestEmitDropsWhenQueueFull(t *testing.T) {
	Dropped.Reset()
	e := newEmitter(nil)
	for i := range queueSize + 5 {
		op := promote()
		op.Namespace = fmt.Sprintf("team-%d", i%8)
		e.Emit(op)
	}
	if v := testutil.ToFloat64(Dropped.WithLabelValues("queue-full")); v != 5 {
		t.Fatalf("queue-full = %v", v)
	}
}

// runUntilEmpty runs e's worker until its queue drains and the in-flight
// publish returns, then stops it.
func runUntilEmpty(t *testing.T, ctx context.Context, e *Emitter, calls *atomic.Int32, want int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error)
	go func() { done <- e.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() < want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEmitPublishFailureCountsAndNeverRetries(t *testing.T) {
	Dropped.Reset()
	var calls atomic.Int32
	e := newEmitter(func(context.Context, runrecord.Record) error {
		calls.Add(1)
		return errors.New("registry down")
	})
	var lines []string
	e.Emit(promote())
	runUntilEmpty(t, logged(&lines), e, &calls, 1)
	if calls.Load() != 1 || testutil.ToFloat64(Dropped.WithLabelValues("publish-failed")) != 1 {
		t.Fatalf("calls %d, publish-failed %v", calls.Load(), testutil.ToFloat64(Dropped.WithLabelValues("publish-failed")))
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "run record not published") || !strings.Contains(lines[0], testUID) {
		t.Fatalf("log = %q", lines)
	}
}

func TestNewRefusesABadRepository(t *testing.T) {
	for _, path := range []string{"UPPER/Case", "https://ghcr.io/acme/stats", "ghcr.io/acme/stats:v1",
		"ghcr.io/acme/stats@sha256:" + strings.Repeat("0", 64)} {
		if _, err := New(path, nil, nil, nil, "cluster-1", "test"); err == nil {
			t.Errorf("New(%q) must fail at startup", path)
		}
	}
}

func TestNewExportsEveryReasonAtZero(t *testing.T) {
	Dropped.Reset()
	newEmitter(nil)
	if n := testutil.CollectAndCount(Dropped); n != len(dropReasons) {
		t.Fatalf("series = %d, want %d", n, len(dropReasons))
	}
	for _, r := range dropReasons {
		if v := testutil.ToFloat64(Dropped.WithLabelValues(r)); v != 0 {
			t.Fatalf("%s = %v", r, v)
		}
	}
}

// TestNewReusesTheRepository: the token a record's publish fetched serves
// the next record, so one burst costs one token exchange, not one per record.
func TestNewReusesTheRepository(t *testing.T) {
	Dropped.Reset()
	var one, three atomic.Int32
	publishAndDrain(t, tokenRegistry(t, &one), promote())
	ops := []*dv1.Operation{promote(), promote(), promote()}
	for i, op := range ops {
		op.UID = types.UID(fmt.Sprintf("%d"+testUID[1:], i))
	}
	publishAndDrain(t, tokenRegistry(t, &three), ops...)
	if one.Load() == 0 || three.Load() != one.Load() {
		t.Fatalf("tokens: %d for one record, %d for three", one.Load(), three.Load())
	}
	if v := testutil.ToFloat64(Dropped.WithLabelValues("publish-failed")); v != 0 {
		t.Fatalf("publish-failed = %v", v)
	}
}

func TestEmitCapsOneNamespace(t *testing.T) {
	Dropped.Reset()
	var published []string
	var calls atomic.Int32
	e := newEmitter(func(_ context.Context, r runrecord.Record) error {
		published = append(published, r.Stage.Namespace)
		calls.Add(1)
		return nil
	})
	for range namespaceCap + 3 {
		e.Emit(promote())
	}
	other := promote()
	other.Namespace = "payments"
	e.Emit(other)
	if v := testutil.ToFloat64(Dropped.WithLabelValues("namespace-full")); v != 3 {
		t.Fatalf("namespace-full = %v", v)
	}
	runUntilEmpty(t, t.Context(), e, &calls, namespaceCap+1)
	if len(published) != namespaceCap+1 || published[len(published)-1] != "payments" {
		t.Fatalf("published %d, last %v", len(published), published[len(published)-1:])
	}
	if len(e.perNS) != 0 {
		t.Fatalf("dequeued records must release their namespace slots: %v", e.perNS)
	}
}

func TestEmitAfterStopCountsShutdown(t *testing.T) {
	Dropped.Reset()
	e := newEmitter(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	e.Emit(promote())
	if v := testutil.ToFloat64(Dropped.WithLabelValues("shutdown")); v != 1 || len(e.q) != 0 {
		t.Fatalf("shutdown = %v, queued %d", v, len(e.q))
	}
}

func TestEmitNoStageCounted(t *testing.T) {
	Dropped.Reset()
	e := newEmitter(nil)
	op := promote()
	op.Status.Policy = nil
	e.Emit(op)
	if testutil.ToFloat64(Dropped.WithLabelValues("no-stage")) != 1 || len(e.q) != 0 {
		t.Fatalf("no-stage %v, queued %d", testutil.ToFloat64(Dropped.WithLabelValues("no-stage")), len(e.q))
	}
}

func TestRunStopsOnContextDone(t *testing.T) {
	Dropped.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var lines []string
	if err := newEmitter(nil).Run(logr.NewContext(ctx, funcr.New(func(_, a string) { lines = append(lines, a) }, funcr.Options{}))); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 || testutil.ToFloat64(Dropped.WithLabelValues("shutdown")) != 0 {
		t.Fatalf("log %q", lines)
	}
}

func TestRunCountsQueuedOnShutdown(t *testing.T) {
	Dropped.Reset()
	release := make(chan struct{})
	pub, started := hanging(release)
	e := newEmitter(pub)
	var lines []string
	ctx, cancel := context.WithCancel(logged(&lines))
	done := make(chan error)
	go func() { done <- e.Run(ctx) }()
	e.Emit(promote())
	<-started
	for range 3 {
		e.Emit(promote())
	}
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(Dropped.WithLabelValues("shutdown")); v != 3 {
		t.Fatalf("shutdown = %v", v)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "run records not published at shutdown") || !strings.Contains(lines[0], `"count"=3`) {
		t.Fatalf("log = %q", lines)
	}
}

func TestRunCountsInFlightAtShutdownAsPublishFailed(t *testing.T) {
	Dropped.Reset()
	started := make(chan struct{})
	e := newEmitter(func(ctx context.Context, _ runrecord.Record) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- e.Run(ctx) }()
	e.Emit(promote())
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(Dropped.WithLabelValues("publish-failed")); v != 1 {
		t.Fatalf("publish-failed = %v", v)
	}
	if v := testutil.ToFloat64(Dropped.WithLabelValues("shutdown")); v != 0 {
		t.Fatalf("shutdown = %v", v)
	}
}

func TestPublishTimeoutBoundsAPublish(t *testing.T) {
	Dropped.Reset()
	var calls atomic.Int32
	e := newEmitter(func(ctx context.Context, _ runrecord.Record) error {
		defer calls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	})
	e.PublishTimeout = 10 * time.Millisecond
	e.Emit(promote())
	runUntilEmpty(t, t.Context(), e, &calls, 1)
	if v := testutil.ToFloat64(Dropped.WithLabelValues("publish-failed")); v != 1 {
		t.Fatalf("publish-failed = %v", v)
	}
}
