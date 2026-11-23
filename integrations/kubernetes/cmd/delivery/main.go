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
	"cmp"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/credentials"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/delivery"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/runstats"
	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
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
		"How often a Stage is looked at when nothing it watches (its operations, upstream Stage and the Flux or "+
			"Argo CD objects the promoter watches) changed.")
	argocd := fs.Bool("argocd", false, "Stages may apply through Argo CD Applications: watch and cache them.")
	flux := fs.Bool("flux", true, "Stages may apply through Flux OCIRepositories and Kustomizations: watch them.")
	fluxHelm := fs.Bool("flux-helm", false, "Stages may apply through Flux HelmReleases: watch them.")
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
	pubRegistry := fs.String("publishers-registry", "",
		"The registry (host/org) the installation's services publish to. Stages with candidates.registry accept a "+
			"version only when its Sigstore signature comes from the publisher rule derived from it: on ghcr.io and "+
			"registry.gitlab.com the organization's CI on the default branch. "+
			"Empty: no rule, so only the signed delivery policy or a person's confirmation (a pin) accepts a signer.")
	pubBranch := fs.String("publishers-default-branch", "main",
		"The default branch whose CI the derived publisher rule accepts.")
	pubRoot := fs.String("publishers-trusted-root", "",
		"A Sigstore trusted root (trusted_root.json) publisher signatures must chain to. Empty fetches public-good "+
			"Sigstore's root through TUF, caching nothing on disk.")
	pubPolicy := fs.String("publishers-policy", "",
		"The signed delivery policy (repository:tag) whose publisher rules replace the derived rule once verified. "+
			"One that never verified gives no rule: only pinned signers are accepted. Empty: none.")
	pubPolicySigners := fs.String("publishers-policy-signers", "",
		"A trust config (YAML) whose role: policy keys sign the delivery policy. Required with --publishers-policy.")
	recordsRepo := fs.String("records-repository", "",
		"The recorder's streams repository (host/path), where it writes the identity pins of confirmed signers. "+
			"Empty reads no pins.")
	pinSignerConfig := fs.String("pin-signer-config", "",
		"A trust config (YAML) naming this cluster's recorder key or keyless identity for streams pins/*/*: "+
			"the pins the recorder writes verify under it. Empty: no new signer can be confirmed.")
	recordTrustConfig := fs.String("record-trust-config", "",
		"The recorder's trust config (YAML, delivery.recorder.trust): only its keys that list an explicit pins/ "+
			"stream also verify pins.")
	admissionPolicies := fs.String("admission-policies", "",
		"Comma-separated ValidatingAdmissionPolicies (each with a binding of the same name) the promoter trusts. "+
			"While one is missing, fails open or is not bound with Deny to every object, no operation is admitted, "+
			"no approval counts and every Stage is not Ready. Required: with none set, the promoter admits nothing.")
	registry := fs.String("registry", "", registryUsage)
	statsRepository := fs.String("stats-repository", "",
		"A repository (host/path) the promoter publishes an unsigned run record of every concluded operation to, "+
			"best effort. Empty records none.")
	statsDockerConfig := fs.String("stats-docker-config", "",
		"A directory holding a config.json with push credentials for --stats-repository. "+
			"Empty uses the promoter's own registry credentials.")
	fingerprints := fs.String("fingerprints", "",
		"A file (YAML or JSON) of the workload managers and promoters this installation runs, "+
			"as the chart's fingerprints value. A workload a configured manager applies reads external: <product>, "+
			"and the promoter writes no pointer for it.")
	environments := fs.Bool("environments", false,
		"Publish the installation ladder and the rungs Pacto proposes from the cluster in the ConfigMap "+
			core.EnvironmentsConfigMap+" of the promoter's namespace.")
	ladder := fs.String("environment-ladder", "staging,production",
		"The installation ladder, comma-separated in promotion order: each rung takes what the one before verified.")
	ladderRepo := fs.String("ladder-repository", "",
		"The approvals repository (host/path) people push signed installation ladder confirmations to. "+
			"Empty reads none, so only the declared installation ladder holds.")
	zopts := zap.Options{}
	zopts.BindFlags(fs)
	_ = fs.Parse(args)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))

	mgr, err := newManager(*metricsAddr, *probeAddr, *leaderElect, "pacto-delivery-promoter", float32(*qps), *burst)
	if err != nil {
		return err
	}
	user, policy, err := promoterPolicy(*controllerUser, *identities)
	if err != nil {
		return err
	}

	ident, err := kubeIdentity(mgr)
	if err != nil {
		return err
	}
	clusterID := ident.ID
	scope, keychain := writerScope(*registry, *statsRepository)
	insecure := oci.EnvInsecureRegistries()
	providers, err := core.ParseProviders(*gateProviders)
	if err != nil {
		return err
	}
	configured, err := core.NewGates(providers, &http.Client{Timeout: 30 * time.Second}, signing.OpenKey)
	if err != nil {
		return err
	}
	managers, err := loadManagers(*fingerprints)
	if err != nil {
		return err
	}
	// Discovery is cached for the life of the process; Infra invalidates it
	// once on a kind it does not list, so a Crossplane kind served after
	// start is read, and one still missing reads Unknown, never Pass.
	dc, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	cluster := delivery.Cluster{Client: mgr.GetClient(), Cache: mgr.GetCache(), CacheApplications: *argocd,
		NoFlux: !*flux, FluxHelm: *fluxHelm, Managers: managers, Infra: kubecluster.Infra(memory.NewMemCacheClient(dc)),
		Registry: func(hosts ...string) core.Registry {
			return oci.NewClient(keychain, oci.WithInsecureRegistries(slices.Concat(insecure, hosts)...))
		}}
	if *upstreamTrust != "" {
		if cluster.Upstream, err = upstreamReader(*upstreamTrust, keychain); err != nil {
			return err
		}
	}
	pubs, err := publishers(*pubRegistry, *pubBranch, *pubRoot, keychain, insecure, *resync)
	if err != nil {
		return err
	}
	recordOpts := ocireferrer.Options{Keychain: keychain, Insecure: insecure, Scope: scope}
	if err := trustSources(pubs, policy, recordOpts, *records, *pubPolicy, *pubPolicySigners,
		*recordsRepo, *pinSignerConfig, *recordTrustConfig); err != nil {
		return err
	}
	gates := delivery.NewGates(cluster, configured)
	stats, err := addStats(mgr, *statsRepository, *statsDockerConfig, keychain, insecure, scope, clusterID)
	if err != nil {
		return err
	}
	if err := (&delivery.OperationReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster,
		Policy: policy, Workers: *workers, Gates: gates, Now: time.Now, Poll: *poll, Records: *records,
		RecordOptions: recordOpts,
		Freshness:     *authFreshness,
		RecordedTTL:   *recordedTTL,
		//nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder(), as cmd/main.go
		Events:            mgr.GetEventRecorderFor("pacto-delivery"),
		Stats:             stats,
		AdmissionPolicies: delivery.SplitList(*admissionPolicies)}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&delivery.StageReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster,
		Policy: policy, Workers: *workers, ClusterID: clusterID, Gates: gates, Now: time.Now,
		Resync: *resync, AdmissionPolicies: delivery.SplitList(*admissionPolicies), Freshness: *authFreshness,
		Publishers: pubs,
		//nolint:staticcheck // TODO: migrate to mgr.GetEventRecorder(), as cmd/main.go
		Events: mgr.GetEventRecorderFor("pacto-delivery")}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&delivery.EnvironmentReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
		Namespace: os.Getenv("POD_NAMESPACE"), ClusterID: clusterID, ClusterName: ident.Name, NameSource: ident.NameSource,
		Ladder: delivery.SplitList(*ladder), Off: !*environments, Now: time.Now,
		Resync: *resync, Policy: policy, LadderRepository: *ladderRepo,
		RecordOptions: recordOpts, Registry: *registry, Cluster: cluster,
		Accepted: pubs.NewestAccepted, Placement: pubs.VerifiedPlacement}).SetupWithManager(mgr); err != nil {
		return err
	}
	return startPromoter(mgr, user)
}

