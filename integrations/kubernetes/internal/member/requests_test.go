/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package member

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/google/go-containerregistry/pkg/registry"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	inUID   = "9f1c-member"
	inNS    = "billing"
	inSelf  = "system:serviceaccount:pacto:pacto-operator"
	inAlice = "oidc:alice@acme.com"
	dA      = "sha256:aaaa000000000000000000000000000000000000000000000000000000000000"
	dB      = "sha256:bbbb000000000000000000000000000000000000000000000000000000000000"
)

var inNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

// opened is what the open hook answers for one request bundle.
type opened struct {
	req   core.OperationRequest
	user  string
	at    time.Time
	class core.ActorClass
	why   string
}

// writer forwards only Create to the cluster and counts it; any other write
// fails the test.
type writer struct {
	client.Writer
	t       *testing.T
	c       client.Client
	now     *time.Time
	creates int
	fail    error
}

func (w *writer) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	w.creates++
	if w.fail != nil {
		return w.fail
	}
	obj.SetCreationTimestamp(metav1.NewTime(*w.now))
	obj.SetUID(types.UID("uid-" + obj.GetName()))
	return w.c.Create(ctx, obj, opts...)
}

type intakeEnv struct {
	t      *testing.T
	i      *Intake
	c      client.Client
	w      *writer
	repo   *remote.Repository
	now    time.Time
	n      int
	mu     sync.Mutex
	fault  func(*http.Request) bool
	reqs   map[string]opened
	joins  map[string]core.VerifiedJoin
	rights map[string]bool
	revErr error
	logs   []string
	listOp error
	listNS error
}

func inScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = dv1.AddToScheme(s)
	return s
}

func inStage(name, namespace, env string) *dv1.Stage {
	return &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:   core.StageSpec{Service: core.ServiceRef{Name: "billing"}, Environment: env, ApplyRef: core.ApplyRef{Name: "billing"}},
		Status: core.StageStatus{Pointer: dA, Verified: []string{dA}, Stream: core.Stream{ID: inUID + "/" + namespace + "/" + name}}}
}

func newIntake(t *testing.T, objs ...client.Object) *intakeEnv {
	t.Helper()
	e := &intakeEnv{t: t, now: inNow, reqs: map[string]opened{}, joins: map[string]core.VerifiedJoin{}, rights: map[string]bool{}}
	inner := registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		f := e.fault
		e.mu.Unlock()
		if f != nil && f(r) {
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusForbidden)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e.repo, _ = ocireferrer.Open(strings.TrimPrefix(srv.URL, "http://")+"/acme/pacto-targets", ocireferrer.Options{})
	if objs == nil {
		objs = []client.Object{inStage("billing-prod", inNS, "production")}
	}
	e.c = fake.NewClientBuilder().WithScheme(inScheme()).WithObjects(objs...).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*dv1.OperationList); ok && e.listOp != nil {
				return e.listOp
			}
			for _, o := range opts {
				if _, ok := o.(client.InNamespace); ok && e.listNS != nil {
					return e.listNS
				}
			}
			return c.List(ctx, list, opts...)
		}}).Build()
	e.w = &writer{t: t, c: e.c, now: &e.now}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(key)
	e.i = &Intake{Reader: e.c, Writer: e.w, Records: &deliveryrecord.Reader{}, Repo: e.repo, Signer: signing.Keyed(kp, ""), Key: key,
		ClusterID: inUID, ClusterName: "eu-prod", NameSource: fleet.ClusterNameFromNodeLabel, Self: core.Actor{User: inSelf, Groups: []string{"system:serviceaccounts"}},
		Policy: func(context.Context) (core.ClassPolicy, error) { return core.ClassPolicy{}, nil },
		Review: func(_ context.Context, user, _ string) (bool, error) {
			if e.revErr != nil {
				return false, e.revErr
			}
			allowed, ok := e.rights[user]
			return allowed || !ok, nil
		},
		Now: func() time.Time { return e.now }, Interval: time.Hour,
		Log: funcr.New(func(prefix, args string) { e.logs = append(e.logs, args) }, funcr.Options{})}
	e.i.open = func(b []byte, _ core.ClassPolicy) (core.OperationRequest, string, time.Time, core.ActorClass, string) {
		o, ok := e.reqs[string(b)]
		if !ok {
			return core.OperationRequest{}, "", time.Time{}, "", "a member request was not counted: no signer"
		}
		return o.req, o.user, o.at, o.class, o.why
	}
	e.i.openJoin = func(_ core.ClassPolicy, b []byte) core.VerifiedJoin { return e.joins[string(b)] }
	return e
}

func (e *intakeEnv) setFault(f func(*http.Request) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fault = f
}

