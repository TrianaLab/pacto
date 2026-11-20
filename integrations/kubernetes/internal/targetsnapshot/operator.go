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
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"oras.land/oras-go/v2/registry/remote"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// Config is what the operator hands the publisher.
type Config struct {
	// Registry is the installation registry (--registry); empty publishes
	// nothing.
	Registry string
	// ClusterID is the kube-system namespace UID.
	ClusterID string
	// Namespace and ServiceAccount are the operator's own, written as the
	// snapshot's publisher claim.
	Namespace, ServiceAccount string
	// EvidenceURL is the managed Evidence Server, empty when it is off.
	EvidenceURL string
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
	repo, err := ocireferrer.Open(oci.InstallationRepository(cfg.Registry, oci.TargetsRepository), ocireferrer.Options{
		Keychain: cfg.Keychain, Insecure: cfg.Insecure, Scope: oci.NewPushScope(cfg.Registry)})
	if err != nil {
		return nil, fmt.Errorf("the target snapshot repository: %w", err)
	}
	kp, _ := signing.FromPrivateKey(cfg.Key) // a full-size Ed25519 key always loads
	return &TargetPublisher{Client: cfg.Reader, Repo: repo, Signer: signing.Keyed(kp, ""), ClusterID: cfg.ClusterID,
		// The publisher claim, inlined: the operator never links the delivery packages.
		Publisher:  "system:serviceaccount:" + cfg.Namespace + ":" + cfg.ServiceAccount,
		Namespaces: cfg.Watched, OnPush: cfg.OnPush, Mode: targetsMode(repo, cfg.ClusterID),
		EvidenceURL: cfg.EvidenceURL, HTTP: &http.Client{Timeout: 10 * time.Second},
		Now: time.Now, Interval: core.TargetSnapshotInterval, Log: ctrl.Log.WithName("targets")}, nil
}

// targetsMode is the registry mode of the repository holding the cluster's
// snapshot, probed against its cluster-<uid> tag; empty before the first push
// or when the probe cannot tell.
func targetsMode(repo *remote.Repository, clusterID string) func(context.Context) string {
	return func(ctx context.Context) string {
		desc, err := repo.Resolve(ctx, "cluster-"+clusterID)
		if err != nil {
			return ""
		}
		caps, err := ocireferrer.Probes.Probe(ctx, repo, desc.Digest.String(), false)
		if err != nil {
			return ""
		}
		return caps.Mode()
	}
}
