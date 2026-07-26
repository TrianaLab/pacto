/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/trianalab/pacto/v2/pkg/oci"
)

// OCIPuller pulls Pacto contract bundles from OCI registries.
type OCIPuller struct {
	keychain authn.Keychain
}

// NewOCIPuller creates an OCIPuller with credentials resolved via the Pacto
// library keychain (env vars, pacto config, gh CLI, Docker config).
func NewOCIPuller() *OCIPuller {
	return &OCIPuller{
		keychain: oci.NewKeychain(oci.CredentialOptions{
			Token:    os.Getenv("PACTO_REGISTRY_TOKEN"),
			Username: os.Getenv("PACTO_REGISTRY_USERNAME"),
			Password: os.Getenv("PACTO_REGISTRY_PASSWORD"),
		}),
	}
}

// Pull fetches a Pacto bundle from an OCI registry reference.
// The ref may have an "oci://" prefix which is stripped.
// If the ref has no explicit tag or digest, the highest semver tag is resolved.
// An optional authOverride provides per-call credentials (e.g. from a K8s Secret).
func (p *OCIPuller) Pull(ctx context.Context, ref string, authOverride *authn.AuthConfig) (*LoadResult, error) {
	ref = strings.TrimPrefix(ref, "oci://")

	kc := p.effectiveKeychain(authOverride)
	client := oci.NewClient(kc)

	// Resolve tag if not explicitly specified
	resolvedRef, err := oci.ResolveRef(ctx, client, ref, "")
	if err != nil {
		slog.Warn("Tag resolution failed, using original ref", "ref", ref, "error", err)
		resolvedRef = ref
	} else if resolvedRef != ref {
		slog.Info("Resolved OCI tag", "original", ref, "resolved", resolvedRef)
	}

	bundle, err := client.Pull(ctx, resolvedRef)
	if err != nil {
		return nil, fmt.Errorf("failed to pull %q: %w", resolvedRef, err)
	}

	// Resolve the OCI manifest digest for immutable identity tracking.
	var digest string
	if d, resolveErr := client.Resolve(ctx, resolvedRef); resolveErr == nil {
		digest = d
	} else {
		slog.Warn("Failed to resolve OCI digest", "ref", resolvedRef, "error", resolveErr)
	}

	// Populate RawYAML from bundle FS if the client didn't set it
	rawYAML := bundle.RawYAML
	if len(rawYAML) == 0 && bundle.FS != nil {
		if data, readErr := fs.ReadFile(bundle.FS, "pacto.yaml"); readErr == nil {
			rawYAML = data
		}
	}

	return &LoadResult{
		Contract:       bundle.Contract,
		RawYAML:        rawYAML,
		BundleFS:       bundle.FS,
		ResolvedRef:    resolvedRef,
		ResolvedDigest: digest,
	}, nil
}

// ListTags returns all semver tags for an OCI repository, sorted descending.
func (p *OCIPuller) ListTags(ctx context.Context, ref string, authOverride *authn.AuthConfig) ([]string, error) {
	ref = strings.TrimPrefix(ref, "oci://")
	// Strip digest
	if idx := strings.Index(ref, "@"); idx >= 0 {
		ref = ref[:idx]
	}
	// Strip any existing tag
	if idx := strings.LastIndex(ref, ":"); idx > strings.LastIndex(ref, "/") {
		ref = ref[:idx]
	}

	kc := p.effectiveKeychain(authOverride)
	client := oci.NewClient(kc)

	tags, err := client.ListTags(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags for %q: %w", ref, err)
	}

	return oci.FilterSemverTags(tags), nil
}

// effectiveKeychain returns a keychain that prepends the override credentials (if any)
// before the default keychain configured at OCIPuller construction time.
func (p *OCIPuller) effectiveKeychain(authOverride *authn.AuthConfig) authn.Keychain {
	if authOverride == nil {
		return p.keychain
	}
	return authn.NewMultiKeychain(
		staticKeychain{auth: authOverride},
		p.keychain,
	)
}

// staticKeychain returns fixed credentials for all registries.
type staticKeychain struct {
	auth *authn.AuthConfig
}

func (k staticKeychain) Resolve(_ authn.Resource) (authn.Authenticator, error) {
	return authn.FromConfig(*k.auth), nil
}
