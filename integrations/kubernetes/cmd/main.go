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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
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
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/scope"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
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
	watchNamespace                                   string // deprecated --watch-namespace, merged into namespaces
	watchNamespacesRaw                               string
	namespaces                                       []string

	enableDashboard          bool
	dashboardOCISecrets      string
	dashboardResources       component.Resources
	dashboardObservation     []dashboard.ObservationSource
	dashboardAuth            dashboard.AuthConfig
	dashboardExtraScopes     string
	dashboardMiraURL         string
	dashboardStatsRegistry   string
	dashboardDeliveryNS      string
	dashboardDeliveryTrust   dashboard.KeyRef
	dashboardRepositories    []string
	dashboardExternalCatalog string

	enableEvidence            bool
	evidenceTrustSecret       string
	evidenceTrustConfigKey    string
	evidenceCredentialsSecret string
	evidenceSubjects          []string
	evidenceResources         component.Resources

	stabilizationWindow               time.Duration
	metricsObservation                observer.Mode
	probing                           observer.Mode
	enableInterfaceNameMatchDiscovery bool
	enableKernelEvidence              bool
	kernelEvidenceNamespace           string
	kernelEvidenceStaleness           time.Duration
	callerIgnoreNamespaces            []string

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
	fs.StringVar(&o.watchNamespacesRaw, "watch-namespaces", "",
		"Restrict the controller and the managed dashboard to these namespaces (comma-separated). "+
			"Empty (default) means cluster-wide.")
	fs.StringVar(&o.watchNamespace, "watch-namespace", "",
		"Deprecated: use --watch-namespaces. Merged into the --watch-namespaces list.")
	fs.StringVar(&o.dashboardOCISecrets, "dashboard-oci-secrets", "",
		"Optional: comma-separated list of Secret names in the operator namespace for OCI registry credentials. "+
			"Supports Opaque (registry + token, or registry + username + password) and kubernetes.io/dockerconfigjson "+
			"secrets; later Secrets override earlier ones for the same registry.")
	bindResourceFlags(fs, &o.dashboardResources, "dashboard", "dashboard")
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
		"OpenID Connect issuer people sign in to the dashboard with. Every view and action then runs with each "+
			"person's own token and RBAC; the delivery views need sign-in.")
	fs.StringVar(&o.dashboardAuth.InternalIssuerURL, "dashboard-oidc-internal-issuer-url", "",
		"Where the dashboard reaches the issuer when it differs from the public issuer URL browsers use, such as an "+
			"in-cluster Service; ID tokens must still name the public issuer.")
	fs.StringVar(&o.dashboardAuth.DisplayName, "dashboard-oidc-display-name", "",
		"The identity provider's name on the dashboard's sign-in button, as in Sign in with Keycloak (empty: OIDC).")
	fs.StringVar(&o.dashboardAuth.CAConfigMap, "dashboard-oidc-ca-configmap", "",
		"ConfigMap in the operator namespace holding the PEM bundle of the CAs that sign the issuer's certificate.")
	fs.StringVar(&o.dashboardAuth.CASecret, "dashboard-oidc-ca-secret", "",
		"Secret in the operator namespace holding that PEM bundle, instead of a ConfigMap.")
	fs.StringVar(&o.dashboardAuth.CAKey, "dashboard-oidc-ca-key", "ca.crt",
		"Key of the PEM bundle in that ConfigMap or Secret.")
	fs.StringVar(&o.dashboardDeliveryTrust.Name, "dashboard-delivery-trust-configmap", "",
		"ConfigMap in the operator namespace holding the trust config the dashboard verifies delivery records with, "+
			"so History reads signed records.")
	fs.StringVar(&o.dashboardDeliveryTrust.Key, "dashboard-delivery-trust-key", "trust.yaml",
		"Key of the trust config in that ConfigMap.")
	fs.Func("dashboard-repositories",
		"Comma-separated oci:// references the dashboard reads beside the contracts it discovers from Pacto "+
			"resources, such as golden paths, which are never deployed.",
		func(v string) error {
			o.dashboardRepositories = append(o.dashboardRepositories, strings.Split(v, ",")...)
			return nil
		})
	fs.StringVar(&o.dashboardExternalCatalog, "dashboard-external-catalog", "",
		"Organization external catalog the dashboard merges over Pacto's default, as oci://REPOSITORY@sha256:DIGEST.")
	fs.StringVar(&o.dashboardAuth.ClientID, "dashboard-oidc-client-id", "", "The dashboard's OpenID Connect client id.")
	fs.StringVar(&o.dashboardAuth.RedirectURL, "dashboard-oidc-redirect-url", "",
		"The dashboard's /auth/callback as browsers reach it; TLS must terminate in front of it.")
	fs.StringVar(&o.dashboardExtraScopes, "dashboard-oidc-extra-scopes", "",
		"Comma-separated scopes beyond openid, profile and groups.")
	fs.StringVar(&o.dashboardAuth.ClientSecretName, "dashboard-oidc-client-secret-name", "",
		"Secret in the operator namespace holding the client secret, passed to the dashboard by secretKeyRef.")
	fs.StringVar(&o.dashboardAuth.ClientSecretKey, "dashboard-oidc-client-secret-key", "client-secret",
		"Key of the client secret in that Secret.")
	fs.StringVar(&o.dashboardAuth.UsernameClaim, "dashboard-oidc-username-claim", "",
		"ID token claim the API server takes the username from; must match its claim mapping (empty: sub).")
	fs.StringVar(&o.dashboardAuth.UsernamePrefix, "dashboard-oidc-username-prefix", "",
		"Prefix the API server puts before the username; must match its claim mapping "+
			"(empty: the issuer URL and #; - for none).")
	fs.StringVar(&o.dashboardAuth.GroupsClaim, "dashboard-oidc-groups-claim", "",
		"ID token claim the API server takes the groups from; must match its claim mapping (empty: groups).")
	fs.StringVar(&o.dashboardAuth.GroupsPrefix, "dashboard-oidc-groups-prefix", "",
		"Prefix the API server puts before every group; must match its claim mapping.")
	fs.DurationVar(&o.dashboardAuth.SessionIdleTimeout, "dashboard-session-idle-timeout", 0,
		"A dashboard session without a request for this long ends (0: 1h).")
	fs.DurationVar(&o.dashboardAuth.SessionMaxAge, "dashboard-session-max-age", 0,
		"A dashboard session ends this long after sign-in, renewed or not (0: 12h).")
	fs.BoolVar(&o.dashboardAuth.TokenLogin, "dashboard-token-login", false,
		"Let people sign in to the dashboard with a Kubernetes token; a ServiceAccount token minted for the "+
			"pacto-dashboard audience reads the fleet only, and a person's own token acts as them.")
	fs.BoolVar(&o.dashboardAuth.AllowAnonymous, "dashboard-allow-anonymous", false,
		"Serve the dashboard without sign-in when neither an issuer nor token sign-in is set; "+
			"everyone who reaches it reads what its ServiceAccount reads.")
	fs.StringVar(&o.dashboardDeliveryNS, "dashboard-delivery-namespace", "",
		"Namespace Pacto delivery runs in, whose published identity policy the dashboard reads; "+
			"empty means pacto-delivery.")
	fs.StringVar(&o.dashboardMiraURL, "dashboard-mira-url", "",
		"Mira's address, to link the dashboard's health-check citations.")
	fs.StringVar(&o.dashboardStatsRegistry, "dashboard-stats-registry", "",
		"Stats repository the dashboard's Metrics page reads run records from, with the dashboard's OCI credentials; "+
			"empty: the page explains how to turn recording on.")
	fs.BoolVar(&o.enableEvidence, "enable-evidence-server", false,
		"Enable the managed Pacto Evidence Server deployment. Disabled by default.")
	fs.Func("evidence-subject",
		"Repeatable: an exact contract revision evidence is stored on, as oci://<repo>@sha256:<digest>. "+
			"The registry holding it is the durable evidence store: accepted records are published as OCI 1.1 "+
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
	fs.StringVar(&o.evidenceTrustConfigKey, "evidence-trust-config-key", "",
		"Optional: key of the trust Secret holding a structured trust config (pacto.dev/evidence-trust/v1, a .yaml key). "+
			"Set it to grant per-key allowlists such as ResourceProvisioned. Empty trusts every *.pub key in the Secret.")
	bindResourceFlags(fs, &o.evidenceResources, "evidence", "Evidence Server")
	fs.BoolVar(&o.showVersion, "version", false, "Print version information and exit.")
	fs.DurationVar(&o.stabilizationWindow, "stabilization-window", 2*time.Minute,
		"The stabilization window duration for compliance assertions before they trigger a false condition. "+
			"Assertions must remain unsatisfied for this entire window before being considered a failure.")
	fs.Var(&o.metricsObservation, "enable-metrics-observation",
		"Metrics observation (discovery and active probe), written `=mode`: on, off or auto. auto observes metrics only in "+
			"namespaces labelled pacto.trianalab.io/observe with metrics. The bare flag means on. Off by default; "+
			"when off, metrics read Unsupported.")
	fs.Var(&o.probing, "enable-probing",
		"Active in-cluster probing (health endpoints over HTTP and each present interface's wire protocol), "+
			"written `=mode`: "+
			"on, off or auto. auto probes only in namespaces labelled pacto.trianalab.io/observe with probing. "+
			"The bare flag means on. Off by default; when off, health uses passive readiness-probe and "+
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
	fs.Func("caller-coverage-ignore-namespace",
		"Namespace whose host-network pods are taken not to call workloads, so they leave a Service's caller "+
			"coverage complete (repeatable). kube-system and the other cluster namespaces and the node agent's "+
			"are always ignored.",
		func(v string) error {
			o.callerIgnoreNamespaces = append(o.callerIgnoreNamespaces, v)
			return nil
		})
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

// bindResourceFlags registers --<prefix>-{cpu,memory}-{request,limit} for one component's container.
func bindResourceFlags(fs *flag.FlagSet, rc *component.Resources, prefix, container string) {
	for _, f := range []struct {
		name, resource, kind string
		into                 *string
	}{
		{"cpu-request", "CPU", "request", &rc.CPURequest},
		{"cpu-limit", "CPU", "limit", &rc.CPULimit},
		{"memory-request", "Memory", "request", &rc.MemoryRequest},
		{"memory-limit", "Memory", "limit", &rc.MemoryLimit},
	} {
		help := fmt.Sprintf("%s %s for the %s container.", f.resource, f.kind, container)
		fs.StringVar(f.into, prefix+"-"+f.name, "", help+" Empty uses the built-in default.")
	}
}

// noStrayArgs refuses positional arguments. Boolean-style flags never take the next argument as their
// value, so `--enable-probing off` reads as on and parsing stops at "off", silently dropping every later
// flag.
func noStrayArgs(fs *flag.FlagSet) error {
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q: write a flag value after an equals sign, as in "+
			"--enable-probing=off", fs.Arg(0))
	}
	return nil
}