// push writes bundle on stream as a predicate record and returns its
// manifest digest.
func (e *intakeEnv) push(stream, predicate string, bundle []byte) string {
	e.t.Helper()
	caps, err := deliveryrecord.EnsureAnchor(e.t.Context(), e.repo, stream)
	if err != nil {
		e.t.Fatal(err)
	}
	ref, err := deliveryrecord.Push(e.t.Context(), e.repo, stream, caps.Referrers, predicate, bundle, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	_, d, _ := strings.Cut(ref, "@")
	return d
}

// request pushes a request alice signed now for dB on billing in
// production, edited, and returns its manifest digest.
func (e *intakeEnv) request(edit ...func(*opened)) string {
	e.t.Helper()
	o := opened{req: core.OperationRequest{Cluster: inUID, Hub: "hub-uid", Service: core.ServiceRef{Name: "billing"}, Environment: "production",
		Digest: dB, Requester: inAlice, Reason: "ship it", Placement: core.RequestPlacement{Source: core.RequestPlacedAdvertised, ConfirmedBy: inAlice},
		At: e.now.Format(time.RFC3339), Expires: e.now.Add(24 * time.Hour).Format(time.RFC3339)}, user: inAlice, at: e.now, class: core.ClassHuman}
	for _, f := range edit {
		f(&o)
	}
	e.n++
	b := fmt.Appendf(nil, `{"request":%d}`, e.n)
	e.reqs[string(b)] = o
	return e.push(core.ClusterRequestStream(inUID), core.PredicateOperationRequest, b)
}

// outcomes are the outcomes written for manifest, read unverified.
func (e *intakeEnv) outcomes(manifest string) []core.RequestOutcome {
	e.t.Helper()
	recs, _, err := (&deliveryrecord.Reader{}).ListRequestOutcomes(e.t.Context(), e.repo, inUID, manifest)
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]core.RequestOutcome, 0, len(recs))
	for _, f := range recs {
		st, err := core.OpenRequestOutcome(f.Bundle, nil, "", nil)
		if err != nil {
			e.t.Fatal(err)
		}
		out = append(out, st.Predicate)
	}
	return out
}

func (e *intakeEnv) poll() { e.i.poll(e.t.Context()) }

