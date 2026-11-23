package cli

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// newClaimCommand is pacto claim: pacto declare for an external system. It
// plans and publishes through the same declaration, with an owner and no
// workload.
func newClaimCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	var req app.DeclareRequest
	var yes bool
	cmd := &cobra.Command{
		Use:   "claim <identity>",
		Short: "Claim an external system: publish its declared contract with an owner, signed by you",
		Long: `Claims an external system the fleet lists, such as stripe or
aws/rds/eu-west-1/orders-db, for a team. The claim is a declared contract:
service.external names the system, service.owner.team the team, and its
pacto.inferred.yaml declaration names inferredFrom external:<identity>. It is
published to <registry>/<name>, where the name is --name or the identity's
last segment. No workload is read and nothing is copied from the inferred
contract Pacto publishes under pacto-inferred; the kubeconfig cluster, when
it is reachable, is read only for the installation signer policy.

Without --yes it prints the plan and writes nothing; the plan ends with its
intent digest. --yes prints the plan, then publishes it signed keyless by you
through an interactive Sigstore login (a browser, or a device code with
--device). No Git access is needed. Only that login writes a declared
version: a key, an ambient OIDC token or a CI identity writes the next
draft-<n> tag instead, and the output names the command a person runs to
accept it with --from-draft.

Naming the first owner of an external system takes a default approver of the
installation's owner policy; changing it takes a person who belongs to the
current owner or a default approver. Anyone else is refused before anything is
pushed. The owner policy is read from the signed target snapshots in
<registry>/pacto-targets, verified with --delivery-trust. The fleet routes an
unclaimed external system to the default approvers for routing only: routing
grants no right.

--offering names the platform offering the system is meant to join, such as
postgresql. It is written as the display-only metadata
run.pacto.external.offering, never as service.offering, so the claim never
lists the system as a provider of the offering. The plan says how many
providers the offering has and who owns them, read from the offerings
published under --registry; joining an offering another team provides writes
a draft-<n> that a person of that team accepts with --from-draft. A claim
changes no managed resource: it stays outside contract until a provisioning
record from the offering's provider names it.`,
		Example: `  # What claiming stripe for team-platform would publish
  pacto claim stripe --owner team-platform --registry ghcr.io/acme

  # Claim it, signed by you
  pacto claim stripe --owner team-platform --registry ghcr.io/acme --yes

  # Claim an RDS instance and name the offering it is meant to join
  pacto claim aws/rds/123456789012/eu-west-1/orders-db --owner team-shop --offering postgresql --registry ghcr.io/acme

  # Accept a claim an agent wrote as a draft
  pacto claim stripe --registry ghcr.io/acme --from-draft draft-1 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if req.Draft && req.FromDraft != "" {
				return errDraftAndFromDraft
			}
			// An accepted draft names its owner; anything else claims for one.
			if req.Owner == "" && req.FromDraft == "" {
				return errors.New("claiming " + args[0] + " names its owner: pass --owner <team>")
			}
			req.Service = fleet.ExternalSubjectPrefix + args[0]
			// The cluster holds only the installation signer policy here: a
			// client that fails leaves it unread, which the plan says.
			req.Dynamic, _ = declareDynamic(req)
			return declareRun(cmd, svc, v, d, req, yes)
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Owner, "owner", "", "the team that owns the external system (taken from the draft with --from-draft)")
	f.StringVar(&req.Name, "name", "", "the service name the claim is published under (default: the identity's last segment)")
	f.StringVar(&req.Offering, "offering", "", "the platform offering the system is meant to join, written as display-only metadata (taken from the draft with --from-draft)")
	declareFlags(f, &req, &yes)
	return cmd
}
