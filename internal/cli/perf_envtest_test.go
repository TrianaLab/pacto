//go:build perfenvtest

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The delivery and fleet views of the dashboard against a real apiserver:
// 60 namespaces, 500 services in 4 environments (2,000 Stages), 10,000
// Operations and three people whose RBAC differs. Each view is measured at
// 1, 10 and 50 callers at once, at a quarter of the size and at full size,
// for latency, allocations and how many apiserver requests one dashboard
// request costs. Run it with `make perf-envtest`; PERF_REPORT=path writes
// the table.

var (
	gvrCRD       = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	gvrNS        = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	gvrCM        = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	gvrRole      = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}
	gvrCRole     = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
	gvrRB        = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}
	gvrCRB       = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
	gvrPerfStage = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}
	gvrPerfOp    = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "operations"}
	gvrPerfAppr  = schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "approvals"}
	gvrPerfOCI   = schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}
)

const (
	perfNamespaces = 60
	perfPolicyNS   = "pacto-delivery"
	hotStage       = "svc-000-prod-eu" // the Stage with the longest history
)

var perfEnvs = []string{"dev", "staging", "prod-eu", "prod-us"}

func perfNS(i int) string  { return fmt.Sprintf("ns-%02d", i%perfNamespaces) }
func perfSvc(i int) string { return fmt.Sprintf("svc-%03d", i) }
func perfDigest(svc string, v int) string {
	return fmt.Sprintf("sha256:%064x", uint64(len(svc))<<32|uint64(v)<<16|uint64(svcIndex(svc)))
}
func svcIndex(svc string) int { var i int; _, _ = fmt.Sscanf(svc, "svc-%d", &i); return i }

// people: alice reads three namespaces, bob forty, audrey (an auditor)
// every namespace, read-only.
var people = []perfPerson{
	{name: "alice", groups: []string{"team-a"}, token: idToken("alice", []string{"team-a"})},
	{name: "bob", groups: []string{"team-b"}, token: idToken("bob", []string{"team-b"})},
	{name: "audrey", groups: []string{"auditors"}, token: idToken("audrey", []string{"auditors"})},
}

// auditors are audrey and 49 colleagues in the same group: fifty distinct
// people asking at once, so nothing one person's requests share helps.
var auditors = func() []perfPerson {
	out := []perfPerson{people[2]}
	for i := 1; i < 50; i++ {
		name := fmt.Sprintf("auditor-%02d", i)
		out = append(out, perfPerson{name: name, groups: []string{"auditors"}, token: idToken(name, []string{"auditors"})})
	}
	return out
}()

func readable(who string) []string {
	if strings.HasPrefix(who, "auditor-") {
		who = "audrey"
	}
	n := map[string]int{"alice": 3, "bob": 40, "audrey": perfNamespaces}[who]
	out := []string{}
	for i := range n {
		out = append(out, perfNS(i))
	}
	return out
}

// counter counts the requests that reach the apiserver.
type counter struct{ n atomic.Int64 }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// wrap counts beneath every other wrapper, so an answer a client-side
// cache served is not counted.
func (c *counter) wrap(cfg *rest.Config) *rest.Config {
	cfg = rest.CopyConfig(cfg)
	outer := cfg.WrapTransport
	cfg.WrapTransport = func(base http.RoundTripper) http.RoundTripper {
		var rt http.RoundTripper = roundTrip(func(r *http.Request) (*http.Response, error) { c.n.Add(1); return base.RoundTrip(r) })
		if outer != nil {
			rt = outer(rt)
		}
		return rt
	}
	return cfg
}

// perfRegistry answers every artifact with a contract whose version is the
// digest's position, as a registry would.
type perfRegistry struct{}

func (perfRegistry) Annotations(_ context.Context, ref string) (map[string]string, error) {
	_, d, _ := strings.Cut(ref, "@")
	return map[string]string{delivery.ContractAnnotation: "oci://reg.example/pactos/c@" + d}, nil
}

