package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// ladderGrant is the grant a person needs, to their own user, to confirm
// the installation ladder.
const ladderGrant = "kubectl create clusterrole pacto-ladder-approver --verb=approve --resource=environmentladders.delivery.pacto.trianalab.io, bound to your user"

func newDeliveryLadderCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ladder",
		Short: "Confirm the installation ladder, the environments Pacto creates Stages for",
	}
	confirm := &cobra.Command{
		Use:   "confirm",
		Short: "Confirm the installation ladder Pacto proposes, signed as you",
		Long: "Confirm the installation ladder Pacto proposes: you sign it as yourself, and the promoter then creates Stages for the confirmed rungs. " +
			"The ladder is the chart value delivery.environments.ladder plus the rungs Pacto proposes from namespaces and cluster names, " +
			"each a leaf that takes its versions from the registry. pacto delivery inbox shows the proposal and its digest. " +
			"The command signs keyless after a fresh login in your browser and records the confirmation in the approvals repository the promoter reads. " +
			"You need approve on environmentladders.delivery.pacto.trianalab.io in the cluster, granted to your user (" + ladderGrant + "); " +
			"groups are not checked, because a certificate names none. The operator must list approver signers in delivery.identities.signers. " +
			"With --create-namespaces you also let Pacto create the namespaces it waits to place services in, " +
			"when delivery.environments.createNamespaces is on: you must be allowed to read them in the ConfigMap " + delivery.PlacementsConfigMap + ".",
		Example: "  pacto delivery ladder confirm --proposal sha256:5be1...\n  pacto delivery ladder confirm --proposal sha256:5be1... --drop qa\n" +
			"  pacto delivery ladder confirm --create-namespaces",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return confirmLadder(cmd, v, d)
		},
	}
	confirm.Flags().String("proposal", "", "the proposal digest pacto delivery inbox showed you; nothing is signed when the proposal changed since")
	confirm.Flags().StringArray("drop", nil, "a proposed rung to leave out, repeatable; Pacto never proposes it again")
	confirm.Flags().String("issuer", "", "the approver signer's OIDC issuer, when the installation lists several")
	confirm.Flags().Bool("create-namespaces", false, "also let Pacto create the namespaces it waits to place services in; you sign the list you can read")
	cmd.AddCommand(confirm)
	return cmd
}

// ladderConfirmation is what pacto delivery ladder confirm signed: the
// installation ladder in order, the rungs dropped, who signed it and where
// the record is. Note says why nothing was signed.
type ladderConfirmation struct {
	Ladder  []delivery.LadderRung `json:"ladder"`
	Dropped []string              `json:"dropped,omitempty"`
	// Namespaces are the namespaces the person let Pacto create.
	Namespaces []string                   `json:"namespaces,omitempty"`
	By         string                     `json:"by,omitempty"`
	Record     string                     `json:"record,omitempty"`
	Mode       capabilities.ReferrersMode `json:"mode,omitempty"`
	Note       string                     `json:"note,omitempty"`
}

// ladderConfirm is a confirmation on its way to a signed record.
type ladderConfirm struct {
	cmd     *cobra.Command
	v       *viper.Viper
	d       deps
	dyn     dynamic.Interface
	c       *app.Delivery
	view    delivery.EnvironmentsView
	cluster string
	policy  delivery.ClassPolicy
	signer  delivery.PersonSigner
	user    string
	// namespaces are the namespace candidates --create-namespaces signs.
	namespaces []string
}

// confirmLadder signs the person's confirmation of the proposal the
// promoter publishes, minus --drop, and records it. Every check that can
// refuse runs before the login.
func confirmLadder(cmd *cobra.Command, v *viper.Viper, d deps) error {
	l := &ladderConfirm{cmd: cmd, v: v, d: d}
	var err error
	if l.dyn, err = d.newDynamic(); err != nil {
		return err
	}
	if l.c, _, err = deliveryClientFor(cmd, d, l.dyn); err != nil {
		return err
	}
	if l.view, err = l.c.LadderView(cmd.Context()); err != nil {
		return err
	}
	format := v.GetString(outputFormatKey)
	if create, _ := cmd.Flags().GetBool("create-namespaces"); create {
		if l.namespaces, err = l.c.NamespaceCandidates(cmd.Context()); err != nil {
			return err
		}
		if len(l.namespaces) == 0 {
			return errors.New("no namespace waits to be created: Pacto creates namespaces only with delivery.environments.createNamespaces on")
		}
	}
	if len(l.view.Proposal) == 0 && len(l.namespaces) == 0 {
		res := ladderConfirmation{Ladder: slices.Concat(l.view.Declared, l.view.Confirmed), Note: "nothing to confirm"}
		return formatResult(cmd, format, res, func() error {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "nothing to confirm: the installation ladder is %s\n", ladderLine(res.Ladder))
			return nil
		}, nil)
	}
	rec, err := l.prepare()
	if err != nil {
		return err
	}
	res, err := l.sign(rec)
	if err != nil {
		return err
	}
	return formatResult(cmd, format, res, func() error { l.print(res); return nil }, nil)
}