func (e *intakeEnv) ops() []dv1.Operation {
	e.t.Helper()
	var l dv1.OperationList
	if err := e.c.List(e.t.Context(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

// refusedWith polls and wants manifest refused with code and nothing created.
func (e *intakeEnv) refusedWith(manifest, code string) core.RequestOutcome {
	e.t.Helper()
	e.poll()
	got := e.outcomes(manifest)
	if len(got) != 1 || got[0].State != core.RequestRefused || got[0].Code != code || got[0].Next == "" || e.w.creates != 0 {
		e.t.Fatalf("want one %s refusal and no Operation, got %+v (creates %d)", code, got, e.w.creates)
	}
	return got[0]
}

func TestIntakeRaisesAgentOperation(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	e.poll()
	ops := e.ops()
	if len(ops) != 1 {
		t.Fatalf("%d operations", len(ops))
	}
	wantRaised(t, ops[0], m, e.i.Self)
	if len(e.outcomes(m)) != 0 {
		t.Fatal("an outcome before the intent record")
	}
	long := strings.Repeat("s", 60)
	e = newIntake(t, inStage(long, inNS, "production"))
	m1, m2 := e.request(), e.request(func(o *opened) {
		o.req.Digest = "sha256:cccc000000000000000000000000000000000000000000000000000000000000"
	})
	e.poll()
	names := map[string]bool{}
	for _, o := range e.ops() {
		names[o.Name] = true
		if len(o.Name) > 63 || !strings.HasSuffix(o.Name, "-req-"+strings.TrimPrefix(o.Annotations[RequestAnnotation], "sha256:")[:10]) {
			t.Fatalf("name %q", o.Name)
		}
	}
	if len(names) != 2 || m1 == m2 {
		t.Fatalf("two requests on one long Stage: %v", names)
	}
	if !strings.Contains(strings.Join(e.logs, "\n"), "raised a promote") {
		t.Fatal(e.logs)
	}
}

// wantRaised wants o to be the agent promote the request m raises on
// billing-prod: actor self, on alice's behalf, from the pointer to dB.
func wantRaised(t *testing.T, o dv1.Operation, m string, self core.Actor) {
	t.Helper()
	hex := strings.TrimPrefix(m, "sha256:")
	want := core.OperationSpec{Type: core.TypePromote, Stage: "billing-prod", Facet: core.FacetPointer, Params: core.Params{From: dA, To: dB},
		Actor: self, OnBehalfOf: &core.Principal{User: inAlice},
		Reason: "requested by " + inAlice + " from hub hub-uid (claimed) (request " + m[:19] + "): ship it"}
	want.IntentDigest = core.IntentDigest(want, inStage("billing-prod", inNS, "production").Spec)
	if o.Name != "billing-prod-req-"+hex[:10] || o.Labels[RequestLabel] != hex[:40] || o.Annotations[RequestAnnotation] != m ||
		len(o.Finalizers) != 1 || o.Spec.OnBehalfOf == nil || *o.Spec.OnBehalfOf != *want.OnBehalfOf || o.Spec.Reason != want.Reason ||
		o.Spec.IntentDigest != want.IntentDigest || o.Spec.Actor.User != inSelf || o.Spec.Params != want.Params || o.Spec.Type != want.Type {
		t.Fatalf("%+v", o)
	}
}

func TestIntakeHubClaimed(t *testing.T) {
	e := newIntake(t)
	keyID := signing.Hint(e.i.Key.Public())
	join := func(n int, hub string) {
		b := fmt.Appendf(nil, `{"join":%d}`, n)
		e.joins[string(b)] = core.VerifiedJoin{Record: core.ClusterJoin{Cluster: inUID, Hub: hub, KeyID: keyID, Decision: core.JoinApprove}, SignedAt: inNow, Verified: true}
		e.push(core.ClusterJoinStream(inUID), core.PredicateClusterJoin, b)
	}
	join(1, "hub-uid")
	e.request()
	e.request(func(o *opened) { o.req.Hub = "other-hub"; o.req.Digest = dA[:len(dA)-1] + "1" })
	e.poll()
	reasons := map[string]bool{}
	for _, o := range e.ops() {
		reasons[strings.Split(o.Spec.Reason, " (request")[0]] = true
	}
	if !reasons["requested by "+inAlice+" from hub hub-uid"] || !reasons["requested by "+inAlice+" from hub other-hub (claimed)"] {
		t.Fatalf("%v", reasons)
	}
	// Two hubs approve: neither is this cluster's hub, said once a minute.
	join(2, "other-hub")
	e.now = e.now.Add(2 * time.Minute)
	if hub := e.i.ownHub(t.Context(), core.ClassPolicy{}); hub != "" || !strings.Contains(strings.Join(e.logs, "\n"), "more than one hub") {
		t.Fatalf("%q %v", hub, e.logs)
	}
	e.now = e.now.Add(2 * time.Minute)
	e.setFault(func(r *http.Request) bool { return true })
	if hub := e.i.ownHub(t.Context(), core.ClassPolicy{}); hub != "" {
		t.Fatal(hub)
	}
}

func TestIntakeRefusesOutsideAllowlist(t *testing.T) {
	e := newIntake(t)
	e.i.Observed = []string{"other"}
	if o := e.refusedWith(e.request(), core.RefusalOutsideAllowlist); !strings.Contains(o.Reason, "namespace billing") {
		t.Fatal(o.Reason)
	}
	e = newIntake(t, inStage("billing-staging", inNS, "staging"))
	if o := e.refusedWith(e.request(func(o *opened) { o.req.Environment = "staging" }), core.RefusalOutsideAllowlist); !strings.Contains(o.Reason, "environment staging") {
		t.Fatal(o.Reason)
	}
	e = newIntake(t)
	e.i.Allow.Services = []string{"pay*"}
	if o := e.refusedWith(e.request(), core.RefusalOutsideAllowlist); !strings.Contains(o.Reason, "pay*") || !strings.Contains(o.Next, "delivery.remote.allow") {
		t.Fatalf("%+v", o)
	}
}

func TestIntakeRefusesWithoutGrant(t *testing.T) {
	e := newIntake(t)
	e.revErr = errors.New("apiserver down")
	m := e.request()
	e.poll()
	if len(e.outcomes(m)) != 0 || e.w.creates != 0 {
		t.Fatal("a failed review recorded something")
	}
	e.revErr, e.rights[inAlice] = nil, false
	if o := e.refusedWith(m, core.RefusalNotAllowedHere); !strings.Contains(o.Reason, "namespace billing") || !strings.Contains(o.Next, "kubectl create rolebinding") {
		t.Fatalf("%+v", o)
	}
}

func TestIntakeRefusesExpired(t *testing.T) {
	e := newIntake(t)
	m := e.request(func(o *opened) {
		o.at, o.req.At, o.req.Expires = inNow.Add(-2*time.Hour), inNow.Add(-2*time.Hour).Format(time.RFC3339), inNow.Add(-time.Hour).Format(time.RFC3339)
	})
	e.refusedWith(m, core.RefusalRequestExpired)
}

func TestIntakeRefusesFutureDatedRequest(t *testing.T) {
	e := newIntake(t)
	m := e.request(func(o *opened) {
		o.req.At, o.req.Expires = "2027-06-01T00:00:00Z", "2027-06-08T00:00:00Z"
	})
	e.refusedWith(m, core.RefusalRequestExpired)
}

func TestIntakeLifetimeFromSignedAt(t *testing.T) {
	e := newIntake(t)
	e.i.RecordedTTL = 24 * time.Hour
	signed := inNow.Add(-25 * time.Hour)
	m := e.request(func(o *opened) {
		o.at, o.req.At, o.req.Expires = signed, signed.Format(time.RFC3339), inNow.Add(6*24*time.Hour).Format(time.RFC3339)
	})
	e.refusedWith(m, core.RefusalRequestExpired)
}

func TestIntakeRefusesStaleRequestAfterNewerShipped(t *testing.T) {
	verified := inStage("billing-prod", inNS, "production")
	verified.Status.Verified = []string{dB, dA}
	e := newIntake(t, verified)
	if o := e.refusedWith(e.request(), core.RefusalPointerMoved); !strings.Contains(o.Reason, "before") {
		t.Fatal(o.Reason)
	}
	e = newIntake(t)
	if o := e.refusedWith(e.request(func(o *opened) { o.req.Digest = dA }), core.RefusalPointerMoved); !strings.Contains(o.Reason, "already runs") {
		t.Fatal(o.Reason)
	}
	moved := inStage("billing-prod", inNS, "production")
	moved.Status.LastOp = "uid-last"
	last := &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: inNS, Name: "last", UID: "uid-last", CreationTimestamp: metav1.NewTime(inNow.Add(time.Minute))}}
	older := &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: inNS, Name: "older", UID: "uid-older", CreationTimestamp: metav1.NewTime(inNow.Add(-time.Hour))}}
	e = newIntake(t, moved, last, older)
	if o := e.refusedWith(e.request(), core.RefusalPointerMoved); !strings.Contains(o.Reason, "operation last") || !strings.Contains(o.Next, "plan again") {
		t.Fatal(o.Reason)
	}
	e = newIntake(t, moved, older)
	e.request()
	e.poll()
	if e.w.creates != 1 {
		t.Fatal("a lastOp no longer there or older than the request stops nothing")
	}
	e = newIntake(t, moved)
	e.request()
	e.listNS = errors.New("down")
	e.poll()
	if e.w.creates != 0 {
		t.Fatal("an unreadable lastOp raised")
	}
}