func (perfRegistry) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	_, d, _ := strings.Cut(ref, "@")
	return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
		Service: contract.Service{Name: "c", Version: "1." + d[len(d)-6:len(d)-4] + ".0", Owner: contract.Owner{Team: "team-a"}}}}, nil
}

func (perfRegistry) Resolve(context.Context, string) (string, error) { return "", nil }

// ── seeding ────────────────────────────────────────────────────────────

// toU is v as an object of apiVersion and kind.
func toU(v any, apiVersion, kind string) *unstructured.Unstructured {
	b, _ := json.Marshal(v)
	u := &unstructured.Unstructured{}
	_ = json.Unmarshal(b, &u.Object)
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	return u
}

// parallel runs f(i) for i in [0,n) on 32 workers, failing on the first error.
func parallel(t testing.TB, n int, f func(i int) error) {
	t.Helper()
	var wg sync.WaitGroup
	var first atomic.Pointer[error]
	next := atomic.Int64{}
	for range 32 {
		wg.Go(func() {
			for i := int(next.Add(1) - 1); i < n && first.Load() == nil; i = int(next.Add(1) - 1) {
				if err := f(i); err != nil {
					first.CompareAndSwap(nil, &err)
				}
			}
		})
	}
	wg.Wait()
	if e := first.Load(); e != nil {
		t.Fatal(*e)
	}
}