// prepare refuses what would not count and returns the record to sign,
// with no signer and no time yet.
func (l *ladderConfirm) prepare() (delivery.LadderRecord, error) {
	shown, _ := l.cmd.Flags().GetString("proposal")
	drop, _ := l.cmd.Flags().GetStringArray("drop")
	issuer, _ := l.cmd.Flags().GetString("issuer")
	ctx, view := l.cmd.Context(), l.view
	if shown != "" && shown != view.ProposalDigest {
		return delivery.LadderRecord{}, app.ErrLadderChanged
	}
	rungs, dropped, err := app.LadderRungs(view, drop)
	if err != nil {
		return delivery.LadderRecord{}, err
	}
	if view.Repository == "" {
		return delivery.LadderRecord{}, errors.New("the promoter reads no ladder records: it needs the recorder's approvals repository (delivery.recorder) on the pacto-operator chart")
	}
	if l.cluster, err = kubecluster.ID(ctx, l.dyn); err != nil {
		return delivery.LadderRecord{}, err
	}
	if view.Stream != delivery.LadderStream(l.cluster) {
		return delivery.LadderRecord{}, fmt.Errorf("the installation ladder names the stream %s, not one of this cluster %s", view.Stream, l.cluster)
	}
	if err := l.pickSigner(issuer); err != nil {
		return delivery.LadderRecord{}, err
	}
	g := approverGrant{res: authn.EnvironmentLadderResource, where: "the cluster", forbidden: "LadderConfirmForbidden", needsUser: "LadderConfirmNeedsUserGrant", grant: ladderGrant}
	if l.user, err = g.check(l.cmd, l.dyn, authn.SubjectAccessReview(l.dyn)); err != nil {
		return delivery.LadderRecord{}, err
	}
	return delivery.LadderRecord{ClusterID: l.cluster, Rungs: rungs, ProposalDigest: view.ProposalDigest, Dropped: dropped,
		NamespacesDigest: delivery.NamespacesDigest(l.namespaces)}, nil
}

// pickSigner picks the installation's approver signer of issuer.
func (l *ladderConfirm) pickSigner(issuer string) error {
	p, err := l.c.ClassPolicy(l.cmd.Context())
	if err != nil {
		return fmt.Errorf("the installation's approver signers could not be read: %w", err)
	}
	l.policy = p
	if len(p.Signers) == 0 {
		return errors.New("this installation accepts no signed approvals: set delivery.identities.signers on the pacto-operator chart")
	}
	s, ok := pickSigner(p, issuer)
	if !ok {
		var issuers []string
		for _, s := range p.Signers {
			issuers = append(issuers, s.Issuer)
		}
		return fmt.Errorf("pick the approver signer with --issuer: the installation accepts %s", strings.Join(issuers, ", "))
	}
	l.signer = s
	return nil
}

// sign logs the person in and writes rec signed by them.
func (l *ladderConfirm) sign(rec delivery.LadderRecord) (ladderConfirmation, error) {
	_, _ = fmt.Fprintf(l.cmd.ErrOrStderr(), "Confirming the installation ladder %s\n", ladderLine(rec.Rungs))
	s, email, err := personLogin(l.cmd, l.v, l.d, l.signer, l.policy, l.user, "a confirmation", "the installation ladder of cluster "+l.cluster)
	if err != nil {
		return ladderConfirmation{}, err
	}
	rec.Signer = delivery.PublisherIdentity{Issuer: l.signer.Issuer, Subject: email}
	rec.At = l.c.Now().UTC().Format(time.RFC3339)
	ref, mode, err := l.c.WriteLadder(l.cmd.Context(), rec, s, l.view.Repository)
	if err != nil {
		return ladderConfirmation{}, err
	}
	return ladderConfirmation{Ladder: rec.Rungs, Dropped: rec.Dropped, Namespaces: l.namespaces, By: l.user, Record: ref, Mode: mode}, nil
}

// print prints what was confirmed, and the write-once tag the record sits
// under when the registry has no Referrers.
func (l *ladderConfirm) print(res ladderConfirmation) {
	w := l.cmd.OutOrStdout()
	_, _ = fmt.Fprintf(w, "confirmed %s as %s; recorded %s\n", ladderLine(res.Ladder), res.By, res.Record)
	if len(res.Namespaces) > 0 {
		_, _ = fmt.Fprintf(w, "allowed Pacto to create the namespaces %s\n", strings.Join(res.Namespaces, ", "))
	}
	if res.Mode == capabilities.ReferrersWriteOnceTags {
		_, dg, _ := strings.Cut(res.Record, "@")
		_, _ = fmt.Fprintf(w, "pushed %s:%s (write-once tag; registry has no Referrers)\n", l.view.Repository,
			deliveryrecord.KindTag(l.view.Stream, deliveryrecord.KindLadder, digest.Digest(dg)))
	}
}

// ladderLine is an installation ladder as one line: the chain joined by ->,
// then each leaf after a +.
func ladderLine(rungs []delivery.LadderRung) string {
	var b strings.Builder
	for i, r := range rungs {
		switch {
		case i == 0:
		case r.Upstream != "":
			b.WriteString(" -> ")
		default:
			b.WriteString(" + ")
		}
		b.WriteString(r.Name)
	}
	return b.String()
}
