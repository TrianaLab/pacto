package app

import (
	"cmp"
	"context"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// InitOptions holds options for the init command: the folder, what the caller
// already knows and whether to only plan. See [pactify.Options].
type InitOptions = pactify.Options

// InitResult is the plan and, once applied, what was added.
type InitResult = pactify.Result

// Init pactifies a folder: an empty one gets a blank contract, one that holds
// a service gets a contract read from its Dockerfile and manifests. With
// opts.Plan nothing is written; otherwise only missing files and lines are.
// A folder whose pacto.yaml is completed gets a record naming this CLI and
// the folder's commit.
func (s *Service) Init(ctx context.Context, opts InitOptions) (*InitResult, error) {
	logging.LoggerFromContext(ctx).Debug("initializing pacto project", "dir", opts.Dir, "name", opts.Name)
	opts.PactoVersion = s.buildVersion()
	if abs, err := s.env().absPath(cmp.Or(opts.Dir, opts.Name, ".")); err == nil {
		opts.Commit = gitRevision(abs)
	}
	return pactify.Init(ctx, opts, s.env().writeFile)
}