func create(ctx context.Context, ri dynamic.ResourceInterface, u *unstructured.Unstructured, status bool) error {
	st := u.Object["status"]
	out, err := ri.Create(ctx, u, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	if err != nil || !status || st == nil {
		return err
	}
	out.Object["status"] = st
	_, err = ri.UpdateStatus(ctx, out, metav1.UpdateOptions{})
	return err
}

func installCRDs(t testing.TB, ctx context.Context, adm dynamic.Interface) {
	bases := filepath.Join("..", "..", "integrations", "kubernetes", "config", "crd", "bases")
	files, _ := filepath.Glob(filepath.Join(bases, "*.yaml"))
	docs := []string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		must(t, err)
		docs = append(docs, string(b))
	}
	// Flux's OCIRepository, as much of it as delivery reads.
	docs = append(docs, `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: ocirepositories.source.toolkit.fluxcd.io}
spec:
  group: source.toolkit.fluxcd.io
  names: {kind: OCIRepository, listKind: OCIRepositoryList, plural: ocirepositories, singular: ocirepository}
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    subresources: {status: {}}
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}`)
	for _, d := range docs {
		u := &unstructured.Unstructured{}
		must(t, k8syaml.Unmarshal([]byte(d), &u.Object))
		if _, err := adm.Resource(gvrCRD).Create(ctx, u, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Minute)
	for _, g := range []schema.GroupVersionResource{gvrPerfStage, gvrPerfOp, gvrPerfAppr, gvrPerfOCI} {
		for {
			if _, err := adm.Resource(g).List(ctx, metav1.ListOptions{Limit: 1}); err == nil {
				break
			} else if time.Now().After(deadline) {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func installRBAC(t testing.TB, ctx context.Context, adm dynamic.Interface) {
	rules := []any{
		map[string]any{"apiGroups": []any{"delivery.pacto.trianalab.io"}, "resources": []any{"stages", "operations", "approvals"}, "verbs": []any{"get", "list", "watch"}},
		map[string]any{"apiGroups": []any{"pacto.trianalab.io"}, "resources": []any{"pactos", "pactorevisions"}, "verbs": []any{"get", "list", "watch"}},
		map[string]any{"apiGroups": []any{"source.toolkit.fluxcd.io"}, "resources": []any{"ocirepositories"}, "verbs": []any{"get", "list"}},
	}
	obj := func(kind, ns, name string, extra map[string]any) *unstructured.Unstructured {
		m := map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": kind, "metadata": map[string]any{"name": name, "namespace": ns}}
		for k, v := range extra {
			m[k] = v
		}
		return &unstructured.Unstructured{Object: m}
	}
	subject := func(kind, name string) []any {
		return []any{map[string]any{"kind": kind, "name": name, "apiGroup": "rbac.authorization.k8s.io"}}
	}
	ref := func(kind, name string) map[string]any {
		return map[string]any{"kind": kind, "name": name, "apiGroup": "rbac.authorization.k8s.io"}
	}
	must(t, create(ctx, adm.Resource(gvrCRole), obj("ClusterRole", "", "pacto-delivery-reader", map[string]any{"rules": rules}), false))
	must(t, create(ctx, adm.Resource(gvrCRB), obj("ClusterRoleBinding", "", "auditors", map[string]any{
		"subjects": subject("Group", "auditors"), "roleRef": ref("ClusterRole", "pacto-delivery-reader")}), false))
	for _, who := range []struct{ group, person string }{{"team-a", "alice"}, {"team-b", "bob"}} {
		for _, ns := range readable(who.person) {
			must(t, create(ctx, adm.Resource(gvrRB).Namespace(ns), obj("RoleBinding", ns, who.group, map[string]any{
				"subjects": subject("Group", who.group), "roleRef": ref("ClusterRole", "pacto-delivery-reader")}), false))
		}
	}
	// Every authenticated user reads the published identity policy.
	must(t, create(ctx, adm.Resource(gvrRole).Namespace(perfPolicyNS), obj("Role", perfPolicyNS, "identities", map[string]any{
		"rules": []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps"}, "resourceNames": []any{delivery.PolicyConfigMap}, "verbs": []any{"get"}}}}), false))
	must(t, create(ctx, adm.Resource(gvrRB).Namespace(perfPolicyNS), obj("RoleBinding", perfPolicyNS, "identities", map[string]any{
		"subjects": subject("Group", "system:authenticated"), "roleRef": ref("Role", "identities")}), false))
	policy, _ := json.Marshal(delivery.DefaultClassPolicy())
	must(t, create(ctx, adm.Resource(gvrCM).Namespace(perfPolicyNS), &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": delivery.PolicyConfigMap},
		"data": map[string]any{delivery.PolicyKey: string(policy)}}}, false))
}

// world is what is seeded so far.
type world struct {
	services int
	ops      int
	stages   []delivery.StageObject
}

func seedStages(t testing.TB, ctx context.Context, adm dynamic.Interface, w *world, services int) {
	r := rand.New(rand.NewPCG(uint64(services), 7))
	var add []delivery.StageObject
	for i := w.services; i < services; i++ {
		svc, ns := perfSvc(i), perfNS(i)
		var published []string
		for v := range 13 {
			published = append(published, perfDigest(svc, v))
		}
		for _, env := range perfEnvs {
			ss := delivery.StageStatus{Pointer: published[10], Verified: published[8:11], Published: published, Candidate: published[12],
				Level:      delivery.Level([]string{"observe", "advise", "auto-nonbreaking", "autonomous"}[r.IntN(4)]),
				Conditions: []delivery.Condition{{Type: "Ready", Status: "True", Reason: "Ready", LastTransitionTime: "2026-10-20T10:00:00Z"}},
				Stream:     delivery.Stream{ID: "cid/" + ns + "/" + svc + "-" + env}}
			for k := range 20 {
				ss.Stream.Concluded = append(ss.Stream.Concluded, delivery.Concluded{UID: fmt.Sprintf("u-%d", k), Seq: int64(k + 1), Outcome: delivery.Succeeded})
			}
			if r.IntN(50) == 0 {
				ss.Frozen, ss.FrozenReason = true, "Errored"
			}
			add = append(add, delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: ns, Name: svc + "-" + env},
				Spec:   delivery.StageSpec{Service: delivery.ServiceRef{Name: svc}, Environment: env, ApplyRef: delivery.ApplyRef{Name: svc + "-" + env}},
				Status: ss})
		}
	}
	parallel(t, len(add), func(i int) error {
		st := add[i]
		u := toU(st, "delivery.pacto.trianalab.io/v1alpha1", "Stage")
		if err := create(ctx, adm.Resource(gvrPerfStage).Namespace(st.Metadata.Namespace), u, true); err != nil {
			return err
		}
		oci := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "source.toolkit.fluxcd.io/v1", "kind": "OCIRepository",
			"metadata": map[string]any{"name": st.Spec.ApplyRef.Name, "namespace": st.Metadata.Namespace},
			"spec":     map[string]any{"url": "oci://reg.example/apps/" + st.Spec.Service.Name, "ref": map[string]any{"digest": st.Status.Pointer}}}}
		return create(ctx, adm.Resource(gvrPerfOCI).Namespace(st.Metadata.Namespace), oci, false)
	})
	w.services, w.stages = services, append(w.stages, add...)
}

// seedOps adds Operations up to total: a tenth on the hot Stage, the rest
// spread over every Stage; most concluded, some held for a team, some
// failed, errored or in flight, with an Approval on every tenth concluded.
func seedOps(t testing.TB, ctx context.Context, adm dynamic.Interface, w *world, total int) {
	r := rand.New(rand.NewPCG(uint64(total), 11))
	type planned struct {
		op       delivery.OperationObject
		approval bool
	}
	var add []planned
	for k := w.ops; k < total; k++ {
		st := w.stages[r.IntN(len(w.stages))]
		if k%10 == 0 {
			st = w.stages[2] // svc-000-prod-eu
		}
		svc := st.Spec.Service.Name
		from, to := perfDigest(svc, r.IntN(12)), perfDigest(svc, r.IntN(12))
		o := delivery.OperationObject{Metadata: delivery.ObjectMeta{Namespace: st.Metadata.Namespace, Name: fmt.Sprintf("op-%05d", k),
			Labels: map[string]string{"delivery.pacto.trianalab.io/stage": st.Metadata.Name}},
			Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: st.Metadata.Name, Facet: delivery.FacetPointer,
				Params: delivery.Params{From: from, To: to}, Actor: delivery.Actor{User: perfIssuer + "#alice", Groups: []string{"team-a"}}}}
		o.Spec.IntentDigest = delivery.IntentDigest(o.Spec, st.Spec)
		s := &o.Status
		switch p := r.IntN(100); {
		case p < 85:
			s.Phase, s.Outcome, s.Verification, s.Reason = delivery.PhaseConcluded, delivery.Succeeded, delivery.Verified, "Verified"
			s.Seq, s.Applied = int64(k+1), true
		case p < 90:
			s.Phase, s.Outcome, s.Reason, s.Seq = delivery.PhaseConcluded, delivery.Failed, "VerifyFailed: mira: error ratio", int64(k+1)
		case p < 92:
			s.Phase, s.Outcome, s.Reason = delivery.PhaseConcluded, delivery.Errored, "Errored: the apply did not converge"
		case p < 97:
			team := []string{"team-a", "team-b", "team-c"}[r.IntN(3)]
			s.Phase, s.Question = delivery.PhaseHeld, "Approve the breaking change?"
			s.Plan = &delivery.Plan{Floor: delivery.Floor{Human: true, Decision: delivery.Approve, Owners: []string{team}, Consumers: []string{"checkout"}}}
		default:
			s.Phase, s.Applied = delivery.PhaseVerifying, true
		}
		s.Verdicts = []delivery.Verdict{{Gate: "contract", Phase: "pre", Result: delivery.Pass, Message: "compatible"},
			{Gate: "mira", Phase: "verify", Result: delivery.Pass, Message: "error ratio 0.1%"}}
		add = append(add, planned{o, s.Phase == delivery.PhaseConcluded && k%10 == 3})
	}
	parallel(t, len(add), func(i int) error {
		o := add[i].op
		u := toU(o, "delivery.pacto.trianalab.io/v1alpha1", "Operation")
		ri := adm.Resource(gvrPerfOp).Namespace(o.Metadata.Namespace)
		if err := create(ctx, ri, u, true); err != nil {
			return fmt.Errorf("operation %s: %w", o.Metadata.Name, err)
		}
		if !add[i].approval {
			return nil
		}
		a := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "delivery.pacto.trianalab.io/v1alpha1", "kind": "Approval",
			"metadata": map[string]any{"name": o.Metadata.Name + "-ok", "namespace": o.Metadata.Namespace},
			"spec": map[string]any{"operation": o.Metadata.Name, "operationUID": "uid-" + o.Metadata.Name, "intentDigest": o.Spec.IntentDigest,
				"decision": "approve", "by": perfIssuer + "#bob", "groups": []any{"team-b"}}}}
		return create(ctx, adm.Resource(gvrPerfAppr).Namespace(o.Metadata.Namespace), a, false)
	})
	w.ops = total
}

