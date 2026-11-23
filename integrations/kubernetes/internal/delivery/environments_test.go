/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrlrt "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const deliveryNS = "pacto-delivery"

// envFixture is an EnvironmentReconciler over a fake client holding objs,
// with every write recorded by kind.
type envFixture struct {
	r      *EnvironmentReconciler
	c      client.WithWatch
	writes []string
	// review answers a SubjectAccessReview (allowed by default); reviews are
	// the ones asked.
	review  func(authorizationv1.SubjectAccessReviewSpec) (authorizationv1.SubjectAccessReviewStatus, error)
	reviews []authorizationv1.SubjectAccessReviewSpec
}

func newEnvFixture(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) *envFixture {
	t.Helper()
	f := &envFixture{}
	base := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()
	f.c = base
	f.review = func(authorizationv1.SubjectAccessReviewSpec) (authorizationv1.SubjectAccessReviewStatus, error) {
		return authorizationv1.SubjectAccessReviewStatus{Allowed: true}, nil
	}
	funcs.Create = func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
		if sar, ok := o.(*authorizationv1.SubjectAccessReview); ok {
			f.reviews = append(f.reviews, sar.Spec)
			var err error
			sar.Status, err = f.review(sar.Spec)
			return err
		}
		f.writes = append(f.writes, "create/"+o.GetObjectKind().GroupVersionKind().Kind+o.GetName())
		if o.GetUID() == "" { // the fake client sets none
			o.SetUID(types.UID("uid-" + o.GetNamespace() + "-" + o.GetName()))
		}
		return c.Create(ctx, o, opts...)
	}
	funcs.Patch = func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
		f.writes = append(f.writes, "patch/"+o.GetName())
		return c.Patch(ctx, o, p, opts...)
	}
	cl := interceptor.NewClient(base, funcs)
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	f.r = &EnvironmentReconciler{Client: cl, APIReader: cl, Namespace: deliveryNS, ClusterID: "cid",
		ClusterName: "eu-1", NameSource: fleet.ClusterNameFromNodeLabel, Ladder: []string{"staging", "production"},
		Now: func() time.Time { return now }, Resync: time.Minute}
	return f
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// ladderPacto is a reconciled Pacto of service in namespace ns.
func ladderPacto(ns, service string) *unstructured.Unstructured {
	u := pactoObj(service, service, dA, "Compliant", 1)
	u.SetNamespace(ns)
	return u
}