func TestIntakeRefusesOtherCluster(t *testing.T) {
	e := newIntake(t)
	if o := e.refusedWith(e.request(func(o *opened) { o.req.Cluster = "other-uid" }), core.RefusalNotForThisCluster); !strings.Contains(o.Reason, "other-uid") {
		t.Fatal(o.Reason)
	}
}

func TestIntakeRefusesNonPerson(t *testing.T) {
	e := newIntake(t)
	m := e.request(func(o *opened) { o.class, o.why = core.ClassCI, "agents cannot approve" })
	if o := e.refusedWith(m, core.RefusalNotAPerson); !strings.Contains(o.Reason, "ci") {
		t.Fatal(o.Reason)
	}
}

func TestIntakeSkipsUnverified(t *testing.T) {
	e := newIntake(t)
	before := promtest.ToFloat64(unverifiedRequests)
	m := e.push(core.ClusterRequestStream(inUID), core.PredicateOperationRequest, []byte(`{"forged":1}`))
	person := e.request(func(o *opened) { o.why = "a member request signed by eve@gmail.com was not counted" })
	e.poll()
	e.poll()
	if len(e.outcomes(m)) != 0 || len(e.outcomes(person)) != 0 || e.w.creates != 0 {
		t.Fatal("an unverified request was answered")
	}
	if got := promtest.ToFloat64(unverifiedRequests) - before; got != 2 {
		t.Fatalf("metric +%v, want 2 (rejected from the bound after the first poll)", got)
	}
	if n := strings.Count(strings.Join(e.logs, "\n"), "did not verify"); n != 2 {
		t.Fatalf("logged %d times", n)
	}
	e.i.done = nil
	e.i.Records = &deliveryrecord.Reader{} // a restart reads it again and logs nothing new
	e.poll()
	if n := strings.Count(strings.Join(e.logs, "\n"), "did not verify"); n != 2 {
		t.Fatalf("logged %d times", n)
	}
}

func TestIntakeNoStageAndAmbiguous(t *testing.T) {
	e := newIntake(t, inStage("billing-staging", inNS, "staging"))
	if o := e.refusedWith(e.request(), core.RefusalNoStage); !strings.Contains(o.Next, "create the Stage") {
		t.Fatal(o.Next)
	}
	e = newIntake(t, inStage("billing-prod", inNS, "production"), inStage("billing-prod", "billing-eu", "production"))
	if o := e.refusedWith(e.request(), core.RefusalAmbiguousStage); !strings.Contains(o.Reason, "billing/billing-prod") || !strings.Contains(o.Reason, "billing-eu/billing-prod") {
		t.Fatal(o.Reason)
	}
	e = newIntake(t, inStage("billing-prod", inNS, "production"), inStage("billing-prod", "billing-eu", "production"))
	e.i.Allow.Namespaces = []string{"billing-eu"}
	e.request()
	e.poll()
	if ops := e.ops(); len(ops) != 1 || ops[0].Namespace != "billing-eu" {
		t.Fatal("the allowlist picks the one Stage")
	}
}