func perfFleet(t testing.TB, services int) *fleet.Query {
	var revs []fleet.RawRevision
	var targets []fleet.RawTarget
	for i := range services {
		svc := perfSvc(i)
		revs = append(revs, fleet.RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
			Service: contract.Service{Name: svc, Version: "1.0.0", Owner: contract.Owner{Team: []string{"team-a", "team-b", "team-c"}[i%3]}}}}})
		targets = append(targets, fleet.RawTarget{Scope: perfNS(i), Kind: fleet.SourceKindKubernetes, Name: svc, Service: svc})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "k8s",
		&fleet.Collection{Revisions: revs, Targets: targets}))
	must(t, err)
	return fleet.NewQuery(snap)
}

// ── the dashboard ──────────────────────────────────────────────────────

type perfTokenKey struct{}

// perfDashboard serves the dashboard on loopback exactly as `pacto
// dashboard` does in shared mode, except that the bearer token stands in for
// the session cookie the OIDC layer would resolve.
func perfDashboard(t testing.TB, cp *controlPlane, cnt *counter, q *atomic.Pointer[fleet.Query]) string {
	newClient := func(c *rest.Config) (dynamic.Interface, error) { return dynamic.NewForConfig(cnt.wrap(c)) }
	id := &dashboardIdentity{cluster: cp.config("", 0), auth: authn.NewShared(&authn.OIDC{}, nil), policyNS: perfPolicyNS,
		registry: func(...string) app.DeliveryRegistry { return perfRegistry{} }, newClient: newClient, now: time.Now,
		served: func(*rest.Config, string) (bool, error) { return true, nil },
		state: func(ctx context.Context) authn.State {
			tok, _ := ctx.Value(perfTokenKey{}).(string)
			return authn.State{SignedIn: tok != "", Token: tok, Person: authn.PersonFromToken(tok)}
		},
		revisions: map[string]delivery.Revision{}, reads: newReadCache()}
	id.viewer = authn.NewViewer(clusterReview(cp.config(cp.admin, 0), newClient))
	o := dashboard.Options{UI: fstest.MapFS{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Fleet:     func(context.Context) (*fleet.Query, error) { return q.Load(), nil },
		Published: func() *fleet.Query { return q.Load() },
		Auth: &dashboard.Auth{Login: true, Routes: http.NotFoundHandler(),
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), perfTokenKey{}, tok)))
				})
			},
			State: func(*http.Request) dashboard.AuthState { return dashboard.AuthState{SignedIn: true} }},
		View: id.view, Delivery: id.delivery, DeliveryInstalled: id.deliveryInstalled}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = dashboard.NewServer(o).ServeOnListener(ctx, ln) }()
	return "http://" + ln.Addr().String()
}

