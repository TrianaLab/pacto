/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/observer"
)

func pullSecretPacto(name string) *pactov1.Pacto {
	return &pactov1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "payments", UID: types.UID("u-" + name)},
		Spec: pactov1.PactoSpec{
			ContractRef: pactov1.ContractRef{OCI: "ghcr.io/org/svc:1.0.0", PullSecretRef: "regcred"},
		},
	}
}

// The API server, not a stub, refuses the read: the operator identity has no secrets get in payments.
var _ = Describe("TestPullSecretForbiddenSetsPullSecretNotGranted", func() {
	It("reads ContractValid=Unknown, reason PullSecretNotGranted, and names the grant", func() {
		user, err := testEnv.AddUser(envtest.User{Name: "pacto-operator-without-secrets"}, nil)
		Expect(err).NotTo(HaveOccurred())
		denied, err := client.New(user.Config(), client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		pacto := pullSecretPacto("ps-not-granted")
		r := newReconciler(pacto)
		r.APIReader = denied
		loaded := false
		r.Loader = &mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
			loaded = true
			return nil, errors.New("must not load without credentials")
		}}

		_, err = r.Reconcile(ctx, reconcileReq(pacto.Name, pacto.Namespace))
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded).To(BeFalse())

		p := &pactov1.Pacto{}
		Expect(r.Get(ctx, client.ObjectKeyFromObject(pacto), p)).To(Succeed())
		Expect(p.Status.ContractStatus).To(Equal(pactov1.ContractStatusUnknown))
		Expect(p.Status.Validation).To(BeNil(), "the contract was never read, so it is never Invalid")
		cond := meta.FindStatusCondition(p.Status.Conditions, pactov1.ConditionContractValid)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
		Expect(cond.Reason).To(Equal(pactov1.ReasonPullSecretNotGranted))
		Expect(cond.Message).To(Equal("the operator may not read Secret payments/regcred: list payments/regcred in controller.secretGrants"))
	})
})

// forbiddenReader refuses every Secret read the way the API server does without a grant.
func forbiddenReader(t *testing.T) client.Reader {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(newScheme()).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, key client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return apierrors.NewForbidden(corev1.Resource("secrets"), key.Name, errors.New("no grant"))
		},
	}).Build()
}

func TestPullSecretForbiddenScopedNamesWatchNamespaces(t *testing.T) {
	pacto := pullSecretPacto("ps-scoped")
	r := newReconciler(pacto)
	r.APIReader = forbiddenReader(t)
	r.Scoped = true

	if _, err := r.Reconcile(context.Background(), reconcileReq(pacto.Name, pacto.Namespace)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cond := meta.FindStatusCondition(getPacto(t, r, pacto.Name, pacto.Namespace).Status.Conditions, pactov1.ConditionContractValid)
	want := "the operator may not read Secret payments/regcred although it watches payments: check the chart's watch Role and RoleBinding in payments or list payments/regcred in controller.secretGrants"
	if cond == nil || cond.Reason != pactov1.ReasonPullSecretNotGranted || cond.Message != want {
		t.Fatalf("condition = %+v, want reason PullSecretNotGranted and message %q", cond, want)
	}
}

// No Secret watch: a rotated pull Secret is read on the next periodic reconcile.
func TestPullSecretRotationReadOnRequeue(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "regcred", Namespace: "payments"},
		Data:       map[string][]byte{"token": []byte("old")},
	}
	pacto := pullSecretPacto("ps-rotate")
	r := newReconciler(pacto, secret)
	var tokens []string
	r.Loader = &tokenLoader{seen: &tokens}

	res, err := r.Reconcile(context.Background(), reconcileReq(pacto.Name, pacto.Namespace))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatal("every reconcile must requeue: it is what reads a rotated pull Secret")
	}

	secret.Data["token"] = []byte("new")
	if err := r.Update(context.Background(), secret); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), reconcileReq(pacto.Name, pacto.Namespace)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 2 || tokens[0] != "old" || tokens[1] != "new" {
		t.Fatalf("tokens seen by the loader = %v, want [old new]", tokens)
	}
}

// tokenLoader records the registry token each Load was given.
type tokenLoader struct{ seen *[]string }

func (l *tokenLoader) Load(_ context.Context, _, _ string, auth *authn.AuthConfig) (*loader.LoadResult, error) {
	tok := ""
	if auth != nil {
		tok = auth.RegistryToken
	}
	*l.seen = append(*l.seen, tok)
	return nil, errors.New("stop after the credentials")
}

func (l *tokenLoader) ListTags(context.Context, string, *authn.AuthConfig) ([]string, error) {
	return nil, errors.New("not used")
}

func TestRevisionMirrorSkipsNotGrantedPullSecret(t *testing.T) {
	pacto := pullSecretPacto("ps-mirror")
	r := newReconciler(pacto)
	r.APIReader = forbiddenReader(t)
	rec := record.NewFakeRecorder(5)
	r.Recorder = rec
	listed := false
	r.Loader = &mockLoader{listTagsFn: func(context.Context, string) ([]string, error) {
		listed = true
		return nil, nil
	}}

	if err := (&RevisionMirror{Reconciler: r}).Sync(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if listed {
		t.Fatal("the mirror must skip a Pacto whose pull Secret it may not read")
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Fatalf("the reconcile reports it on the Pacto; the mirror emits nothing, got %v", events)
	}
}

// The observer's configuration grant hint follows the same mode as the pull
// secret one, so the reconciler hands its Scoped flag to every collection.
func TestBuildCollectInputCarriesScoped(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		r := &PactoReconciler{Scoped: scoped}
		in := r.buildCollectInput(&pactov1.Pacto{}, &effectiveContract{loaded: &loader.LoadResult{}}, observer.Setting{}, observer.Setting{})
		if in.Scoped != scoped {
			t.Errorf("Scoped = %v, want %v", in.Scoped, scoped)
		}
	}
}
