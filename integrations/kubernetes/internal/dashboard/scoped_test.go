/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func dashboardEnv(t *testing.T, cfg Config) map[string]string {
	t.Helper()
	deploy := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
	env := map[string]string{}
	for _, e := range deploy.Spec.Template.Spec.Containers[0].Env {
		if e.Value != nil {
			env[*e.Name] = *e.Value
		}
	}
	return env
}

func TestDeploymentEnvListsNamespaces(t *testing.T) {
	env := dashboardEnv(t, Config{Image: "img", Namespace: "pacto", WatchNamespaces: []string{"ledger", "payments"}})
	if got := env["PACTO_DASHBOARD_NAMESPACE"]; got != "ledger,payments" {
		t.Fatalf("PACTO_DASHBOARD_NAMESPACE = %q, want ledger,payments", got)
	}
	if _, ok := dashboardEnv(t, Config{Image: "img", Namespace: "pacto"})["PACTO_DASHBOARD_NAMESPACE"]; ok {
		t.Fatal("a cluster-wide dashboard must not be narrowed")
	}
}

// The local review is the only one a dashboard without a ClusterRole can ask.
// With delivery on, or cluster-wide, it keeps the cluster review (and its
// namespace-less services).
func TestDeploymentEnvLocalReviewOnlyWithoutClusterRole(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"scoped without delivery", Config{WatchNamespaces: []string{"payments"}}, "local"},
		{"scoped with delivery", Config{WatchNamespaces: []string{"payments"}, DeliveryNamespace: "pacto-delivery"}, ""},
		{"cluster-wide", Config{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.Image, tc.cfg.Namespace = "img", "pacto"
			if got := dashboardEnv(t, tc.cfg)["PACTO_DASHBOARD_REVIEW"]; got != tc.want {
				t.Fatalf("PACTO_DASHBOARD_REVIEW = %q, want %q", got, tc.want)
			}
		})
	}
}

// applyRecorder wraps a fake client and records the kinds of cluster RBAC applied.
func applyRecorder(applied *[]string) interceptor.Funcs {
	return interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			switch ac := obj.(type) {
			case *rbacv1ac.ClusterRoleApplyConfiguration:
				*applied = append(*applied, "ClusterRole")
				for _, r := range ac.Rules {
					*applied = append(*applied, "rule:"+strings.Join(r.Resources, ","))
				}
			case *rbacv1ac.ClusterRoleBindingApplyConfiguration:
				*applied = append(*applied, "ClusterRoleBinding")
			}
			return c.Apply(ctx, obj, opts...)
		},
	}
}

func TestSyncScopedAppliesNoClusterRole(t *testing.T) {
	var applied []string
	r := newReconcilerWithInterceptors(Config{Enabled: true, Image: "img", Namespace: "pacto",
		WatchNamespaces: []string{"ledger", "payments"}}, applyRecorder(&applied))
	if err := r.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("a scoped dashboard without delivery applied cluster RBAC: %v", applied)
	}
}

func TestSyncScopedPrunesStaleClusterRole(t *testing.T) {
	r := newReconciler(Config{Enabled: true, Image: "img", Namespace: "pacto", WatchNamespaces: []string{"payments"}},
		managedRBAC("pacto")...)
	ctx := context.Background()
	if err := r.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	assertClusterResourceNotFound(t, r.Client, ctx, &rbacv1.ClusterRole{})
	assertClusterResourceNotFound(t, r.Client, ctx, &rbacv1.ClusterRoleBinding{})
}

func forbidden(resource string) error {
	return apierrors.NewForbidden(schema.GroupResource{Group: rbacv1.GroupName, Resource: resource}, Name, errors.New("not permitted"))
}

// A scoped operator holds no right to look at cluster RBAC. It says so once,
// with the command that removes the leftover, and keeps the dashboard running.
func TestSyncScopedPruneForbiddenWarns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		funcs interceptor.Funcs
	}{
		{"read forbidden", interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*rbacv1.ClusterRoleBinding); ok {
					return forbidden("clusterrolebindings")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}},
		{"delete forbidden", interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*rbacv1.ClusterRoleBinding); ok {
					return forbidden("clusterrolebindings")
				}
				return c.Delete(ctx, obj, opts...)
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReconcilerWithInterceptors(Config{Enabled: true, Image: "img", Namespace: "pacto",
				WatchNamespaces: []string{"payments"}}, tc.funcs, managedRBAC("pacto")...)
			rec := record.NewFakeRecorder(10)
			r.Recorder = rec
			for range 2 {
				if err := r.Sync(context.Background()); err != nil {
					t.Fatalf("a forbidden prune must not fail the sync: %v", err)
				}
			}
			close(rec.Events)
			var events []string
			for e := range rec.Events {
				events = append(events, e)
			}
			if len(events) != 1 {
				t.Fatalf("want one Warning, got %v", events)
			}
			if e := events[0]; !strings.HasPrefix(e, "Warning StaleClusterRBAC ") || !strings.Contains(e, staleClusterRBACHint) {
				t.Fatalf("event = %q", e)
			}
		})
	}
}

