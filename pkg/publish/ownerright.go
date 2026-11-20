package publish

import (
	"context"
	"errors"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// OwnerRightInput is a published bundle's owner change: where it lives, the
// owner it sets over the service's effective owner, the owner policy and
// the signer class rules its signers are classified with.
type OwnerRightInput struct {
	Registry     Registry
	Root         root.TrustedMaterial
	TLog         bool
	Repo, Digest string
	Service      string
	Proposed     contract.Owner
	Current      delivery.OwnerClaim
	Policy       *delivery.OwnerPolicy
	PolicyFrom   string
	ClassRules   []delivery.SignerClassRule
	Now          time.Time
}

// VerifyOwnerRight judges the owner a published bundle sets from its
// certificates: every keyless signer that verifies (SignersOf), classified
// by the class rules, decided by delivery.JudgeOwnerSigners. No signature,
// or none that verifies, is OWNER_SIGNER_UNVERIFIED, never allowed.
func VerifyOwnerRight(ctx context.Context, in OwnerRightInput) delivery.OwnerRight {
	signers, err := SignersOf(ctx, in.Registry, in.Root, in.TLog, in.Repo, in.Digest)
	why := ""
	switch {
	case errors.Is(err, ErrNoSignature):
		why = "no Sigstore signature is attached to " + in.Digest
	case err != nil:
		why = capVerdictReason("no signature could be verified: " + err.Error())
	}
	for i := range signers {
		signers[i] = delivery.Classified(signers[i], in.ClassRules)
	}
	return delivery.JudgeOwnerSigners(in.Service, signers, why, in.Current,
		delivery.OwnerClaim{Owner: in.Proposed}, in.Policy, in.PolicyFrom, in.Now)
}
