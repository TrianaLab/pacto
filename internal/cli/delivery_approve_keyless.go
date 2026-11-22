package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// approveRoute says whether a decision on Stage st is signed keyless, with
// the signer of issuer (or the only one configured), and why not when it is
// recorded in the cluster instead. A keyless identity carries no groups and
// no login time, so a Stage that needs either keeps the cluster path.
func approveRoute(p delivery.ClassPolicy, st *delivery.StageObject, issuer string) (delivery.PersonSigner, string) {
	signer, ok := pickSigner(p, issuer)
	switch {
	case st.Status.Stream.Approvals == "":
		return signer, "the Stage keeps no approvals repository"
	case !ok:
		return signer, "the installation accepts no signed approvals from this issuer (delivery.identities.signers)"
	case delivery.PublishedFreshness(st.Status.AuthFreshness) > 0:
		return signer, "the Stage asks for a fresh login, which a certificate cannot show"
	case len(p.HumanGroups) > 0:
		return signer, "humanGroups is set, and a signed approval carries no groups"
	case p.TeamNarrowing:
		return signer, "owner teams narrow who may answer, and a signed approval carries no groups"
	}
	return signer, ""
}

// pickSigner is the approver signer of issuer, or the only one configured
// when issuer is empty.
func pickSigner(p delivery.ClassPolicy, issuer string) (delivery.PersonSigner, bool) {
	if issuer == "" && len(p.Signers) == 1 {
		return p.Signers[0], true
	}
	return p.SignerFor(issuer)
}

// ambientTokens are the variables that hand a process a token it did not
// log in for. Approving never reads them, and refuses while they are set.
var ambientTokens = []string{"SIGSTORE_ID_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"}

// keylessLogin logs the person in again in their browser and returns a
// keyless signer bound to that login alone, with the email it names. what
// is the operation the decision answers, as the prompt names it.
func keylessLogin(cmd *cobra.Command, v *viper.Viper, d deps, signer delivery.PersonSigner, decision, what string) (delivery.Signer, string, error) {
	for _, env := range ambientTokens {
		if os.Getenv(env) != "" {
			return nil, "", fmt.Errorf("approving needs your own fresh login; unset %s", env)
		}
	}
	if signer.TrustedRoot == "" {
		return nil, "", fmt.Errorf("the signer for %s names no trustedRoot, so a signed approval would verify against nothing", signer.Issuer)
	}
	tr, err := root.NewTrustedRootFromJSON([]byte(signer.TrustedRoot))
	if err != nil {
		return nil, "", fmt.Errorf("the trusted root of the signer for %s: %w", signer.Issuer, err)
	}
	client, err := authn.IssuerClient(signer.Issuer, "", v.GetString("delivery.oidc-ca-file"))
	if err != nil {
		return nil, "", err
	}
	token, err := d.freshLogin(cmd.Context(), authn.FreshConfig{IssuerURL: signer.Issuer, HTTPClient: client,
		ClientID: cmp.Or(signer.ClientID, v.GetString("delivery.oidc-client-id")), ClientSecret: v.GetString("delivery.oidc-client-secret"),
		ExtraScopes: append(v.GetStringSlice("delivery.oidc-extra-scopes"), "email"), Port: v.GetInt("delivery.oidc-redirect-port"),
		Prompt: func(u string) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "To sign %s on %s, log in again in your browser:\n\n  %s\n\n", decision, what, u)
		}})
	if err != nil {
		return nil, "", err
	}
	// Fulcio puts the token's email in the certificate; without one the SAN
	// would be a URI, which never approves.
	email := authn.Claims{UsernameClaim: "email", UsernamePrefix: "-"}.Person(token).User
	if email == authn.Anonymous.User {
		return nil, "", errors.New("the login's token carries no email claim, which a signed approval names; ask your identity provider for the email scope")
	}
	fulcio, _ := cmd.Flags().GetString("fulcio-url")
	tsa, _ := cmd.Flags().GetString("timestamp-url")
	s, err := signing.Keyless(cmp.Or(signer.FulcioURL, fulcio), cmp.Or(signer.TimestampURL, tsa), "",
		func(context.Context) (string, error) { return token, nil }, tr)
	return s, email, err
}

// approveKeyless signs the person's decision keyless with a fresh login and
// pushes it to the Stage's approvals repository.
func approveKeyless(cmd *cobra.Command, v *viper.Viper, d deps, c *app.Delivery, signer delivery.PersonSigner, st *delivery.StageObject, decision, ns, name, note string) error {
	s, email, err := keylessLogin(cmd, v, d, signer, decision, ns+"/"+name)
	if err != nil {
		return err
	}
	ref, err := c.ApproveSigned(cmd.Context(), ns, name, delivery.Decision(decision), note, s, signer.Issuer, email)
	if err != nil {
		return err
	}
	w, stream := cmd.OutOrStdout(), st.Status.Stream
	if ref.Caveat != "" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ref.Caveat)
	}
	if ref.Mode == capabilities.ReferrersWriteOnceTags {
		repo, dg, _ := strings.Cut(ref.Ref, "@")
		_, _ = fmt.Fprintf(w, "approval signed by %s (keyless) on %s/%s, pushed %s:%s (write-once tag; registry has no Referrers)\n",
			email, ns, name, repo, deliveryrecord.KindTag(stream.ID, deliveryrecord.KindApproval, digest.Digest(dg)))
	} else {
		_, _ = fmt.Fprintf(w, "approval signed by %s (keyless) on %s/%s, attached to the approvals anchor %s\n", email, ns, name, stream.Approvals)
	}
	_, _ = fmt.Fprintf(w, "record %s at %s\n", ref.Digest, ref.Ref)
	return nil
}
