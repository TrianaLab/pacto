package pactify

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/plugin"
)

// plugins returns a step for every plugin the golden path names and runs the
// ones the caller named. A plugin reads a copy of the planned files and
// what it returns is planned add-only; it never sees the target folder.
func (p *planner) plugins(ctx context.Context, listed []contract.TemplatePlugin, c *contract.Contract, planned fs.FS) ([]fleet.CreationStep, error) {
	t := p.opts.Template
	names := make([]string, len(listed))
	for i, pl := range listed {
		names[i] = pl.Name
	}
	for _, n := range t.RunPlugins {
		if !slices.Contains(names, n) {
			return nil, fmt.Errorf("the golden path names no plugin %s; it names %s", n, namesText(names))
		}
	}
	if len(t.RunPlugins) > 0 && t.TrustConfigured && t.Signature.Status != fleet.StepStatusDone {
		return nil, errors.New("plugins run only from a golden path whose signature a template key verified")
	}
	find := t.Find
	if find == nil {
		find = plugin.Find
	}
	var steps []fleet.CreationStep
	for _, pl := range listed {
		step := fleet.CreationStep{Step: fleet.StepPlugin, Name: pl.Name, Status: fleet.StepStatusSkipped}
		bin, err := find(pl.Name)
		named := slices.Contains(t.RunPlugins, pl.Name)
		switch {
		case err != nil && named:
			return nil, fmt.Errorf("the plugin %s is not installed: install pacto-plugin-%s: %w", pl.Name, pl.Name, err)
		case err != nil:
			step.Detail = "not installed: install pacto-plugin-" + pl.Name
		case !named:
			step.Detail = "pass --run-plugins to run " + bin
		case p.opts.Plan:
			step.Status, step.Detail = fleet.StepStatusPlanned, "would run "+bin
		default:
			if step.Detail, err = p.runPlugin(ctx, pl, bin, c, planned); err != nil {
				return nil, err
			}
			step.Status = fleet.StepStatusDone
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// runPlugin runs one plugin over a temporary copy of the planned files, with
// a fresh empty output folder, and plans the files it returns. Both folders
// are removed after, so anything it wrote there is discarded.
func (p *planner) runPlugin(ctx context.Context, pl contract.TemplatePlugin, bin string, c *contract.Contract, planned fs.FS) (string, error) {
	if p.opts.Template.Run == nil {
		return "", fmt.Errorf("the plugin %s cannot run: no plugin runner is configured", pl.Name)
	}
	tmp, err := os.MkdirTemp("", "pacto-plugin-*")
	if err != nil {
		return "", fmt.Errorf("running the plugin %s: %w", pl.Name, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	bundleDir, outputDir := filepath.Join(tmp, "bundle"), filepath.Join(tmp, "output")
	if err := os.CopyFS(bundleDir, planned); err != nil {
		return "", fmt.Errorf("running the plugin %s: %w", pl.Name, err)
	}
	_ = os.Mkdir(outputDir, 0o700) // ponytail: tmp was just created and holds only bundle
	options := make(map[string]any, len(pl.Options))
	for k, v := range pl.Options {
		options[k] = v
	}
	resp, err := p.opts.Template.Run(ctx, pl.Name, plugin.GenerateRequest{ProtocolVersion: plugin.ProtocolVersion,
		Contract: c, BundleDir: bundleDir, OutputDir: outputDir, Options: options})
	if err != nil {
		return "", fmt.Errorf("running the plugin %s (%s): %w", pl.Name, bin, err)
	}
	var added, kept int
	for _, f := range resp.Files {
		if !filepath.IsLocal(f.Path) {
			return "", fmt.Errorf("the plugin %s returned %q, a path outside the folder", pl.Name, f.Path)
		}
		name := filepath.ToSlash(filepath.Clean(f.Path))
		if strings.EqualFold(name, "pacto.lock") {
			p.unknown("pacto.lock: the plugin %s returned one, which was not written: run pacto lock to pin the references", pl.Name)
			kept++
			continue
		}
		action, err := p.addFile("the plugin "+pl.Name, name, []byte(f.Content))
		if err != nil {
			return "", err
		}
		if action == Add {
			added++
		} else {
			kept++
		}
	}
	return fmt.Sprintf("ran %s: added %d files, kept %d", bin, added, kept), nil
}

// namesText lists the plugin names a golden path gives.
func namesText(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