func (f *envFixture) pass(t *testing.T) delivery.EnvironmentsView {
	t.Helper()
	res, err := f.r.Reconcile(t.Context(), reconcile.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != time.Minute {
		t.Fatalf("requeue %v", res.RequeueAfter)
	}
	return f.view(t)
}

func (f *envFixture) view(t *testing.T) delivery.EnvironmentsView {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := f.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.EnvironmentsConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	var v delivery.EnvironmentsView
	if err := json.Unmarshal([]byte(cm.Data["view.json"]), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEnvironmentReconcilerWritesDeclaredLadder(t *testing.T) {
	f := newEnvFixture(t, interceptor.Funcs{})
	v := f.pass(t)
	want := []delivery.LadderRung{
		{Name: "staging", Source: ladderSource, Provenance: delivery.RungDeclared},
		{Name: "production", Upstream: "staging", Source: ladderSource, Provenance: delivery.RungDeclared}}
	if len(v.Declared) != 2 || v.Declared[0] != want[0] || v.Declared[1] != want[1] {
		t.Fatalf("declared %+v", v.Declared)
	}
	if len(v.Proposal) != 0 || v.ProposalDigest != "" || v.Completeness != fleet.CompletenessComplete || v.AsOf != "2026-11-23T10:00:00Z" {
		t.Fatalf("view %+v", v)
	}
	// The next pass merge-patches the ConfigMap it created.
	later := time.Date(2026, 11, 23, 10, 1, 0, 0, time.UTC)
	f.r.Now = func() time.Time { return later }
	if v := f.pass(t); v.AsOf != "2026-11-23T10:01:00Z" {
		t.Fatalf("asOf %s", v.AsOf)
	}
	if len(f.writes) != 2 || f.writes[0] != "create/"+delivery.EnvironmentsConfigMap || f.writes[1] != "patch/"+delivery.EnvironmentsConfigMap {
		t.Fatalf("writes %v: only the view, never a Stage", f.writes)
	}
}

func TestEnvironmentReconcilerProposesFromNamespaces(t *testing.T) {
	f := newEnvFixture(t, interceptor.Funcs{},
		namespace("payments-preview"), namespace("orders-preview"), namespace("payments-staging"), namespace("orders-staging"),
		ladderPacto("payments-staging", "payments"), ladderPacto("orders-staging", "orders"))
	v := f.pass(t)
	if len(v.Proposal) != 1 || v.Proposal[0] != (delivery.LadderRung{Name: "preview", Source: "namespaces *-preview, 2 found",
		Provenance: delivery.RungInferred}) {
		t.Fatalf("proposal %+v", v.Proposal)
	}
	if v.ProposalDigest != delivery.ProposalDigest(v.Proposal) || v.ProposalDigest == "" {
		t.Fatalf("digest %q", v.ProposalDigest)
	}
}

func TestEnvironmentReconcilerKnownServicesFromPactos(t *testing.T) {
	unreconciled := ladderPacto("cart-staging", "cart")
	unstructured.RemoveNestedField(unreconciled.Object, "status", "contract")
	f := newEnvFixture(t, interceptor.Funcs{},
		namespace("payments-preview"), namespace("cart-preview"), namespace("ghost-preview"),
		ladderPacto("payments-staging", "payments"), unreconciled)
	if v := f.pass(t); len(v.Proposal) != 0 {
		t.Fatalf("only reconciled Pactos name known services: %+v", v.Proposal)
	}
	// The cluster's advertised environments reach the proposal: a Pacto
	// labelled with an environment advertises it.
	labelled := ladderPacto("cart-staging", "cart")
	labelled.SetLabels(map[string]string{delivery.EnvironmentLabel: "qa"})
	f = newEnvFixture(t, interceptor.Funcs{}, labelled)
	if v := f.pass(t); len(v.Proposal) != 1 || v.Proposal[0].Name != "qa" {
		t.Fatalf("proposal %+v", v.Proposal)
	}
}

func TestEnvironmentReconcilerOffWritesNothing(t *testing.T) {
	f := newEnvFixture(t, interceptor.Funcs{}, namespace("payments-preview"))
	f.r.Off = true
	if res, err := f.r.Reconcile(t.Context(), reconcile.Request{}); err != nil || res.RequeueAfter != 0 {
		t.Fatalf("res %+v err %v", res, err)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes %v", f.writes)
	}
	// Off registers nothing: the manager is never touched.
	if err := f.r.SetupWithManager(nil); err != nil {
		t.Fatal(err)
	}
}

func TestNamespaceListForbiddenIsPartial(t *testing.T) {
	forbidden := errors.New("forbidden")
	f := newEnvFixture(t, interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
		return forbidden
	}}, namespace("payments-preview"))
	v := f.pass(t)
	if v.Completeness != fleet.CompletenessPartial ||
		v.CompletenessReason != "could not read the namespaces: forbidden; the Pactos: forbidden" {
		t.Fatalf("view %+v", v)
	}
	if len(v.Declared) != 2 || len(v.Proposal) != 0 {
		t.Fatalf("view %+v", v)
	}
}

func TestEnvironmentViewReadFailureIsReturned(t *testing.T) {
	boom := errors.New("boom")
	f := newEnvFixture(t, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
		return boom
	}})
	if _, err := f.r.Reconcile(t.Context(), reconcile.Request{}); !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes %v", f.writes)
	}
}

// TestEnvironmentReconcilerSetup: the reconciler registers, watching Pactos
// only where the cluster serves them, and every event maps to one pass.
func TestEnvironmentReconcilerSetup(t *testing.T) {
	for _, served := range []bool{false, true} {
		mapper := meta.NewDefaultRESTMapper(nil)
		if served {
			mapper.Add(gvkPacto, meta.RESTScopeNamespace)
		}
		skip := true
		mgr, err := ctrlrt.NewManager(&rest.Config{Host: "https://127.0.0.1:1"}, ctrlrt.Options{Scheme: newScheme(),
			Metrics:        metricsserver.Options{BindAddress: "0"},
			Controller:     config.Controller{SkipNameValidation: &skip},
			MapperProvider: func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return mapper, nil }})
		if err != nil {
			t.Fatal(err)
		}
		if err := (&EnvironmentReconciler{}).SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
	}
	if got := onePass(t.Context(), namespace("x")); len(got) != 1 || got[0].Name != delivery.EnvironmentsConfigMap {
		t.Fatalf("requests %v", got)
	}
}

