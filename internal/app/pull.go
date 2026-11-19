package app

import (
	"context"
	"fmt"
	"os"

	"github.com/sigstore/sigstore/pkg/cryptoutils"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// PullOptions holds options for the pull command.
type PullOptions struct {
	Ref    string
	Output string
	// Verify, when set, is the signature the bundle must carry (a key, or a
	// keyless identity and issuer): one that does not verify refuses the
	// pull before anything is extracted.
	Verify *contract.PublishSign
}

// PullResult holds the result of the pull command.
type PullResult struct {
	Ref     string `json:"ref"`
	Output  string `json:"output"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Pull fetches a contract bundle from an OCI registry and extracts it to disk.
func (s *Service) Pull(ctx context.Context, opts PullOptions) (*PullResult, error) {
	if err := s.requireBundleStore(); err != nil {
		return nil, err
	}

	parsed := graph.ParseDependencyRef(opts.Ref)
	if !parsed.IsOCI() {
		return nil, fmt.Errorf("pull requires an OCI reference (oci://...): got %q", opts.Ref)
	}

	logging.LoggerFromContext(ctx).Debug("resolving OCI reference", "ref", parsed.Location)
	location, err := oci.ResolveRef(ctx, s.BundleStore, parsed.Location, "")
	if err != nil {
		return nil, err
	}

	if opts.Verify != nil {
		if location, err = s.verifyPull(ctx, location, *opts.Verify); err != nil {
			return nil, err
		}
	}

	logging.LoggerFromContext(ctx).Debug("pulling bundle from registry", "ref", location)
	bundle, err := s.BundleStore.Pull(ctx, location)
	if err != nil {
		return nil, err
	}

	output := opts.Output
	if output == "" {
		// The output dir falls back to the REMOTE-controlled service name; refuse a
		// name that would escape the working directory via path traversal.
		if err := safeOutputName(bundle.Contract.Service.Name); err != nil {
			return nil, err
		}
		output = bundle.Contract.Service.Name
	}

	logging.LoggerFromContext(ctx).Debug("extracting bundle to disk", "output", output)
	if err := extractBundleFS(bundle.FS, output); err != nil {
		return nil, fmt.Errorf("failed to extract bundle: %w", err)
	}

	logging.LoggerFromContext(ctx).Debug("pull complete", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)
	return &PullResult{
		Ref:     location,
		Output:  output,
		Name:    bundle.Contract.Service.Name,
		Version: bundle.Contract.Service.Version,
	}, nil
}

// verifyPull resolves location to its digest and checks that digest carries
// a signature that verifies under sign, with the verifier publish uses. It
// returns repo@digest, so the bundle pulled is the one verified.
func (s *Service) verifyPull(ctx context.Context, location string, sign contract.PublishSign) (string, error) {
	reg := s.env().publishRegistry()
	rest, digest := contract.SplitOCIDigest(location)
	repo, _, _ := contract.SplitOCITag(rest)
	if digest == "" {
		r, err := reg.Head(ctx, location)
		if err != nil {
			return "", err
		}
		if r == nil {
			return "", fmt.Errorf("%s does not exist", location)
		}
		digest = r.Digest
	}
	ref := repo + "@" + digest
	p, err := pullPolicy(sign)
	if err == nil {
		err = publish.VerifyDigest(ctx, reg, bundleVerifier(p), repo, digest)
	}
	if err != nil {
		return "", fmt.Errorf("%s is not signed as required, so nothing was pulled: %w", ref, err)
	}
	return ref, nil
}

// pullPolicy is the policy a pulled bundle's signature must verify under:
// the public key file sign.Key, else the keyless identity and issuer under
// public-good Sigstore's trusted root, at a time its timestamp authority or
// transparency log observed.
func pullPolicy(sign contract.PublishSign) (signing.Policy, error) {
	if sign.Key != "" {
		raw, err := os.ReadFile(sign.Key)
		if err != nil {
			return signing.Policy{}, err
		}
		pub, err := cryptoutils.UnmarshalPEMToPublicKey(raw)
		if err != nil {
			return signing.Policy{}, fmt.Errorf("the public key %s: %w", sign.Key, err)
		}
		return signing.Policy{Key: pub}, nil
	}
	tm, err := publicGoodRoot(signing.PublicTUF)
	if err != nil {
		return signing.Policy{}, fmt.Errorf("public-good Sigstore's trusted root: %w", err)
	}
	return signing.Policy{Identity: sign.Identity, Issuer: sign.Issuer, Root: tm}, nil
}
