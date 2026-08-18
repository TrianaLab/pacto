package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/k8sclient"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// stubK8s installs a live Kubernetes source that answers, under the kubeconfig
// context id ctxName. An empty ctxName reproduces the in-cluster case: a dashboard
// pod has no kubeconfig context, so the source falls back to the id "k8s".
func stubK8s(t *testing.T, ctxName string) {
	t.Helper()
	origClient, origCtx := newK8sClient, currentKubeContext
	t.Cleanup(func() { newK8sClient, currentKubeContext = origClient, origCtx })
	newK8sClient = func() (k8sclient.K8sClient, error) {
		return &fakeFleetK8sClient{
			disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
			listData: []byte(`{"items":[{"metadata":{"name":"svc-a","namespace":"prod"},"status":{"contractStatus":"Compliant","contract":{"serviceName":"svc-a"}}}]}`),
		}, nil
	}
	currentKubeContext = func() string { return ctxName }
}

// writeTrace writes an OTLP/JSON export naming one caller and one callee, and
// returns its directory and file name — the shape an operator-managed mount has,
// where the export sits directly inside the source's own directory.
func writeTrace(t *testing.T, from, to string) (root, file string) {
	t.Helper()
	root = t.TempDir()
	file = "traces.json"
	body := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"` + from + `"}}]},
	  "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"` + to + `"}}]}]}]}]}`
	if err := os.WriteFile(filepath.Join(root, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, file
}

// TestService_Fleet_SourceIDsMustBeUniqueAcrossTheWholeFleet is the Data Source
// identity acceptance.
//
// A declared observation name becomes the Product's Data Source key, so it has to
// be unique against every OTHER source the same dashboard assembles — not merely
// against the other observation sources. Each case below is a configuration that
// reads as perfectly reasonable in isolation and publishes two semantic sources
// under one Product identity.
func TestService_Fleet_SourceIDsMustBeUniqueAcrossTheWholeFleet(t *testing.T) {
	root, file := writeTrace(t, "web", "payments")

	for _, tc := range []struct {
		name    string
		id      string
		opts    FleetOptions
		kubeCtx string
		stubK8s bool
		want    string
	}{
		{
			// The counterexample. In a dashboard pod there is no kubeconfig context,
			// so the live cluster source falls back to the id "k8s" — the same id an
			// operator-managed observation source is free to declare.
			name:    "in-cluster kubernetes fallback",
			id:      "k8s",
			opts:    FleetOptions{IncludeK8s: true},
			stubK8s: true,
			want:    `"k8s" is claimed by observation and kubernetes`,
		},
		{
			// The same collision under a named kubeconfig context, so the rule is
			// about the id the source actually declares, not about the literal "k8s".
			name:    "named kubeconfig context",
			id:      "prod-eu",
			opts:    FleetOptions{IncludeK8s: true},
			kubeCtx: "prod-eu",
			stubK8s: true,
			want:    `"prod-eu" is claimed by observation and kubernetes`,
		},
		{
			name: "evidence server over http",
			id:   "evidence-http",
			opts: FleetOptions{EvidenceURLs: []string{"http://evidence.invalid"}},
			want: `"evidence-http" is claimed by evidence-http and observation`,
		},
		{
			name: "local bundle root",
			id:   "local",
			opts: FleetOptions{LocalRoots: []string{t.TempDir()}},
			want: `"local" is claimed by local and observation`,
		},
		{
			name: "target-state fixture",
			id:   "target-state",
			opts: FleetOptions{TargetStateFiles: []string{filepath.Join(t.TempDir(), "targets.yaml")}},
			want: `"target-state" is claimed by target-state and observation`,
		},
		{
			// The ad-hoc `--traces` convenience names its sources positionally. A
			// declarative source is free to declare that same name.
			name: "ad-hoc positional observation id",
			id:   "observation",
			opts: FleetOptions{ObservationSources: TraceFileSources([]string{filepath.Join(root, file)})},
			want: `"observation" is claimed by observation and observation`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stubK8s {
				stubK8s(t, tc.kubeCtx)
			}
			opts := tc.opts
			opts.ObservationSources = append(opts.ObservationSources,
				ObservationSourceSpec{ID: tc.id, Root: root, Path: file})

			_, err := NewService(nil, nil).Fleet(context.Background(), opts)
			if err == nil {
				t.Fatal("a snapshot was published with two sources under one Data Source identity")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name the collision %s", err, tc.want)
			}
		})
	}
}