// ── measuring ──────────────────────────────────────────────────────────

type perfView struct{ name, path string }

var perfViews = []perfView{
	{"inbox", "/api/delivery/inbox"},
	{"environments", "/api/delivery/environments"},
	{"environments?env&q", "/api/delivery/environments?environment=prod-eu&q=svc-01"},
	{"history (all)", "/api/delivery/history"},
	{"history (hot Stage)", "/api/delivery/history?namespace=ns-00&stage=" + hotStage},
	{"published", "/api/delivery/published"},
	{"autonomy", "/api/delivery/autonomy"},
	{"fleet overview", "/api/fleet/overview"},
	{"fleet entities", "/api/fleet/entities?kinds=service&limit=25"},
}

type perfResult struct {
	scale, view, who    string
	callers, requests   int
	failed              int // requests that failed or ran past perfHTTP's timeout
	p50, p95            time.Duration
	coldAPI, apiPerReq  float64
	allocsPerReq, kbReq uint64
}

var perfHTTP = &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{MaxIdleConnsPerHost: 200}}

func get(t testing.TB, base, path, token string) []byte {
	b, err := fetch(base, path, token)
	must(t, err)
	return b
}

func fetch(base, path, token string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := perfHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %d %s", path, resp.StatusCode, b)
	}
	return b, nil
}

