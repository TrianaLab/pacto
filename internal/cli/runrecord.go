package cli

import (
	"context"
	"time"

	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

// statsTimeout bounds one run-record publish, so a slow registry never holds
// a pipeline. A var so tests can shorten it.
var statsTimeout = 30 * time.Second

// defaultPublishRunRecord publishes r to the stats repository repo with the
// shared keychain and insecure-host list, and returns "<repo>@<digest>".
func defaultPublishRunRecord(ctx context.Context, repo string, r runrecord.Record) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, statsTimeout)
	defer cancel()
	target, err := registry.Open(repo, oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries())
	if err != nil {
		return "", err
	}
	desc, err := registry.Publish(ctx, target, r)
	if err != nil {
		return "", err
	}
	return repo + "@" + desc.Digest.String(), nil
}