// TestService_Fleet_SourceIDCollisionWithOCIAndCache covers the two source kinds
// that need a bundle store, in one snapshot: both are configured, so the error
// must name BOTH collisions rather than stopping at the first.
func TestService_Fleet_SourceIDCollisionWithOCIAndCache(t *testing.T) {
	root, file := writeTrace(t, "web", "payments")
	_, err := NewService(nil, nil).Fleet(context.Background(), FleetOptions{
		OCIRefs:      []string{"oci://example.invalid/svc:1.0.0"},
		IncludeCache: true,
		ObservationSources: []ObservationSourceSpec{
			{ID: "oci", Root: root, Path: file},
			{ID: "cache", Root: root, Path: file},
		},
	})
	if err == nil {
		t.Fatal("expected the colliding oci and cache identities to be refused")
	}
	for _, want := range []string{`"cache" is claimed by observation and cache`, `"oci" is claimed by observation and oci`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %s", err, want)
		}
	}
}

// TestService_Fleet_SourceIDCollisionIsOrderIndependent proves the answer is a
// property of the configured set, not of the order it was written in. Two
// collisions, reported the same way whichever entry comes first.
func TestService_Fleet_SourceIDCollisionIsOrderIndependent(t *testing.T) {
	root, file := writeTrace(t, "web", "payments")
	stubK8s(t, "")

	build := func(specs ...ObservationSourceSpec) string {
		t.Helper()
		_, err := NewService(nil, nil).Fleet(context.Background(), FleetOptions{
			IncludeK8s: true, IncludeCache: true, ObservationSources: specs,
		})
		if err == nil {
			t.Fatal("expected a collision")
		}
		return err.Error()
	}
	k8s := ObservationSourceSpec{ID: "k8s", Root: root, Path: file}
	cache := ObservationSourceSpec{ID: "cache", Root: root, Path: file}
	if forward, reversed := build(k8s, cache), build(cache, k8s); forward != reversed {
		t.Errorf("permuting the configuration changed the answer:\n%s\n%s", forward, reversed)
	}
}

// TestService_Fleet_DistinctSourceIDsPublishOneToOne is the positive half: with
// every id distinct, the snapshot builds, and every Data Source the Product lists
// resolves to exactly one source detail. That is the invariant the rejection
// exists to protect — one Product Data Source key, one semantic source.
func TestService_Fleet_DistinctSourceIDsPublishOneToOne(t *testing.T) {
	stubK8s(t, "")
	rootA, fileA := writeTrace(t, "web", "payments")
	rootB, fileB := writeTrace(t, "checkout", "orders")
	local := t.TempDir()
	writeLocalBundle(t, filepath.Join(local, "svc-a"), "svc-a")

	snap, err := NewService(nil, nil).Fleet(context.Background(), FleetOptions{
		IncludeK8s: true,
		LocalRoots: []string{local},
		ObservationSources: []ObservationSourceSpec{
			{ID: "orders-traces", Root: rootA, Path: fileA},
			{ID: "checkout-traces", Root: rootB, Path: fileB},
		},
	})
	if err != nil {
		t.Fatalf("a configuration with distinct identities was refused: %v", err)
	}

	q := fleet.NewQuery(snap)
	list, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindSource}})
	if err != nil {
		t.Fatalf("entities: %v", err)
	}
	if list.Total != 4 {
		t.Fatalf("listed %d data sources, want 4 (k8s, local, two observation)", list.Total)
	}
	seen := map[string]bool{}
	for _, ref := range list.Entities {
		if seen[ref.Key] {
			t.Fatalf("data source key %q listed twice", ref.Key)
		}
		seen[ref.Key] = true
		detail, err := q.EntityDetail(fleet.KindSource, ref.Key)
		if err != nil {
			t.Fatalf("a listed data source has no detail: %v", err)
		}
		if detail.Entity.Key != ref.Key {
			t.Errorf("detail for %q resolved to %q", ref.Key, detail.Entity.Key)
		}
		// The kind is what distinguishes two sources a shared key would have merged.
		if detail.Entity.Secondary != ref.Secondary {
			t.Errorf("data source %q: list says %q, detail says %q", ref.Key, ref.Secondary, detail.Entity.Secondary)
		}
	}
	for _, id := range []string{"k8s", "local", "orders-traces", "checkout-traces"} {
		if !seen[id] {
			t.Errorf("data source %q never reached the Product", id)
		}
	}
}

