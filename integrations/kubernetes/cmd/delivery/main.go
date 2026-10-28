/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Command delivery is the opt-in Pacto delivery image (pacto-delivery), with
// two subcommands deployed as two Deployments. `promoter` is the
// leader-elected controller that decides which version each Stage runs; it
// holds no signing key and only reads the registry. `recorder` writes the
// signed record of every concluded operation; it is the only holder of the
// signing key and the registry push credentials.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/delivery"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

var version = "dev"

func main() {
	run := map[string]func([]string) error{"promoter": runPromoter, "recorder": runRecorder}
	if len(os.Args) < 2 || run[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "usage: delivery promoter|recorder [flags]  (pacto-delivery "+version+")")
		os.Exit(2)
	}
	if err := run[os.Args[1]](os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, os.Args[1]+":", err)
		os.Exit(1)
	}
}

func runPromoter(args []string) error {
	fs := flag.NewFlagSet("promoter", flag.ExitOnError)
	metricsAddr := fs.String("metrics-bind-address", ":8080",
		"The address the metrics endpoint binds to, or 0 to disable it.")
	probeAddr := fs.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	leaderElect := fs.Bool("leader-elect", true, "Elect one active promoter.")
	controllerUser := fs.String("controller-user", "",
		"The promoter's own apiserver username. Empty derives it from POD_NAMESPACE and SERVICE_ACCOUNT.")
	identities := fs.String("identities", "",
		"The identity policy (agent, break-glass and human groups, the team group prefix and whether an owner team "+
			"is required) as JSON, mounted from the ConfigMap the chart publishes for clients. Empty keeps the defaults.")
	workers := fs.Int("workers", 4,
		"How many operations and Stages the promoter reconciles at once, "+
			"so one slow health provider does not hold up the rest.")
	poll := fs.Duration("poll", 3*time.Second, "How often an operation waiting on Flux, Pacto or a person is looked at.")
	resync := fs.Duration("resync", delivery.DefaultResync,
		"How often a Stage is looked at when nothing it watches (its operations, upstream Stage, OCIRepositories, "+
			"Kustomizations and Application) changed.")
	argocd := fs.Bool("argocd", false, "Stages may apply through Argo CD Applications: watch and cache them.")
	qps := fs.Float64("kube-api-qps", 50, "The apiserver requests per second the promoter's client may make.")
	burst := fs.Int("kube-api-burst", 100, "The apiserver requests the promoter's client may burst above kube-api-qps.")
	authFreshness := fs.Duration("auth-freshness", 0,
		"How old a person's login may be when their approval is given; an older one is ignored. Zero: no login is too old.")
	records := fs.Bool("records", false,
		"The recorder runs: leave the record finalizer of every operation that got a seq to it.")
	recordedTTL := fs.Duration("recorded-ttl", 0,
		"Delete a recorded operation this long after it concluded (its record keeps what happened); 0 keeps them all. "+
			"The operation a Stage runs is never deleted.")
	gateProviders := fs.String("gate-providers", "",
		`The health gate providers Stages may name, as JSON: `+
			`[{"name":"mira","type":"mira","url":"https://mira:4318"}]. Types: mira, http, eval. `+
			`Plain http only on loopback or with "allowPlainHTTP": true.`)
	upstreamTrust := fs.String("upstream-trust-config", "",
		"A trust config (YAML) of the recorder keys of other clusters, per stream glob. "+
			"Stages with candidates.upstreamRecords promote only on records these keys signed.")
	admissionPolicies := fs.String("admission-policies", "",
		"Comma-separated ValidatingAdmissionPolicies (each with a binding of the same name) the promoter trusts. "+
			"While one is missing, fails open or is not bound with Deny to every object, no operation is admitted, "+
			"no approval counts and every Stage is not Ready.")
	zopts := zap.Options{}
	zopts.BindFlags(fs)
	_ = fs.Parse(args)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))

	mgr, err := newManager(*metricsAddr, *probeAddr, *leaderElect, "pacto-delivery-promoter", float32(*qps), *burst)
	if err != nil {
		return err
	}
	user := *controllerUser
	if user == "" {
		user = delivery.ServiceAccountUser(os.Getenv("POD_NAMESPACE"), os.Getenv("SERVICE_ACCOUNT"))
	}
	policy, err := delivery.LoadClassPolicy(*identities, user)
	if err != nil {
		return err
	}

	clusterID, err := readClusterID(mgr)
	if err != nil {
		return err
	}
	keychain := oci.NewKeychain(oci.EnvCredentialOptions())
	insecure := oci.EnvInsecureRegistries()
	providers, err := core.ParseProviders(*gateProviders)
	if err != nil {
		return err
	}
	configured, err := core.NewGates(providers, &http.Client{Timeout: 30 * time.Second}, signing.OpenKey)
	if err != nil {
		return err
	}
	cluster := delivery.Cluster{Client: mgr.GetClient(), Cache: mgr.GetCache(), CacheApplications: *argocd,
		Registry: func(hosts ...string) core.Registry {
			return oci.NewClient(keychain, oci.WithInsecureRegistries(slices.Concat(insecure, hosts)...))
		}}
	if *upstreamTrust != "" {
		if cluster.Upstream, err = upstreamReader(*upstreamTrust, keychain); err != nil {
			return err
		}
	}
	gates := delivery.NewGates(cluster, configured)
	if err := (&delivery.OperationReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster,
		Policy: policy, Workers: *workers, Gates: gates, Now: time.Now, Poll: *poll, Records: *records,
		Freshness:   *authFreshness,
		RecordedTTL: *recordedTTL,
		//nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder(), as cmd/main.go
		Events:            mgr.GetEventRecorderFor("pacto-delivery"),
		AdmissionPolicies: delivery.SplitList(*admissionPolicies)}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&delivery.StageReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster,
		Policy: policy, Workers: *workers, ClusterID: clusterID, Gates: gates, Now: time.Now,
		Resync: *resync, AdmissionPolicies: delivery.SplitList(*admissionPolicies), Freshness: *authFreshness,
		//nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder(), as cmd/main.go
		Events: mgr.GetEventRecorderFor("pacto-delivery")}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}
	ctrl.Log.Info("starting the Pacto delivery promoter", "version", version, "controllerUser", user)
	return mgr.Start(ctrl.SetupSignalHandler())
}

