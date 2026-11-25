package app

import (
	"context"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// WriteClusterGroupMapping signs rec, a person's group mapping for a member,
// with signer and pushes it on the member's join stream in repo, its
// pacto-targets repository, through the ambient registry credentials. It
// returns where the record is and the form it took.
func (s *Service) WriteClusterGroupMapping(ctx context.Context, rec delivery.ClusterGroupMapping, signer delivery.Signer, repo string) (string, capabilities.ReferrersMode, error) {
	if err := rec.Validate(); err != nil {
		return "", "", err
	}
	ctx, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	r, err := openTargets(repo)
	if err != nil {
		return "", "", err
	}
	signed, err := delivery.SignClusterGroupMapping(ctx, signer, delivery.NewClusterGroupMappingStatement(rec))
	if err != nil {
		return "", "", err
	}
	return deliveryrecord.PushClusterGroupMapping(ctx, r, signed)
}
