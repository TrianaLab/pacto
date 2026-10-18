/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package nodeagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
)

func newReconciler(cfg Config, funcs interceptor.Funcs, objs ...client.Object) *Reconciler {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = rbacv1.AddToScheme(s)
	_ = pactov1alpha1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(funcs).WithObjects(objs...).Build()
	// The fake client is uncached, so it doubles as the APIReader that
	// cmd/main.go wires to mgr.GetAPIReader() in production.
	return &Reconciler{Client: c, APIReader: c, Scheme: s, Config: cfg}
}

func namespace(enforce string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}}
	if enforce != "" {
		ns.Labels = map[string]string{PodSecurityEnforceLabel: enforce}
	}
	return ns
}

func observation(name string, labels map[string]string) *pactov1alpha1.NodeRuntimeObservation {
	return &pactov1alpha1.NodeRuntimeObservation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test-ns", Labels: labels},
	}
}

// managedObjects are the four objects Sync applies, empty for a Get.
func managedObjects() map[string]client.Object {
	return map[string]client.Object{
		"DaemonSet":      &appsv1.DaemonSet{},
		"RoleBinding":    &rbacv1.RoleBinding{},
		"Role":           &rbacv1.Role{},
		"ServiceAccount": &corev1.ServiceAccount{},
	}
}

// captureLogs returns a context whose logger appends every line to *out.
func captureLogs(out *strings.Builder) context.Context {
	return logf.IntoContext(context.Background(), funcr.New(func(prefix, args string) {
		out.WriteString(prefix + " " + args + "\n")
	}, funcr.Options{}))
}

func TestReconciler_IsRunnableNotReconciler(t *testing.T) {
	var r any = &Reconciler{}
	if _, ok := r.(manager.Runnable); !ok {
		t.Error("Reconciler must be a manager.Runnable")
	}
	if _, ok := r.(reconcile.Reconciler); ok {
		t.Error("Reconciler must not be a reconcile.Reconciler")
	}
}

func TestSync_Enabled_AppliesEverything(t *testing.T) {
	r := newReconciler(enabledCfg(), interceptor.Funcs{}, namespace("privileged"))
	var logs strings.Builder
	if err := r.Sync(captureLogs(&logs)); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	for kind, obj := range managedObjects() {
		if err := r.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: Name}, obj); err != nil {
			t.Errorf("%s not applied: %v", kind, err)
			continue
		}
		if obj.GetLabels()[LabelComponent] != ComponentValue {
			t.Errorf("%s missing component label", kind)
		}
	}
	if strings.Contains(logs.String(), "Pod Security") {
		t.Errorf("a privileged namespace logs no Pod Security error:\n%s", logs.String())
	}
}

// A namespace that enforces baseline or restricted is reported, never fatal: a
// failed first Sync would stop the whole manager.
func TestSync_Enabled_RestrictiveNamespaceIsLoggedNotFatal(t *testing.T) {
	for _, level := range []string{"baseline", "restricted"} {
		r := newReconciler(enabledCfg(), interceptor.Funcs{}, namespace(level))
		var logs strings.Builder
		if err := r.Sync(captureLogs(&logs)); err != nil {
			t.Fatalf("%s: Sync must not fail: %v", level, err)
		}
		if !strings.Contains(logs.String(), "rejects the node agent pods") || !strings.Contains(logs.String(), level) {
			t.Errorf("%s: want a Pod Security error in the log:\n%s", level, logs.String())
		}
		if err := r.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: Name}, &appsv1.DaemonSet{}); err != nil {
			t.Errorf("%s: the DaemonSet is still applied: %v", level, err)
		}
	}
}

func TestSync_Enabled_UnreadableNamespaceIsLogged(t *testing.T) {
	r := newReconciler(enabledCfg(), interceptor.Funcs{})
	var logs strings.Builder
	if err := r.Sync(captureLogs(&logs)); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !strings.Contains(logs.String(), "Could not read the namespace") {
		t.Errorf("want the read failure logged:\n%s", logs.String())
	}
}

func TestSync_Enabled_ApplyErrors(t *testing.T) {
	for kind, match := range map[string]func(runtime.ApplyConfiguration) bool{
		"serviceaccount": func(ac runtime.ApplyConfiguration) bool {
			_, ok := ac.(*corev1ac.ServiceAccountApplyConfiguration)
			return ok
		},
		"role": func(ac runtime.ApplyConfiguration) bool { _, ok := ac.(*rbacv1ac.RoleApplyConfiguration); return ok },
		"rolebinding": func(ac runtime.ApplyConfiguration) bool {
			_, ok := ac.(*rbacv1ac.RoleBindingApplyConfiguration)
			return ok
		},
		"daemonset": func(ac runtime.ApplyConfiguration) bool {
			_, ok := ac.(*appsv1ac.DaemonSetApplyConfiguration)
			return ok
		},
	} {
		r := newReconciler(enabledCfg(), interceptor.Funcs{
			Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
				if match(obj) {
					return fmt.Errorf("boom")
				}
				return c.Apply(ctx, obj, opts...)
			},
		}, namespace(""))
		if err := r.Sync(context.Background()); err == nil || err.Error() != kind+": boom" {
			t.Errorf("%s: want wrapped apply error, got %v", kind, err)
		}
	}
}