func TestIntakeOncePerRequest(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	e.poll()
	e.poll()
	if e.w.creates != 1 {
		t.Fatalf("%d creates", e.w.creates)
	}
	e.rights[inAlice] = false
	refused := e.request(func(o *opened) { o.req.Digest = dA[:len(dA)-1] + "2" })
	e.poll()
	e.poll()
	if len(e.outcomes(refused)) != 1 || len(e.outcomes(m)) != 0 {
		t.Fatal("one refusal per request")
	}
}

func TestIntakeOncePerRequestAcrossRestart(t *testing.T) {
	e := newIntake(t)
	e.rights[inAlice] = false
	m := e.request()
	e.poll()
	e.i.done, e.i.Records = nil, &deliveryrecord.Reader{}
	e.rights[inAlice] = true
	e.poll()
	if got := e.outcomes(m); len(got) != 1 || e.w.creates != 0 {
		t.Fatalf("restart re-answered: %+v %d", got, e.w.creates)
	}
}

// raisedOp is an Operation the intake raised for manifest, created at
// created, with status.
func raisedOp(manifest string, created time.Time, st core.OperationStatus) *dv1.Operation {
	hex := strings.TrimPrefix(manifest, "sha256:")
	return &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: inNS, Name: "billing-prod-req-" + hex[:10], UID: "op-uid",
		CreationTimestamp: metav1.NewTime(created), Labels: map[string]string{RequestLabel: hex[:40]}, Annotations: map[string]string{RequestAnnotation: manifest}},
		Spec:   core.OperationSpec{Type: core.TypePromote, Stage: "billing-prod", Params: core.Params{From: dA, To: dB}, Actor: core.Actor{User: inSelf}},
		Status: st}
}

func TestIntakeNoReraiseAfterOperationExpired(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	e.i.watchConcluded(t.Context()) // nothing raised yet
	if err := e.c.Create(t.Context(), raisedOp(m, inNow, core.OperationStatus{IntentRecord: &core.ApprovalRecordRef{Ref: "r@sha256:1"}})); err != nil {
		t.Fatal(err)
	}
	e.poll() // the raised outcome is written
	_ = e.c.Delete(t.Context(), raisedOp(m, inNow, core.OperationStatus{}))
	e.i.done, e.i.Records = nil, &deliveryrecord.Reader{}
	e.poll()
	if e.w.creates != 0 || len(e.ops()) != 0 {
		t.Fatal("re-raised after the Operation was deleted")
	}
}

