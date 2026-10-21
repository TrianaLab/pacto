/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package main

import (
	"cmp"
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
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
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

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/controller"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/dashboard"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/evidence"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/nodeagent"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
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

// options holds every command-line setting of the operator.
type options struct {
	metricsAddr                                      string
	metricsCertPath, metricsCertName, metricsCertKey string
	enableLeaderElection                             bool
	probeAddr                                        string
	secureMetrics                                    bool
	enableHTTP2                                      bool
	showVersion                                      bool
	watchNamespace                                   string

	enableDashboard      bool
	dashboardOCISecrets  string
	dashboardResources   component.Resources
	dashboardObservation []dashboard.ObservationSource
	dashboardAuth        dashboard.AuthConfig
	dashboardExtraScopes string
	dashboardAgentGroups string
	dashboardBreakGlass  string
	dashboardMiraURL     string

	enableEvidence            bool
	evidenceTrustSecret       string
	evidenceCredentialsSecret string
	evidenceSubjects          []string
	evidenceResources         component.Resources

	stabilizationWindow               time.Duration
	enableMetricsObservation          bool
	enableProbing                     bool
	enableInterfaceNameMatchDiscovery bool
	enableKernelEvidence              bool
	kernelEvidenceNamespace           string
	kernelEvidenceStaleness           time.Duration

	enableNodeAgent          bool
	nodeAgentNamespace       string
	nodeAgentImage           string
	nodeAgentPullSecrets     []string
	nodeAgentPublishInterval time.Duration
	nodeAgentResources       component.Resources

	zap zap.Options
}

// bindFlags registers every operator flag on fs.
func bindFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or set to 0 to disable the metrics service.")
	fs.StringVar(&o.probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&o.enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	fs.BoolVar(&o.secureMetrics, "metrics-secure", false,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=true to enable HTTPS.")
	fs.StringVar(&o.metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	fs.StringVar(&o.metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	fs.StringVar(&o.metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	fs.BoolVar(&o.enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics server")
	fs.BoolVar(&o.enableDashboard, "enable-dashboard", false,
		"Enable the managed Pacto dashboard deployment. Disabled by default.")
	fs.StringVar(&o.watchNamespace, "watch-namespace", "",
		"Restrict the controller to watch a single namespace. Empty (default) means cluster-wide. "+
			"The dashboard inherits this scope automatically.")
	fs.StringVar(&o.dashboardOCISecrets, "dashboard-oci-secrets", "",
		"Optional: comma-separated list of Secret names in the operator namespace for OCI registry credentials. "+
			"Supports Opaque (registry + token, or registry + username + password) and kubernetes.io/dockerconfigjson "+
			"secrets; later Secrets override earlier ones for the same registry.")
	bindResourceFlags(fs, &o.dashboardResources, "dashboard", "dashboard", "50m", "200m", "128Mi", "512Mi")
	fs.Func("dashboard-trace-source",
		"Repeatable: an offline OTLP/JSON trace file to mount read-only into the dashboard, as "+
			"name=NAME,file=RELATIVE_PATH,existingClaim=PVC (or configMap=NAME). NAME is the stable Data Source "+
			"identity. Configures offline input only; Pacto runs no OTLP receiver.",
		func(spec string) error {
			src, err := dashboard.ParseObservationSource(spec)
			if err != nil {
				return err
			}
			o.dashboardObservation = append(o.dashboardObservation, src)
			return nil
		})
	fs.StringVar(&o.dashboardAuth.IssuerURL, "dashboard-oidc-issuer-url", "",
		"OpenID Connect issuer people sign in to the dashboard with. Delivery actions then run with each person's own "+
			"token; without it the dashboard serves no delivery data and no actions.")
	fs.StringVar(&o.dashboardAuth.ClientID, "dashboard-oidc-client-id", "", "The dashboard's OpenID Connect client id.")
	fs.StringVar(&o.dashboardAuth.RedirectURL, "dashboard-oidc-redirect-url", "",
		"The dashboard's /auth/callback as browsers reach it; TLS must terminate in front of it.")
	fs.StringVar(&o.dashboardExtraScopes, "dashboard-oidc-extra-scopes", "",
		"Comma-separated scopes beyond openid, profile and groups.")
	fs.StringVar(&o.dashboardAuth.ClientSecretName, "dashboard-oidc-client-secret-name", "",
		"Secret in the operator namespace holding the client secret, passed to the dashboard by secretKeyRef.")
	fs.StringVar(&o.dashboardAuth.ClientSecretKey, "dashboard-oidc-client-secret-key", "client-secret",
		"Key of the client secret in that Secret.")
	fs.StringVar(&o.dashboardAuth.TeamGroupPrefix, "dashboard-team-group-prefix", "",
		"A contract owner team T is group T or this prefix plus T, as the delivery promoter is configured.")
	fs.StringVar(&o.dashboardAgentGroups, "dashboard-agent-groups", "",
		"Comma-separated groups whose members are agents, as the delivery promoter is configured; empty keeps pacto:agents.")
	fs.StringVar(&o.dashboardBreakGlass, "dashboard-break-glass-groups", "",
		"Comma-separated groups that may override, as the delivery promoter is configured; empty keeps pacto:break-glass.")
	fs.StringVar(&o.dashboardMiraURL, "dashboard-mira-url", "",
		"Mira's address, to link the dashboard's health-check citations.")
	fs.BoolVar(&o.enableEvidence, "enable-evidence-server", false,
		"Enable the managed Pacto Evidence Server deployment. Disabled by default.")
	fs.Func("evidence-subject",
		"Repeatable: an exact contract revision evidence is stored on, as oci://<repo>@sha256:<digest>. "+
			"The registry holding it IS the durable evidence store — accepted records are published as OCI 1.1 "+
			"referrers of that manifest. At least one is required when the Evidence Server is enabled.",
		func(subject string) error {
			o.evidenceSubjects = append(o.evidenceSubjects, subject)
			return nil
		})
	fs.StringVar(&o.evidenceCredentialsSecret, "evidence-credentials-secret", "",
		"Optional: name of an existing kubernetes.io/dockerconfigjson Secret with contract-registry credentials, "+
			"mounted read-only. Empty means anonymous or in-cluster registry access.")
	fs.StringVar(&o.evidenceTrustSecret, "evidence-trust-secret", "",
		"Name of a Secret of trusted producer public keys, mounted read-only. Required when the Evidence Server is enabled.")
	bindResourceFlags(fs, &o.evidenceResources, "evidence", "Evidence Server")
	fs.BoolVar(&o.showVersion, "version", false, "Print version information and exit.")
	fs.DurationVar(&o.stabilizationWindow, "stabilization-window", 2*time.Minute,
		"The stabilization window duration for compliance assertions before they trigger a false condition. "+
			"Assertions must remain unsatisfied for this entire window before being considered a failure.")
	fs.BoolVar(&o.enableMetricsObservation, "enable-metrics-observation", false,
		"Enable full metrics observation (discovery + active probe). When disabled, metrics dimension returns Unsupported.")
	fs.BoolVar(&o.enableProbing, "enable-probing", false,
		"Enable active in-cluster probing: health capability endpoints over HTTP (Tier A) and the wire protocol "+
			"of each present interface. Off by default; when off, health uses passive readiness-probe and "+
			"EndpointSlice signals only and interface protocols read unverified.")
	fs.BoolVar(&o.enableInterfaceNameMatchDiscovery, "interface-name-match-discovery", false,
		"Enable resolving an unbound interface's Service port by matching a Service port whose name equals "+
			"the interface name (positive availability assist only; never produces an absent or error result).")
	fs.BoolVar(&o.enableKernelEvidence, "enable-kernel-evidence", false,
		"Merge the node agent's kernel observations (NodeRuntimeObservation objects) into interface and "+
			"dependency evidence. Off by default; when off, evidence comes from the Kubernetes API only.")
	fs.StringVar(&o.kernelEvidenceNamespace, "kernel-evidence-namespace", "",
		"Namespace the node agent publishes NodeRuntimeObservation objects and heartbeat Leases to. "+
			"Empty uses --node-agent-namespace.")
	fs.DurationVar(&o.kernelEvidenceStaleness, "kernel-evidence-staleness", 0,
		"How old a node's heartbeat (its Lease renewal, or the observation's lastPublished without a Lease) may be "+
			"before its evidence reads stale. 0 uses three times the publish interval the node agent reports.")
	fs.BoolVar(&o.enableNodeAgent, "enable-node-agent", false,
		"Enable the managed node agent DaemonSet, which observes TCP socket state with eBPF on every node and "+
			"publishes it as NodeRuntimeObservation objects. Disabled by default.")
	fs.StringVar(&o.nodeAgentNamespace, "node-agent-namespace", nodeagent.DefaultNamespace,
		"Dedicated namespace the node agent runs and publishes in. The operator labels it privileged for Pod "+
			"Security, so it must not be the operator namespace.")
	fs.StringVar(&o.nodeAgentImage, "node-agent-image", "",
		"Image the node agent runs pacto agent from. Empty uses the dashboard image this binary was built "+
			"with, the runtime image the Evidence Server runs too.")
	fs.Func("node-agent-image-pull-secret",
		"Secret the node agent's ServiceAccount pulls its image with (repeatable).",
		func(v string) error {
			o.nodeAgentPullSecrets = append(o.nodeAgentPullSecrets, v)
			return nil
		})
	fs.DurationVar(&o.nodeAgentPublishInterval, "node-agent-publish-interval", 30*time.Second,
		"How often each node agent publishes its node's NodeRuntimeObservation.")
	bindResourceFlags(fs, &o.nodeAgentResources, "node-agent", "node agent")
	o.zap.BindFlags(fs)
}

// bindResourceFlags registers --<prefix>-{cpu,memory}-{request,limit} for one component's container. examples,
// when given, are the cpu-request, cpu-limit, memory-request and memory-limit values the help shows.
func bindResourceFlags(fs *flag.FlagSet, rc *component.Resources, prefix, container string, examples ...string) {
	for i, f := range []struct {
		name, resource, kind string
		into                 *string
	}{
		{"cpu-request", "CPU", "request", &rc.CPURequest},
		{"cpu-limit", "CPU", "limit", &rc.CPULimit},
		{"memory-request", "Memory", "request", &rc.MemoryRequest},
		{"memory-limit", "Memory", "limit", &rc.MemoryLimit},
	} {
		example := ""
		if i < len(examples) {
			example = " (e.g. " + examples[i] + ")"
		}
		help := fmt.Sprintf("%s %s for the %s container%s.", f.resource, f.kind, container, example)
		fs.StringVar(f.into, prefix+"-"+f.name, "", help+" Empty uses the built-in default.")
	}
}

func main() {
	// Production logging by default: JSON, info level, no stack traces on warnings. --zap-devel turns on the
	// development encoder.
	o := options{zap: zap.Options{Development: false}}
	bindFlags(flag.CommandLine, &o)
	flag.Parse()

	if o.showVersion {
		fmt.Printf("pacto-operator %s (commit: %s, built: %s)\n", version, gitCommit, buildDate)
		fmt.Printf("dashboard-image: %s\n", dashboardImage)
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&o.zap)))

	setupLog.Info("Starting pacto-operator",
		"version", version,
		"commit", gitCommit,
		"built", buildDate,
		"dashboard-image", dashboardImage,
	)

	// The dashboard and the Evidence Server are always deployed to the operator's own namespace; the node
	// agent to its own (--node-agent-namespace).
	operatorNamespace := os.Getenv("POD_NAMESPACE")
	if operatorNamespace == "" {
		setupLog.Error(nil, "POD_NAMESPACE environment variable must be set (use the downward API)")
		os.Exit(1)
	}
	// The kernel evidence namespace defaults to the managed node agent's.
	o.kernelEvidenceNamespace = cmp.Or(o.kernelEvidenceNamespace, o.nodeAgentNamespace)

	mgr, err := newManager(&o)
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	pactoReconciler := &controller.PactoReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		//nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder()
		Recorder:                          mgr.GetEventRecorderFor("pacto-controller"),
		Loader:                            loader.New(),
		StabilizationWindow:               o.stabilizationWindow,
		EnableMetricsObservation:          o.enableMetricsObservation,
		EnableProbing:                     o.enableProbing,
		EnableInterfaceNameMatchDiscovery: o.enableInterfaceNameMatchDiscovery,
		KernelEvidence:                    o.enableKernelEvidence,
		KernelEvidenceNamespace:           o.kernelEvidenceNamespace,
		KernelEvidenceStaleness:           o.kernelEvidenceStaleness,
	}
	if err := pactoReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "Pacto")
		os.Exit(1)
	}

	// Tag mirroring runs on its own ticker rather than inside Reconcile: it costs a
	// registry ListTags plus a Load per tag, and Reconcile fires on every write to
	// every watched workload.
	if err := mgr.Add(&controller.RevisionMirror{Reconciler: pactoReconciler}); err != nil {
		setupLog.Error(err, "Failed to add revision mirror")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := addComponents(mgr, &o, operatorNamespace); err != nil {
		setupLog.Error(err, "Failed to set up the managed components")
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

// newManager builds the controller manager: metrics server, leader election, health probes and the cache.
// No webhook server is configured: the operator registers no webhook, and controller-runtime only starts one
// when something asks the manager for it.
func newManager(o *options) (ctrl.Manager, error) {
	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	var tlsOpts []func(*tls.Config)
	if !o.enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			setupLog.Info("Disabling HTTP/2")
			c.NextProtos = []string{"http/1.1"}
		})
	}

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   o.metricsAddr,
		SecureServing: o.secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if o.secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production: with cert-manager, the [METRICS-WITH-CERTS] section of
	// config/default/kustomization.yaml and [PROMETHEUS-WITH-CERTS] of config/prometheus/kustomization.yaml
	// provide managed certificates.
	if len(o.metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", o.metricsCertPath, "metrics-cert-name", o.metricsCertName, "metrics-cert-key", o.metricsCertKey)

		metricsServerOptions.CertDir = o.metricsCertPath
		metricsServerOptions.CertName = o.metricsCertName
		metricsServerOptions.KeyName = o.metricsCertKey
	}

	mgrOpts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		HealthProbeBindAddress: o.probeAddr,
		LeaderElection:         o.enableLeaderElection,
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

	mgrOpts.Cache = cacheOptions(o)
	if o.watchNamespace != "" {
		setupLog.Info("Watching single namespace", "namespace", o.watchNamespace)
	}

	return ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
}

// cacheOptions scopes the manager's cache. Pods are cached only for the kernel merge, which reads a handful
// of their fields: the transform drops the rest (managedFields included) before they reach the cache. The
// node agent's observations and heartbeat Leases are read in the kernel evidence namespace only, whatever
// --watch-namespace says: a cluster-wide Lease informer would cache every controller's leader election.
// --watch-namespace restricts everything else to that one namespace.
func cacheOptions(o *options) cache.Options {
	opts := cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.Pod{}: {Transform: observer.TrimPod},
		},
	}
	// A ByObject entry makes the manager resolve the kind at startup, so registering the
	// node agent kinds only with kernel evidence on keeps an operator that has it off
	// starting even where the NodeRuntimeObservation CRD was never applied (helm upgrade
	// installs no new CRDs).
	if o.enableKernelEvidence {
		kernel := cache.ByObject{Namespaces: map[string]cache.Config{o.kernelEvidenceNamespace: {}}}
		opts.ByObject[&pactov1alpha1.NodeRuntimeObservation{}] = kernel
		opts.ByObject[&coordinationv1.Lease{}] = kernel
	}
	if o.watchNamespace != "" {
		opts.DefaultNamespaces = map[string]cache.Config{o.watchNamespace: {}}
	}
	return opts
}