func TestSync_Disabled_NothingToDo(t *testing.T) {
	r := newReconciler(Config{Namespace: "test-ns"}, interceptor.Funcs{})
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

func TestSync_DisabledAfterEnabled_RemovesEverythingOwned(t *testing.T) {
	foreign := observation("hand-written", nil)
	r := newReconciler(enabledCfg(), interceptor.Funcs{}, namespace(""),
		observation("node-a", Labels()), observation("node-b", Labels()), foreign)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("enable: %v", err)
	}
	r.Config.Enabled = false
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("disable: %v", err)
	}
	ctx := context.Background()
	for kind, obj := range managedObjects() {
		if err := r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, obj); !apierrors.IsNotFound(err) {
			t.Errorf("%s should be gone, got %v", kind, err)
		}
	}
	list := &pactov1alpha1.NodeRuntimeObservationList{}
	if err := r.List(ctx, list, client.InNamespace("test-ns")); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != foreign.Name {
		t.Errorf("only the unlabelled observation survives, got %d items", len(list.Items))
	}
}

func TestCleanup_Errors(t *testing.T) {
	gr := schema.GroupResource{Group: "pacto.trianalab.io", Resource: "noderuntimeobservations"}
	for _, tc := range []struct {
		name    string
		funcs   interceptor.Funcs
		wantErr string
	}{
		{
			name: "prune get fails",
			funcs: interceptor.Funcs{Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return fmt.Errorf("boom")
			}},
			wantErr: "failed to get DaemonSet: boom",
		},
		{
			name: "list fails",
			funcs: interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return fmt.Errorf("boom")
			}},
			wantErr: "failed to list NodeRuntimeObservations: boom",
		},
		{
			name: "list forbidden is tolerated",
			funcs: interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return apierrors.NewForbidden(gr, "", fmt.Errorf("no"))
			}},
		},
		{
			name: "missing CRD is tolerated",
			funcs: interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: gr.Group, Kind: "NodeRuntimeObservation"}}
			}},
		},
		{
			name: "delete fails",
			funcs: interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return fmt.Errorf("boom")
			}},
			wantErr: "failed to delete NodeRuntimeObservation node-a: boom",
		},
		{
			name: "delete races a concurrent delete",
			funcs: interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return apierrors.NewNotFound(gr, "node-a")
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReconciler(Config{Namespace: "test-ns"}, tc.funcs, observation("node-a", Labels()))
			err := r.Sync(context.Background())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestStart_Disabled(t *testing.T) {
	r := newReconciler(Config{Namespace: "test-ns"}, interceptor.Funcs{})
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start with the node agent disabled should return nil: %v", err)
	}
}

// An enabled Sync deletes the agent observations of nodes that no longer exist
// and keeps the rest: a live node's, and an object the agent did not write.
func TestSync_Enabled_PrunesObservationsOfRemovedNodes(t *testing.T) {
	foreign := observation("gone-but-foreign", nil)
	r := newReconciler(enabledCfg(), interceptor.Funcs{}, namespace(""),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}},
		observation("node-a", Labels()), observation("node-gone", Labels()), foreign)
	var logs strings.Builder
	if err := r.Sync(captureLogs(&logs)); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	list := &pactov1alpha1.NodeRuntimeObservationList{}
	if err := r.List(context.Background(), list, client.InNamespace("test-ns")); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list.Items))
	for _, o := range list.Items {
		names = append(names, o.Name)
	}
	if strings.Join(names, ",") != "gone-but-foreign,node-a" {
		t.Errorf("survivors = %v, want the live node's and the foreign object", names)
	}
	if !strings.Contains(logs.String(), "removed node") {
		t.Errorf("the prune is logged:\n%s", logs.String())
	}
}

// The orphan prune is best effort: a list, node read or delete error is logged
// and Sync still succeeds.
func TestSync_Enabled_PruneErrorsAreLoggedNotFatal(t *testing.T) {
	boom := fmt.Errorf("boom")
	for name, funcs := range map[string]interceptor.Funcs{
		"list": {List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
			return boom
		}},
		"node get": {Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
			if _, ok := o.(*corev1.Node); ok {
				return boom
			}
			return c.Get(ctx, k, o, opts...)
		}},
		"delete": {Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
			return boom
		}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newReconciler(enabledCfg(), funcs, namespace(""), observation("node-gone", Labels()))
			var logs strings.Builder
			if err := r.Sync(captureLogs(&logs)); err != nil {
				t.Fatalf("Sync must not fail on a prune error: %v", err)
			}
			if !strings.Contains(logs.String(), "boom") {
				t.Errorf("the error is logged:\n%s", logs.String())
			}
		})
	}
}