func main() {
	// Production logging by default: JSON, info level, no stack traces on warnings. --zap-devel turns on the
	// development encoder.
	o := options{zap: zap.Options{Development: false}}
	bindFlags(flag.CommandLine, &o)
	flag.Parse()
	if err := noStrayArgs(flag.CommandLine); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if o.showVersion {
		fmt.Printf("pacto-operator %s (commit: %s, built: %s)\n", version, gitCommit, buildDate)
		fmt.Printf("dashboard-image: %s\n", dashboardImage)
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&o.zap)))
	o.namespaces = scope.Namespaces(o.watchNamespacesRaw, o.watchNamespace)
	if o.watchNamespace != "" {
		setupLog.Info("--watch-namespace is deprecated; use --watch-namespaces")
	}

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
		MetricsObservation:                o.metricsObservation,
		Probing:                           o.probing,
		EnableInterfaceNameMatchDiscovery: o.enableInterfaceNameMatchDiscovery,
		KernelEvidence:                    o.enableKernelEvidence,
		KernelEvidenceNamespace:           o.kernelEvidenceNamespace,
		KernelEvidenceStaleness:           o.kernelEvidenceStaleness,
		CallerIgnoreNamespaces:            o.callerIgnoreNamespaces,
		Scoped:                            len(o.namespaces) > 0,
		ServiceEntries:                    servesServiceEntries(mgr),
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
// servesServiceEntries reports whether the cluster serves Istio's networking.istio.io/v1 ServiceEntry at
// startup. Without it the externals list reads no ServiceEntry and says so in its coverage.
// ponytail: an Istio installed later is read after the operator restarts.
func servesServiceEntries(mgr ctrl.Manager) bool {
	_, err := mgr.GetRESTMapper().RESTMapping(schema.GroupKind{Group: "networking.istio.io", Kind: "ServiceEntry"}, "v1")
	return err == nil
}

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

	// The Metrics options configure the metrics server.
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
		// can access the metrics endpoint. The chart grants the RBAC they need. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production: mount managed certificates (cert-manager, for one)
	// and point --metrics-cert-path at them.
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

	mgrOpts.Cache = scope.CacheOptions(o.namespaces, o.kernelEvidenceNamespace, o.enableKernelEvidence)
	if len(o.namespaces) > 0 {
		setupLog.Info("Watching namespaces", "namespaces", o.namespaces)
	}

	return ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
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
		WatchNamespaces:    o.namespaces,
		OCISecrets:         parsedOCISecrets,
		OwnerRef:           ownerRef,
		EvidenceSourceURL:  evidenceSourceURL,
		InsecureRegistries: insecureRegistries,
		Observation:        o.dashboardObservation,
		Resources:          o.dashboardResources,
		Auth:               o.dashboardAuth,
		DeliveryNamespace:  o.dashboardDeliveryNS,
		MiraURL:            o.dashboardMiraURL,
		StatsRegistry:      o.dashboardStatsRegistry,
		DeliveryTrust:      o.dashboardDeliveryTrust,
		Repositories:       o.dashboardRepositories,
		ExternalCatalog:    o.dashboardExternalCatalog,
	}
	if o.dashboardExtraScopes != "" {
		dashCfg.Auth.ExtraScopes = strings.Split(o.dashboardExtraScopes, ",")
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
		//nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder()
		Recorder: mgr.GetEventRecorderFor("pacto-dashboard"),
	}); err != nil {
		return fmt.Errorf("failed to add dashboard reconciler: %w", err)
	}

	// The cluster id names the cluster in Evidence Server reports; read once.
	evidenceCfg := evidence.Config{
		Cluster:            readClusterIdentity(mgr),
		Enabled:            o.enableEvidence,
		Image:              dashboardImage, // the runtime image runs `pacto evidence serve`
		Namespace:          namespace,
		Subjects:           o.evidenceSubjects,
		TrustSecret:        o.evidenceTrustSecret,
		TrustConfigKey:     o.evidenceTrustConfigKey,
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

// readClusterIdentity reads the cluster id (the kube-system namespace UID) and
// a display name. A failure is logged and leaves the identity empty: Evidence
// Server reports then carry no cluster.
func readClusterIdentity(mgr ctrl.Manager) kubecluster.Identity {
	dyn, err := dynamic.NewForConfig(mgr.GetConfig())
	if err == nil {
		var ident kubecluster.Identity
		if ident, err = kubecluster.Identify(context.Background(), dyn, ""); err == nil {
			return ident
		}
	}
	setupLog.Info("the cluster id could not be read; Evidence Server reports carry no cluster", "error", err)
	return kubecluster.Identity{}
}
