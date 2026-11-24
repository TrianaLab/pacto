package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/dynamic"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// The member subcommands, for the delivery-member kind scenario:
//
//	joinsign request -root FILE -registry HOST:PORT/acme -context HUB -cluster UID \
//	    -requester oidc:alice@acme.test -email alice@acme.test -issuer URL \
//	    -service billing -environment production -digest sha256:HEX [-reason TEXT] [-ready FILE]
//	joinsign approve-intent -root FILE -registry HOST:PORT/acme -context HUB \
//	    -email bob@acme.test -issuer URL -intent REF [-ready FILE]
//
// request signs one member request as the person, through the same
// (*app.Service).WriteOperationRequests `pacto delivery propose --confirm`
// calls; approve-intent signs a decision on the Operation a member's intent
// record names, through the same MemberTrust, IntentAt and ApproveIntent
// `pacto delivery approve --intent --registry` calls. Both read the hub's
// members with the kubeconfig context -context. Each run has its own
// Sigstore, so the member verifies a run only once its approver signers list
// the run's issuer and root: the command writes the root, prints it and
// waits for -ready to exist before it signs, so the scenario lists the run
// first and the member never reads a request it cannot verify.
const (
	cmdRequest       = "request"
	cmdApproveIntent = "approve-intent"
	// readyWait bounds how long a run waits for its ready file.
	readyWait = 15 * time.Minute
)

// memberCmd is a member subcommand as its flags name it.
type memberCmd struct {
	root, registry, kubeContext, cluster, requester, email, issuer, ready string
	service, environment, digest, reason, intent                          string
}

// hubFunc gives the options the hub's members are read with, for the
// pacto-targets repository repo.
type hubFunc func(ctx context.Context, repo string) (app.MemberPlanOptions, error)

// parseMember reads the flags of the member subcommand cmd.
func parseMember(cmd string, args []string) (memberCmd, error) {
	var m memberCmd
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.StringVar(&m.root, "root", "", "where to write the private Sigstore's trusted_root.json")
	fs.StringVar(&m.registry, "registry", "", "the registry the members publish to (host:port/path)")
	fs.StringVar(&m.kubeContext, "context", "", "the hub's kubeconfig context (default: the current one)")
	fs.StringVar(&m.email, "email", "", "the email the certificate names")
	fs.StringVar(&m.issuer, "issuer", "https://idp.acme.test", "the OIDC issuer the certificate attests")
	fs.StringVar(&m.ready, "ready", "", "a file to wait for before signing")
	fs.StringVar(&m.cluster, "cluster", "", "request: the member's UID")
	fs.StringVar(&m.requester, "requester", "", "request: the Kubernetes user the request names, such as oidc:alice@acme.test")
	fs.StringVar(&m.service, "service", "", "request: the service to promote")
	fs.StringVar(&m.environment, "environment", "", "request: the environment to promote it in")
	fs.StringVar(&m.digest, "digest", "", "request: the sha256: digest to promote")
	fs.StringVar(&m.reason, "reason", "", "request: why, for the record")
	fs.StringVar(&m.intent, "intent", "", "approve-intent: the intent record, <repository>:<tag> or <repository>@<digest>")
	return m, fs.Parse(args)
}

// kubeHub reads the hub through the kubeconfig context name: its uid, its
// approver signers and its approve reviews.
func kubeHub(name string) hubFunc {
	return func(ctx context.Context, repo string) (app.MemberPlanOptions, error) {
		cfg, err := k8sclient.ConfigFor(name)
		if err != nil {
			return app.MemberPlanOptions{}, err
		}
		dyn, err := dynamic.NewForConfig(cfg)
		if err != nil {
			return app.MemberPlanOptions{}, err
		}
		hub, err := kubecluster.ID(ctx, dyn)
		if err != nil {
			return app.MemberPlanOptions{}, err
		}
		return app.MemberPlanOptions{Repository: repo, Hub: hub, Authorize: authn.SubjectAccessReview(dyn),
			Delivery: &app.Delivery{Dynamic: dyn, PolicyNamespace: delivery.PolicyNamespace, Now: time.Now}}, nil
	}
}

