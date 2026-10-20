/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Command delivery is the opt-in Pacto delivery image (pacto-delivery). Its one
// subcommand in M0 is `promoter`: the leader-elected controller that decides
// which version each Stage runs. It holds no signing keys and only reads the
// registry. The recorder arrives in M5.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

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
)

var version = "dev"

func main() {
	if len(os.Args) < 2 || os.Args[1] != "promoter" {
		fmt.Fprintln(os.Stderr, "usage: delivery promoter [flags]  (pacto-delivery "+version+")")
		os.Exit(2)
	}
	if err := runPromoter(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "promoter:", err)
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
	gateProviders := fs.String("gate-providers", "",
		`The health gate providers Stages may name, as JSON: `+
			`[{"name":"mira","type":"mira","url":"http://mira:4318"}]. Types: mira, http.`)
	zopts := zap.Options{}
	zopts.BindFlags(fs)
	_ = fs.Parse(args)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(dv1.AddToScheme(scheme))
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: *metricsAddr},
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         *leaderElect,
		LeaderElectionID:       "pacto-delivery-promoter",
	})
	if err != nil {
		return err
	}
	user := *controllerUser
	if user == "" {
		user = delivery.ServiceAccountUser(os.Getenv("POD_NAMESPACE"), os.Getenv("SERVICE_ACCOUNT"))
	}
	policy := core.ClassPolicy{ControllerUser: user, AgentGroups: delivery.SplitList(*agentGroups),
		BreakGlassGroups: delivery.SplitList(*breakGlassGroups), TeamGroupPrefix: *teamGroupPrefix}

	// The cluster half of every stream id: the kube-system namespace uid.
	kubeSystem := &corev1.Namespace{}
	if err := mgr.GetAPIReader().Get(context.Background(), client.ObjectKey{Name: "kube-system"}, kubeSystem); err != nil {
		return fmt.Errorf("reading the cluster id: %w", err)
	}

	keychain := oci.NewKeychain(oci.CredentialOptions{
		Token:    os.Getenv("PACTO_REGISTRY_TOKEN"),
		Username: os.Getenv("PACTO_REGISTRY_USERNAME"),
		Password: os.Getenv("PACTO_REGISTRY_PASSWORD"),
	})
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
		Policy: policy, Gates: gates, Now: time.Now, Poll: *poll}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&delivery.StageReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Cluster: cluster,
		Policy: policy, ClusterID: string(kubeSystem.UID), Gates: gates, Now: time.Now,
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