// measure warms the view once (its apiserver requests are coldAPI), then
// serves it n times from callers goroutines at once.
func measure(t testing.TB, base string, cnt *counter, scale string, v perfView, ps []perfPerson, callers, n int) perfResult {
	before := cnt.n.Load()
	get(t, base, v.path, ps[0].token)
	cold := float64(cnt.n.Load() - before)
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	before = cnt.n.Load()
	lat := make([]time.Duration, n)
	var failed atomic.Int64
	next := atomic.Int64{}
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			for i := int(next.Add(1) - 1); i < n; i = int(next.Add(1) - 1) {
				start := time.Now()
				if _, err := fetch(base, v.path, ps[i%len(ps)].token); err != nil {
					failed.Add(1)
				}
				lat[i] = time.Since(start)
			}
		})
	}
	wg.Wait()
	runtime.ReadMemStats(&ms1)
	slices.Sort(lat)
	who := ps[0].name
	if len(ps) > 1 {
		who = fmt.Sprintf("%d auditors", min(callers, len(ps)))
	}
	return perfResult{scale: scale, view: v.name, who: who, callers: callers, requests: n, failed: int(failed.Load()),
		p50: lat[n/2], p95: lat[(n*95-1)/100], coldAPI: cold, apiPerReq: float64(cnt.n.Load()-before) / float64(n),
		allocsPerReq: (ms1.Mallocs - ms0.Mallocs) / uint64(n), kbReq: (ms1.TotalAlloc - ms0.TotalAlloc) / uint64(n) / 1024}
}

func requests(callers int) int { return max(5, callers) }

// checkRBAC fails unless each person's environments hold exactly the
// Stages of the namespaces they may read.
func checkRBAC(t testing.TB, base string, w *world) {
	for _, p := range people {
		nss := readable(p.name)
		want := 0
		for _, st := range w.stages {
			if slices.Contains(nss, st.Metadata.Namespace) {
				want++
			}
		}
		got := 0
		for offset := 0; ; offset += 200 {
			var out struct {
				Page struct {
					NextOffset *int `json:"nextOffset"`
				} `json:"page"`
				Environments []struct {
					Namespace string `json:"namespace"`
				} `json:"environments"`
			}
			must(t, json.Unmarshal(get(t, base, fmt.Sprintf("/api/delivery/environments?limit=200&offset=%d", offset), p.token), &out))
			for _, e := range out.Environments {
				if !slices.Contains(nss, e.Namespace) {
					t.Fatalf("%s sees a Stage in %s, which they may not read", p.name, e.Namespace)
				}
			}
			got += len(out.Environments)
			if out.Page.NextOffset == nil {
				break
			}
		}
		if got != want {
			t.Fatalf("%s sees %d Stages, want %d", p.name, got, want)
		}
	}
}

