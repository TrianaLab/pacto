/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
)

// INV-5 says Secret VALUES never enter operator memory. A typed Secret watch defeats it
// before any observer code runs: controller-runtime keeps a full corev1.Secret (.Data
// included) in the informer store for every Secret in scope.
//
// The probe is a cache read with ReaderFailOnMissingInformer set, so a read can never
// lazily create the informer it is asking about: an informer either already exists
// (because the watch built it) or the read fails with ErrResourceNotCached.
//
// That same property is why this spec covers WATCHES only and cannot be widened to
// cover a typed cached READ: the setting suppresses the lazy informer creation such a
// read leaks through, so the read would fail here rather than leak and the probe would
// stay green either way. The other half of INV-5 -- that no component's Secret Get goes
// through a cached client -- is pinned in internal/dashboard/secret_read_test.go.
var _ = Describe("INV-5: the pull-secret watch", func() {
	It("caches Secret metadata but never Secret values", func() {
		// An empty namespace, so this second manager reconciles nothing the other specs own.
		const ns = "kube-node-lease"

		mgrCtx, cancelMgr := context.WithCancel(ctx)
		DeferCleanup(cancelMgr)

		skipNameValidation := true // the suite manager already registered a "pacto" controller
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:     scheme.Scheme,
			Metrics:    metricsserver.Options{BindAddress: "0"},
			Controller: config.Controller{SkipNameValidation: &skipNameValidation},
			Cache: cache.Options{
				ReaderFailOnMissingInformer: true,
				DefaultNamespaces:           map[string]cache.Config{ns: {}},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect((&PactoReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor("inv5"), //nolint:staticcheck // matches suite_test
			Loader:   loader.New(),
		}).SetupWithManager(mgr)).To(Succeed())

		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()

		key := client.ObjectKey{Namespace: ns, Name: "absent"}

		// A NotFound (rather than ErrResourceNotCached) means the metadata informer is
		// serving reads, which is also the signal that the Secret watch has started.
		Eventually(func() error {
			partial := &metav1.PartialObjectMetadata{}
			partial.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
			return mgr.GetCache().Get(mgrCtx, key, partial)
		}, 30*time.Second, 100*time.Millisecond).Should(WithTransform(apierrors.IsNotFound, BeTrue()),
			"the Secret watch never started a metadata informer")

		// That same watch is the only thing that could have started a TYPED one.
		var notCached *cache.ErrResourceNotCached
		Expect(errors.As(mgr.GetCache().Get(mgrCtx, key, &corev1.Secret{}), &notCached)).To(BeTrue(),
			"a typed Secret informer is running, so every Secret's .Data is held in heap (INV-5)")
	})
})
