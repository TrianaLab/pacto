package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// isRecordRef says whether a record argument is a registry reference
// rather than a record id: a reference names a repository.
func isRecordRef(arg string) bool { return strings.Contains(arg, "/") }

// recordClient reads records by registry reference with no cluster: no
// kubeconfig is read. It verifies with --trust and, for an approval a person
// signed, with the approver signers of the identity policy --signers names.
func recordClient(cmd *cobra.Command, d deps) (*app.Delivery, error) {
	trust, err := app.DeliveryTrust(trustPath(cmd))
	if err != nil {
		return nil, err
	}
	policy, err := signersPolicy(cmd)
	if err != nil {
		return nil, err
	}
	return &app.Delivery{Records: d.newRecordRepo, Trust: trust, Policy: policy, Now: time.Now}, nil
}

// signersPolicy is the identity policy --signers names, JSON or YAML, as the
// chart publishes it; none is the default policy, which lists no signer.
func signersPolicy(cmd *cobra.Command) (delivery.ClassPolicy, error) {
	path, _ := cmd.Flags().GetString("signers")
	if path == "" {
		return delivery.DefaultClassPolicy(), nil
	}
	raw, err := os.ReadFile(path)
	var doc any
	if err == nil {
		err = yaml.Unmarshal(raw, &doc)
	}
	if err != nil {
		return delivery.ClassPolicy{}, fmt.Errorf("--signers: %w", err)
	}
	j, _ := json.Marshal(doc) // decoded from YAML: plain data
	p, err := delivery.ParseClassPolicy(j)
	if err != nil {
		return delivery.ClassPolicy{}, fmt.Errorf("--signers %s: %w", path, err)
	}
	return p, nil
}

const signersFlagHelp = "the installation's identity policy (JSON or YAML, the pacto-delivery-identities ConfigMap's policy) " +
	"whose signers verify an approval a person signed; used with a record reference"

// runRecordAt reads the record at ref with no cluster and prints it. verify
// fails when it does not verify.
func runRecordAt(cmd *cobra.Command, v *viper.Viper, d deps, ref string, verify bool) error {
	for _, f := range []string{"stage", "all"} {
		if cmd.Flags().Changed(f) {
			return fmt.Errorf("pass a record reference or --%s, not both", f)
		}
	}
	c, err := recordClient(cmd, d)
	if err != nil {
		return err
	}
	at, err := c.RecordAt(cmd.Context(), ref)
	if err != nil {
		return err
	}
	err = formatResult(cmd, v.GetString(outputFormatKey), at, func() error {
		printRecordAt(cmd.OutOrStdout(), at)
		return nil
	}, nil)
	if err == nil && verify && !at.Verified {
		err = fmt.Errorf("the record at %s does not verify: %s", ref, at.Note)
	}
	return err
}

// printRecordAt prints a record read by reference: an approval on one line
// with who signed it and whether it counted, an intent with how to approve
// it, an operation record as record get prints one.
func printRecordAt(w io.Writer, at *delivery.RecordAt) {
	switch {
	case at.Approval != nil:
		p := at.Approval.Predicate
		who, result := at.Signer, at.Note
		if !at.Verified {
			who = "a " + p.Signer.Kind + " (unverified)"
		} else {
			result = "decision recorded (signature verified); " + countedPhrase(at)
		}
		_, _ = fmt.Fprintf(w, "approval %s on %s by %s: %s\n", p.Decision, p.Operation.Name, who, result)
		_, _ = fmt.Fprintf(w, "as of %s\n", at.AsOf)
	case at.Intent != nil:
		p := at.Intent.Predicate
		_, _ = fmt.Fprintf(w, "intent %s on %s/%s asked by %s: %s\n", p.Intent.Name, p.Intent.Stage.Namespace, p.Intent.Stage.Name, p.Actor.User, at.Note)
		if at.Verified && p.Approvals != "" {
			_, _ = fmt.Fprintf(w, "approve it with: pacto delivery approve --intent %s --trust <recorder public key> --signers <identity policy>\n", at.Ref)
		}
		_, _ = fmt.Fprintf(w, "as of %s\n", at.AsOf)
	default:
		printRecordView(w, at.Operation)
	}
}

// countedPhrase says whether a verified approval counted.
func countedPhrase(at *delivery.RecordAt) string {
	if at.Counted == delivery.CountedUnknown {
		return "unknown: it counted only if the operation record lists it (" + at.CountedWhy + ")"
	}
	return at.Counted
}

// errApproveOne is the approve command's argument rule.
var errApproveOne = errors.New("pass an operation name or --intent <record reference>, not both")

// approveIntent approves the operation an intent record names, with no
// cluster: the intent must verify with --trust, and the decision is signed
// keyless with a fresh login under a signer --signers names.
func approveIntent(cmd *cobra.Command, v *viper.Viper, d deps, svc *app.Service, ref, decision, note string) error {
	if keyless, _ := cmd.Flags().GetBool("keyless"); !keyless {
		return errors.New("--intent always signs keyless, with no cluster to record in; drop --keyless=false or approve with a kubeconfig by operation name")
	}
	c, err := recordClient(cmd, d)
	if err != nil {
		return err
	}
	if registry, _ := cmd.Flags().GetString("registry"); registry != "" && trustPath(cmd) == "" {
		if err := memberIntentTrust(cmd, d, svc, c, ref); err != nil {
			return err
		}
	}
	in, err := c.IntentAt(cmd.Context(), ref)
	if err != nil {
		return err
	}
	issuer := v.GetString("delivery.oidc-issuer-url")
	signer, ok := c.Policy.SignerFor(issuer)
	if issuer == "" && len(c.Policy.Signers) == 1 {
		signer, ok = c.Policy.Signers[0], true
	}
	switch {
	case !ok && issuer != "":
		return fmt.Errorf("the identity policy --signers names no signer for the issuer %s", issuer)
	case !ok:
		return errors.New("approving from an intent needs a signer: pass --signers <file> with the installation's identity policy " +
			"(and --oidc-issuer-url when it lists several), or approve with a kubeconfig")
	case len(c.Policy.HumanGroups) > 0:
		// A keyless identity carries no groups, so ApproverIdentity would call
		// it an agent; say why before the browser login.
		return errors.New("humanGroups is set, and a signed approval carries no groups, so it would never count: approve with a kubeconfig")
	}
	op := in.Predicate.Intent
	s, email, err := keylessLogin(cmd, v, d, signer, decision, op.Stage.Namespace+"/"+op.Name)
	if err != nil {
		return err
	}
	r, err := c.ApproveIntent(cmd.Context(), in, delivery.Decision(decision), note, s, signer.Issuer, email)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "approval signed by %s (keyless) on %s/%s (Stage %s), pushed to the approvals repository %s\nrecord %s at %s\n"+
		"the promoter decides whether it counts (a Stage that asks for a fresh login, or owner teams that narrow who may answer, never count it); "+
		"check with: pacto delivery record verify %s\n",
		email, op.Stage.Namespace, op.Name, op.Stage.Name, in.Predicate.Approvals, r.Digest, r.Ref, r.Ref)
	return nil
}
