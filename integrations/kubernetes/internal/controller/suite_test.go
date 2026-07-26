/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/loader"
	// +kubebuilder:scaffold:imports
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = pactov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	// KUBEBUILDER_ASSETS (set by `make test` to the version matching the build)
	// wins. Only fall back to scanning bin/k8s when it is unset, and pick the
	// newest version present so stale older downloads aren't selected ahead of the
	// current one (a 1.31 control plane against 1.36 CRDs fails informer sync).
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if dir := getLatestEnvTestBinaryDir(); dir != "" {
			testEnv.BinaryAssetsDirectory = dir
		}
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Start the controller manager in the background so reconciliation runs.
	// Disable the metrics listener: tests don't scrape it, and binding the
	// default :8080 collides with anything already on that port (a dev dashboard,
	// another suite), which fails mgr.Start and stalls every spec on cache sync.
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&PactoReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("pacto-controller"), //nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder()
		Loader:   loader.New(),
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err := mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Eventually(func() error {
		return testEnv.Stop()
	}, time.Minute, time.Second).Should(Succeed())
})

// getLatestEnvTestBinaryDir scans bin/k8s and returns the newest version
// directory that holds a kube-apiserver binary. Multiple versions accumulate
// there over time; picking the newest keeps the control plane in step with the
// CRDs and controller-runtime client the build is compiled against.
func getLatestEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(basePath, entry.Name(), "kube-apiserver")); err == nil {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	// Directory names are "<major>.<minor>.<patch>-<os>-<arch>"; lexical sort
	// orders them by version for the current naming scheme.
	sort.Strings(names)
	return filepath.Join(basePath, names[len(names)-1])
}