const (
	bob     = "bob@acme.com"
	bobUser = "oidc:" + bob
)

// ladderEnv is an envFixture reading ladder records from an in-process
// approvals repository, under approver signers of a private Sigstore.
type ladderEnv struct {
	*envFixture
	reg  *testRegistry
	ss   *sigtest.Sigstore
	repo string
}

func newLadderEnv(t *testing.T, objs ...client.Object) *ladderEnv {
	t.Helper()
	le := &ladderEnv{envFixture: newEnvFixture(t, interceptor.Funcs{}, objs...), reg: newTestRegistry(t), ss: sigtest.New(t)}
	le.repo = le.reg.host + "/acme/pacto-streams-approvals"
	le.r.LadderRepository, le.r.Policy = le.repo, withSigners(delivery.ClassPolicy{}, le.ss)
	r, _ := ocireferrer.Open(le.repo, ocireferrer.Options{})
	if _, err := deliveryrecord.EnsureAnchor(t.Context(), r, delivery.LadderStream("cid")); err != nil {
		t.Fatal(err)
	}
	return le
}

// ladderRecord is bob's confirmation of staging -> production plus preview,
// dropping qa, as of now, with edit applied.
func ladderRecord(edit ...func(*delivery.LadderRecord)) delivery.LadderRecord {
	l := delivery.LadderRecord{ClusterID: "cid", ProposalDigest: "sha256:" + strings.Repeat("5b", 32), Dropped: []string{"qa"},
		Signer: delivery.PublisherIdentity{Issuer: personIssuer, Subject: bob}, At: time.Now().UTC().Format(time.RFC3339),
		Rungs: []delivery.LadderRung{
			{Name: "staging", Source: ladderSource, Provenance: delivery.RungDeclared},
			{Name: "production", Upstream: "staging", Source: ladderSource, Provenance: delivery.RungDeclared},
			{Name: "preview", Source: "namespaces *-preview, 2 found", Provenance: delivery.RungInferred}}}
	for _, f := range edit {
		f(&l)
	}
	return l
}

// push signs l with tok's certificate and pushes it on the ladder stream of
// cid, and returns where it is.
func (le *ladderEnv) push(t *testing.T, tok string, l delivery.LadderRecord) string {
	t.Helper()
	sum := sha256.Sum256([]byte(delivery.LadderStream(l.ClusterID)))
	st := delivery.LadderStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateEnvironmentLadder, Predicate: l,
		Subject: []delivery.Subject{{Name: "ladder", Digest: map[string]string{"sha256": hex.EncodeToString(sum[:])}}}}
	s, err := signing.Keyless(le.ss.Fulcio, le.ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(st)
	b, err := s.Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	return le.pushRaw(t, b)
}

