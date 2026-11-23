package app

import (
	"context"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// PublishInferredOptions configures PublishInferred: the fleet sources, the
// registry the installation repositories live under and the installation
// key, as its signer and a verifier of that key.
type PublishInferredOptions struct {
	Fleet    FleetOptions
	Registry string
	Signer   publish.Signer
	Verify   publish.BundleVerifier
}

// PublishInferred publishes the inferred contract of every external system
// the fleet snapshot knows under <registry>/pacto-inferred, when its surface
// changed: the writer of an installation that runs no operator.
func (s *Service) PublishInferred(ctx context.Context, opts PublishInferredOptions) (inferredcontract.Report, error) {
	snap, err := s.Fleet(ctx, opts.Fleet.withoutListings())
	if err != nil {
		return inferredcontract.Report{}, err
	}
	env := s.env()
	root := oci.InstallationRepository(contract.TrimOCIScheme(opts.Registry), contract.InferredRepository)
	return inferredcontract.Publish(ctx, inferredcontract.Deps{Registry: env.publishRegistry(), Signer: opts.Signer, Verify: opts.Verify,
		Pacto: s.buildVersion(), Now: env.now}, root, snap)
}