func runRecorder(args []string) error {
	fs := flag.NewFlagSet("recorder", flag.ExitOnError)
	metricsAddr := fs.String("metrics-bind-address", ":8080",
		"The address the metrics endpoint binds to, or 0 to disable it.")
	probeAddr := fs.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	leaderElect := fs.Bool("leader-elect", true, "Elect one active recorder.")
	poll := fs.Duration("poll", 30*time.Second, "How long a registry or signer failure backs off.")
	streams := fs.String("streams-repository", "",
		"The repository (host/path) that holds one anchor per stream. Required.")
	keyFile := fs.String("signing-key-file", "", "A PEM private key to sign with (Ed25519, ECDSA P-256 or RSA).")
	kmsKey := fs.String("kms-key", "",
		"A KMS key URI to sign with (awskms://, gcpkms://, azurekms://, hashivault://).")
	keyless := fs.Bool("keyless", false, "Sign keyless with a Fulcio certificate for the pod's identity.")
	identity := fs.String("keyless-identity", "", "The certificate identity keyless signing produces.")
	issuer := fs.String("keyless-issuer", "", "The OIDC issuer keyless signing uses.")
	fulcio := fs.String("keyless-fulcio-url", "", "The Fulcio that issues the keyless certificate.")
	tsa := fs.String("keyless-timestamp-url", "",
		"The timestamp authority whose signed time a keyless certificate verifies at.")
	token := fs.String("keyless-token-file", "",
		"The OIDC token Fulcio checks: a projected service account token for the sigstore audience.")
	trustedRoot := fs.String("keyless-trusted-root", "",
		"The Sigstore trusted root (trusted_root.json) naming that Fulcio and timestamp authority.")
	trustFile := fs.String("trust-config", "",
		"A trust config (YAML) of other keys whose records the recorder accepts, per stream glob, "+
			"such as the old key during a rotation.")
	sink := fs.String("cdevents-sink", "", "A URL to POST a CDEvent to for every record written, best effort.")
	zopts := zap.Options{}
	zopts.BindFlags(fs)
	_ = fs.Parse(args)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	if *streams == "" {
		return fmt.Errorf("--streams-repository is required")
	}

	ctx := context.Background()
	signer, own, err := delivery.NewSigner(ctx, delivery.SigningConfig{KeyFile: *keyFile, KMSKey: *kmsKey,
		Keyless: *keyless, Identity: *identity, Issuer: *issuer, FulcioURL: *fulcio, TimestampURL: *tsa,
		TokenFile: *token, TrustedRootFile: *trustedRoot})
	if err != nil {
		return err
	}
	trust, err := delivery.RecorderTrust(own, *trustFile)
	if err != nil {
		return err
	}
	verify, _ := signing.TrustVerifier(trust) // RecorderTrust checked it
	mgr, err := newManager(*metricsAddr, *probeAddr, *leaderElect, "pacto-delivery-recorder", 0, 0)
	if err != nil {
		return err
	}
	clusterID, err := readClusterID(mgr)
	if err != nil {
		return err
	}
	keychain := oci.NewKeychain(oci.EnvCredentialOptions())
	insecure := oci.EnvInsecureRegistries()
	store := &delivery.RecordStore{Streams: *streams, Verify: verify,
		Options: ocireferrer.Options{Keychain: keychain, Insecure: insecure}}
	cluster := delivery.Cluster{Client: mgr.GetClient()}
	recorder := &delivery.Recorder{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster, Store: store,
		Signer: signer, Trust: trust.Digest(), ClusterID: clusterID, Now: time.Now, Poll: *poll, GapGrace: time.Minute}
	if *sink != "" {
		recorder.Events = &delivery.Events{Sink: *sink, Source: "/pacto-delivery/" + clusterID}
	}
	if err := recorder.SetupWithManager(mgr); err != nil {
		return err
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	// Ready only while the streams repository answers native referrers
	// discovery: a registry without it can never hold records.
	if err := mgr.AddReadyzCheck("records", func(req *http.Request) error {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		return store.Probe(ctx)
	}); err != nil {
		return err
	}
	ctrl.Log.Info("starting the Pacto delivery recorder", "version", version, "streams", *streams)
	return mgr.Start(ctrl.SetupSignalHandler())
}

// newManager builds a manager whose client makes qps requests a second,
// bursting to burst; zero keeps client-go's 20 and 30. Its client reads
// unstructured objects live (Cluster.Client).
func newManager(metricsAddr, probeAddr string, leaderElect bool, id string,
	qps float32, burst int) (ctrl.Manager, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(dv1.AddToScheme(scheme))
	cfg := ctrl.GetConfigOrDie()
	if qps > 0 {
		cfg.QPS, cfg.Burst = qps, burst
	}
	return ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       id,
	})
}

