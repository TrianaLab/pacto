// Command joinsign plays a person approving or removing a member cluster
// against a private Sigstore, so the cluster-join kind scenario writes the
// keyless record `pacto cluster approve` writes, without a browser login.
//
//	joinsign -root FILE -registry HOST:PORT/acme -cluster UID -hub UID \
//	    -approver oidc:alice@acme.test -email alice@acme.test -key sha256:HEX \
//	    -public-key BASE64 [-issuer URL] [-remove] [-name NAME]
//
// It starts a private Sigstore (pkg/signing/testutil, as keylesssign does),
// writes its trusted_root.json to FILE and calls (*app.Service).WriteClusterJoin
// with a keyless signer for -email from -issuer. The record goes to
// <registry>/pacto-targets with the ambient registry credentials
// (PACTO_REGISTRY_* or the docker config, PACTO_INSECURE_REGISTRIES for plain
// HTTP), after the re-read of the member's snapshot the CLI also runs: a
// snapshot no longer signed by -key is refused with ClusterKeyMismatch and
// nothing is written. -key and -public-key are the member's snapshotKeyID and
// snapshotKey from its <release>-detected ConfigMap. The Sigstore lives only
// while the command runs, so every run has its own root: the hub lists one
// approver signer per run, each with its own issuer and trustedRoot.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
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

// request is one decision as the flags name it.
type request struct {
	root, registry, cluster, hub, approver, email, key, publicKey, issuer, name string
	remove                                                                      bool
}

func main() {
	var r request
	flag.StringVar(&r.root, "root", "", "where to write the private Sigstore's trusted_root.json")
	flag.StringVar(&r.registry, "registry", "", "the registry the members publish to (host:port/path)")
	flag.StringVar(&r.cluster, "cluster", "", "the member cluster's UID")
	flag.StringVar(&r.hub, "hub", "", "the hub cluster's UID")
	flag.StringVar(&r.approver, "approver", "", "the Kubernetes user the record names, such as oidc:alice@acme.test")
	flag.StringVar(&r.email, "email", "", "the email the certificate names")
	flag.StringVar(&r.key, "key", "", "the member key the person checked, sha256:<hex>")
	flag.StringVar(&r.publicKey, "public-key", "", "that key: base64 of its PKIX DER (the member's snapshotKey)")
	flag.StringVar(&r.issuer, "issuer", "https://idp.acme.test", "the OIDC issuer the certificate attests")
	flag.StringVar(&r.name, "name", "", "display name override, a DNS label")
	flag.BoolVar(&r.remove, "remove", false, "sign a removal instead of an approval")
	flag.Parse()
	if err := run(context.Background(), testutil.New(tb{}), r, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

// run writes ss's trusted root to r.root and records r's decision signed as
// r.email from r.issuer, printing where the record is.
func run(ctx context.Context, ss *testutil.Sigstore, r request, out io.Writer) error {
	if r.root == "" || r.registry == "" || r.cluster == "" || r.hub == "" || r.approver == "" || r.email == "" || r.key == "" || r.publicKey == "" {
		return errors.New("usage: joinsign -root FILE -registry HOST:PORT/PATH -cluster UID -hub UID -approver USER -email EMAIL -key sha256:HEX -public-key BASE64 [-issuer URL] [-remove] [-name NAME]")
	}
	if err := os.WriteFile(r.root, ss.RootJSON, 0o600); err != nil {
		return err
	}
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return testutil.Token(r.email, r.issuer), nil }, ss.Root)
	if err != nil {
		return err
	}
	rec := delivery.ClusterJoin{Cluster: r.cluster, Hub: r.hub, KeyID: strings.TrimPrefix(r.key, "sha256:"), PublicKey: r.publicKey,
		Decision: delivery.JoinApprove, Name: r.name, Approver: r.approver, At: time.Now().UTC().Format(time.RFC3339)}
	if r.remove {
		rec.Decision = delivery.JoinRemove
	}
	ref, mode, err := app.NewService(nil, nil).WriteClusterJoin(ctx, rec, s, strings.TrimSuffix(r.registry, "/")+"/"+delivery.TargetSnapshotRepository)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%s %s as %s; recorded %s (%s)\n", rec.Decision, r.cluster, r.approver, ref, mode)
	return err
}