func (le *ladderEnv) pushRaw(t *testing.T, b []byte) string {
	t.Helper()
	r, _ := ocireferrer.Open(le.repo, ocireferrer.Options{})
	ref, err := deliveryrecord.Push(t.Context(), r, delivery.LadderStream("cid"), capabilities.ReferrersNative, delivery.PredicateEnvironmentLadder, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func hasNote(v delivery.EnvironmentsView, part string) bool {
	return slices.ContainsFunc(v.Notes, func(n string) bool { return strings.Contains(n, part) })
}

func TestConfirmedLadderReachesView(t *testing.T) {
	le := newLadderEnv(t, namespace("payments-preview"), namespace("orders-preview"), namespace("payments-qa"), namespace("orders-qa"),
		namespace("payments-dev"), namespace("orders-dev"),
		ladderPacto("payments-preview", "payments"), ladderPacto("orders-preview", "orders"))
	ref := le.push(t, sigtest.Token(bob, personIssuer), ladderRecord())
	v := le.pass(t)
	want := delivery.LadderRung{Name: "preview", Source: "namespaces *-preview, 2 found", Provenance: delivery.RungConfirmed}
	if len(v.Confirmed) != 1 || v.Confirmed[0] != want || v.ConfirmedBy != bobUser || !strings.HasSuffix(ref, v.Record) || v.ConfirmedAt == "" {
		t.Fatalf("view %+v", v)
	}
	if v.Repository != le.repo || v.Stream != "cid/pacto-delivery/ladder" || v.Completeness != fleet.CompletenessComplete || len(v.Notes) != 0 {
		t.Fatalf("view %+v", v)
	}
	// Neither the confirmed preview nor the dropped qa is asked again.
	if len(v.Proposal) != 1 || v.Proposal[0].Name != "development" {
		t.Fatalf("proposal %+v", v.Proposal)
	}
	if len(le.reviews) != 1 || le.reviews[0].User != bobUser || *le.reviews[0].ResourceAttributes != environmentLadderResource {
		t.Fatalf("reviews %+v", le.reviews)
	}
}

func TestLadderRecordIssuerBound(t *testing.T) {
	le := newLadderEnv(t)
	le.push(t, sigtest.Token(bob, "https://other.example"), ladderRecord())
	v := le.pass(t)
	if len(v.Confirmed) != 0 || !hasNote(v, "a ladder record was not counted") || len(le.reviews) != 0 {
		t.Fatalf("view %+v", v)
	}
}

func TestLadderRecordMachineIdentityRefused(t *testing.T) {
	le := newLadderEnv(t)
	ci := "https://github.com/acme/infra/.github/workflows/ladder.yml@refs/heads/main"
	le.push(t, sigtest.Token(ci, personIssuer), ladderRecord(func(l *delivery.LadderRecord) { l.Signer.Subject = ci }))
	v := le.pass(t)
	if len(v.Confirmed) != 0 || !hasNote(v, "agents cannot approve") || len(le.reviews) != 0 {
		t.Fatalf("view %+v", v)
	}
}

// TestLadderRecordUserWithoutApproveNotCounted: a person the cluster does not
// let approve environmentladders confirms nothing; the record is asked again
// next pass, since a grant can be added.
func TestLadderRecordUserWithoutApproveNotCounted(t *testing.T) {
	le := newLadderEnv(t)
	le.review = func(authorizationv1.SubjectAccessReviewSpec) (authorizationv1.SubjectAccessReviewStatus, error) {
		return authorizationv1.SubjectAccessReviewStatus{}, nil
	}
	le.push(t, sigtest.Token(bob, personIssuer), ladderRecord())
	v := le.pass(t)
	if len(v.Confirmed) != 0 || !hasNote(v, bobUser+" may not approve environmentladders") {
		t.Fatalf("view %+v", v)
	}
	le.pass(t)
	if len(le.reviews) != 2 {
		t.Fatalf("reviews %d", len(le.reviews))
	}
}

// TestLadderRecordGroupClaimIgnored: the review names the user the
// certificate maps to and no group, whatever groups the token claimed.
func TestLadderRecordGroupClaimIgnored(t *testing.T) {
	le := newLadderEnv(t)
	le.push(t, sigtest.TokenWith(map[string]string{"sub": bob, "iss": personIssuer, "groups": "system:masters"}), ladderRecord())
	if v := le.pass(t); len(v.Confirmed) != 1 {
		t.Fatalf("view %+v", v)
	}
	if len(le.reviews) != 1 || le.reviews[0].User != bobUser || len(le.reviews[0].Groups) != 0 {
		t.Fatalf("reviews %+v", le.reviews)
	}
}

// TestLadderRecordNotCountedWhenItLies: a record naming another signer than
// its certificate, another cluster, or that the signer of its issuer cannot
// map, never counts.
func TestLadderRecordNotCountedWhenItLies(t *testing.T) {
	for name, c := range map[string]struct {
		edit   func(*delivery.LadderRecord)
		policy func(*delivery.ClassPolicy)
		want   string
	}{
		"another signer":  {edit: func(l *delivery.LadderRecord) { l.Signer.Subject = alice }, want: "names the signer " + alice},
		"another cluster": {edit: func(l *delivery.LadderRecord) { l.ClusterID = "other" }, want: "of cluster other"},
		"unmappable":      {policy: func(p *delivery.ClassPolicy) { p.Signers[0].Subjects = "" }, want: "vouches for nobody"},
	} {
		t.Run(name, func(t *testing.T) {
			le := newLadderEnv(t)
			if c.policy != nil {
				c.policy(&le.r.Policy)
			}
			l := ladderRecord()
			if c.edit != nil {
				c.edit(&l)
			}
			le.push(t, sigtest.Token(bob, personIssuer), l)
			if v := le.pass(t); len(v.Confirmed) != 0 || !hasNote(v, c.want) || len(le.reviews) != 0 {
				t.Fatalf("view %+v", v)
			}
		})
	}
}

// TestRecordListingFailureKeepsLastLadder: a listing, a repository or a
// review that fails keeps the last confirmed ladder, partial with the
// reason.
func TestRecordListingFailureKeepsLastLadder(t *testing.T) {
	le := newLadderEnv(t)
	le.push(t, sigtest.Token(bob, personIssuer), ladderRecord())
	if v := le.pass(t); len(v.Confirmed) != 1 {
		t.Fatalf("view %+v", v)
	}
	kept := func(part string) {
		t.Helper()
		v := le.pass(t)
		if len(v.Confirmed) != 1 || v.ConfirmedBy != bobUser || v.Completeness != fleet.CompletenessPartial || !strings.Contains(v.CompletenessReason, part) {
			t.Fatalf("view %+v", v)
		}
	}
	le.reg.fault = func(*http.Request) bool { return true }
	kept("could not read the ladder records: ")
	le.reg.fault = nil
	le.review = func(authorizationv1.SubjectAccessReviewSpec) (authorizationv1.SubjectAccessReviewStatus, error) {
		return authorizationv1.SubjectAccessReviewStatus{}, errors.New("apiserver down")
	}
	kept("whether " + bobUser + " may approve environmentladders: apiserver down")
	le.review = func(authorizationv1.SubjectAccessReviewSpec) (authorizationv1.SubjectAccessReviewStatus, error) {
		return authorizationv1.SubjectAccessReviewStatus{EvaluationError: "webhook timed out"}, nil
	}
	kept("could not be evaluated: webhook timed out")
	le.r.LadderRepository = "Not A Repository"
	kept("could not read the ladder records: ")
}

// TestLadderRecordFloodBounded: more than MaxLadderRecords records cut the
// listing (partial); the ones that never verify are rejected so they free the
// bound, and the newest counted record by verified signing time wins. An
// older counted record is rejected too, so it never takes the bound again.
func TestLadderRecordFloodBounded(t *testing.T) {
	le := newLadderEnv(t)
	le.push(t, sigtest.Token(alice, personIssuer), ladderRecord(func(l *delivery.LadderRecord) {
		l.Signer.Subject, l.Rungs, l.Dropped = alice, l.Rungs[:2], nil
	}))
	le.ss.Advance(time.Minute)
	for i := range delivery.MaxLadderRecords {
		le.pushRaw(t, fmt.Appendf(nil, `{"junk":%d}`, i))
	}
	le.push(t, sigtest.Token(bob, personIssuer), ladderRecord(func(l *delivery.LadderRecord) { l.At = time.Now().Add(time.Minute).UTC().Format(time.RFC3339) }))
	v := le.pass(t)
	if v.Completeness != fleet.CompletenessPartial || !strings.Contains(v.CompletenessReason, "more than 20 ladder records") {
		t.Fatalf("view %+v", v)
	}
	v = le.pass(t)
	if v.Completeness != fleet.CompletenessComplete || v.ConfirmedBy != bobUser || len(v.Confirmed) != 1 {
		t.Fatalf("view %+v", v)
	}
	// Alice's older record no longer takes a review.
	n := len(le.reviews)
	le.pass(t)
	if got := len(le.reviews) - n; got != 1 {
		t.Fatalf("reviews this pass %d, want bob's only", got)
	}
}

func TestLadderNeedsRepositoryAndSigners(t *testing.T) {
	le := newLadderEnv(t)
	le.push(t, sigtest.Token(bob, personIssuer), ladderRecord())
	le.r.Policy = delivery.ClassPolicy{}
	if v := le.pass(t); len(v.Confirmed) != 0 || !hasNote(v, "no approver signer is configured") {
		t.Fatalf("view %+v", v)
	}
	le.r.LadderRepository = ""
	if v := le.pass(t); len(v.Confirmed) != 0 || len(v.Notes) != 0 || v.Repository != "" || v.Stream != "" {
		t.Fatalf("view %+v", v)
	}
}

// TestStaleChainNoteReachesView: a record signed over another chain still
// confirms its kept rungs, and the view says the chart value wins.
func TestStaleChainNoteReachesView(t *testing.T) {
	le := newLadderEnv(t)
	le.r.Ladder = []string{"staging", "canary", "production"}
	le.push(t, sigtest.Token(bob, personIssuer), ladderRecord())
	if v := le.pass(t); len(v.Confirmed) != 1 || !hasNote(v, "now names staging -> canary -> production, which wins") {
		t.Fatalf("view %+v", v)
	}
}
