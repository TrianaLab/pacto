package app

import (
	"context"
	"path"
	"path/filepath"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/override"
)

// CIOptions configure one pacto ci plan.
type CIOptions struct {
	Root     string
	Registry string // --registry; empty lets the CI context and images decide
	Fleet    FleetOptions
	// Getenv reads the CI context; nil is an empty environment, never
	// os.Getenv, so only the CLI hands the runner's variables in.
	Getenv func(string) string
}

// CIPlan plans every service folder under opts.Root: the CI context, the
// registry with its source and one row per folder. It reads the repository
// and writes nothing. The results map holds each impacted row's answer by
// service.
func (s *Service) CIPlan(ctx context.Context, opts CIOptions) (*ci.Plan, map[string]*impact.Result, error) {
	cctx := ci.Detect(opts.Getenv)
	p := &ci.Plan{Platform: cctx.Platform, Base: cctx.Base, OIDC: cctx.OIDC}
	walk, err := discover.Walk(ctx, opts.Root, func(dir string, err error) error {
		if dir == opts.Root {
			return err
		}
		p.Notes = append(p.Notes, "unreadable, not planned: "+err.Error())
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	for _, sk := range walk.Skipped {
		p.Skipped = append(p.Skipped, ci.Skip{Rel: sk.Rel, Rule: sk.Rule})
	}
	var images []ci.ImageRef
	for _, dir := range walk.Dirs {
		row, imgs := s.ciRow(dir, relSlash(opts.Root, dir))
		p.Rows = append(p.Rows, row)
		images = append(images, imgs...)
	}
	var notes []string
	p.Registry, notes = ci.ResolveRegistry(opts.Registry, cctx, images)
	p.Notes = append(p.Notes, notes...)
	p.Settle()
	return p, nil, nil
}

// ciRow is the row of the service folder dir (rel below the root) and the
// images its inferred record names.
func (s *Service) ciRow(dir, rel string) (ci.Row, []ci.ImageRef) {
	row := ci.Row{Dir: rel, Impact: ci.Impact{Gate: impact.GateUnverified, Text: "unverified (not planned)"}}
	b, err := loadLocalBundleNamed(dir, dirBase(dir), override.Overrides{}, s.localInputs(dir, ""), nil)
	if err != nil {
		row.Errors = append(row.Errors, err.Error())
	}
	if b == nil {
		return row, nil
	}
	row.Service = b.Contract.Service.Name
	rec, _ := contract.ReadInferredRecord(b.FS) // ponytail: the record was just written by completion
	if rec == nil {
		return row, nil
	}
	row.Inferred = len(rec.Fields)
	var images []ci.ImageRef
	for _, f := range rec.Fields {
		if f.Field == "image" {
			images = append(images, ci.ImageRef{Image: f.Value, Source: path.Join(rel, f.Source)})
		}
	}
	return row, images
}

// relSlash is dir's slash path below root, "." for the root itself.
func relSlash(root, dir string) string {
	rel, _ := filepath.Rel(root, dir) // Walk yields only dirs under root
	return filepath.ToSlash(rel)
}
