/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package targetsnapshot

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"k8s.io/apimachinery/pkg/util/validation"
	"oras.land/oras-go/v2/registry/remote"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// Config is what the operator hands the publisher.
type Config struct {
	// Registry is the installation registry (--registry); empty publishes
	// nothing.
	Registry string
	// ClusterID is the kube-system namespace UID.
	ClusterID string
	// ClusterName and ClusterNameSource are the cluster's display name and
	// where it came from (kubecluster.Identify), published in the join block.
	ClusterName, ClusterNameSource string
	// Namespace and ServiceAccount are the operator's own, written as the
	// snapshot's publisher claim.
	Namespace, ServiceAccount string
	// EvidenceURL is the managed Evidence Server, read on every publish;
	// nil or empty while it is off.
	EvidenceURL func() string
	// Watched are the namespaces a scoped operator reads; empty reads all.
	Watched []string
	// Key is the installation key (installkey.Ensure).
	Key ed25519.PrivateKey
	// Keychain answers only under the push prefixes (credentials.OperatorKeychain
	// with allowHostOnly).
	Keychain authn.Keychain
	Insecure []string
	Reader   client.Reader
	// OnPush receives the result of every publish attempt.
	OnPush func(detect.PushResult)
	// OwnerPolicy is the chart's declare block (--owner-policy); nil publishes
	// none.
	OwnerPolicy *core.OwnerPolicy
	// Linked is the linker's last result; nil carries no linked targets.
	Linked func() core.LinkedTargets
}

// ForOperator is the operator's snapshot publisher, or nil when no registry is
// set. It writes only <registry>/pacto-targets, through the push scope, signed
// with the installation key. With a registry, a cluster id it could not read
// is a start-up error: the tag cluster-<uid> would be refused on every tick.
func ForOperator(cfg Config) (manager.Runnable, error) {
	if cfg.Registry == "" {
		return nil, nil
	}
	if cfg.ClusterID == "" {
		return nil, errors.New("the target snapshot needs the cluster id: the operator could not read the kube-system namespace UID")
	}
	if len(cfg.Key) != ed25519.PrivateKeySize {
		return nil, errors.New("the target snapshot needs the installation key")
	}
	repo, err := openTargets(cfg)
	if err != nil {
		return nil, err
	}
	kp, _ := signing.FromPrivateKey(cfg.Key) // a full-size Ed25519 key always loads
	log := ctrl.Log.WithName("targets")
	name, source := joinName(cfg, log)
	return &TargetPublisher{Client: cfg.Reader, Repo: repo, Signer: signing.Keyed(kp, ""), Key: cfg.Key,
		ClusterID: cfg.ClusterID, ClusterName: name, ClusterNameSource: source,
		// The publisher claim, inlined: the operator never links the delivery packages.
		Publisher:  "system:serviceaccount:" + cfg.Namespace + ":" + cfg.ServiceAccount,
		Namespaces: cfg.Watched, OnPush: cfg.OnPush, Mode: targetsMode(referrersProbe(repo, cfg.ClusterID)),
		EvidenceURL: cfg.EvidenceURL, OwnerPolicy: cfg.OwnerPolicy, Linked: cfg.Linked, HTTP: &http.Client{Timeout: 10 * time.Second},
		Now: time.Now, Interval: core.TargetSnapshotInterval, Log: log}, nil
}

// joinName is the display name the join block publishes: the cluster's own
// when it is a DNS label, else the uid's first 8 characters (a name that is
// not a label is logged once).
func joinName(cfg Config, log logr.Logger) (name, source string) {
	if len(validation.IsDNS1123Label(cfg.ClusterName)) == 0 && cfg.ClusterNameSource != "" {
		return cfg.ClusterName, cfg.ClusterNameSource
	}
	if cfg.ClusterName != "" {
		log.Info("the cluster name " + strconv.Quote(cfg.ClusterName) + " is not a DNS label; members are named by uid until a person sets --name on approve")
	}
	return cfg.ClusterID[:min(8, len(cfg.ClusterID))], kubecluster.NameFromUID
}

// openTargets opens <registry>/pacto-targets through the push scope.
func openTargets(cfg Config) (*remote.Repository, error) {
	repo, err := ocireferrer.Open(oci.InstallationRepository(cfg.Registry, oci.TargetsRepository), ocireferrer.Options{
		Keychain: cfg.Keychain, Insecure: cfg.Insecure, Scope: oci.NewPushScope(cfg.Registry)})
	if err != nil {
		return nil, fmt.Errorf("the target snapshot repository: %w", err)
	}
	return repo, nil
}

// ReferrersProbe probes the registry holding cfg's target snapshot against
// its cluster-<uid> tag, through the process-wide ocireferrer.Probes, so it
// shares the answer the snapshot's own mode probe caches. It errs before the
// first push, when the tag does not resolve, and when the probe cannot tell.
func ReferrersProbe(cfg Config) (func(context.Context) (capabilities.Capabilities, error), error) {
	repo, err := openTargets(cfg)
	if err != nil {
		return nil, err
	}
	return referrersProbe(repo, cfg.ClusterID), nil
}

func referrersProbe(repo *remote.Repository, clusterID string) func(context.Context) (capabilities.Capabilities, error) {
	return func(ctx context.Context) (capabilities.Capabilities, error) {
		desc, err := repo.Resolve(ctx, "cluster-"+clusterID)
		if err != nil {
			return capabilities.Capabilities{}, err
		}
		return ocireferrer.Probes.Probe(ctx, repo, desc.Digest.String(), false)
	}
}

// targetsMode is the registry mode of the repository holding the cluster's
// snapshot, from probe; empty before the first push or when the probe cannot
// tell.
func targetsMode(probe func(context.Context) (capabilities.Capabilities, error)) func(context.Context) string {
	return func(ctx context.Context) string {
		caps, err := probe(ctx)
		if err != nil {
			return ""
		}
		return caps.Mode()
	}
}
