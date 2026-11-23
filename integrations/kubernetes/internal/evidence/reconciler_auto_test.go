/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/detect"
)

func autoCfg() Config {
	cfg := enabledCfg()
	cfg.Enabled, cfg.Auto, cfg.Subjects = false, true, nil
	return cfg
}

// autoReconciler is an auto reconciler whose gate reads the reconciler's own
// client and holds every prerequisite.
func autoReconciler(funcs interceptor.Funcs, objs ...client.Object) *Reconciler {
	objs = append(objs, trustSecret(keyBytes()))
	r := newReconcilerWithInterceptors(autoCfg(), funcs, objs...)
	g := newGate()
	g.Reader = r.APIReader
	r.Gate = g
	return r
}

func deployed(t *testing.T, r *Reconciler) bool {
	t.Helper()
	err := r.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: Name}, &appsv1.Deployment{})
	return err == nil
}

func TestReconcilerAutoAppliesWhenOn(t *testing.T) {
	r := autoReconciler(interceptor.Funcs{})
	if err := r.Sync(context.Background()); err != nil || !deployed(t, r) {
		t.Fatalf("err %v, deployed %v", err, deployed(t, r))
	}
}

func TestReconcilerAutoCleansUpWhenKnownOff(t *testing.T) {
	r := autoReconciler(interceptor.Funcs{}, managedDeployment("test-ns"), managedService("test-ns"))
	r.Gate.Scoped = false
	if err := r.Sync(context.Background()); err != nil || deployed(t, r) {
		t.Fatalf("err %v, deployed %v", err, deployed(t, r))
	}
}

func TestReconcilerAutoKeepsStateWhenUnknown(t *testing.T) {
	r := autoReconciler(interceptor.Funcs{}, managedDeployment("test-ns"), managedService("test-ns"))
	r.Gate.LastPush = func() *detect.PushResult { return nil }
	if err := r.Sync(context.Background()); err != nil || !deployed(t, r) {
		t.Fatalf("unknown deleted the server: err %v", err)
	}
	// Unknown never creates one either.
	r = autoReconciler(interceptor.Funcs{})
	r.Gate.LastPush = func() *detect.PushResult { return nil }
	if err := r.Sync(context.Background()); err != nil || deployed(t, r) {
		t.Fatalf("unknown created the server: err %v", err)
	}
}

func TestReconcilerAutoFirstSyncNeverErrors(t *testing.T) {
	r := autoReconciler(interceptor.Funcs{
		Apply: func(context.Context, client.WithWatch, runtime.ApplyConfiguration, ...client.ApplyOption) error {
			return errors.New("simulated apply error")
		}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("auto start failed: %v", err)
	}
}
