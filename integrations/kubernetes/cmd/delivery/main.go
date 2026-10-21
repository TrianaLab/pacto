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
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
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
	agentGroups := fs.String("agent-groups", "pacto:agents",
		"Comma-separated groups whose members are agents, never humans.")
	breakGlassGroups := fs.String("break-glass-groups", "pacto:break-glass",
		"Comma-separated groups that may override and edit the pointer.")
	teamGroupPrefix := fs.String("team-group-prefix", "",
		"A contract owner team T is identity-provider group T, or this prefix plus T. "+
			"Only members of the teams a question names may answer it.")
	poll := fs.Duration("poll", 3*time.Second, "How often an operation waiting on Flux, Pacto or a person is looked at.")
	records := fs.Bool("records", false,
		"The recorder runs: leave the record finalizer of every operation that got a seq to it.")
	recordedTTL := fs.Duration("recorded-ttl", 0,
		"Delete a recorded operation this long after it concluded (its record keeps what happened); 0 keeps them all. "+
			"The operation a Stage runs is never deleted.")
	gateProviders := fs.String("gate-providers", "",
		`The health gate providers Stages may name, as JSON: `+
			`[{"name":"mira","type":"mira","url":"http://mira:4318"}]. Types: mira, http.`)
	zopts := zap.Options{}
	zopts.BindFlags(fs)
	_ = fs.Parse(args)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))

	mgr, err := newManager(*metricsAddr, *probeAddr, *leaderElect, "pacto-delivery-promoter")
	if err != nil {
		return err
	}
	user := *controllerUser
	if user == "" {
		user = delivery.ServiceAccountUser(os.Getenv("POD_NAMESPACE"), os.Getenv("SERVICE_ACCOUNT"))
	}
	policy := core.ClassPolicy{ControllerUser: user, AgentGroups: delivery.SplitList(*agentGroups),
		BreakGlassGroups: delivery.SplitList(*breakGlassGroups), TeamGroupPrefix: *teamGroupPrefix}

	clusterID, err := readClusterID(mgr)
	if err != nil {
		return err
	}
	keychain := registryKeychain()
	insecure := loader.InsecureClientOptions(os.Getenv(loader.InsecureRegistriesEnvVar))
	providers, err := core.ParseProviders(*gateProviders)
	if err != nil {
		return err
	}
	configured, err := core.NewGates(providers, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	cluster := delivery.Cluster{Client: mgr.GetClient(), Registry: func(hosts ...string) core.Registry {
		return oci.NewClient(keychain, slices.Concat(insecure, []oci.ClientOption{oci.WithInsecureRegistries(hosts...)})...)
	}}
	gates := delivery.NewGates(cluster, configured)
	if err := (&delivery.OperationReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster,
		Policy: policy, Gates: gates, Now: time.Now, Poll: *poll, Records: *records, RecordedTTL: *recordedTTL}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&delivery.StageReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster,
		Policy: policy, ClusterID: clusterID, Gates: gates, Now: time.Now,
		Poll: *poll}).SetupWithManager(mgr); err != nil {
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
	keyFile := fs.String("signing-key-file", "", "A PKCS#8 PEM Ed25519 private key to sign with.")
	kmsKey := fs.String("kms-key", "",
		"A cosign KMS key URI to sign with (awskms://, gcpkms://, azurekms://, hashivault://).")
	keyless := fs.Bool("keyless", false, "Sign keyless with a Fulcio certificate for the pod's identity.")
	identity := fs.String("keyless-identity", "", "The certificate identity keyless signing produces.")
	issuer := fs.String("keyless-issuer", "", "The OIDC issuer keyless signing uses.")
	cosign := fs.String("cosign", "cosign", "The cosign binary, for KMS and keyless signing.")
	trustFile := fs.String("trust-config", "",
		"A trust config (YAML) of other keys whose records the recorder accepts, per stream glob, such as the old key during a rotation.")
	sink := fs.String("cdevents-sink", "", "A URL to POST a CDEvent to for every record written, best effort.")
	zopts := zap.Options{}
	zopts.BindFlags(fs)
	_ = fs.Parse(args)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	if *streams == "" {
		return fmt.Errorf("--streams-repository is required")
	}

	ctx := context.Background()
	signer, own, err := delivery.NewSigner(ctx, delivery.SigningConfig{Ed25519KeyFile: *keyFile, KMSKey: *kmsKey,
		Keyless: *keyless, Identity: *identity, Issuer: *issuer, Cosign: *cosign}, nil)
	if err != nil {
		return err
	}
	trust, err := delivery.RecorderTrust(own, *trustFile)
	if err != nil {
		return err
	}
	verify, _ := trust.Verifier() // RecorderTrust checked it
	mgr, err := newManager(*metricsAddr, *probeAddr, *leaderElect, "pacto-delivery-recorder")
	if err != nil {
		return err
	}
	clusterID, err := readClusterID(mgr)
	if err != nil {
		return err
	}
	keychain := registryKeychain()
	insecure := delivery.SplitList(os.Getenv(loader.InsecureRegistriesEnvVar))
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

func newManager(metricsAddr, probeAddr string, leaderElect bool, id string) (ctrl.Manager, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(dv1.AddToScheme(scheme))
	return ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
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

// registryKeychain is Pacto's ordered keychain: the env token or user and
// password, then a mounted Docker config (DOCKER_CONFIG).
func registryKeychain() authn.Keychain {
	return oci.NewKeychain(oci.CredentialOptions{
		Token:    os.Getenv("PACTO_REGISTRY_TOKEN"),
		Username: os.Getenv("PACTO_REGISTRY_USERNAME"),
		Password: os.Getenv("PACTO_REGISTRY_PASSWORD"),
	})
}
