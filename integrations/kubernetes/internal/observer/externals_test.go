package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	extNS       = "payments"
	credURL     = "postgres://user:pass@db.xyz.rds.amazonaws.com/app?password=x"
	jwtFixture  = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJl"
	seGroupVers = "networking.istio.io/v1"
)

func extScheme() *runtime.Scheme {
	s := newScheme()
	_ = discoveryv1.AddToScheme(s)
	_ = unversioned.AddToScheme(s)
	s.AddKnownTypeWithName(serviceEntryList.GroupVersion().WithKind("ServiceEntry"), &unstructured.Unstructured{})
	s.AddKnownTypeWithName(serviceEntryList, &unstructured.UnstructuredList{})
	return s
}

func extDeployment(c corev1.Container) *appsv1.Deployment {
	d := makeDeployment(extNS, "payments", nil)
	c.Name = "app"
	d.Spec.Template.Spec.Containers = []corev1.Container{c}
	return d
}

func extInput() CollectInput {
	return CollectInput{Namespace: extNS, WorkloadName: "payments", WorkloadKind: "Deployment", ServiceEntries: true}
}

func extObserve(t *testing.T, fns interceptor.Funcs, input CollectInput, objs ...client.Object) ([]unversioned.ExternalStatus, unversioned.CallerCoverage) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(extScheme()).WithObjects(objs...).WithInterceptorFuncs(fns).Build()
	return New(c).observeExternals(context.Background(), input)
}

func cm(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: extNS}, Data: data}
}

func envVal(name, value string) corev1.EnvVar { return corev1.EnvVar{Name: name, Value: value} }

func summary(es []unversioned.ExternalStatus) string {
	b := make([]string, 0, len(es))
	for _, e := range es {
		b = append(b, fmt.Sprintf("%s|%s|%s|%s|%s|%s", e.Identity, e.Class, e.Confidence, e.Kind, e.Host, e.Where))
	}
	return strings.Join(b, "\n")
}

func wantCoverage(t *testing.T, got unversioned.CallerCoverage, state, reason string) {
	t.Helper()
	if got.State != state || got.Reason != reason {
		t.Fatalf("coverage = %+v, want %s %s", got, state, reason)
	}
}

func TestObserveExternalsEnvAndConfigMap(t *testing.T) {
	d := extDeployment(corev1.Container{
		Env: []corev1.EnvVar{
			envVal("STRIPE_API_BASE", "https://api.stripe.com/v1"),
			{Name: "DB", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "app"}, Key: "DATABASE_URL"}}},
			{Name: "MISSING", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "gone"}, Key: "X"}}},
			{Name: "POD", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
		},
		EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app"}}}, {}},
	})
	got, cov := extObserve(t, interceptor.Funcs{}, extInput(), d, cm("app", map[string]string{"DATABASE_URL": "postgres://db.xyz.rds.amazonaws.com:5432/app"}))
	want := "aws/rds|managed-cloud|likely|configMap|db.xyz.rds.amazonaws.com:5432|configmap/app key DATABASE_URL\n" +
		"stripe|vendor-api|likely|env|api.stripe.com|deployment/payments env STRIPE_API_BASE"
	if summary(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", summary(got), want)
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

// failGet fails the test on a Get the predicate rejects.
func failGet(t *testing.T, bad func(client.ObjectKey, client.Object) bool) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if bad(key, obj) {
				t.Errorf("read %T %s", obj, key)
			}
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.SecretList); ok {
				t.Errorf("listed Secrets")
			}
			return c.List(ctx, list, opts...)
		},
	}
}

func TestObserveExternalsIgnoresVolumeConfigMap(t *testing.T) {
	d := extDeployment(corev1.Container{})
	d.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: "v", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "mounted"}}}}}
	fns := failGet(t, func(_ client.ObjectKey, obj client.Object) bool { _, ok := obj.(*corev1.ConfigMap); return ok })
	got, _ := extObserve(t, fns, extInput(), d, cm("mounted", map[string]string{"URL": "https://api.stripe.com"}), cm("loose", map[string]string{"URL": "https://api.stripe.com"}))
	if len(got) != 0 {
		t.Fatalf("got %s", summary(got))
	}
}