func TestIntakeNoRaiseWhenOutcomesCut(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	var calls atomic.Int32 // the request listing reads, the outcome listing fails
	e.setFault(func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/referrers/") && calls.Add(1) > 1 })
	e.poll()
	if e.w.creates != 0 {
		t.Fatal("raised with an unreadable outcome listing")
	}
	e.setFault(nil)
	for k := range core.MaxRequestOutcomes + 1 {
		o := core.RequestOutcome{Request: m, Cluster: inUID, State: core.RequestConcluded, At: inNow.Format(time.RFC3339),
			Operation: &core.RequestedOperation{Namespace: inNS, Name: fmt.Sprint(k)}, Outcome: core.Succeeded}
		b, _ := core.SignRequestOutcome(t.Context(), e.i.Signer, core.NewRequestOutcomeStatement(o))
		if _, _, err := deliveryrecord.PushRequestOutcome(t.Context(), e.repo, b); err != nil {
			t.Fatal(err)
		}
	}
	e.poll()
	if e.w.creates != 0 {
		t.Fatal("raised with a cut outcome listing")
	}
	e.poll() // the concluded ones verified, so they still count against the bound
	if e.w.creates != 0 {
		t.Fatal("raised with a cut outcome listing")
	}
}

func TestIntakeOutcomeFromOtherKeyIgnored(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(other)
	o := core.RequestOutcome{Request: m, Cluster: inUID, State: core.RequestRefused, Code: core.RefusalNoStage, Reason: "forged", Next: "x", At: inNow.Format(time.RFC3339)}
	b, _ := core.SignRequestOutcome(t.Context(), signing.Keyed(kp, ""), core.NewRequestOutcomeStatement(o))
	if _, _, err := deliveryrecord.PushRequestOutcome(t.Context(), e.repo, b); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if e.w.creates != 1 {
		t.Fatal("a refusal another key signed stopped the raise")
	}
}

func TestIntakeRaisedCarriesIntentRecord(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	if err := e.c.Create(t.Context(), raisedOp(m, inNow, core.OperationStatus{IntentRecord: &core.ApprovalRecordRef{Ref: "reg/acme@sha256:intent"}})); err != nil {
		t.Fatal(err)
	}
	e.poll()
	got := e.outcomes(m)
	if len(got) != 1 || got[0].State != core.RequestRaised || got[0].Operation.IntentRecord != "reg/acme@sha256:intent" ||
		got[0].Operation.Name != raisedOp(m, inNow, core.OperationStatus{}).Name || got[0].Digest != dB || e.w.creates != 0 {
		t.Fatalf("%+v %d", got, e.w.creates)
	}
}

func TestIntakeRaisedWithoutRecorderAfterTimeout(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	if err := e.c.Create(t.Context(), raisedOp(m, inNow, core.OperationStatus{})); err != nil {
		t.Fatal(err)
	}
	e.poll()
	if len(e.outcomes(m)) != 0 {
		t.Fatal("raised before the wait")
	}
	e.now = e.now.Add(raisedWait)
	e.poll()
	if got := e.outcomes(m); len(got) != 1 || got[0].State != core.RequestRaised || got[0].Operation.IntentRecord != "" || got[0].Reason != "this member keeps no intent records" {
		t.Fatalf("%+v", got)
	}
}

func TestIntakeConcludedOutcome(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	st := core.OperationStatus{Phase: core.PhaseConcluded, Outcome: core.Rejected, Reason: "GateFailed", Message: "pacto.published: no publisher signature for billing",
		Verdicts: []core.Verdict{{Gate: "other", Result: core.Pass}, {Gate: core.GatePublished, Result: core.Fail, Message: "no publisher signature for billing"}}}
	if err := e.c.Create(t.Context(), raisedOp(m, inNow, st)); err != nil {
		t.Fatal(err)
	}
	e.poll()
	got := e.outcomes(m)
	states := map[core.RequestState]core.RequestOutcome{}
	for _, o := range got {
		states[o.State] = o
	}
	c := states[core.RequestConcluded]
	if len(got) != 2 || states[core.RequestRaised].State == "" || c.Outcome != core.Rejected || c.Published != "Fail: no publisher signature for billing" ||
		c.Reason != "GateFailed: pacto.published: no publisher signature for billing" {
		t.Fatalf("%+v", got)
	}
	e.poll()
	if len(e.outcomes(m)) != 2 {
		t.Fatal("concluded twice")
	}
	if published(nil) != "" {
		t.Fatal("no verdict")
	}
}

func TestIntakeIgnoresLabelFromOtherActor(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	op := raisedOp(m, inNow, core.OperationStatus{Outcome: core.Succeeded})
	op.Spec.Actor.User = "mallory"
	noAnn := raisedOp(m, inNow, core.OperationStatus{Outcome: core.Succeeded})
	noAnn.Name, noAnn.Annotations, noAnn.Labels = "unannotated", nil, map[string]string{RequestLabel: "other"}
	if err := e.c.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Create(t.Context(), noAnn); err != nil {
		t.Fatal(err)
	}
	e.i.watchConcluded(t.Context())
	if len(e.outcomes(m)) != 0 {
		t.Fatal("an outcome for another actor's label")
	}
	e.poll()
	if e.w.creates != 1 {
		t.Fatal("another actor's label kept the intake from raising")
	}
}

func TestIntakeUnreadableStreamWritesNothing(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	e.setFault(func(*http.Request) bool { return true })
	e.poll()
	e.setFault(nil)
	if e.w.creates != 0 || len(e.outcomes(m)) != 0 {
		t.Fatal("wrote with an unreadable stream")
	}
	e.i.Policy = func(context.Context) (core.ClassPolicy, error) { return core.ClassPolicy{}, errors.New("no policy") }
	e.poll()
	if e.w.creates != 0 {
		t.Fatal("raised without the policy")
	}
}

func TestIntakeReadsThroughAPIReader(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	e.poll()
	if e.w.creates != 1 {
		t.Fatal("the writer did not create")
	}
	// A failed create is logged and retried; a failed write of an outcome is
	// retried; a failed Stage list or Operation list waits.
	e = newIntake(t)
	e.w.fail = errors.New("denied")
	e.request()
	e.poll()
	if !strings.Contains(strings.Join(e.logs, "\n"), "could not be created") {
		t.Fatal(e.logs)
	}
	e = newIntake(t)
	e.rights[inAlice] = false
	refused := e.request()
	e.i.Signer = failingSigner{}
	e.poll()
	if len(e.outcomes(refused)) != 0 || !strings.Contains(strings.Join(e.logs, "\n"), "could not be written") {
		t.Fatal("an unsigned outcome")
	}
	e = newIntake(t)
	e.request()
	e.listOp = errors.New("down")
	e.poll()
	if e.w.creates != 0 || !strings.Contains(strings.Join(e.logs, "\n"), "could not be listed") {
		t.Fatal(e.logs)
	}
	_ = m
}

type failingSigner struct{}

func (failingSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("no key")
}

func TestIntakeStageListFails(t *testing.T) {
	e := newIntake(t)
	e.c = fake.NewClientBuilder().WithScheme(inScheme()).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*dv1.StageList); ok {
				return errors.New("down")
			}
			return c.List(ctx, list, opts...)
		}}).Build()
	e.i.Reader = e.c
	e.request()
	e.poll()
	if e.w.creates != 0 || !strings.Contains(strings.Join(e.logs, "\n"), "waits for the next poll") {
		t.Fatal(e.logs)
	}
}

