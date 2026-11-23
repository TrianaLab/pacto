/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package main

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/controller"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/credentials"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/dashboard"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/delivery"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/detection"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/evidence"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/inferredpublish"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/installkey"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/linker"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/member"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/nodeagent"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/offeringpublish"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/scope"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/targetsnapshot"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
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
	dashboardBackstageURL    string
	dashboardBackstageToken  dashboard.KeyRef
	dashboardStatsRegistry   string
	dashboardIndex           string
	dashboardDeliveryNS      string
	dashboardDeliveryTrust   dashboard.KeyRef
	dashboardRepositories    []string
	dashboardExternalCatalog string
	dashboardFingerprints    string
	dashboardOwnerPolicy     string
	dashboardDeclare         dashboard.Config // the Sigstore, TrustedRootConfigMap, Registry, DeclareRegistrySecret

	enableEvidence            bool
	evidenceAuto              bool
	idpRecorder               string
	evidenceTrustSecret       string
	evidenceTrustConfigKey    string
	evidenceCredentialsSecret string
	evidenceSubjects          []string
	evidenceSubjectPrefixes   []string
	evidenceResources         component.Resources

	stabilizationWindow               time.Duration
	metricsObservation                observer.Mode
	probing                           observer.Mode
	enableInterfaceNameMatchDiscovery bool
	enableKernelEvidence              bool
	kernelEvidenceNamespace           string
	kernelEvidenceStaleness           time.Duration
	egressGatewayLogs                 string
	callerIgnoreNamespaces            []string

	enableNodeAgent          bool
	nodeAgentNamespace       string
	nodeAgentImage           string
	nodeAgentPullSecrets     []string
	nodeAgentPublishInterval time.Duration
	nodeAgentResources       component.Resources

	registry             string
	profile              string
	hubURL               string
	hubCAFile            string
	crossplaneOfferings  bool
	registryPullSecret   string
	registryPushPrefixes []string

	ownerPolicy        string
	trustedRoot        string
	publishersRegistry string
	publishersBranch   string
	publishersPolicy   string
	policySigners      string

	zap zap.Options
}

