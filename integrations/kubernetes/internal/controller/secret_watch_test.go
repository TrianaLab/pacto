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

	"github.com/trianalab/pacto/integrations/kubernetes/v7/internal/loader"
)

// INV-5 says Secret VALUES never enter operator memory, and a name-scoped Secret grant
// cannot list or watch. The operator therefore watches no Secret at all: every Secret it
// reads goes through the uncached API reader, and the periodic requeue re-reads a rotated
// pull Secret within one check interval.
//
// The probe is a cache read with ReaderFailOnMissingInformer set, so a read can never
// lazily create the informer it is asking about: an informer either already exists
// (because a watch built it) or the read fails with ErrResourceNotCached. The other half
// of INV-5 -- that no component's Secret Get goes through a cached client -- is pinned in
// internal/dashboard/secret_read_test.go and TestSecretConfigurationReadsUncached.
var _ = Describe("TestNoSecretWatch", func() {
	It("starts no Secret informer, metadata or typed", func() {
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

		// The Service watch serving reads is the signal that every watch has started.
		key := client.ObjectKey{Namespace: ns, Name: "absent"}
		Eventually(func() error {
			return mgr.GetCache().Get(mgrCtx, key, &corev1.Service{})
		}, 30*time.Second, 100*time.Millisecond).Should(WithTransform(apierrors.IsNotFound, BeTrue()),
			"the Service watch never started")

		var notCached *cache.ErrResourceNotCached
		partial := &metav1.PartialObjectMetadata{}
		partial.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Secret"))
		Expect(errors.As(mgr.GetCache().Get(mgrCtx, key, partial), &notCached)).To(BeTrue(),
			"a Secret metadata informer is running: it needs list and watch on every Secret in scope")
		Expect(errors.As(mgr.GetCache().Get(mgrCtx, key, &corev1.Secret{}), &notCached)).To(BeTrue(),
			"a typed Secret informer is running, so every Secret's .Data is held in heap (INV-5)")
	})
})