func TestObserveExternalsSecretKeyRefKeyNameOnly(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{{Name: "PAY_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "creds"}, Key: "STRIPE_SECRET_KEY"}}}}})
	got, _ := extObserve(t, failGet(t, func(_ client.ObjectKey, obj client.Object) bool { _, ok := obj.(*corev1.Secret); return ok }), extInput(), d)
	want := "stripe|vendor-api|possible|secretKeyRef||deployment/payments env PAY_KEY from secret/creds key STRIPE_SECRET_KEY"
	if summary(got) != want {
		t.Fatalf("got %s", summary(got))
	}
}

func TestObserveExternalsSecretRefNameOnly(t *testing.T) {
	d := extDeployment(corev1.Container{EnvFrom: []corev1.EnvFromSource{
		{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "stripe-credentials"}}},
		{Prefix: "TWILIO_", SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "other"}}},
	}})
	got, _ := extObserve(t, failGet(t, func(_ client.ObjectKey, obj client.Object) bool { _, ok := obj.(*corev1.Secret); return ok }), extInput(), d)
	want := "stripe|vendor-api|possible|secretRef||deployment/payments envFrom secret/stripe-credentials\n" +
		"twilio|vendor-api|possible|secretRef||deployment/payments envFrom secret/other"
	if summary(got) != want {
		t.Fatalf("got %s", summary(got))
	}
}

func TestObserveExternalsNeverReadsSecrets(t *testing.T) {
	d := extDeployment(corev1.Container{
		Env:     []corev1.EnvVar{{Name: "STRIPE_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "s"}, Key: "k"}}}},
		EnvFrom: []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "s"}}}},
	})
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: extNS}, StringData: map[string]string{"k": "https://api.stripe.com"}}
	extObserve(t, failGet(t, func(_ client.ObjectKey, obj client.Object) bool { _, ok := obj.(*corev1.Secret); return ok }), extInput(), d, secret)
}

func TestObserveExternalsRedactsCredentialURL(t *testing.T) {
	d := extDeployment(corev1.Container{
		Env:     []corev1.EnvVar{envVal("DATABASE_URL", credURL), envVal("API_TOKEN", jwtFixture)},
		EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "settings"}}}},
	})
	got, _ := extObserve(t, interceptor.Funcs{}, extInput(), d, cm("settings", map[string]string{"DB_URL": credURL, "SESSION_TOKEN": jwtFixture, "auth.password": "s3cr3t.v4lue"}))
	b, _ := json.Marshal(unversioned.PactoStatus{Externals: got})
	for _, bad := range []string{"user", "pass", "/app", "?", jwtFixture, "s3cr3t", "TOKEN"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("status carries %q: %s", bad, b)
		}
	}
	if !strings.Contains(string(b), "db.xyz.rds.amazonaws.com") || len(got) != 2 {
		t.Fatalf("got %s", summary(got))
	}
}

func extService(name string, mut func(*corev1.Service)) *corev1.Service {
	s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: extNS}}
	mut(s)
	return s
}

func TestObserveExternalsExternalNameService(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("LEGACY_DB", "legacy-db:5432")}})
	svc := extService("legacy-db", func(s *corev1.Service) {
		s.Spec.Type, s.Spec.ExternalName = corev1.ServiceTypeExternalName, "db1.corp-legacy.co.uk"
	})
	got, _ := extObserve(t, interceptor.Funcs{}, extInput(), d, svc)
	want := "host/corp-legacy.co.uk|legacy|likely|externalNameService|db1.corp-legacy.co.uk|service/legacy-db from deployment/payments env LEGACY_DB"
	if summary(got) != want {
		t.Fatalf("got %s", summary(got))
	}
}

func TestObserveExternalsClusterIPServiceNoEntry(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("ORDERS", "orders:8080"), envVal("OTHER", "http://orders."+extNS+".svc.cluster.local"), envVal("NOPE", "absent:80")}})
	svc := extService("orders", func(s *corev1.Service) { s.Spec.Selector = map[string]string{"app": "orders"} })
	got, cov := extObserve(t, interceptor.Funcs{}, extInput(), d, svc)
	if len(got) != 0 {
		t.Fatalf("got %s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

func TestObserveExternalsCrossNamespaceServiceNotRead(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("X", "victim.other-ns:5432")}})
	victim := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "victim", Namespace: "other-ns"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "secret.target.co.uk"}}
	fns := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Namespace != extNS {
				t.Errorf("read %s outside %s", key, extNS)
			}
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			t.Errorf("listed %T", list)
			return nil
		},
	}
	got, _ := extObserve(t, fns, extInput(), d, victim)
	if strings.Contains(summary(got), "target") {
		t.Fatalf("got %s", summary(got))
	}
}

func TestObserveExternalsSelectorlessService(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("ERP", "http://erp")}})
	svc := extService("erp", func(*corev1.Service) {})
	slice := func(name string, at discoveryv1.AddressType, addr string) *discoveryv1.EndpointSlice {
		return &discoveryv1.EndpointSlice{
			ObjectMeta:  metav1.ObjectMeta{Name: name, Namespace: extNS, Labels: map[string]string{discoveryv1.LabelServiceName: "erp"}},
			AddressType: at, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{addr}}},
		}
	}
	got, _ := extObserve(t, interceptor.Funcs{}, extInput(), d, svc, slice("a", discoveryv1.AddressTypeFQDN, "erp.corp-legacy.co.uk"), slice("b", discoveryv1.AddressTypeIPv4, "10.0.0.1"))
	want := "host/corp-legacy.co.uk|legacy|likely|selectorlessService|erp.corp-legacy.co.uk|service/erp from deployment/payments env ERP"
	if summary(got) != want {
		t.Fatalf("got %s", summary(got))
	}
}