// addComponents validates the dashboard, Evidence Server and node agent configurations and registers each
// sub-component with the manager. A disabled component is still registered: its first sync is its cleanup.
func addComponents(mgr ctrl.Manager, o *options, namespace string) error {
	if o.enableDashboard && dashboardImage == "" {
		return fmt.Errorf("dashboard enabled but no dashboard image was set at build time: " +
			"this binary was not built with the required -ldflags for dashboard support")
	}

	var parsedOCISecrets []string
	for s := range strings.SplitSeq(o.dashboardOCISecrets, ",") {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			parsedOCISecrets = append(parsedOCISecrets, trimmed)
		}
	}

	ownerRef := operatorOwnerRef(mgr, namespace)

	// When the Evidence Server is enabled, wire the dashboard to its internal
	// Service so the dashboard consumes the evidence Operational Graph
	// contribution read-only over HTTP (it never touches the evidence bucket).
	evidenceSourceURL := ""
	if o.enableEvidence {
		evidenceSourceURL = fmt.Sprintf("http://%s.%s.svc:%d", evidence.Name, namespace, evidence.EvidencePort)
	}

	// The controller and the workloads it manages resolve contract refs against
	// the same registries, so the plain-HTTP allowance is inherited from the
	// operator's own environment rather than configured three times.
	insecureRegistries := os.Getenv(loader.InsecureRegistriesEnvVar)

	dashCfg := dashboard.Config{
		Enabled:            o.enableDashboard,
		Image:              dashboardImage,
		Namespace:          namespace,
		WatchNamespace:     o.watchNamespace,
		OCISecrets:         parsedOCISecrets,
		OwnerRef:           ownerRef,
		EvidenceSourceURL:  evidenceSourceURL,
		InsecureRegistries: insecureRegistries,
		Observation:        o.dashboardObservation,
		Resources:          o.dashboardResources,
		Auth:               o.dashboardAuth,
		MiraURL:            o.dashboardMiraURL,
	}
	if o.dashboardExtraScopes != "" {
		dashCfg.Auth.ExtraScopes = strings.Split(o.dashboardExtraScopes, ",")
	}
	if o.dashboardAgentGroups != "" {
		dashCfg.Auth.AgentGroups = strings.Split(o.dashboardAgentGroups, ",")
	}
	if o.dashboardBreakGlass != "" {
		dashCfg.Auth.BreakGlassGroups = strings.Split(o.dashboardBreakGlass, ",")
	}
	if err := dashCfg.Validate(); err != nil {
		return fmt.Errorf("invalid dashboard configuration: %w", err)
	}
	if err := mgr.Add(&dashboard.Reconciler{
		Client: mgr.GetClient(),
		// Uncached reader for cleanup: disabling the dashboard must not start a
		// cluster-scoped informer whose list/watch RBAC is granted only when the
		// dashboard is enabled (that would fail cache sync and crashloop).
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Config:    dashCfg,
	}); err != nil {
		return fmt.Errorf("failed to add dashboard reconciler: %w", err)
	}

	evidenceCfg := evidence.Config{
		Enabled:            o.enableEvidence,
		Image:              dashboardImage, // the runtime image runs `pacto evidence serve`
		Namespace:          namespace,
		Subjects:           o.evidenceSubjects,
		TrustSecret:        o.evidenceTrustSecret,
		CredentialsSecret:  o.evidenceCredentialsSecret,
		InsecureRegistries: insecureRegistries,
		Resources:          o.evidenceResources,
		OwnerRef:           ownerRef,
	}
	if err := evidenceCfg.Validate(); err != nil {
		return fmt.Errorf("invalid evidence configuration: %w", err)
	}
	if err := mgr.Add(&evidence.Reconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Config:    evidenceCfg,
	}); err != nil {
		return fmt.Errorf("failed to add evidence reconciler: %w", err)
	}

	nodeAgentCfg := nodeagent.Config{
		Enabled:           o.enableNodeAgent,
		Image:             cmp.Or(o.nodeAgentImage, dashboardImage), // the runtime image runs `pacto agent`
		Namespace:         o.nodeAgentNamespace,
		OperatorNamespace: namespace,
		PublishInterval:   o.nodeAgentPublishInterval,
		Resources:         o.nodeAgentResources,
		ImagePullSecrets:  o.nodeAgentPullSecrets,
	}
	if err := nodeAgentCfg.Validate(); err != nil {
		return fmt.Errorf("invalid node agent configuration: %w", err)
	}
	// Observations nobody merges, or merges from elsewhere, are a silent
	// misconfiguration worth a loud line.
	if o.enableNodeAgent && !o.enableKernelEvidence {
		setupLog.Info("Node agent enabled without --enable-kernel-evidence: its observations are published " +
			"but never merged into compliance evidence")
	}
	if o.enableNodeAgent && o.kernelEvidenceNamespace != o.nodeAgentNamespace {
		setupLog.Info("Kernel evidence is read from a namespace the managed node agent does not publish to",
			"kernelEvidenceNamespace", o.kernelEvidenceNamespace, "nodeAgentNamespace", o.nodeAgentNamespace)
	}
	if err := mgr.Add(&nodeagent.Reconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Config:    nodeAgentCfg,
	}); err != nil {
		return fmt.Errorf("failed to add node agent reconciler: %w", err)
	}
	return nil
}

// operatorOwnerRef discovers the operator's own Deployment for the ownerReferences of the managed
// components' resources, so ArgoCD displays them in the operator's resource tree. nil when the Deployment
// is not named (OPERATOR_DEPLOYMENT_NAME) or cannot be read.
func operatorOwnerRef(mgr ctrl.Manager, namespace string) *metav1ac.OwnerReferenceApplyConfiguration {
	name := os.Getenv("OPERATOR_DEPLOYMENT_NAME")
	if name == "" {
		return nil
	}
	operatorDeploy := &appsv1.Deployment{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := mgr.GetAPIReader().Get(context.Background(), key, operatorDeploy); err != nil {
		setupLog.Error(err, "Failed to look up operator Deployment for ownerReference", "deployment", name)
		return nil
	}
	return metav1ac.OwnerReference().
		WithAPIVersion("apps/v1").
		WithKind("Deployment").
		WithName(operatorDeploy.Name).
		WithUID(operatorDeploy.UID)
}