// tamperedFleet builds a snapshot from two observation sources where one of them
// — `orders-traces` — has had a symlink out of its own mount planted in its
// export storage, pointing at a perfectly parseable trace that names endpoints
// nothing else does. Consuming it would therefore be visible in the answer.
//
// `checkout-traces` is the healthy sibling, and the local bundles define its
// endpoints so its observed edge resolves to real services rather than to an
// unresolved-identity limitation.
func tamperedFleet(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	healthyRoot, healthyFile := writeTrace(t, "web", "payments")

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"stolen"}}]},
	  "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"exfiltrated"}}]}]}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tampered := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(tampered, "traces.json")); err != nil {
		t.Fatal(err)
	}

	local := t.TempDir()
	writeLocalBundle(t, filepath.Join(local, "web"), "web")
	writeLocalBundle(t, filepath.Join(local, "payments"), "payments")

	snap, err := NewService(nil, nil).Fleet(context.Background(), FleetOptions{
		LocalRoots: []string{local},
		ObservationSources: []ObservationSourceSpec{
			{ID: "orders-traces", Root: tampered, Path: "traces.json"},
			{ID: "checkout-traces", Root: healthyRoot, Path: healthyFile},
		},
	})
	if err != nil {
		t.Fatalf("one broken source must degrade, not fail the build: %v", err)
	}
	return snap
}

// TestService_Fleet_EscapingObservationSourceIsExplicitUnavailableKnowledge is the
// other half of the rooted read: refusing to leave the mount is not silence. The
// broken source becomes an explicit SOURCE_UNAVAILABLE limitation attributed to
// its declared identity.
func TestService_Fleet_EscapingObservationSourceIsExplicitUnavailableKnowledge(t *testing.T) {
	snap := tamperedFleet(t)

	// The refusal is knowledge, not silence: the Product learns this named source
	// did not answer, so an absent edge is never read as an absent dependency.
	var unavailable *fleet.Limitation
	for i, l := range snap.Limitations {
		if l.Code == fleet.LimitationSourceUnavailable && l.Source == "orders-traces" {
			unavailable = &snap.Limitations[i]
		}
	}
	if unavailable == nil {
		t.Fatalf("expected SOURCE_UNAVAILABLE for orders-traces, got %+v", snap.Limitations)
	}
	var state *fleet.SourceState
	for i, s := range snap.Sources {
		if s.ID == "orders-traces" {
			state = &snap.Sources[i]
		}
	}
	if state == nil || state.Status != fleet.SourceUnavailable {
		t.Fatalf("source state = %+v, want an unavailable orders-traces", state)
	}
	// Categorized, not echoed: the snapshot says this source did not answer without
	// republishing the underlying error text (which would carry the mount's paths).
	if state.Error == nil || state.Error.Code != "UNAVAILABLE" {
		t.Errorf("source error = %+v, want a categorized UNAVAILABLE", state.Error)
	}
}

// TestService_Fleet_EscapedBytesNeverReachTheSnapshot is the containment half: the
// healthy sibling still answers under its own identity, and nothing the escaping
// symlink pointed at was consumed.
func TestService_Fleet_EscapedBytesNeverReachTheSnapshot(t *testing.T) {
	snap := tamperedFleet(t)

	// The healthy sibling is unaffected: its edge is in the snapshot, attributed to
	// its own declared identity.
	var healthyEdge bool
	for _, rel := range snap.Relationships {
		if rel.Provenance == fleet.ProvenanceObserved && rel.ToService == "payments" {
			healthyEdge = true
			if rel.Source != "checkout-traces" {
				t.Errorf("observed edge attributed to %q, want checkout-traces", rel.Source)
			}
		}
	}
	if !healthyEdge {
		t.Errorf("the healthy source stopped answering: relationships = %+v", snap.Relationships)
	}

	// Nothing the symlink pointed at was consumed. Searching the whole serialized
	// snapshot catches it wherever it might have landed, not only where expected.
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("exfiltrated")) || bytes.Contains(body, []byte("stolen")) {
		t.Error("bytes from outside the declared mount reached the snapshot")
	}
}

// TestCheckSourceIDsAreUnique_NoSources covers the empty configuration: nothing
// configured is not a collision.
func TestCheckSourceIDsAreUnique_NoSources(t *testing.T) {
	if err := checkSourceIDsAreUnique(nil); err != nil {
		t.Errorf("no sources = %v, want no error", err)
	}
}

// TestService_Fleet_TripleCollisionNamesEveryClaimant proves a third source
// claiming an already-contested id is reported too, rather than the message
// stopping at the first pair.
func TestService_Fleet_TripleCollisionNamesEveryClaimant(t *testing.T) {
	err := checkSourceIDsAreUnique([]fleet.Source{
		fleet.NewFailingSource("k8s", "kubernetes", errors.New("x")),
		fleet.NewFailingSource("k8s", "oci", errors.New("x")),
		fleet.NewFailingSource("k8s", "observation", errors.New("x")),
	})
	if err == nil || !strings.Contains(err.Error(), "kubernetes and oci and observation") {
		t.Fatalf("error = %v, want every claimant named", err)
	}
}