func serviceEntry(ns, name string, hosts, exportTo []string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{}}}
	u.SetAPIVersion(seGroupVers)
	u.SetKind("ServiceEntry")
	u.SetNamespace(ns)
	u.SetName(name)
	_ = unstructured.SetNestedStringSlice(u.Object, hosts, "spec", "hosts")
	if exportTo != nil {
		_ = unstructured.SetNestedStringSlice(u.Object, exportTo, "spec", "exportTo")
	}
	return u
}

func TestObserveExternalsServiceEntry(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("PARTNER_URL", "https://api.partner.co.uk:8443/x")}})
	got, cov := extObserve(t, interceptor.Funcs{}, extInput(), d, serviceEntry(extNS, "partner", []string{"*.partner.co.uk", "other.example.com"}, nil))
	want := "host/partner.co.uk|unclassified|likely|env|api.partner.co.uk:8443|deployment/payments env PARTNER_URL\n" +
		"host/partner.co.uk|legacy|likely|serviceEntry|api.partner.co.uk:8443|serviceentry/payments/partner host *.partner.co.uk"
	if summary(got) != want {
		t.Fatalf("got\n%s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

func TestObserveExternalsServiceEntryExportToRespected(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("PARTNER_URL", "https://api.partner.co.uk")}})
	got, _ := extObserve(t, interceptor.Funcs{}, extInput(), d,
		serviceEntry("mesh", "shared", []string{"api.partner.co.uk"}, []string{"*"}),
		serviceEntry("team-b", "private", []string{"api.partner.co.uk"}, []string{"."}))
	s := summary(got)
	// An entry exported from another namespace is reported, never named.
	if !strings.Contains(s, "serviceentry (exported) host api.partner.co.uk") || strings.Contains(s, "mesh") || strings.Contains(s, "shared") || strings.Contains(s, "team-b") {
		t.Fatalf("got %s", s)
	}
}

// TestObserveExternalsServiceEntryOffNoHostComplete: with no host in the pod
// spec, no ServiceEntry could add anything, so the reader being off leaves the
// list complete.
func TestObserveExternalsServiceEntryOffNoHostComplete(t *testing.T) {
	in := extInput()
	in.ServiceEntries = false
	got, cov := extObserve(t, interceptor.Funcs{}, in, extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("LOG_LEVEL", "debug")}}))
	if len(got) != 0 {
		t.Fatalf("got %s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

// TestObserveExternalsObjectHostUnclassifiedPartial: an ExternalName under a
// private suffix gives no identity, so the list says it is partial instead of
// reading as "uses none".
func TestObserveExternalsObjectHostUnclassifiedPartial(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("LEGACY_DB", "legacy-db:5432")}})
	svc := extService("legacy-db", func(s *corev1.Service) {
		s.Spec.Type, s.Spec.ExternalName = corev1.ServiceTypeExternalName, "db1.corp.internal"
	})
	got, cov := extObserve(t, interceptor.Funcs{}, extInput(), d, svc)
	if len(got) != 0 {
		t.Fatalf("got %s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoveragePartial, "object-host-unclassified")
}

func TestObserveExternalsServiceEntryOffPartial(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("STRIPE_API_BASE", "https://api.stripe.com")}})
	in := extInput()
	in.ServiceEntries = false
	got, cov := extObserve(t, interceptor.Funcs{}, in, d)
	if len(got) != 1 {
		t.Fatalf("got %s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoveragePartial, "serviceentry-reader-off")
}

func TestObserveExternalsServiceEntryUnreadable(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("STRIPE_API_BASE", "https://api.stripe.com")}})
	for _, failAt := range []int{1, 2} {
		n := 0
		fns := interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if n++; n == failAt {
				return errors.New("forbidden")
			}
			return c.List(ctx, list, opts...)
		}}
		_, cov := extObserve(t, fns, extInput(), d)
		wantCoverage(t, cov, unversioned.CallerCoveragePartial, "serviceentry-unreadable")
	}
}