// startPromoter adds the promoter's health checks and runs the manager.
func startPromoter(mgr manager.Manager, user string) error {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}
	ctrl.Log.Info("starting the Pacto delivery promoter", "version", version, "controllerUser", user)
	return mgr.Start(ctrl.SetupSignalHandler())
}

// promoterPolicy resolves the promoter's own apiserver username (derived from
// POD_NAMESPACE and SERVICE_ACCOUNT when user is empty) and loads the identity
// policy for it.
func promoterPolicy(user, identities string) (string, core.ClassPolicy, error) {
	if user == "" {
		user = delivery.ServiceAccountUser(os.Getenv("POD_NAMESPACE"), os.Getenv("SERVICE_ACCOUNT"))
	}
	policy, err := delivery.LoadClassPolicy(identities, user)
	return user, policy, err
}

// registryUsage is the --registry help of both delivery subcommands.
const registryUsage = "The installation registry (host/path): this component pushes only under its installation " +
	"repositories and its own repositories, and reaches them with the cloud workload identity. Empty: only its own."

// writerScope is a delivery writer's push scope over registry and repos,
// and the keychain that reaches them with the cloud workload identity only
// under that scope (no pull Secret: that is the operator's alone).
func writerScope(registry string, repos ...string) (*oci.PushScope, authn.Keychain) {
	scope := oci.NewPushScope(registry, repos...)
	kc, _ := credentials.OperatorKeychain(nil, "", scope.Prefixes(), true) // no pull Secret: never fails
	return scope, kc
}

