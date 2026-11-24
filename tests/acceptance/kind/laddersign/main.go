// Command laddersign plays a person confirming the installation ladder
// against a private Sigstore, so the delivery-environments kind scenario
// writes the keyless record `pacto delivery ladder confirm` writes, without a
// browser login.
//
//	laddersign -root FILE -view FILE -cluster UID -email EMAIL [-issuer URL]
//
// It starts a private Sigstore (pkg/signing/testutil, as joinsign does),
// writes its trusted_root.json to FILE and signs, as -email from -issuer, the
// record the CLI builds from the installation ladder in -view (the view.json
// of the promoter's pacto-delivery-environments ConfigMap): its declared
// chain, then every rung confirmed before and every proposed rung, each a
// leaf, bound to the view's proposal digest. The record goes on the
// cluster's ladder stream in the approvals repository the view names, with
// the ambient registry credentials (PACTO_REGISTRY_* or the docker config,
// PACTO_INSECURE_REGISTRIES for plain HTTP). The Sigstore lives only while
// the command runs, so the promoter lists this run's issuer and root among
// its approver signers (delivery.identities.signers) afterwards.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// tb is the testing.TB testutil.New asks for, outside a test: a fatal error
// ends the command, and the servers it starts stop when the command exits.
type tb struct{ testing.TB }

func (tb) Helper()                      {}
func (tb) Cleanup(func())               {} // ponytail: the process exit stops the servers
func (tb) Fatal(args ...any)            { log.Fatal(args...) }
func (tb) Fatalf(f string, args ...any) { log.Fatalf(f, args...) }

// request is one confirmation as the flags name it.
type request struct{ root, view, cluster, email, issuer string }

func main() {
	var r request
	flag.StringVar(&r.root, "root", "", "where to write the private Sigstore's trusted_root.json")
	flag.StringVar(&r.view, "view", "", "the view.json of the promoter's pacto-delivery-environments ConfigMap")
	flag.StringVar(&r.cluster, "cluster", "", "the cluster's UID (the kube-system namespace's)")
	flag.StringVar(&r.email, "email", "", "the email the certificate names")
	flag.StringVar(&r.issuer, "issuer", "https://idp.acme.test", "the OIDC issuer the certificate attests")
	flag.Parse()
	if err := run(context.Background(), testutil.New(tb{}), r, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

// run writes ss's trusted root to r.root and records the confirmation of the
// installation ladder in r.view signed as r.email from r.issuer, printing
// where the record is.
func run(ctx context.Context, ss *testutil.Sigstore, r request, out io.Writer) error {
	if r.root == "" || r.view == "" || r.cluster == "" || r.email == "" {
		return errors.New("usage: laddersign -root FILE -view FILE -cluster UID -email EMAIL [-issuer URL]")
	}
	raw, err := os.ReadFile(r.view)
	if err != nil {
		return err
	}
	var view delivery.EnvironmentsView
	if err := json.Unmarshal(raw, &view); err != nil {
		return fmt.Errorf("%s: %w", r.view, err)
	}
	if view.Repository == "" {
		return errors.New("the view names no approvals repository: the promoter reads no ladder records")
	}
	rungs, _, err := app.LadderRungs(view, nil)
	if err != nil {
		return err
	}
	rec := delivery.LadderRecord{ClusterID: r.cluster, Rungs: rungs, ProposalDigest: view.ProposalDigest,
		Signer: delivery.PublisherIdentity{Issuer: r.issuer, Subject: r.email}, At: time.Now().UTC().Format(time.RFC3339)}
	if err := rec.Check(time.Now()); err != nil {
		return err
	}
	if err := os.WriteFile(r.root, ss.RootJSON, 0o600); err != nil {
		return err
	}
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return testutil.Token(r.email, r.issuer), nil }, ss.Root)
	if err != nil {
		return err
	}
	signed, err := delivery.SignLadder(ctx, s, delivery.NewLadderStatement(rec))
	if err != nil {
		return err
	}
	repo, err := ocireferrer.Open(view.Repository, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
	if err != nil {
		return err
	}
	stream := delivery.LadderStream(r.cluster)
	caps, err := deliveryrecord.EnsureAnchor(ctx, repo, stream)
	if err != nil {
		return err
	}
	ref, err := deliveryrecord.Push(ctx, repo, stream, caps.Referrers, delivery.PredicateEnvironmentLadder, signed, nil)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "confirmed %d rungs of %s as %s; recorded %s\n", len(rungs), r.cluster, r.email, ref)
	return err
}
