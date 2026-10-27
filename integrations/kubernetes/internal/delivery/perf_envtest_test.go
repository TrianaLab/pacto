//go:build perfenvtest

/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/go-logr/logr"
	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestPerfPromoter: a burst of 200 promotes, one per Stage, against a real
// apiserver with the promoter's reconcilers running in a manager as
// cmd/delivery wires them (4 workers, a 3s poll, client-go's 20/30 QPS).
// It measures how long each promote takes to move its pointer, the deepest
// the operation queue gets and how many apiserver requests one operation
// costs. Run it with `make perf-envtest`.
func TestPerfPromoter(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set: run make perf-envtest")
	}
	const n, namespaces = 200, 10
	ctrl.SetLogger(logr.Discard())
	te := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true, CRDs: fluxCRDs()}
	cfg, err := te.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = te.Stop() })
	cfg.QPS, cfg.Burst = 20, 30 // what ctrl.GetConfig gives the promoter
	var calls atomic.Int64
	var byKindMu sync.Mutex
	byKind := map[string]int{}
	counted := rest.CopyConfig(cfg)
	counted.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return perfRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			byKindMu.Lock()
			byKind[r.Method+" "+resourceOf(r.URL.Path)]++
			byKindMu.Unlock()
			return rt.RoundTrip(r)
		})
	})
	kinds := func() string {
		byKindMu.Lock()
		defer byKindMu.Unlock()
		out := fmt.Sprint(byKind)
		clear(byKind)
		return out
	}
	seedCfg := rest.CopyConfig(cfg)
	seedCfg.QPS = -1
	c, err := client.New(seedCfg, client.Options{Scheme: newScheme()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// The world: n Stages over the namespaces, each running A with its apply
	// OCIRepository and Kustomization, and the Pacto that evaluated A.
	for i := range namespaces {
		must(t, c.Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace",
			"metadata": map[string]any{"name": fmt.Sprintf("perf-%d", i)}}}))
	}
	must(t, c.Create(ctx, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}}))
	p := pactoObj("payments", "payments", c1, "Compliant", 2)
	p.Object["spec"] = map[string]any{"contractRef": map[string]any{"oci": "reg.example/pactos/payments@" + c1}}
	status := p.Object["status"]
	must(t, c.Create(ctx, p))
	p.Object["status"] = status
	must(t, c.Status().Update(ctx, p))
	stages := make([]*dv1.Stage, n)
	perfParallel(t, n, func(i int) error {
		space, name := fmt.Sprintf("perf-%d", i%namespaces), fmt.Sprintf("payments-%03d", i)
		s := stageObj(dA)
		s.Namespace, s.Name, s.Spec.ApplyRef.Name = space, name, name
		s.Spec.Candidates.OCIRepositoryRef.Name = name + "-candidates"
		s.Status.Stream.ID = "cid/" + space + "/" + name
		st := s.Status
		if err := c.Create(ctx, s); err != nil {
			return err
		}
		s.Status = st
		if err := c.Status().Update(ctx, s); err != nil {
			return err
		}
		stages[i] = s
		oci := ociRepo(name, dA, "latest@"+dA, true)
		oci.SetNamespace(space)
		oci.SetLabels(map[string]string{ApplyRefLabel: name})
		ociStatus := oci.Object["status"]
		if err := c.Create(ctx, oci); err != nil {
			return err
		}
		oci.Object["status"] = ociStatus
		if err := c.Status().Update(ctx, oci); err != nil {
			return err
		}
		// The candidate source offers what runs now, so no Stage proposes a
		// promote of its own while the burst is measured.
		cand := verifiedRepo(name+"-candidates", "latest@"+dA)
		cand.SetNamespace(space)
		candStatus := cand.Object["status"]
		if err := c.Create(ctx, cand); err != nil {
			return err
		}
		cand.Object["status"] = candStatus
		if err := c.Status().Update(ctx, cand); err != nil {
			return err
		}
		k := kustomization(name, name, "True", "ReconciliationSucceeded", "latest@"+dA, "latest@"+dA)
		k.SetNamespace(space)
		ks := k.Object["status"]
		if err := c.Create(ctx, k); err != nil {
			return err
		}
		k.Object["status"] = ks
		return c.Status().Update(ctx, k)
	})

	opts := ctrl.Options{Scheme: newScheme(), Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"}
	if os.Getenv("PERF_CACHE_UNSTRUCTURED") != "" {
		opts.Client.Cache = &client.CacheOptions{Unstructured: true}
	}
	mgr, err := ctrl.NewManager(counted, opts)
	must(t, err)
	reg := &fakeRegistry{contracts: map[string]string{dA: contractRef(c1), dB: contractRef(c2)},
		bundles: map[string]*contract.Bundle{contractRef(c1): bundle("1.0.0"), contractRef(c2): bundle("1.1.0")}}
	cluster := Cluster{Client: mgr.GetClient(), Registry: func(...string) delivery.Registry { return reg }}
	policy := delivery.ClassPolicy{ControllerUser: promoterSA, AgentGroups: []string{"pacto:agents"}, BreakGlassGroups: []string{"pacto:break-glass"}}
	gates := NewGates(cluster, map[string]delivery.Gate{"mira": &fakeHealth{result: delivery.Pass}})
	must(t, (&OperationReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster, Policy: policy,
		Workers: 4, Gates: gates, Now: time.Now, Poll: 3 * time.Second}).SetupWithManager(mgr))
	must(t, (&StageReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster, Policy: policy,
		Workers: 4, ClusterID: "cid", Gates: gates, Now: time.Now, Poll: 3 * time.Second}).SetupWithManager(mgr))
	go func() { _ = mgr.Start(ctx) }()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("the cache never synced")
	}
	time.Sleep(5 * time.Second) // the Stages' first reconciles settle
	kinds()
	idle := calls.Load()
	time.Sleep(5 * time.Second)
	idleRate := float64(calls.Load()-idle) / 5
	t.Logf("idle apiserver requests over 5s: %s", kinds())

	// The burst: every promote at once, as an agent fleet would.
	before := calls.Load()
	created := make([]time.Time, n)
	start := time.Now()
	perfParallel(t, n, func(i int) error {
		s := stages[i]
		o := opObj(fmt.Sprintf("promote-%03d", i), delivery.TypePromote, dA, dB, bot)
		o.Namespace, o.Spec.Stage, o.UID = s.Namespace, s.Name, ""
		o.Spec.IntentDigest = delivery.IntentDigest(o.Spec, s.Spec)
		created[i] = time.Now()
		return c.Create(ctx, o)
	})
	var maxDepth float64
	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Go(func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				maxDepth = max(maxDepth, queueDepth(t, "delivery-operation"))
			}
		}
	})
	moved := make([]time.Duration, n)
	deadline := time.Now().Add(10 * time.Minute)
	for left := n; left > 0; {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d promotes never moved their pointer", left, n)
		}
		time.Sleep(100 * time.Millisecond)
		list := &dv1.OperationList{}
		must(t, c.List(ctx, list))
		left = 0
		for _, o := range list.Items {
			var i int
			if _, err := fmt.Sscanf(o.Name, "promote-%03d", &i); err != nil {
				continue
			}
			switch {
			case o.Status.Applied && moved[i] == 0:
				moved[i] = time.Since(created[i])
			case o.Status.Phase == delivery.PhaseConcluded && !o.Status.Applied:
				t.Fatalf("%s concluded without moving: %s %s", o.Name, o.Status.Reason, o.Status.Message)
			case moved[i] == 0:
				left++
			}
		}
	}
	wall := time.Since(start)
	t.Logf("apiserver requests during the burst: %s", kinds())
	close(stop)
	sampler.Wait()
	used := calls.Load() - before - int64(idleRate*wall.Seconds())
	slices.Sort(moved)
	perOp := float64(used) / n
	t.Logf("promoter: %d promotes, all moved in %s; time to move p50 %s p95 %s; max queue depth %.0f; %.1f apiserver requests per operation (%.1f/s idle)",
		n, wall.Round(time.Millisecond), moved[n/2].Round(time.Millisecond), moved[n*95/100].Round(time.Millisecond), maxDepth, perOp, idleRate)
	if path := os.Getenv("PERF_REPORT"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		must(t, err)
		defer func() { _ = f.Close() }()
		_, err = fmt.Fprintf(f, "\n| promotes | all moved | time to move p50 | p95 | max queue depth | apiserver req/operation |\n"+
			"|---:|---:|---:|---:|---:|---:|\n| %d | %s | %s | %s | %.0f | %.1f |\n", n, wall.Round(time.Millisecond),
			moved[n/2].Round(time.Millisecond), moved[n*95/100].Round(time.Millisecond), maxDepth, perOp)
		must(t, err)
	}
	// Budgets: generous tripwires over the README numbers.
	if perOp > promoterMaxAPIPerOp {
		t.Errorf("%.1f apiserver requests per promote, budget %d", perOp, promoterMaxAPIPerOp)
	}
	if wall > promoterMaxBurst {
		t.Errorf("the burst took %s, budget %s", wall, promoterMaxBurst)
	}
}

