package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// statsReadTimeout bounds one whole read of a stats repository. A var so
// tests can shorten it.
var statsReadTimeout = registry.ReadTimeout

// defaultStatsRepo opens the stats repository run records are read from, with
// the CLI's credentials and plain-HTTP hosts.
func defaultStatsRepo(path string) (*remote.Repository, error) {
	return registry.Open(path, oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries(), nil)
}

// StatsRequest is one stats answer to compute.
type StatsRequest struct {
	// Registry is the stats repository to read.
	Registry string
	Query    stats.Query
	Scope    stats.Scope
	// Now is the answer's clock; zero is the service's clock.
	Now time.Time
	// OperationAccess, when set, reviews the caller's access to the Stage
	// namespaces the operation records name and returns which it may see.
	OperationAccess func(ctx context.Context, namespaces []string) (func(string) bool, error)
}

// Stats reads the run records of req.Registry within bounds and totals them
// for req.Query as req.Scope lets the caller see them. It writes nothing.
func (s *Service) Stats(ctx context.Context, req StatsRequest) (*stats.Stats, error) {
	now := req.Now
	if now.IsZero() {
		now = s.env().now()
	}
	var set stats.RecordSet
	var err error
	// The anchor of the filtered service, else, for a restricted caller, only
	// the anchors of the services in its view, so hidden records are never
	// listed or charged against the bounds; else every anchor.
	switch {
	case req.Query.Service != "":
		set, err = s.ReadRunRecords(ctx, req.Registry, []string{req.Query.Service})
	case req.Scope.Restricted && len(req.Scope.Names) == 0: // nothing in view to read
	case req.Scope.Restricted:
		set, err = s.ReadRunRecords(ctx, req.Registry, req.Scope.Names)
	default:
		set, err = s.ReadRunRecords(ctx, req.Registry, nil)
	}
	if err != nil {
		return nil, statsReadError(req, err)
	}
	scope := req.Scope
	if req.OperationAccess != nil {
		visible, err := req.OperationAccess(ctx, stats.OperationNamespaces(set))
		if err != nil {
			return nil, fmt.Errorf("checking your Kubernetes permissions failed: %w", err)
		}
		scope.OperationVisible = visible
	}
	st := stats.Aggregate(set, req.Query, scope, now)
	st.Registry = req.Registry
	return st, nil
}

// ReadRunRecords reads, within bounds and the read timeout, the run records
// of repository on the anchors of services, or on every anchor when services
// is nil. It writes nothing, and its error is the registry's, unwrapped: the
// caller decides what a reader may learn from it.
func (s *Service) ReadRunRecords(ctx context.Context, repository string, services []string) (stats.RecordSet, error) {
	repo, err := s.env().statsRepo(repository)
	if err != nil {
		return stats.RecordSet{}, err
	}
	var anchors []string
	for _, name := range services {
		anchors = append(anchors, runrecord.AnchorTag(name))
	}
	readCtx, cancel := context.WithTimeout(ctx, statsReadTimeout)
	defer cancel()
	return registry.Read(readCtx, repo, anchors)
}

// statsReadError says which repository a read failed on. A restricted caller
// learns nothing else unless the deadline or a cancel caused it: a registry
// error can carry a request URL, and an anchor tag in it hashes a service
// name.
func statsReadError(req StatsRequest, err error) error {
	if req.Scope.Restricted && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("reading run records from %s failed", req.Registry)
	}
	return fmt.Errorf("reading run records from %s: %w", req.Registry, err)
}