func TestPerfEnvtest(t *testing.T) {
	cp := startControlPlane(t, append(people[:2:2], auditors...))
	ctx := context.Background()
	adm, err := dynamic.NewForConfig(cp.config(cp.admin, -1))
	must(t, err)
	installCRDs(t, ctx, adm)
	nss := []string{perfPolicyNS}
	for i := range perfNamespaces {
		nss = append(nss, perfNS(i))
	}
	parallel(t, len(nss), func(i int) error {
		return create(ctx, adm.Resource(gvrNS), &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": nss[i]}}}, false)
	})
	installRBAC(t, ctx, adm)

	cnt := &counter{}
	var q atomic.Pointer[fleet.Query]
	base := perfDashboard(t, cp, cnt, &q)
	w := &world{}
	var results []perfResult
	scales := []struct {
		name          string
		services, ops int
	}{{"500 Stages / 2k ops", 125, 2000}, {"2,000 Stages / 10k ops", 500, 10000}}
	if os.Getenv("PERF_QUICK") != "" {
		scales = scales[:1]
	}
	for _, sc := range scales {
		start := time.Now()
		seedStages(t, ctx, adm, w, sc.services)
		seedOps(t, ctx, adm, w, sc.ops)
		q.Store(perfFleet(t, sc.services))
		t.Logf("seeded %s in %s", sc.name, time.Since(start).Round(time.Second))
		checkRBAC(t, base, w)
		if path := os.Getenv("PERF_CPUPROFILE"); path != "" {
			f, err := os.Create(path)
			must(t, err)
			must(t, pprof.StartCPUProfile(f))
			defer pprof.StopCPUProfile()
		}
		for _, v := range perfViews {
			if only := os.Getenv("PERF_VIEWS"); only != "" && !strings.Contains(only, v.name) {
				continue
			}
			for _, p := range people[:2] {
				results = append(results, measure(t, base, cnt, sc.name, v, []perfPerson{p}, 1, requests(1)))
			}
			for _, c := range []int{1, 10, 50} {
				r := measure(t, base, cnt, sc.name, v, auditors, c, requests(c))
				results = append(results, r)
				t.Logf("%-24s %-20s %-6s c=%-2d p50=%-8s p95=%-8s api/req=%.1f", r.scale, r.view, r.who, c, r.p50.Round(time.Millisecond), r.p95.Round(time.Millisecond), r.apiPerReq)
			}
		}
	}
	report := perfTable(results)
	t.Log("\n" + report)
	if path := os.Getenv("PERF_REPORT"); path != "" {
		must(t, os.WriteFile(path, []byte(report), 0o644))
	}
	perfBudgets(t, results)
}

func perfTable(rs []perfResult) string {
	var b strings.Builder
	b.WriteString("| scale | view | who | callers | p50 | p95 | failed | apiserver req (cold) | apiserver req/request (warm) | allocs/req | KiB/req |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s | %d/%d | %.0f | %.1f | %d | %d |\n", r.scale, r.view, r.who, r.callers,
			r.p50.Round(time.Millisecond), r.p95.Round(time.Millisecond), r.failed, r.requests, r.coldAPI, r.apiPerReq, r.allocsPerReq, r.kbReq)
	}
	return b.String()
}

// perfBudget is a regression tripwire, generous on purpose: about twice the
// numbers in tests/perf/README.md, which a loaded laptop measured. The
// apiserver count is checked on the auditors' rows, fifty distinct people
// whom no per-person cache helps, so it is the same on any machine; the p95
// at 10 callers, at both sizes.
type perfBudget struct {
	maxAPI float64       // apiserver requests per dashboard request
	maxP95 time.Duration // at 10 callers
}

var perfBudgetTable = map[string]perfBudget{
	"inbox":               {8, 15 * time.Second},
	"environments":        {8, 15 * time.Second},
	"environments?env&q":  {8, 15 * time.Second},
	"history (all)":       {60, 20 * time.Second},
	"history (hot Stage)": {4, 5 * time.Second},
	"published":           {3, 8 * time.Second},
	"autonomy":            {4, 8 * time.Second},
	"fleet overview":      {2, 2 * time.Second},
	"fleet entities":      {2, 2 * time.Second},
}

func perfBudgets(t testing.TB, rs []perfResult) {
	for _, r := range rs {
		if r.failed > 0 {
			t.Errorf("%s %s for %s: %d of %d requests failed", r.scale, r.view, r.who, r.failed, r.requests)
		}
		b := perfBudgetTable[r.view]
		if strings.HasSuffix(r.who, "auditors") && r.apiPerReq > b.maxAPI {
			t.Errorf("%s %s for %s: %.1f apiserver requests per request, budget %.0f", r.scale, r.view, r.who, r.apiPerReq, b.maxAPI)
		}
		if r.callers == 10 && r.p95 > b.maxP95 {
			t.Errorf("%s %s at 10 callers: p95 %s, budget %s", r.scale, r.view, r.p95, b.maxP95)
		}
	}
}
