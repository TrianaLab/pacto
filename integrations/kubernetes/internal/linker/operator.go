/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// Config is what the operator hands the linker.
type Config struct {
	// DeploymentName is the operator Deployment; empty runs no linker.
	DeploymentName, Namespace string
	// Cluster is the kube-system namespace UID, "" when it could not be read.
	Cluster string
	// Registry is the installation registry; empty turns the digest signal
	// off.
	Registry string
	Watched  []string
	// Keychain is the installation keychain the index is read with;
	// LoaderKeychain the contract loader's, which reads the images records.
	Keychain, LoaderKeychain authn.Keychain
	Insecure                 []string
	Reader                   client.Reader
	Writer                   client.Client
}

// ForOperator is the operator's linker, nil when the operator runs outside the
// chart (no Deployment name owns its ConfigMap key). Every registry client it
// builds passes the push scope, although the linker only reads. The caller
// sets Evaluate and EvaluateAnonymous.
func ForOperator(cfg Config) (*Linker, error) {
	if cfg.DeploymentName == "" {
		return nil, nil
	}
	l := &Linker{Reader: cfg.Reader, Writer: cfg.Writer, Index: &Index{}, Namespace: cfg.Namespace,
		ConfigMap: cfg.DeploymentName + "-detected", DeploymentName: cfg.DeploymentName, Cluster: cfg.Cluster,
		Watched: cfg.Watched, Now: time.Now}
	if cfg.Registry == "" {
		return l, nil
	}
	scope := oci.NewPushScope(cfg.Registry)
	indexRepo := oci.InstallationRepository(cfg.Registry, oci.IndexRepository)
	repo, err := ocireferrer.Open(indexRepo, ocireferrer.Options{Keychain: cfg.Keychain, Insecure: cfg.Insecure, Scope: scope})
	if err != nil {
		return nil, fmt.Errorf("the organization index repository: %w", err)
	}
	l.Tags = func(ctx context.Context) ([]string, error) { return ocireferrer.Tags(ctx, repo, "") }
	l.Registry = registryReader{repo: repo, indexRepo: indexRepo,
		images: publish.NewScopedClient(cfg.LoaderKeychain, scope, nil, cfg.Insecure...)}
	return l, nil
}

// registryReader is the [Reader] over the organization index repository and
// the bundles' images records.
type registryReader struct {
	repo      *remote.Repository
	indexRepo string
	images    *publish.Client
}

// Entry reads the entry under tag, checking each bound before the read it
// guards, then binds it to the index repository and tag.
func (r registryReader) Entry(ctx context.Context, tag string) (orgindex.Entry, error) {
	desc, err := r.repo.Resolve(ctx, tag)
	if err == nil && desc.Size > orgindex.MaxManifestBytes {
		err = fmt.Errorf("the manifest is %d bytes, over the %d-byte bound", desc.Size, orgindex.MaxManifestBytes)
	}
	var raw, body []byte
	var layer ocispec.Descriptor
	if err == nil {
		raw, err = content.FetchAll(ctx, r.repo, desc)
	}
	if err == nil {
		layer, err = orgindex.EntryLayer(raw)
	}
	if err == nil {
		body, err = content.FetchAll(ctx, r.repo, layer)
	}
	var e orgindex.Entry
	if err == nil {
		e, err = orgindex.Decode(body)
	}
	if err == nil {
		err = e.Validate(r.indexRepo, tag)
	}
	return e, err
}

// Images reads the images records of the bundle repo@digest.
func (r registryReader) Images(ctx context.Context, repo, digest string) ([]publish.ImagesRecord, bool, error) {
	return r.images.ReadImages(ctx, repo, digest)
}
