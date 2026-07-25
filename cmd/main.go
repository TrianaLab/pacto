/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/controller"
	"github.com/trianalab/pacto-operator/internal/dashboard"
	"github.com/trianalab/pacto-operator/internal/loader"
	// +kubebuilder:scaffold:imports
)

// Build metadata — injected via ldflags at build time.
var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"

	// dashboardImage is the full dashboard container image reference.
	// Set at build time via ldflags to couple the dashboard version to the
	// Pacto library dependency used by the controller.
	dashboardImage = ""
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(pactov1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

//nolint:gocyclo // Sequential setup code — inherent to Kubebuilder main().
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var enableDashboard bool
	var watchNamespace string
	var dashboardOCISecret string
	var dashboardOCISecrets string
	var dashboardCPURequest, dashboardCPULimit string
	var dashboardMemoryRequest, dashboardMemoryLimit string
	var showVersion bool
	var stabilizationWindow time.Duration
	var enableMetricsObservation bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or set to 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=true to enable HTTPS.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.BoolVar(&enableDashboard, "enable-dashboard", false,
		"Enable the managed Pacto dashboard deployment. Disabled by default.")
	flag.StringVar(&watchNamespace, "watch-namespace", "",
		"Restrict the controller to watch a single namespace. Empty (default) means cluster-wide. "+
			"The dashboard inherits this scope automatically.")
	flag.StringVar(&dashboardOCISecret, "dashboard-oci-secret", "",
		"Optional: name of a Secret in the operator namespace containing OCI registry credentials. "+
			"Supports Opaque (registry + token, or registry + username + password) and kubernetes.io/dockerconfigjson secrets. "+
			"Ignored when --dashboard-oci-secrets is set.")
	flag.StringVar(&dashboardOCISecrets, "dashboard-oci-secrets", "",
		"Optional: comma-separated list of Secret names in the operator namespace for OCI registry credentials. "+
			"Takes precedence over --dashboard-oci-secret.")
	flag.StringVar(&dashboardCPURequest, "dashboard-cpu-request", "",
		"CPU request for the dashboard container (e.g. 50m). Empty uses the built-in default.")
	flag.StringVar(&dashboardCPULimit, "dashboard-cpu-limit", "",
		"CPU limit for the dashboard container (e.g. 200m). Empty uses the built-in default.")
	flag.StringVar(&dashboardMemoryRequest, "dashboard-memory-request", "",
		"Memory request for the dashboard container (e.g. 128Mi). Empty uses the built-in default.")
	flag.StringVar(&dashboardMemoryLimit, "dashboard-memory-limit", "",
		"Memory limit for the dashboard container (e.g. 512Mi). Empty uses the built-in default.")
	flag.BoolVar(&showVersion, "version", false, "Print version information and exit.")
	flag.DurationVar(&stabilizationWindow, "stabilization-window", 2*time.Minute,
		"The stabilization window duration for compliance assertions before they trigger a false condition. "+
			"Assertions must remain unsatisfied for this entire window before being considered a failure.")
	flag.BoolVar(&enableMetricsObservation, "enable-metrics-observation", false,
		"Enable full metrics observation (discovery + active probe). When disabled, metrics dimension returns Unsupported.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if showVersion {
		fmt.Printf("pacto-operator %s (commit: %s, built: %s)\n", version, gitCommit, buildDate)
		fmt.Printf("dashboard-image: %s\n", dashboardImage)
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("Starting pacto-operator",
		"version", version,
		"commit", gitCommit,
		"built", buildDate,
		"dashboard-image", dashboardImage,
	)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgrOpts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "a4917283.pacto.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	}

	// Restrict the cache to a single namespace when --watch-namespace is set.
	if watchNamespace != "" {
		mgrOpts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				watchNamespace: {},
			},
		}
		setupLog.Info("Watching single namespace", "namespace", watchNamespace)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	if err := (&controller.PactoReconciler{
		Client:                   mgr.GetClient(),
		Scheme:                   mgr.GetScheme(),
		Recorder:                 mgr.GetEventRecorderFor("pacto-controller"), //nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder()
		Loader:                   loader.New(),
		StabilizationWindow:      stabilizationWindow,
		EnableMetricsObservation: enableMetricsObservation,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "Pacto")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// Dashboard feature toggle — always deployed to the operator's own namespace.
	dashboardNamespace := os.Getenv("POD_NAMESPACE")
	if dashboardNamespace == "" {
		setupLog.Error(nil, "POD_NAMESPACE environment variable must be set (use the downward API)")
		os.Exit(1)
	}

	if enableDashboard && dashboardImage == "" {
		setupLog.Error(nil, "Dashboard enabled but no dashboard image was set at build time. "+
			"This binary was not built with the required -ldflags for dashboard support.")
		os.Exit(1)
	}

	var parsedOCISecrets []string
	if dashboardOCISecrets != "" {
		for s := range strings.SplitSeq(dashboardOCISecrets, ",") {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				parsedOCISecrets = append(parsedOCISecrets, trimmed)
			}
		}
	}

	// Discover the operator's own Deployment for setting ownerReferences on dashboard resources.
	// This enables ArgoCD to display dashboard resources in the operator's resource tree.
	var ownerRef *metav1ac.OwnerReferenceApplyConfiguration
	if operatorDeployName := os.Getenv("OPERATOR_DEPLOYMENT_NAME"); operatorDeployName != "" {
		operatorDeploy := &appsv1.Deployment{}
		if err := mgr.GetAPIReader().Get(context.Background(), client.ObjectKey{
			Namespace: dashboardNamespace,
			Name:      operatorDeployName,
		}, operatorDeploy); err != nil {
			setupLog.Error(err, "Failed to look up operator Deployment for ownerReference",
				"deployment", operatorDeployName)
		} else {
			ownerRef = metav1ac.OwnerReference().
				WithAPIVersion("apps/v1").
				WithKind("Deployment").
				WithName(operatorDeploy.Name).
				WithUID(operatorDeploy.UID)
		}
	}

	dashCfg := dashboard.Config{
		Enabled:        enableDashboard,
		Image:          dashboardImage,
		Namespace:      dashboardNamespace,
		WatchNamespace: watchNamespace,
		OCISecret:      dashboardOCISecret,
		OCISecrets:     parsedOCISecrets,
		OwnerRef:       ownerRef,
		Resources: dashboard.ResourcesConfig{
			CPURequest:    dashboardCPURequest,
			CPULimit:      dashboardCPULimit,
			MemoryRequest: dashboardMemoryRequest,
			MemoryLimit:   dashboardMemoryLimit,
		},
	}
	if err := dashCfg.Validate(); err != nil {
		setupLog.Error(err, "Invalid dashboard configuration")
		os.Exit(1)
	}

	dashReconciler := &dashboard.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: dashCfg,
	}
	if err := mgr.Add(dashReconciler); err != nil {
		setupLog.Error(err, "Failed to add dashboard reconciler")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