func TestIntakeStart(t *testing.T) {
	e := newIntake(t)
	e.i.open, e.i.openJoin = nil, nil
	e.i.WhoAmI = func(context.Context) (core.Actor, error) { return core.Actor{}, errors.New("forbidden") }
	if err := e.i.Start(t.Context()); err != nil || !strings.Contains(strings.Join(e.logs, "\n"), "member requests are off") {
		t.Fatal(err, e.logs)
	}
	e.i.WhoAmI = func(context.Context) (core.Actor, error) { return core.Actor{User: "me"}, nil }
	e.i.Interval = time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := e.i.Start(ctx); err != nil || e.i.Self.User != "me" || e.i.open == nil {
		t.Fatal(err)
	}
}

func TestRemember(t *testing.T) {
	m := map[string]bool{}
	for k := range maxRemembered {
		m = remember(m, fmt.Sprint(k))
	}
	if m = remember(m, "x"); len(m) != 1 || !m["x"] {
		t.Fatal(len(m))
	}
}

func TestIntakeDoneSkipsWithoutReading(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	e.i.done = remember(nil, m)
	e.poll()
	if e.w.creates != 0 || len(e.outcomes(m)) != 0 {
		t.Fatal("a request already done was answered")
	}
}

func TestIntakeConcludeRetries(t *testing.T) {
	e := newIntake(t)
	m := e.request()
	op := raisedOp(m, inNow, core.OperationStatus{Outcome: core.Succeeded})
	if err := e.c.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	e.setFault(func(*http.Request) bool { return true })
	e.i.watchConcluded(t.Context())
	e.setFault(nil)
	good := e.i.Signer
	e.i.Signer = failingSigner{}
	e.i.watchConcluded(t.Context())
	if len(e.outcomes(m)) != 0 {
		t.Fatal("an outcome with no listing or no signer")
	}
	e.i.Signer = good
	raised := core.RequestOutcome{Request: m, Cluster: inUID, State: core.RequestRaised, At: inNow.Format(time.RFC3339),
		Operation: &core.RequestedOperation{Namespace: inNS, Name: op.Name}}
	b, _ := core.SignRequestOutcome(t.Context(), good, core.NewRequestOutcomeStatement(raised))
	if _, _, err := deliveryrecord.PushRequestOutcome(t.Context(), e.repo, b); err != nil {
		t.Fatal(err)
	}
	e.i.Signer = failingSigner{}
	e.i.watchConcluded(t.Context())
	e.i.Signer = good
	if got := e.outcomes(m); len(got) != 1 || e.i.done[m+"/concluded"] {
		t.Fatalf("concluded with a failing signer: %+v", got)
	}
	e.i.watchConcluded(t.Context())
	if got := e.outcomes(m); len(got) != 2 || !e.i.done[m+"/concluded"] {
		t.Fatalf("%+v", got)
	}
}

// TestOpenRequestVerifiesPerson: the default openers verify a request and a
// join record a person signed keyless under the approver signers.
func TestOpenRequestVerifiesPerson(t *testing.T) {
	ss := sigtest.New(t)
	const issuer = "https://login.acme.example"
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return sigtest.Token("alice@acme.com", issuer), nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := core.ClassPolicy{Signers: []core.PersonSigner{{Issuer: issuer, TrustedRoot: string(ss.RootJSON), UsernamePrefix: "oidc:", Subjects: `.+@acme\.com`}}}
	req := core.OperationRequest{Cluster: inUID, Hub: "hub-uid", Service: core.ServiceRef{Name: "billing"}, Environment: "production", Digest: dB,
		Requester: inAlice, Placement: core.RequestPlacement{Source: core.RequestPlacedAdvertised, ConfirmedBy: inAlice},
		At: time.Now().UTC().Format(time.RFC3339), Expires: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}
	st := core.OperationRequestStatement{Type: core.StatementType, PredicateType: core.PredicateOperationRequest, Predicate: req,
		Subject: []core.Subject{{Name: core.ClusterRequestStream(inUID), Digest: map[string]string{"sha256": strings.TrimPrefix(dB, "sha256:")}}}}
	payload, _ := json.Marshal(st)
	b, err := s.Sign(t.Context(), core.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	got, user, at, class, why := openRequest(b, p)
	if got.Digest != dB || user != inAlice || at.IsZero() || class != core.ClassHuman || why != "" {
		t.Fatalf("%+v %q %v %q %q", got, user, at, class, why)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	j := core.ClusterJoin{Cluster: inUID, Hub: "hub-uid", KeyID: signing.Hint(pub), PublicKey: base64.StdEncoding.EncodeToString(der),
		Decision: core.JoinApprove, Approver: inAlice, At: "2026-11-23T10:00:00Z"}
	jb, err := core.SignClusterJoin(t.Context(), s, core.NewClusterJoinStatement(j))
	if err != nil {
		t.Fatal(err)
	}
	if v := signing.VerifyJoinRecord(p, jb); !v.Verified || v.Record.Hub != "hub-uid" || v.SignedAt.IsZero() {
		t.Fatalf("%+v", v)
	}
	if v := signing.VerifyJoinRecord(core.ClassPolicy{}, jb); v.Verified {
		t.Fatal("verified with no signer")
	}
}

// reviewCluster answers SelfSubjectReviews as the operator and
// SubjectAccessReviews with allowed and evalErr, recording the last one;
// with fail it refuses every create.
type reviewCluster struct {
	sar     authorizationv1.SubjectAccessReviewSpec
	allowed bool
	evalErr string
	fail    bool
}

func (rc *reviewCluster) client() client.Client {
	return fake.NewClientBuilder().WithScheme(inScheme()).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			if rc.fail {
				return errors.New("down")
			}
			switch o := obj.(type) {
			case *authenticationv1.SelfSubjectReview:
				o.Status.UserInfo = authenticationv1.UserInfo{Username: inSelf, Groups: []string{"system:serviceaccounts"}}
			case *authorizationv1.SubjectAccessReview:
				rc.sar = o.Spec
				o.Status.Allowed, o.Status.EvaluationError = rc.allowed, rc.evalErr
			}
			return nil
		}}).Build()
}