func TestObserveExternalsPodSpecErrorUnavailable(t *testing.T) {
	fns := interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
		return errors.New("boom")
	}}
	got, cov := extObserve(t, fns, extInput())
	if got != nil {
		t.Fatalf("got %s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageUnavailable, "pod-spec-unavailable")
}

func TestObserveExternalsDeclaredDependencyExternalName(t *testing.T) {
	d := extDeployment(corev1.Container{})
	svc := extService("ledger", func(s *corev1.Service) {
		s.Spec.Type, s.Spec.ExternalName = corev1.ServiceTypeExternalName, "ledger.corp-legacy.co.uk"
	})
	sibling := &unversioned.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "ledger", Namespace: extNS},
		Spec:       unversioned.PactoSpec{Target: unversioned.TargetRef{ServiceName: "ledger"}},
		Status:     unversioned.PactoStatus{Contract: &unversioned.ContractInfo{ResolvedRef: "ghcr.io/acme/ledger:1.0.0"}},
	}
	in := extInput()
	in.Contract = &contract.Contract{Dependencies: []contract.Dependency{{Name: "ledger", Ref: "oci://ghcr.io/acme/ledger"}}}
	c := fake.NewClientBuilder().WithScheme(extScheme()).WithObjects(d, svc, sibling).WithStatusSubresource(sibling).Build()
	if err := c.Status().Update(context.Background(), sibling); err != nil {
		t.Fatal(err)
	}
	got, _ := New(c).observeExternals(context.Background(), in)
	want := "host/corp-legacy.co.uk|legacy|likely|externalNameService|ledger.corp-legacy.co.uk|service/ledger from dependency ledger"
	if summary(got) != want {
		t.Fatalf("got %s", summary(got))
	}
}

func TestObserveExternalsReadErrorsPartial(t *testing.T) {
	d := extDeployment(corev1.Container{
		Env:     []corev1.EnvVar{envVal("LEGACY", "legacy:1"), envVal("ERP", "erp:1")},
		EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "app"}}}},
	})
	cases := map[string]struct {
		fns    interceptor.Funcs
		reason string
	}{
		"configmap": {interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.ConfigMap); ok {
				return errors.New("forbidden")
			}
			return c.Get(ctx, key, obj, opts...)
		}}, "configmap-unreadable"},
		"service": {interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return errors.New("forbidden")
			}
			return c.Get(ctx, key, obj, opts...)
		}}, "service-unreadable"},
		"endpointslices": {interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*discoveryv1.EndpointSliceList); ok {
				return errors.New("forbidden")
			}
			return c.List(ctx, list, opts...)
		}}, "service-unreadable"},
		"pactos": {interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*unversioned.PactoList); ok {
				return errors.New("forbidden")
			}
			return c.List(ctx, list, opts...)
		}}, "service-unreadable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := extInput()
			in.Contract = &contract.Contract{Dependencies: []contract.Dependency{{Name: "x", Ref: "oci://r/x"}}}
			_, cov := extObserve(t, tc.fns, in, d, cm("app", nil), extService("erp", func(*corev1.Service) {}))
			wantCoverage(t, cov, unversioned.CallerCoveragePartial, tc.reason)
		})
	}
}

func TestObserveExternalsTruncates(t *testing.T) {
	env := make([]corev1.EnvVar, 0, MaxStatusExternals+5)
	for i := range MaxStatusExternals + 5 {
		env = append(env, envVal(fmt.Sprintf("HOST_%d", i), fmt.Sprintf("https://api.vendor%02d.co.uk", i)))
	}
	in := extInput()
	in.ServiceEntries = false
	got, cov := extObserve(t, interceptor.Funcs{}, in, extDeployment(corev1.Container{Env: env}))
	if len(got) != MaxStatusExternals {
		t.Fatalf("len = %d", len(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoveragePartial, "truncated")
}

func TestObserveExternalsUnresolvedHostNoEntry(t *testing.T) {
	d := extDeployment(corev1.Container{Env: []corev1.EnvVar{envVal("DB_URL", "postgres://10.0.0.7:5432/app"), envVal("V6", "[::1]:80"), envVal("BAD", "http://%zz")}})
	got, _ := extObserve(t, interceptor.Funcs{}, extInput(), d)
	if len(got) != 0 {
		t.Fatalf("got %s", summary(got))
	}
}

func TestObserverExternals(t *testing.T) {
	o := New(fake.NewClientBuilder().WithScheme(extScheme()).Build())
	if es, cov := o.Externals(); es != nil || cov != nil {
		t.Fatal("externals before a Collect")
	}
	if _, _, err := o.Collect(context.Background(), CollectInput{Namespace: extNS, Contract: &contract.Contract{}}); err != nil {
		t.Fatal(err)
	}
	if _, cov := o.Externals(); cov == nil || cov.Reason != "pod-spec-unavailable" {
		t.Fatalf("coverage %+v", cov)
	}
	if !strings.HasSuffix(ExternalCatalog(), " (embedded)") {
		t.Fatal(ExternalCatalog())
	}
}