// bindDashboardDeclareFlags registers how the dashboard signs and publishes
// declarations.
func bindDashboardDeclareFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.dashboardAuth.SignClientID, "dashboard-sign-client-id", "",
		"A second OpenID Connect client at the dashboard's issuer whose audience Fulcio trusts and the API server "+
			"does not: a signed-in person logs in again with it to sign a declaration. Empty: the dashboard saves drafts.")
	fs.StringVar(&o.dashboardAuth.SignClientSecretName, "dashboard-sign-client-secret-name", "",
		"Secret in the operator namespace holding the sign client's secret, passed to the dashboard by secretKeyRef.")
	fs.StringVar(&o.dashboardAuth.SignClientSecretKey, "dashboard-sign-client-secret-key", "client-secret",
		"Key of the sign client's secret in that Secret.")
	fs.StringVar(&o.dashboardAuth.SignRedirectURL, "dashboard-sign-redirect-url", "",
		"The dashboard's signing callback as browsers reach it.")
	fs.StringVar(&o.dashboardDeclare.FulcioURL, "dashboard-fulcio-url", "",
		"Fulcio the dashboard gets a person's signing certificate from (empty: public-good Sigstore).")
	fs.StringVar(&o.dashboardDeclare.RekorURL, "dashboard-rekor-url", "",
		"Rekor the dashboard records signatures in (empty: public-good Sigstore).")
	fs.StringVar(&o.dashboardDeclare.TimestampURL, "dashboard-timestamp-url", "",
		"Timestamp authority the dashboard's signatures use (empty: public-good Sigstore).")
	fs.StringVar(&o.dashboardDeclare.TrustedRootConfigMap, "dashboard-trusted-root-configmap", "",
		"Immutable ConfigMap in the operator namespace whose trusted_root.json the dashboard verifies the declarations "+
			"it signs with (a private Sigstore or an air-gapped install). Empty: public-good Sigstore.")
	fs.StringVar(&o.dashboardDeclare.Registry, "dashboard-registry", "",
		"The installation registry (<host>/<path>): the dashboard lists the golden paths and offerings published "+
			"under it, and publishes declarations there; empty: the dashboard offers no Declare.")
	fs.StringVar(&o.dashboardDeclare.DeclareRegistrySecret, "dashboard-declare-registry-secret", "",
		"An existing kubernetes.io/dockerconfigjson Secret in the operator namespace with push access to "+
			"--dashboard-registry, mounted for the dashboard's Declare writer only. Empty: the dashboard plans only.")
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
			"identity. Configures offline input; the dashboard also receives OTLP/HTTP JSON in the cluster.",
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
		"Deprecated: set --dashboard-registry (the chart's registry value); "+
			"the dashboard lists every golden path and offering published under it. "+
			"Comma-separated oci:// references the dashboard reads beside the contracts it discovers from Pacto "+
			"resources, such as golden paths, which are never deployed.",
		func(v string) error {
			o.dashboardRepositories = append(o.dashboardRepositories, strings.Split(v, ",")...)
			return nil
		})
	fs.StringVar(&o.dashboardExternalCatalog, "dashboard-external-catalog", "",
		"Organization external catalog the dashboard merges over Pacto's default, as oci://REPOSITORY@sha256:DIGEST.")
	fs.StringVar(&o.dashboardFingerprints, "dashboard-fingerprints-configmap", "",
		"ConfigMap in the operator namespace holding the workload manager and promoter fingerprints (fingerprints.yaml) "+
			"the dashboard reads as --fingerprints.")
	fs.StringVar(&o.dashboardAuth.ClientID, "dashboard-oidc-client-id", "", "The dashboard's OpenID Connect client id.")
	fs.StringVar(&o.dashboardAuth.RedirectURL, "dashboard-oidc-redirect-url", "",
		"The dashboard's /auth/callback as browsers reach it; TLS must terminate in front of it.")
	bindDashboardDeclareFlags(fs, o)
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
	fs.BoolVar(&o.dashboardAuth.MCP, "dashboard-mcp", false,
		"Serve MCP to agents at /mcp on the managed dashboard, with its sign-in.")
	fs.StringVar(&o.dashboardAuth.MCPURL, "dashboard-mcp-url", "",
		"The dashboard's /mcp URL as agents reach it (empty: the address the agent used).")
	fs.StringVar(&o.dashboardAuth.MCPExchangeAudience, "dashboard-mcp-exchange-audience", "",
		"With an issuer, the audience an agent's issuer token is exchanged for, one the API server accepts; "+
			"needs --dashboard-mcp-url.")
	fs.StringVar(&o.dashboardDeliveryNS, "dashboard-delivery-namespace", "",
		"Namespace Pacto delivery runs in, whose published identity policy the dashboard reads; "+
			"empty means pacto-delivery.")
	fs.StringVar(&o.dashboardMiraURL, "dashboard-mira-url", "",
		"Mira's address, to link the dashboard's health-check citations.")
	fs.StringVar(&o.dashboardBackstageURL, "dashboard-backstage-url", "",
		"Base URL of a Backstage catalog whose owners, systems and dependencies the dashboard shows as inferred values.")
	fs.StringVar(&o.dashboardBackstageToken.Name, "dashboard-backstage-token-secret", "",
		"Secret in the operator namespace holding the catalog reader token, "+
			"which the dashboard sends only to --dashboard-backstage-url; the operator never reads it.")
	fs.StringVar(&o.dashboardBackstageToken.Key, "dashboard-backstage-token-key", "token",
		"Key of the reader token in that Secret.")
	fs.StringVar(&o.dashboardStatsRegistry, "dashboard-stats-registry", "",
		"Stats repository the dashboard's Metrics page reads run records from, with the dashboard's OCI credentials; "+
			"empty: the page explains how to turn recording on.")
	o.dashboardIndex = dashboardIndexAuto
	fs.Func("dashboard-index",
		"Organization index the dashboard reads every published service from, written `=mode`: auto reads "+
			"<registry>/pacto-index when --registry is set, none reads none. auto by default.",
		func(v string) error {
			if v != dashboardIndexAuto && v != dashboardIndexNone {
				return fmt.Errorf("want auto or none, got %q", v)
			}
			o.dashboardIndex = v
			return nil
		})
	fs.BoolVar(&o.enableEvidence, "enable-evidence-server", false,
		"Enable the managed Pacto Evidence Server deployment. Disabled by default.")
	fs.BoolVar(&o.evidenceAuto, "evidence-server-auto", false,
		"Profile idp: run the managed Evidence Server only while the registry serves native OCI Referrers, "+
			"a subject or subject prefix is set and the trust Secret holds a public key. "+
			"Ignored with --enable-evidence-server.")
	fs.StringVar(&o.idpRecorder, "idp-recorder", "",
		"Profile idp: what the chart decided about the delivery recorder (on, off, no-signer, no-streams, "+
			"signer-incomplete or delivery-off); advertised in the detected ConfigMap, never acted on.")
	fs.Func("evidence-subject",
		"Repeatable: an exact contract revision evidence is stored on, as oci://<repo>@sha256:<digest>. "+
			"The registry holding it is the durable evidence store: accepted records are published as OCI 1.1 "+
			"referrers of that manifest where the registry serves them, as write-once tags in <prefix>pacto-evidence "+
			"under a covering subject prefix otherwise. A subject or a prefix is required when the Evidence Server is enabled.",
		func(subject string) error {
			o.evidenceSubjects = append(o.evidenceSubjects, subject)
			return nil
		})
	fs.Func("evidence-subject-prefix",
		"Repeatable: a registry prefix, oci://<registry>/<path>/, whose immutable revisions evidence may be stored on. "+
			"The Evidence Server records each subject in <prefix>pacto-evidence and accepts new revisions with no restart. "+
			"A subject or a prefix is required when the Evidence Server is enabled.",
		func(prefix string) error {
			o.evidenceSubjectPrefixes = append(o.evidenceSubjectPrefixes, prefix)
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
	o.egressGatewayLogs = detect.GatewayLogsAuto
	fs.Func("egress-gateway-logs",
		"Istio egress gateway access logs, written `=mode`: auto, on or off. on reads the access log tail of the "+
			"pods labelled istio=egressgateway in istio-system to name the calls that left through them (the chart "+
			"then grants pods/log get there); auto reads nothing and proposes on in the detected providers; off "+
			"reads nothing and proposes nothing. auto by default.",
		func(v string) error {
			switch v {
			case detect.GatewayLogsAuto, detect.GatewayLogsOn, detect.GatewayLogsOff:
				o.egressGatewayLogs = v
				return nil
			}
			return fmt.Errorf("want auto, on or off, got %q", v)
		})
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
	fs.StringVar(&o.profile, "profile", "default",
		"Installation profile the chart rendered (default, hardened, idp or hardened,idp); "+
			"advertised in the detected ConfigMap, never acted on.")
	fs.StringVar(&o.registry, "registry", "",
		"The installation registry (host/path): Pacto publishes the target snapshot and inferred contracts under it "+
			"and pushes nowhere else; empty turns those features off.")
	fs.StringVar(&o.hubURL, "hub-url", "",
		"The hub's https:// or wss:// URL. Setting it makes this joined cluster a member: the elected leader dials "+
			"the hub, proves the installation key and answers the hub's per-person reviews with this cluster's own "+
			"SubjectAccessReview. Nothing listens for the hub; needs --registry.")
	fs.BoolVar(&o.crossplaneOfferings, "crossplane-offerings", true,
		"Push each Crossplane XRD as an inferred offering to <registry>/pacto-inferred/crossplane-<clusterID>/services. "+
			"Needs --registry and a cluster-wide operator; the operator only reads XRDs and Compositions.")
	fs.StringVar(&o.hubCAFile, "hub-ca-file", "",
		"File holding the PEM CAs the hub's certificate may chain to, beside the system pool.")
	fs.StringVar(&o.registryPullSecret, "registry-pull-secret", "",
		"namespace/name of the Secret that authenticates to the installation registry; used only under the push prefixes.")
	fs.Func("registry-push-prefix",
		"Repeatable: a repository prefix a Pacto component may push under; the chart renders every one.",
		func(v string) error {
			o.registryPushPrefixes = append(o.registryPushPrefixes, v)
			return nil
		})
	fs.StringVar(&o.ownerPolicy, "owner-policy", "",
		"File holding the owner policy (policy.json from the chart's declare block): who may set a service owner. "+
			"Published in the target snapshot; empty publishes none.")
	fs.StringVar(&o.dashboardOwnerPolicy, "dashboard-owner-policy-configmap", "",
		"Immutable ConfigMap in the operator namespace holding the owner policy (policy.json) the dashboard mounts.")
	fs.StringVar(&o.trustedRoot, "trusted-root", "",
		"File holding a Sigstore trusted root (trusted_root.json) that the signatures of an owner change must chain to. "+
			"Empty fetches public-good Sigstore's root through TUF, caching nothing on disk.")
	fs.StringVar(&o.publishersRegistry, "publishers-registry", "",
		"The registry (host/org) the installation's services publish to: its derived publisher rule (the organization's "+
			"CI on the default branch) is the only CI identity whose signed CODEOWNERS or catalog-info.yaml owner counts "+
			"as attested when an owner change is judged. Empty: no owner is attested.")
	fs.StringVar(&o.publishersBranch, "publishers-default-branch", "main",
		"The default branch whose CI the derived publisher rule accepts.")
	fs.StringVar(&o.publishersPolicy, "publishers-policy", "",
		"The signed delivery policy's tag reference (delivery.publishers.policy): once verified, its publisher rules "+
			"replace the derived rule and its signer class rules classify who signed an owner change, as the fleet does.")
	fs.StringVar(&o.policySigners, "publishers-policy-signers", "",
		"A trust config (YAML) whose role: policy keys sign the delivery policy. Required with --publishers-policy.")
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

	// The installation identity (registryPullSecret and the cloud sources) answers the tenant-facing
	// loader only under the inferred contracts, so a tenant's contract ref never rides on it and never
	// lists the snapshot, index or delivery repositories with it.
	prefixes := credentials.InstallationPrefixes(o.registry, o.registryPushPrefixes)
	tenantPrefixes := credentials.TenantPrefixes(o.registry)
	keychain, err := credentials.OperatorKeychain(mgr.GetAPIReader(), o.registryPullSecret, tenantPrefixes, false)
	if err != nil {
		setupLog.Error(err, "Invalid registry settings")
		os.Exit(1)
	}
	// The snapshot publisher's own instance also answers host-only resources: its one repository is
	// opened behind the push scope.
	// The settings were checked just above.
	publishKeychain, _ := credentials.OperatorKeychain(mgr.GetAPIReader(), o.registryPullSecret, prefixes, true)

	contractLoader := loader.New(loader.WithKeychain(keychain))
	owners, err := ownerCheck(&o, contractLoader)
	if err != nil {
		setupLog.Error(err, "Invalid owner check settings")
		os.Exit(1)
	}

	pactoReconciler := &controller.PactoReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		//nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder()
		Recorder:                          mgr.GetEventRecorderFor("pacto-controller"),
		Loader:                            contractLoader,
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
		CiliumPolicies:                    servesCiliumPolicies(mgr),
		GatewayLogs:                       gatewayLogs(o.egressGatewayLogs, mgr.GetConfig()),
		Owners:                            owners,
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

	if err := addComponents(mgr, &o, operatorNamespace, publishKeychain, keychain, pactoReconciler); err != nil {
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

// servesCiliumPolicies reports whether the cluster serves Cilium's cilium.io/v2 CiliumNetworkPolicy at
// startup. Without it no toFQDNs rule can exist, so the externals list reads none and loses no coverage.
// ponytail: a Cilium installed later is read after the operator restarts.
func servesCiliumPolicies(mgr ctrl.Manager) bool {
	_, err := mgr.GetRESTMapper().RESTMapping(schema.GroupKind{Group: "cilium.io", Kind: "CiliumNetworkPolicy"}, "v2")
	return err == nil
}

// gatewayLogs is the Istio egress gateway log reader when a person set the mode on, nil otherwise: auto and
// off read no log.
func gatewayLogs(mode string, cfg *rest.Config) *observer.GatewayLogs {
	if mode != detect.GatewayLogsOn {
		return nil
	}
	c, err := corev1client.NewForConfig(cfg)
	if err != nil {
		setupLog.Error(err, "Failed to build the egress gateway log reader")
		os.Exit(1)
	}
	return observer.NewGatewayLogs(c)
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
// kc is the target snapshot publisher's keychain, loaderKC the contract loader's; rec judges linked workloads.
func addComponents(mgr ctrl.Manager, o *options, namespace string, kc, loaderKC authn.Keychain,
	rec *controller.PactoReconciler) error {
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

	recorder, err := detection.ParseRecorderState(o.idpRecorder)
	if err != nil {
		return err
	}

	// The controller and the workloads it manages resolve contract refs against
	// the same registries, so the plain-HTTP allowance is inherited from the
	// operator's own environment rather than configured three times.
	insecureRegistries := os.Getenv(loader.InsecureRegistriesEnvVar)

	// The cluster id names the cluster in Evidence Server reports and in what
	// detection writes; read once.
	ident := readClusterIdentity(mgr)
	// While the Evidence Server runs, the dashboard and the target snapshot read
	// its internal Service, so the dashboard consumes the evidence Operational
	// Graph contribution read-only over HTTP (it never touches the evidence bucket).
	gate, evidenceSourceURL, err := evidenceGate(mgr, o, namespace, ident, kc)
	if err != nil {
		return err
	}
	inst, offerings, err := addPublishers(mgr, o, namespace, ident, evidenceSourceURL, kc, loaderKC, rec)
	if err != nil {
		return err
	}

	dashCfg := dashboard.Config{
		Enabled:               o.enableDashboard,
		Image:                 dashboardImage,
		Namespace:             namespace,
		WatchNamespaces:       o.namespaces,
		OCISecrets:            parsedOCISecrets,
		OwnerRef:              ownerRef,
		EvidenceSourceURL:     evidenceSourceURL,
		InsecureRegistries:    insecureRegistries,
		Observation:           o.dashboardObservation,
		Resources:             o.dashboardResources,
		Auth:                  o.dashboardAuth,
		DeliveryNamespace:     o.dashboardDeliveryNS,
		MiraURL:               o.dashboardMiraURL,
		BackstageURL:          o.dashboardBackstageURL,
		BackstageToken:        o.dashboardBackstageToken,
		StatsRegistry:         o.dashboardStatsRegistry,
		Index:                 dashboardIndex(o.dashboardIndex, inst),
		DeliveryTrust:         o.dashboardDeliveryTrust,
		Repositories:          o.dashboardRepositories,
		ExternalCatalog:       o.dashboardExternalCatalog,
		FingerprintsConfigMap: o.dashboardFingerprints,
		OwnerPolicyConfigMap:  o.dashboardOwnerPolicy,
		FulcioURL:             o.dashboardDeclare.FulcioURL,
		RekorURL:              o.dashboardDeclare.RekorURL,
		TimestampURL:          o.dashboardDeclare.TimestampURL,
		TrustedRootConfigMap:  o.dashboardDeclare.TrustedRootConfigMap,
		Registry:              o.dashboardDeclare.Registry,
		DeclareRegistrySecret: o.dashboardDeclare.DeclareRegistrySecret,
	}
	// Detection proposes what the cluster provides in one ConfigMap; it never
	// changes an argument, a rule or a feature. The managed dashboard mounts it
	// as a file.
	if d := detection.ForOperator(os.Getenv("OPERATOR_DEPLOYMENT_NAME"), namespace, o.namespaces,
		o.nodeAgentNamespace, ident, mgr.GetAPIReader(), mgr.GetClient(), detection.ServedFor(mgr.GetConfig()),
		inst, lastPush.Load, detection.Readers{GatewayLogs: o.egressGatewayLogs, KernelEvidence: o.enableKernelEvidence,
			ServiceEntries: servesServiceEntries(mgr), CiliumPolicies: servesCiliumPolicies(mgr)}); d != nil {
		d.Wake, d.Offerings = pushed, offerings
		d.Pieces = idpPieces(inst, gate, o.enableEvidence, recorder)
		if err := mgr.Add(d); err != nil {
			return fmt.Errorf("failed to add detection: %w", err)
		}
		dashCfg.DetectedConfigMap = d.Name
	} else {
		setupLog.Info("Detection is off: OPERATOR_DEPLOYMENT_NAME is not set, so nothing would own its ConfigMap")
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

	evidenceCfg := evidence.Config{
		Cluster:            ident,
		Enabled:            o.enableEvidence,
		Auto:               gate != nil,
		Image:              dashboardImage, // the runtime image runs `pacto evidence serve`
		Namespace:          namespace,
		Subjects:           o.evidenceSubjects,
		SubjectPrefixes:    o.evidenceSubjectPrefixes,
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
		Gate:      gate,
	}); err != nil {
		return fmt.Errorf("failed to add evidence reconciler: %w", err)
	}

	return addNodeAgent(mgr, o, namespace)
}

// idpPieces are the profile idp rows detection writes (the Evidence Server and the recorder), nil outside idp.
func idpPieces(inst *detect.Installation, gate *evidence.Gate, enabled bool, recorder string) func() []detect.Entry {
	if !slices.Contains(inst.Profile, "idp") {
		return nil
	}
	return func() []detect.Entry {
		return []detect.Entry{evidence.Piece(gate, enabled), detect.RecorderPiece(recorder)}
	}
}

// evidenceGate is the Evidence Server's gate under --evidence-server-auto (nil otherwise) and the URL the
// dashboard and the target snapshot read on every use: constant with --enable-evidence-server, the gate's
// while it is on, empty otherwise. The gate probes the target snapshot's repository, as the snapshot does.
func evidenceGate(mgr ctrl.Manager, o *options, namespace string, ident kubecluster.Identity,
	kc authn.Keychain) (*evidence.Gate, func() string, error) {
	url := fmt.Sprintf("http://%s.%s.svc:%d", evidence.Name, namespace, evidence.EvidencePort)
	switch {
	case o.enableEvidence:
		return nil, func() string { return url }, nil
	case !o.evidenceAuto:
		return nil, func() string { return "" }, nil
	}
	g := &evidence.Gate{Reader: mgr.GetAPIReader(), Namespace: namespace, TrustSecret: o.evidenceTrustSecret,
		TrustConfigKey: o.evidenceTrustConfigKey, Registry: o.registry,
		Subjects: append(slices.Clone(o.evidenceSubjects), o.evidenceSubjectPrefixes...), LastPush: lastPush.Load, URL: url,
		FirstPush: firstPush}
	if o.registry != "" {
		probe, err := targetsnapshot.ReferrersProbe(targetsnapshot.Config{Registry: o.registry, ClusterID: ident.ID,
			Keychain: kc, Insecure: oci.EnvInsecureRegistries()})
		if err != nil {
			return nil, nil, err
		}
		g.Probe = probe
	}
	return g, g.EvidenceURL, nil
}

// addNodeAgent validates the node agent configuration and registers its reconciler.
func addNodeAgent(mgr ctrl.Manager, o *options, namespace string) error {
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

// The --dashboard-index modes.
const (
	dashboardIndexAuto = "auto"
	dashboardIndexNone = "none"
)

// dashboardIndex is the organization index the managed dashboard reads: the installation's own, written
// from --registry, when mode is auto. It never comes from the detection ConfigMap, which a third party
// could forge to choose the host that receives the dashboard's registry credentials.
func dashboardIndex(mode string, inst *detect.Installation) string {
	if mode != dashboardIndexAuto || inst == nil {
		return ""
	}
	return inst.Index
}

// addPublishers runs the linker, the target snapshot publisher and, with --hub-url, the member link (both
// signing with the one installation key). It returns the installation block detection advertises on every
// install, whose registry fields need --registry, and the inferred-offering publisher's advertisement, nil
// when that publisher does not run.
func addPublishers(mgr ctrl.Manager, o *options, namespace string, ident kubecluster.Identity,
	evidenceURL func() string, kc, loaderKC authn.Keychain,
	rec *controller.PactoReconciler) (*detect.Installation, func() *detect.OfferingsAd, error) {
	linked, err := addLinker(mgr, o, namespace, ident.ID, kc, loaderKC, rec)
	if err != nil {
		return nil, nil, err
	}
	profiles, err := detection.ParseProfile(o.profile)
	if err != nil {
		return nil, nil, err
	}
	inst := &detect.Installation{Profile: profiles}
	key, offerings, err := addTargetSnapshot(mgr, o, namespace, ident, evidenceURL, kc, inst, linked)
	if err != nil {
		return nil, nil, err
	}
	return inst, offerings, addMember(mgr, o, ident, key)
}

// addMember makes this cluster a member when --hub-url is set: the elected leader dials the hub and answers
// its reviews with this cluster's SelfSubjectReview and SubjectAccessReview. It needs --registry (the
// installation key the hub approved) and the cluster id, and stops the operator at start without them.
func addMember(mgr ctrl.Manager, o *options, ident kubecluster.Identity, key ed25519.PrivateKey) error {
	if o.hubURL == "" {
		return nil
	}
	if o.registry == "" {
		return errors.New("--hub-url needs --registry: a member is a cluster that joined the hub through the registry")
	}
	rv := &member.Reviewer{Own: mgr.GetConfig(), Client: mgr.GetClient(), Now: time.Now,
		NewClient: func(c *rest.Config) (client.Client, error) {
			return client.New(c, client.Options{Scheme: mgr.GetScheme()})
		}}
	link, err := member.New(o.hubURL, o.hubCAFile, ident, key, rv)
	if err != nil {
		return err
	}
	setupLog.Info("This cluster is a member: dialling the hub", "hub", o.hubURL, "cluster", ident.ID)
	return mgr.Add(link)
}

// addLinker runs the linker when the operator runs inside the chart: it links every workload no Pacto
// targets to the published contract it runs and judges it through rec on a Pacto that is never stored.
// It returns the linker's last result for the target snapshot, nil when it is off.
func addLinker(mgr ctrl.Manager, o *options, namespace, clusterID string, kc, loaderKC authn.Keychain,
	rec *controller.PactoReconciler) (func() core.LinkedTargets, error) {
	l, err := linker.ForOperator(linker.Config{DeploymentName: os.Getenv("OPERATOR_DEPLOYMENT_NAME"), Namespace: namespace,
		Cluster: clusterID, Registry: o.registry, Watched: o.namespaces, Keychain: kc, LoaderKeychain: loaderKC,
		Insecure: oci.EnvInsecureRegistries(), Reader: mgr.GetAPIReader(), Writer: mgr.GetClient()})
	if err != nil || l == nil {
		return nil, err
	}
	// An annotation naming a repository outside the organization index is loaded with no credentials,
	// so writing a Deployment never borrows the operator's registry access.
	rec.Linked = l.Siblings
	anonymous := *rec
	anonymous.Loader = loader.New(loader.WithKeychain(authn.NewMultiKeychain()))
	l.Evaluate, l.EvaluateAnonymous = rec.EvaluateDetached, anonymous.EvaluateDetached
	return l.Current, mgr.Add(l)
}

// lastPush is the target snapshot publisher's last attempt, which detection advertises; pushed wakes
// detection after each attempt, so it is advertised at once rather than on the next detection round.
var (
	lastPush atomic.Pointer[detect.PushResult]
	pushed   = make(chan struct{}, 1)
)

// recordPush stores a publish attempt and wakes detection without ever blocking the publisher.
func recordPush(r detect.PushResult) {
	lastPush.Store(&r)
	select {
	case pushed <- struct{}{}:
	default:
	}
}

// firstPush is closed by the target snapshot publisher's first attempt, so the evidence gate checks then.
var firstPush, closeFirstPush = func() (<-chan struct{}, func()) {
	c := make(chan struct{})
	var once sync.Once
	return c, func() { once.Do(func() { close(c) }) }
}()

// addTargetSnapshot runs the target snapshot publisher when --registry is set: it ensures the installation
// key, then publishes <registry>/pacto-targets:cluster-<uid> and the inferred contracts under
// <registry>/pacto-inferred on the elected leader. It fills the registry fields of inst, the installation
// detection advertises, and leaves them empty without --registry. It returns the installation key, nil
// without --registry, and the inferred-offering publisher's advertisement, nil when it does not run. A member
// (--hub-url) publishes no inferred contracts: its hub does, from the merged snapshot. Any missing piece stops
// the operator at start with its reason.
func addTargetSnapshot(mgr ctrl.Manager, o *options, namespace string, ident kubecluster.Identity,
	evidenceURL func() string, kc authn.Keychain, inst *detect.Installation,
	linked func() core.LinkedTargets) (ed25519.PrivateKey, func() *detect.OfferingsAd, error) {
	if o.registry == "" {
		setupLog.Info("Target snapshot and inferred contracts are off: set the registry value")
		return nil, nil, nil
	}
	deploy := os.Getenv("OPERATOR_DEPLOYMENT_NAME")
	if deploy == "" {
		return nil, nil, fmt.Errorf("--registry needs OPERATOR_DEPLOYMENT_NAME: " +
			"the installation key Secret is named after the operator Deployment")
	}
	key, err := installkey.Ensure(context.Background(), mgr.GetAPIReader(), mgr.GetClient(), namespace,
		installkey.SecretName(deploy))
	if err != nil {
		return nil, nil, err
	}
	pub, err := targetsnapshot.ForOperator(targetsnapshot.Config{Registry: o.registry, ClusterID: ident.ID,
		ClusterName: ident.Name, ClusterNameSource: ident.NameSource,
		Namespace: namespace, ServiceAccount: os.Getenv("OPERATOR_SERVICE_ACCOUNT"), EvidenceURL: evidenceURL,
		Watched: o.namespaces, Key: key, Keychain: kc, Insecure: oci.EnvInsecureRegistries(), Reader: mgr.GetAPIReader(),
		OnPush:      func(r detect.PushResult) { recordPush(r); closeFirstPush() },
		OwnerPolicy: readOwnerPolicy(o.ownerPolicy),
		Linked:      linked})
	if err != nil {
		return nil, nil, err
	}
	if err := addInferred(mgr, o, key, kc); err != nil {
		return nil, nil, err
	}
	offerings, err := addOfferings(mgr, o, ident.ID, key, kc)
	if err != nil {
		return nil, nil, err
	}
	inst.Registry = o.registry
	inst.Targets = oci.InstallationRepository(o.registry, oci.TargetsRepository)
	inst.Index = oci.InstallationRepository(o.registry, oci.IndexRepository)
	inst.Inferred = oci.InstallationRepository(o.registry, oci.InferredRepository)
	inst.Pushes = credentials.InstallationPrefixes(o.registry, o.registryPushPrefixes)
	inst.SnapshotKey, inst.SnapshotKeyID = installkey.PublicKeyBase64(key), signing.Hint(key.Public())
	setupLog.Info("Target snapshot is on", "repository", inst.Targets,
		"snapshotKeyID", inst.SnapshotKeyID, "snapshotKey", inst.SnapshotKey)
	return key, offerings, mgr.Add(pub)
}

// addOfferings publishes each Crossplane XRD as an inferred offering under
// <registry>/pacto-inferred/crossplane-<clusterID>/services, signed with the installation key, on the elected
// leader. It runs on a cluster-wide operator with --crossplane-offerings, members included (each cluster
// writes its own repository), and asks on every tick whether Crossplane is served. It returns the
// publisher's advertisement, nil when it does not run.
func addOfferings(mgr ctrl.Manager, o *options, clusterID string, key ed25519.PrivateKey,
	kc authn.Keychain) (func() *detect.OfferingsAd, error) {
	if !o.crossplaneOfferings {
		return nil, nil
	}
	dyn, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		return nil, err
	}
	p, err := offeringpublish.ForOperator(offeringpublish.Config{Registry: o.registry, ClusterID: clusterID, Key: key,
		Keychain: kc, Insecure: oci.EnvInsecureRegistries(), Dynamic: dyn, Served: detection.ServedFor(mgr.GetConfig()),
		Watched: o.namespaces, Pacto: version})
	if err != nil || p == nil {
		return nil, err
	}
	return p.Advertisement, mgr.Add(p)
}

// addInferred publishes the inferred contracts of the externals the Pactos name, signed with the installation
// key, under <registry>/pacto-inferred: the same push scope, on the elected leader. A member publishes none.
func addInferred(mgr ctrl.Manager, o *options, key ed25519.PrivateKey, kc authn.Keychain) error {
	if o.hubURL != "" {
		setupLog.Info("Inferred contracts are off on a member: its hub publishes them")
		return nil
	}
	inferred, err := inferredpublish.ForOperator(inferredpublish.Config{Registry: o.registry, Key: key, Keychain: kc,
		Insecure: oci.EnvInsecureRegistries(), Reader: mgr.GetAPIReader(), Watched: o.namespaces, Pacto: version})
	if err != nil {
		return err
	}
	return mgr.Add(inferred)
}

// readOwnerPolicy reads the mounted owner policy once at start: the ConfigMap
// is immutable and content-named, so a chart change rolls the pod. A policy
// that does not read is logged and none is published, so readers see no policy
// (unknown) rather than an empty one.
func readOwnerPolicy(path string) *core.OwnerPolicy {
	if path == "" {
		return nil
	}
	p, why := core.ReadOwnerPolicy(path)
	if p == nil {
		setupLog.Error(errors.New(why), "The owner policy is not published")
	}
	return p
}

// ownerCheck is what OwnerVerified judges a running bundle's owner change
// with: the mounted owner policy, the trusted root (--trusted-root, else
// public-good Sigstore through TUF), the publisher rule derived from
// --publishers-registry and a registry client with each Pacto's pull
// credentials. A trusted root file that cannot be read fails startup.
func ownerCheck(o *options, ld *loader.Loader) (*controller.OwnerCheck, error) {
	fetch := func() (root.TrustedMaterial, error) { return signing.PublicGoodRootReadOnly(tuf.DefaultMirror) }
	if o.trustedRoot != "" {
		raw, err := os.ReadFile(o.trustedRoot)
		if err != nil {
			return nil, err
		}
		tr, err := root.NewTrustedRootFromJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("--trusted-root: %w", err)
		}
		fetch = func() (root.TrustedMaterial, error) { return tr, nil }
	}
	oc := &controller.OwnerCheck{Root: hourlyRoot(fetch), Now: time.Now,
		PolicyFrom: "no owner policy is mounted (the chart's declare block is empty)",
		Registry: func(auth *authn.AuthConfig) publish.Registry {
			return publish.NewClient(ld.Keychain(auth), oci.EnvInsecureRegistries()...)
		}}
	if o.ownerPolicy != "" {
		oc.Policy, oc.PolicyFrom = core.ReadOwnerPolicy(o.ownerPolicy)
	}
	pubs := core.PublisherConfig{Registry: o.publishersRegistry, DefaultBranch: o.publishersBranch}
	if rule, why := pubs.Rule(); why == "" {
		oc.Rules = []core.PublisherRule{rule}
	}
	if o.publishersPolicy != "" {
		raw, err := os.ReadFile(o.policySigners)
		if err != nil {
			return nil, fmt.Errorf("--publishers-policy-signers: %w", err)
		}
		verify, err := core.PolicyVerifier(raw, signing.OpenKey)
		if err != nil {
			return nil, fmt.Errorf("--publishers-policy-signers: %w", err)
		}
		reg := func(...string) publish.Registry {
			return publish.NewClient(ld.Keychain(nil), oci.EnvInsecureRegistries()...)
		}
		pubs := &delivery.Publishers{Policy: o.publishersPolicy, PolicyVerify: verify, Reg: reg, Now: time.Now}
		oc.DeliveryPolicy = pubs.VerifiedPolicy
	}
	return oc, nil
}

// hourlyRoot is fetch, its last good root reused for an hour: Sigstore
// rotates keys through TUF. A failed fetch is tried again on the next call.
// ponytail: one mutex around the fetch; owner checks are rare (a new digest
// that changes an owner).
func hourlyRoot(fetch func() (root.TrustedMaterial, error)) func() (root.TrustedMaterial, error) {
	var mu sync.Mutex
	var tm root.TrustedMaterial
	var at time.Time
	return func() (root.TrustedMaterial, error) {
		mu.Lock()
		defer mu.Unlock()
		if tm != nil && time.Since(at) < time.Hour {
			return tm, nil
		}
		got, err := fetch()
		if err != nil {
			return nil, err
		}
		tm, at = got, time.Now()
		return tm, nil
	}
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