const (
	promoterMaxAPIPerOp = 25
	promoterMaxBurst    = 4 * time.Minute
)

// resourceOf is the resource (and subresource) a request path names.
func resourceOf(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 0 && parts[0] == "apis" && len(parts) > 2 {
		parts = parts[3:]
	} else if len(parts) > 1 {
		parts = parts[2:]
	}
	if len(parts) > 1 && parts[0] == "namespaces" {
		parts = parts[2:]
	}
	switch len(parts) {
	case 0:
		return "namespaces"
	case 1, 2:
		return parts[0]
	}
	return parts[0] + "/" + parts[2]
}

type perfRoundTrip func(*http.Request) (*http.Response, error)

func (f perfRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func queueDepth(t *testing.T, name string) float64 {
	mfs, err := metrics.Registry.Gather()
	must(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "workqueue_depth" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelIs(m, "name", name) {
				return m.GetGauge().GetValue()
			}
		}
	}
	return 0
}

func labelIs(m *dto.Metric, k, v string) bool {
	for _, l := range m.GetLabel() {
		if l.GetName() == k && l.GetValue() == v {
			return true
		}
	}
	return false
}

func perfParallel(t *testing.T, n int, f func(i int) error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, n)
	sem := make(chan struct{}, 32)
	for i := range n {
		wg.Go(func() { sem <- struct{}{}; errs[i] = f(i); <-sem })
	}
	wg.Wait()
	for _, err := range errs {
		must(t, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fluxCRDs are Flux's OCIRepository and Kustomization, as much of them as
// the promoter reads.
func fluxCRDs() []*apiextensionsv1.CustomResourceDefinition {
	crd := func(group, kind, plural string) *apiextensionsv1.CustomResourceDefinition {
		yes := true
		return &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: plural + "." + group},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{Group: group, Scope: apiextensionsv1.NamespaceScoped,
				Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: kind, ListKind: kind + "List", Plural: plural},
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true,
					Subresources: &apiextensionsv1.CustomResourceSubresources{Status: &apiextensionsv1.CustomResourceSubresourceStatus{}},
					Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object", XPreserveUnknownFields: &yes}}}}}}
	}
	return []*apiextensionsv1.CustomResourceDefinition{
		crd("source.toolkit.fluxcd.io", "OCIRepository", "ocirepositories"),
		crd("kustomize.toolkit.fluxcd.io", "Kustomization", "kustomizations"),
	}
}
