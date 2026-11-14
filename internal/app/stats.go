package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	return registry.Open(path, oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries())
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
	env := s.env()
	now := req.Now
	if now.IsZero() {
		now = env.now()
	}
	repo, err := env.statsRepo(req.Registry)
	if err != nil {
		return nil, fmt.Errorf("reading run records from %s: %w", req.Registry, err)
	}
	var anchors []string
	if req.Query.Service != "" {
		anchors = []string{runrecord.AnchorTag(req.Query.Service)}
	}
	readCtx, cancel := context.WithTimeout(ctx, statsReadTimeout)
	defer cancel()
	set, err := registry.Read(readCtx, repo, anchors)
	if err != nil {
		return nil, statsReadError(req, err)
	}
	scope := req.Scope
	if req.OperationAccess != nil {
		visible, err := req.OperationAccess(ctx, operationNamespaces(set))
		if err != nil {
			return nil, fmt.Errorf("checking your Kubernetes permissions failed: %w", err)
		}
		scope.OperationVisible = visible
	}
	st := stats.Aggregate(set, req.Query, scope, now)
	st.Registry = req.Registry
	return st, nil
}

// statsReadError says which repository a read failed on. A restricted caller
// never learns which anchor failed: its tag hashes a service name.
func statsReadError(req StatsRequest, err error) error {
	var anchor *registry.AnchorError
	if req.Scope.Restricted && errors.As(err, &anchor) {
		return fmt.Errorf("reading run records from %s failed for one service", req.Registry)
	}
	return fmt.Errorf("reading run records from %s: %w", req.Registry, err)
}

// operationNamespaces is the sorted, unique Stage namespaces the operation
// records of set name.
func operationNamespaces(set stats.RecordSet) []string {
	var out []string
	for _, e := range set.Entries {
		if e.Record.Kind == runrecord.KindOperation && e.Record.Stage != nil && e.Record.Stage.Namespace != "" {
			out = append(out, e.Record.Stage.Namespace)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