// runMember writes ss's trusted root to m.root, waits for m.ready and runs
// the member subcommand cmd signed as m.email from m.issuer.
func runMember(ctx context.Context, ss *testutil.Sigstore, cmd string, m memberCmd, hub hubFunc, out io.Writer) error {
	if m.root == "" || m.registry == "" || m.email == "" ||
		(cmd == cmdRequest && (m.cluster == "" || m.requester == "" || m.service == "" || m.environment == "" || m.digest == "")) ||
		(cmd == cmdApproveIntent && m.intent == "") {
		return errors.New("usage: joinsign request|approve-intent -root FILE -registry HOST:PORT/PATH -email EMAIL " +
			"(request: -cluster UID -requester USER -service NAME -environment ENV -digest sha256:HEX; approve-intent: -intent REF)")
	}
	if err := os.WriteFile(m.root, ss.RootJSON, 0o600); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "signing as %s from %s, trusted root %s\n", m.email, m.issuer, m.root); err != nil {
		return err
	}
	if err := waitReady(ctx, m.ready); err != nil {
		return err
	}
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return testutil.Token(m.email, m.issuer), nil }, ss.Root)
	if err != nil {
		return err
	}
	opts, err := hub(ctx, strings.TrimSuffix(m.registry, "/")+"/"+delivery.TargetSnapshotRepository)
	if err != nil {
		return err
	}
	if cmd == cmdRequest {
		return requestOn(ctx, s, m, opts, out)
	}
	return approveIntent(ctx, s, string(ss.RootJSON), m, opts, out)
}

// waitReady waits until path exists, at most readyWait; "" waits for nothing.
func waitReady(ctx context.Context, path string) error {
	deadline := time.Now().Add(readyWait)
	for path != "" {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s never appeared", path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

// requestOn signs and pushes the member request m names, confirming the
// advertised placement as its requester.
func requestOn(ctx context.Context, s delivery.Signer, m memberCmd, opts app.MemberPlanOptions, out io.Writer) error {
	now := time.Now().UTC()
	r := delivery.OperationRequest{Cluster: m.cluster, Hub: opts.Hub, Service: delivery.ServiceRef{Name: m.service}, Environment: m.environment,
		Digest: m.digest, Reason: m.reason, Requester: m.requester,
		Placement: delivery.RequestPlacement{Clusters: []string{m.cluster}, Source: delivery.RequestPlacedAdvertised, ConfirmedBy: m.requester},
		At:        now.Format(time.RFC3339), Expires: now.Add(delivery.DefaultRequestLifetime).Format(time.RFC3339)}
	refs, mode, err := app.NewService(nil, nil).WriteOperationRequests(ctx, []delivery.OperationRequest{r}, s, opts)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "requested %s of %s in %s on %s as %s; recorded %s (%s)\n", m.digest, m.service, m.environment, m.cluster, m.requester, refs[0], mode)
	return err
}

// approveIntent approves the Operation the intent record m names, which
// verifies under the recorder key its member advertises, signed as m.email
// whose run's trusted root is root.
func approveIntent(ctx context.Context, s delivery.Signer, root string, m memberCmd, opts app.MemberPlanOptions, out io.Writer) error {
	trust, _ := app.NewService(nil, nil).MemberTrust(ctx, opts)
	records := func(path string, hosts ...string) (*remote.Repository, error) {
		return ocireferrer.Open(path, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()),
			Insecure: append(oci.EnvInsecureRegistries(), hosts...)})
	}
	d := &app.Delivery{Records: records, Trust: trust, Now: time.Now, Policy: delivery.ClassPolicy{Signers: []delivery.PersonSigner{
		{Issuer: m.issuer, UsernamePrefix: "oidc:", Subjects: `[^@]+@acme\.test`, TrustedRoot: root}}}}
	in, err := d.IntentAt(ctx, m.intent)
	if err != nil {
		return err
	}
	r, err := d.ApproveIntent(ctx, in, delivery.Approve, "approved from the hub", s, m.issuer, m.email)
	if err != nil {
		return err
	}
	op := in.Predicate.Intent
	_, err = fmt.Fprintf(out, "approved %s/%s as %s; recorded %s\n", op.Stage.Namespace, op.Name, m.email, r.Ref)
	return err
}