// readClusterID reads the cluster half of every stream id: the kube-system
// namespace uid.
func readClusterID(mgr ctrl.Manager) (string, error) {
	kubeSystem := &corev1.Namespace{}
	if err := mgr.GetAPIReader().Get(context.Background(), client.ObjectKey{Name: "kube-system"}, kubeSystem); err != nil {
		return "", fmt.Errorf("reading the cluster id: %w", err)
	}
	return string(kubeSystem.UID), nil
}

// upstreamReader reads what a Stage in another cluster verified from its
// signed records, verified against the trust config in file, under a timeout.
func upstreamReader(file string, keychain authn.Keychain) (
	func(context.Context, core.RemoteStage, core.RecordMark, []string) (*core.StageStatus, string), error,
) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	trust, err := core.ParseTrust(raw)
	if err != nil {
		return nil, err
	}
	verify, err := signing.TrustVerifier(trust)
	if err != nil {
		return nil, err
	}
	insecure := oci.EnvInsecureRegistries()
	opts := ocireferrer.Options{Keychain: keychain, Insecure: insecure}
	// One Reader for the process: every record it verified is fetched once.
	reader := &deliveryrecord.Reader{Verify: verify}
	return func(ctx context.Context, r core.RemoteStage, seen core.RecordMark,
		acked []string) (*core.StageStatus, string) {
		repo, err := ocireferrer.Open(r.Repository, opts)
		if err != nil {
			return nil, err.Error()
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return reader.Upstream(ctx, repo, r.Stream, seen, acked)
	}, nil
}
