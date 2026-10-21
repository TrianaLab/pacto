package evidenceoci

import (
	"fmt"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// RepositoryOptions configures how the evidence store reaches its contract
// registries: the shared referrer writer's options.
type RepositoryOptions = ocireferrer.Options

// newRepository opens the repository holding subj's contract revision with the
// native Referrers API required.
func newRepository(subj Subject, opts RepositoryOptions) (*remote.Repository, error) {
	repo, err := ocireferrer.Open(subj.Path(), opts)
	if err != nil {
		return nil, fmt.Errorf("evidence oci: %q: %w", subj.Path(), err)
	}
	return repo, nil
}
