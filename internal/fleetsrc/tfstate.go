package fleetsrc

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/managedres"
)

// TerraformStateSource reads the managed resources a local Terraform or
// OpenTofu state file describes, type, id and region only (see
// [managedres.ParseState]). Each resource names the file by its base name
// and carries no cluster: the CLI read it, not an operator.
type TerraformStateSource struct {
	id, path string
	cat      *externalcatalog.Catalog
	now      func() time.Time
}

// NewTerraformStateSource reads the state file at path, classified with cat.
func NewTerraformStateSource(id, path string, cat *externalcatalog.Catalog, now func() time.Time) *TerraformStateSource {
	return &TerraformStateSource{id: id, path: path, cat: cat, now: now}
}

// ID implements [fleet.Source].
func (s *TerraformStateSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *TerraformStateSource) Kind() string { return managedres.SourceTerraform }

// Collect reads the file. An unreadable or invalid state is a source error.
func (s *TerraformStateSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	rs, err := managedres.ParseState(f, filepath.Base(s.path), s.cat, s.now())
	if err != nil {
		return nil, err
	}
	return &fleet.Collection{ManagedResources: rs}, nil
}
