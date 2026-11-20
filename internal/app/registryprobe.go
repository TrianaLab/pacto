package app

import (
	"context"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// NewRegistryProbe probes the registry of a ref with the environment's
// credentials and plain-HTTP hosts, through the process prober, so each host
// is probed once per process. It is the only place the fleet path meets oras-go.
func NewRegistryProbe() fleetsrc.RegistryProbe {
	kc, insecure := oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries()
	return func(ctx context.Context, ref string) (capabilities.Capabilities, error) {
		r, err := name.ParseReference(contract.TrimOCIScheme(ref))
		if err != nil {
			return capabilities.Capabilities{}, err
		}
		repo, err := ocireferrer.Open(r.Context().Name(), ocireferrer.Options{Keychain: kc, Insecure: insecure})
		if err != nil {
			return capabilities.Capabilities{}, err
		}
		return ocireferrer.Probes.Probe(ctx, repo, r.Identifier(), false)
	}
}