func TestSyncScopedPruneErrorFails(t *testing.T) {
	r := newReconcilerWithInterceptors(Config{Enabled: true, Image: "img", Namespace: "pacto",
		WatchNamespaces: []string{"payments"}}, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*rbacv1.ClusterRole); ok {
				return errors.New("apiserver down")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	if err := r.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "stale cluster role") {
		t.Fatalf("err = %v, want the stale cluster role error", err)
	}
}

func TestSyncScopedWithDeliveryAppliesDeliveryHalf(t *testing.T) {
	var applied []string
	r := newReconcilerWithInterceptors(Config{Enabled: true, Image: "img", Namespace: "pacto",
		WatchNamespaces: []string{"payments"}, DeliveryNamespace: "pacto-delivery"}, applyRecorder(&applied))
	if err := r.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"ClusterRole", "rule:stages,operations", "rule:subjectaccessreviews", "rule:namespaces", "rule:namespaces", "ClusterRoleBinding"}
	if !slices.Equal(applied, want) {
		t.Fatalf("applied = %v, want %v", applied, want)
	}
}

// TestDashboardClusterRoleInventory pins every rule of both shapes of the
// ClusterRole the operator applies, so a new grant is a diff here.
func TestDashboardClusterRoleInventory(t *testing.T) {
	deliveryHalf := []rbacv1.PolicyRule{
		{APIGroups: []string{"delivery.pacto.trianalab.io"}, Resources: []string{"stages", "operations"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"authorization.k8s.io"}, Resources: []string{"subjectaccessreviews"}, Verbs: []string{"create"}},
		{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"list"}},
		{APIGroups: []string{""}, Resources: []string{"namespaces"}, ResourceNames: []string{"kube-system"}, Verbs: []string{"get"}},
	}
	full := append([]rbacv1.PolicyRule{
		{APIGroups: []string{"pacto.trianalab.io"}, Resources: []string{"pactos", "pactorevisions"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"get", "list", "watch"}},
		{APIGroups: []string{"apps"}, Resources: []string{"deployments", "statefulsets", "daemonsets"}, Verbs: []string{"list"}},
		{APIGroups: []string{"batch"}, Resources: []string{"cronjobs"}, Verbs: []string{"list"}},
		{APIGroups: []string{"argoproj.io"}, Resources: []string{"applications"}, Verbs: []string{"list"}},
		{APIGroups: []string{"kustomize.toolkit.fluxcd.io"}, Resources: []string{"kustomizations"}, Verbs: []string{"list"}},
		{APIGroups: []string{"helm.toolkit.fluxcd.io"}, Resources: []string{"helmreleases"}, Verbs: []string{"list"}},
		{APIGroups: []string{"source.toolkit.fluxcd.io"}, Resources: []string{"gitrepositories"}, Verbs: []string{"list"}},
	}, deliveryHalf...)
	for _, tc := range []struct {
		scoped bool
		want   []rbacv1.PolicyRule
	}{{false, full}, {true, deliveryHalf}} {
		role := clusterRoleAC(tc.scoped).(*rbacv1ac.ClusterRoleApplyConfiguration)
		got := make([]rbacv1.PolicyRule, len(role.Rules))
		for i, r := range role.Rules {
			got[i] = rbacv1.PolicyRule{APIGroups: r.APIGroups, Resources: r.Resources, ResourceNames: r.ResourceNames, Verbs: r.Verbs}
		}
		if !slices.EqualFunc(got, tc.want, func(a, b rbacv1.PolicyRule) bool {
			return slices.Equal(a.APIGroups, b.APIGroups) && slices.Equal(a.Resources, b.Resources) &&
				slices.Equal(a.ResourceNames, b.ResourceNames) && slices.Equal(a.Verbs, b.Verbs)
		}) {
			t.Errorf("clusterRoleAC(%v) rules =\n%v\nwant\n%v", tc.scoped, got, tc.want)
		}
	}
}

// The release-namespace secrets get the chart always grants covers the OCI
// credentials: every read goes through APIReader in Config.Namespace, never
// in a watched namespace.
func TestOCISecretsReadFromReleaseNamespace(t *testing.T) {
	const ns = "pacto"
	source := &corev1.Secret{Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"ghcr.io":{"username":"u","password":"p"}}}`)}}
	source.Name, source.Namespace = "registry-creds", ns
	scheme := newScheme()
	var reads []string
	apiReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(source).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				reads = append(reads, key.Namespace)
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
	r := &Reconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), APIReader: apiReader, Scheme: scheme,
		Config: Config{Enabled: true, Image: "img", Namespace: ns, WatchNamespaces: []string{"payments"}, OCISecrets: []string{source.Name}}}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(reads) == 0 || slices.ContainsFunc(reads, func(n string) bool { return n != ns }) {
		t.Fatalf("Secret reads in namespaces %v, want only %s", reads, ns)
	}
}