func TestSelfActor(t *testing.T) {
	rc := &reviewCluster{}
	if a, err := SelfActor(rc.client())(t.Context()); err != nil || a.User != inSelf || len(a.Groups) != 1 {
		t.Fatal(a, err)
	}
	rc.fail = true
	if _, err := SelfActor(rc.client())(t.Context()); err == nil {
		t.Fatal("no error")
	}
}

func TestMayPropose(t *testing.T) {
	rc := &reviewCluster{allowed: true}
	may := MayPropose(rc.client())
	want := authorizationv1.ResourceAttributes{Namespace: inNS, Verb: "create", Group: "delivery.pacto.trianalab.io", Resource: "operations"}
	if ok, err := may(t.Context(), inAlice, inNS); !ok || err != nil || rc.sar.User != inAlice || len(rc.sar.Groups) != 0 || *rc.sar.ResourceAttributes != want {
		t.Fatalf("%v %v %+v", ok, err, rc.sar)
	}
	rc.allowed, rc.evalErr = false, "webhook down"
	if _, err := may(t.Context(), inAlice, inNS); err == nil {
		t.Fatal("an evaluation error is an answer")
	}
	rc.evalErr = ""
	if ok, err := may(t.Context(), inAlice, inNS); ok || err != nil {
		t.Fatal(ok, err)
	}
	rc.fail = true
	if _, err := may(t.Context(), inAlice, inNS); err == nil {
		t.Fatal("no error")
	}
}

func TestClassPolicyFrom(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "pacto-delivery", Name: core.PolicyConfigMap},
		Data: map[string]string{core.PolicyKey: `{"requireOwnerTeam":true,"signers":[{"issuer":"https://i","subjects":".+"}]}`}}
	c := fake.NewClientBuilder().WithScheme(inScheme()).WithObjects(cm).Build()
	p, err := ClassPolicyFrom(c, types.NamespacedName{Namespace: "pacto-delivery", Name: core.PolicyConfigMap})(t.Context())
	if err != nil || len(p.Signers) != 1 {
		t.Fatal(p, err)
	}
	if _, err := ClassPolicyFrom(c, types.NamespacedName{Namespace: "x", Name: "y"})(t.Context()); err == nil {
		t.Fatal("a missing ConfigMap read")
	}
}

// TestJoinRequestsOnlyWithIntake: the join block says the member takes
// requests only once the intake read approver signers and while its
// allowlist takes an environment; it then names their issuers, sorted and
// once each, and the environments the allowlist takes.
func TestJoinRequestsOnlyWithIntake(t *testing.T) {
	var off *Intake
	if off.JoinRequests() != nil {
		t.Fatal("an intake that is off says nothing about requests")
	}
	e := newIntake(t)
	if e.i.JoinRequests() != nil {
		t.Fatal("before a policy read the member claims no requests")
	}
	e.poll()
	if e.i.JoinRequests() != nil {
		t.Fatal("with no approver signer the member takes no requests")
	}
	e.i.Policy = func(context.Context) (core.ClassPolicy, error) {
		return core.ClassPolicy{Signers: []core.PersonSigner{{Issuer: "https://b.example"}, {Issuer: "https://a.example"}, {Issuer: "https://b.example"}}}, nil
	}
	e.poll()
	got := e.i.JoinRequests()
	if got == nil || !slices.Equal(got.Issuers, []string{"https://a.example", "https://b.example"}) || !slices.Equal(got.Environments, []string{"production"}) {
		t.Fatalf("JoinRequests = %+v; want both issuers once and production", got)
	}
	e.i.Allow.Environments = []string{"staging"}
	if got := e.i.JoinRequests(); got != nil {
		t.Fatalf("an allowlist that takes no hosted environment takes no requests, got %+v", got)
	}
}