// addStats runs a run record emitter when repository is set and returns its
// Emit, or nil. A bad repository or docker config fails startup.
func addStats(mgr manager.Manager, repository, dockerConfig string, kc authn.Keychain, insecure []string,
	scope *oci.PushScope, clusterID string) (func(*dv1.Operation), error) {
	if repository == "" {
		return nil, nil
	}
	if err := runstats.CheckDockerConfig(dockerConfig); err != nil {
		return nil, err
	}
	e, err := runstats.New(repository, runstats.Keychain(dockerConfig, kc), insecure, scope, clusterID, version)
	if err != nil {
		return nil, err
	}
	if err := mgr.Add(manager.RunnableFunc(e.Run)); err != nil {
		return nil, err
	}
	ctrl.Log.Info("run records on", "repository", repository)
	return e.Emit, nil
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
	sink := fs.String("cdevents-sink", "", "A URL to POST a CDEvent to for every record written and when a Stage "+
		"is created or deleted (environment.created, environment.deleted), best effort.")
	approvals := fs.String("approvals-repository", "",
		"The repository (host/path) a person's client pushes signed approvals to, one anchor per stream. "+
			"Empty is <streams-repository>-approvals.")
	admissionPolicies := fs.String("admission-policies", "",
		"Comma-separated ValidatingAdmissionPolicy names (each bound by a binding of the same name) that must "+
			"guard Approvals before the recorder signs one on its creator's behalf; empty signs none.")
	registry := fs.String("registry", "", registryUsage)
	identities := fs.String("identities", "",
		"The identity policy as JSON, the one the promoter reads: its approver signers are the issuers whose keyless "+
			"approval records the recorder views. Empty views none.")
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
	policy, err := delivery.LoadClassPolicy(*identities, "")
	if err != nil {
		return err
	}
	mgr, err := newManager(*metricsAddr, *probeAddr, *leaderElect, "pacto-delivery-recorder", 0, 0)
	if err != nil {
		return err
	}
	clusterID, err := kubeClusterID(mgr)
	if err != nil {
		return err
	}
	approvalsRepo := cmp.Or(*approvals, *streams+"-approvals")
	scope, keychain := writerScope(*registry, *streams, approvalsRepo)
	store := &delivery.RecordStore{Streams: *streams, Approvals: approvalsRepo, Verify: verify,
		Options: ocireferrer.Options{Keychain: keychain, Insecure: oci.EnvInsecureRegistries(), Scope: scope}}
	cluster := delivery.Cluster{Client: mgr.GetClient()}
	recorder := &delivery.Recorder{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster, Store: store,
		Signer: signer, Trust: trust.Digest(), ClusterID: clusterID, Now: time.Now, Poll: *poll, GapGrace: time.Minute,
		AdmissionPolicies: delivery.SplitList(*admissionPolicies), Policy: policy}
	if *sink != "" {
		recorder.Events = &delivery.Events{Sink: *sink, Source: "/pacto-delivery/" + clusterID}
	}
	if err := recorder.SetupWithManager(mgr); err != nil {
		return err
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	// Ready while the streams repository can be written and listed in
	// either mode: native referrers, or write-once tags where the registry
	// serves no Referrers API.
	if err := mgr.AddReadyzCheck("records", func(req *http.Request) error {
		ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
		defer cancel()
		_, err := store.Probe(ctx)
		return err
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

// kubeIdentity reads the cluster id and display name (kubecluster.Identify).
// The promoter may not list nodes, so the name is the uid's unless a node
// label is readable.
func kubeIdentity(mgr ctrl.Manager) (kubecluster.Identity, error) {
	dyn, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		return kubecluster.Identity{}, err
	}
	return kubecluster.Identify(context.Background(), dyn, "")
}

// kubeClusterID reads the cluster half of every stream id: the kube-system
// namespace uid.
func kubeClusterID(mgr ctrl.Manager) (string, error) {
	dyn, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		return "", err
	}
	return kubecluster.ID(context.Background(), dyn)
}

// loadManagers is the manager detector of the fingerprints file, the
// built-in one when file is empty.
func loadManagers(file string) (workloadmanager.Detector, error) {
	if file == "" {
		return workloadmanager.Detector{}, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return workloadmanager.Detector{}, err
	}
	defer func() { _ = f.Close() }()
	fp, err := workloadmanager.ParseFingerprints(f)
	return workloadmanager.Detector{Extra: fp.Managers}, err
}

// publishers is the registry watch of the Stages with candidates.registry,
// judging against the rule derived from registry, or nil when registry is
// empty. A trusted root file that cannot be read fails startup.
func publishers(registry, branch, trustedRoot string, keychain authn.Keychain, insecure []string,
	resync time.Duration) (*delivery.Publishers, error) {
	fetch := func() (root.TrustedMaterial, error) { return signing.PublicGoodRootReadOnly(tuf.DefaultMirror) }
	if trustedRoot != "" {
		raw, err := os.ReadFile(trustedRoot)
		if err != nil {
			return nil, err
		}
		tr, err := root.NewTrustedRootFromJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("--publishers-trusted-root: %w", err)
		}
		fetch = func() (root.TrustedMaterial, error) { return tr, nil }
	}
	// No registry derives no rule: the policy and pins still judge (TOFU).
	rule, why := core.PublisherConfig{Registry: registry, DefaultBranch: branch}.Rule()
	return &delivery.Publishers{Root: fetch, Rule: rule, HasRule: why == "", NoRule: why, Resync: resync, Now: time.Now,
		Reg: func(hosts ...string) publish.Registry {
			return publish.NewClient(keychain, slices.Concat(insecure, hosts)...)
		}}, nil
}

// trustSources sets what the registry watch reads beside the derived rule:
// the signed delivery policy and the pins the recorder writes, and why a new
// signer cannot be confirmed when it cannot. A policy with no policy key, or
// a trust config that cannot be read, fails startup.
func trustSources(p *delivery.Publishers, policy core.ClassPolicy, opts ocireferrer.Options, records bool,
	policyRef, policySigners, recordsRepo, pinSigner, recordTrust string) error {
	if policyRef != "" {
		raw, err := os.ReadFile(policySigners)
		if err != nil {
			return fmt.Errorf("--publishers-policy-signers: %w", err)
		}
		if p.PolicyVerify, err = core.PolicyVerifier(raw, signing.OpenKey); err != nil {
			return fmt.Errorf("--publishers-policy-signers: %w", err)
		}
		p.Policy = policyRef
	}
	var raws [2][]byte
	for i, f := range []string{pinSigner, recordTrust} {
		if f == "" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		raws[i] = raw
	}
	verify, ok, err := delivery.PinVerifier(raws[0], raws[1])
	if err != nil {
		return err
	}
	if recordsRepo != "" && ok {
		p.Pins = delivery.PinReader(recordsRepo, opts, verify, policy)
	}
	switch {
	case !records:
		p.CannotPin = "confirming a new signer needs the recorder (delivery.recorder.enabled)"
	case recordsRepo == "" || pinSigner == "":
		p.CannotPin = "confirming a new signer needs delivery.recorder.signing.publicKey or keyless recorder signing, " +
			"so the promoter can verify the pins the recorder writes"
	}
	return nil
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
