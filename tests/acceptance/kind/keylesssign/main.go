// Command keylesssign plays a CI workflow signing its artifacts keylessly
// against a private Sigstore, so a kind scenario proves the promoter's
// registry watch on real Sigstore bundles without the network or a GitHub
// Actions token.
//
//	keylesssign -root FILE -registry HOST:PORT -subject URI -issuer URL \
//	    -repository URL [-ref REF] REPO@DIGEST...
//
// It starts a private Sigstore (pkg/signing/testutil: a certificate
// authority behind a Fulcio API and a timestamp authority), writes its
// trusted_root.json to FILE, and signs every REPO@DIGEST in the registry at
// HOST:PORT (plain HTTP) the way pacto publish does: a certificate whose URI
// SAN is -subject and whose extensions attest -issuer, -repository and -ref,
// as Fulcio issues a CI workload. The Sigstore lives only while the command
// runs, so every digest it should vouch for is signed in one call; the
// trusted root it wrote keeps verifying them afterwards.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/pkg/publish"
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

func main() {
	root := flag.String("root", "", "where to write the private Sigstore's trusted_root.json")
	host := flag.String("registry", "", "the registry (host:port, plain HTTP) holding the digests")
	subject := flag.String("subject", "", "the certificate's URI SAN: the CI workflow identity")
	issuer := flag.String("issuer", "https://token.actions.githubusercontent.com", "the OIDC issuer the certificate attests")
	repository := flag.String("repository", "", "the source repository the certificate attests")
	ref := flag.String("ref", "refs/heads/main", "the source ref the certificate attests")
	flag.Parse()
	if err := run(context.Background(), testutil.New(tb{}), *root, *host, *subject, *issuer, *repository, *ref, flag.Args()); err != nil {
		log.Fatal(err)
	}
}

// run writes ss's trusted root to root and signs each repo@digest of refs in
// the registry at host as the CI identity subject.
func run(ctx context.Context, ss *testutil.Sigstore, root, host, subject, issuer, repository, ref string, refs []string) error {
	if root == "" || host == "" || subject == "" || repository == "" || len(refs) == 0 {
		return errors.New("usage: keylesssign -root FILE -registry HOST:PORT -subject URI -repository URL REPO@DIGEST")
	}
	if err := os.WriteFile(root, ss.RootJSON, 0o600); err != nil {
		return err
	}
	token := testutil.CIToken(subject, issuer, repository, ref)
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return token, nil }, ss.Root)
	if err != nil {
		return err
	}
	reg := publish.NewClient(authn.DefaultKeychain, host)
	for _, r := range refs {
		repo, digest, ok := strings.Cut(r, "@")
		if !ok {
			return fmt.Errorf("%s: want REPO@DIGEST", r)
		}
		if err := publish.SignDigest(ctx, reg, s, host+"/"+repo, digest); err != nil {
			return fmt.Errorf("signing %s: %w", r, err)
		}
	}
	return nil
}
