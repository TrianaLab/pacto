package app

import (
	"context"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// refusalTimeout bounds signing and pushing one refusal record.
var refusalTimeout = 30 * time.Second

// defaultRefusalRepo opens the contract bundle repository a refusal is kept
// in, with the CLI's credentials and plain-HTTP hosts.
func defaultRefusalRepo(path string) (*remote.Repository, error) {
	return ocireferrer.Open(path, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()), Insecure: oci.EnvInsecureRegistries()})
}

// keepRefusal signs a version-rule refusal with the publish signer and keeps
// it in the contract bundle repository, where the dashboard reads it. It
// returns what to tell the person who ran publish, or "" when the version
// rule did not refuse. It never changes the outcome: a refusal that could not
// be kept is said beside it.
func keepRefusal(ctx context.Context, env sysDeps, tools execTools, b *contract.Bundle, p publish.Plan) string {
	r, ok := publish.VersionRefusal(p, env.now())
	if !ok {
		return ""
	}
	pub := b.Contract.Publish
	if pub.Sign == nil {
		return "the refusal was not kept: publish.sign is not configured, and a kept refusal is signed"
	}
	ctx, cancel := context.WithTimeout(ctx, refusalTimeout)
	defer cancel()
	repo := publish.Repo(pub.Bundle)
	if err := pushRefusal(ctx, env, tools, b, *pub.Sign, repo, r); err != nil {
		return "the refusal was not kept in " + repo + ": " + err.Error()
	}
	return "the refusal is kept, signed, in " + repo
}

func pushRefusal(ctx context.Context, env sysDeps, tools execTools, b *contract.Bundle, sign contract.PublishSign, repo string, r delivery.PublishRefusal) error {
	digest, err := oci.BundleDigest(b)
	if err != nil {
		return err
	}
	s, _, err := tools.signing(ctx, sign)
	if err != nil {
		return err
	}
	bundle, _, err := delivery.SignRefusal(ctx, s, delivery.NewRefusalStatement(digest, r))
	if err != nil {
		return err
	}
	rr, err := env.refusalRepo(repo)
	if err != nil {
		return err
	}
	return deliveryrecord.PushRefusal(ctx, rr, bundle)
}
