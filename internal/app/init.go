package app

import (
	"cmp"
	"context"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
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
	if err := s.InitCatalog(ctx, &opts); err != nil {
		return nil, err
	}
	opts.PactoVersion = s.buildVersion()
	if abs, err := s.env().absPath(cmp.Or(opts.Dir, opts.Name, ".")); err == nil {
		opts.Commit = gitRevision(abs)
	}
	return pactify.Init(ctx, opts, s.env().writeFile)
}

// InitCatalog sets the catalog opts classifies external systems with: the
// default merged with the organization catalog s.ExternalCatalogRef names,
// and why that one could not be read, when it could not. Only a reference
// not pinned by digest is an error.
func (s *Service) InitCatalog(ctx context.Context, opts *InitOptions) error {
	cat, lims, err := s.externalCatalog(ctx, FleetOptions{ExternalCatalogRef: s.ExternalCatalogRef})
	if err != nil {
		return err
	}
	opts.Catalog = cat
	for _, l := range lims { // ponytail: one organization catalog, so at most one limitation
		opts.CatalogUnavailable = l.Message
	}
	return nil
}

// completionCatalog is the catalog a local completion classifies with, as
// [Service.InitCatalog] sets it; an organization catalog it could not read
// is a warning, and the default classifies alone.
func (s *Service) completionCatalog(ctx context.Context) (*externalcatalog.Catalog, error) {
	var opts InitOptions
	if err := s.InitCatalog(ctx, &opts); err != nil {
		return nil, err
	}
	if opts.CatalogUnavailable != "" {
		logging.LoggerFromContext(ctx).Warn(opts.CatalogUnavailable)
	}
	return opts.Catalog, nil
}
