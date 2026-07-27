// Command validatebundles runs the FULL Pacto validator over every demo bundle
// (every service and every version directory) OFFLINE and DETERMINISTICALLY, and
// fails if any bundle is not valid.
//
// Why this exists: `pacto validate examples/demo/bundles/<svc>` resolves each
// bundle's dependency + policy refs LIVE from oci://ghcr.io/trianalab/pacto-demo/…
// The published artifacts there are stale (older schema versions), so live
// validation of a dep-bearing bundle fails on a resolution artifact
// (LOCK_UNRESOLVED / stale ref), never on the local contract's own correctness.
// The demo is designed to run OFFLINE: every referenced service already exists in
// ./bundles, so the whole closure resolves BY SERVICE NAME within ./bundles —
// exactly how genlocks and EmbedSource derive the graph. This tool is the offline
// proof that the demo contracts themselves are valid v2.
//
// Live validation against ghcr.io only passes once the release republishes the
// demo bundles as v2; that is a production publish and out of scope here. This
// offline validator is the correct proof of contract validity.
//
// Run from examples/demo:  go run ./validatebundles   (or: make validate)
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/semver"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

const (
	bundlesDir   = "bundles"
	contractFile = "pacto.yaml"
)

// svcVersion is one indexed bundle: its declaring contract, the raw contract
// bytes (needed for structural + policy validation) and its on-disk directory.
type svcVersion struct {
	contract *contract.Contract
	raw      []byte
	dir      string
}

// index maps service name -> version -> bundle. Same shape as genlocks.
type index map[string]map[string]*svcVersion

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "validatebundles:", err)
		os.Exit(1)
	}
}

func run() error {
	idx, err := buildIndex(bundlesDir)
	if err != nil {
		return err
	}
	resolver := &offlineResolver{idx: idx}
	ctx := context.Background()

	// Validate every indexed bundle in a stable order (by directory) so output is
	// deterministic.
	all := make([]*svcVersion, 0)
	for _, versions := range idx {
		for _, sv := range versions {
			all = append(all, sv)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].dir < all[j].dir })

	var failed []string
	for _, sv := range all {
		res := validation.ValidateWithResolver(ctx, sv.contract, sv.raw, os.DirFS(sv.dir), resolver)
		if res.IsValid() {
			continue
		}
		failed = append(failed, sv.dir)
		fmt.Fprintf(os.Stderr, "INVALID %s (%s@%s)\n", sv.dir, sv.contract.Service.Name, sv.contract.Service.Version)
		for _, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "  [%s] %s: %s\n", e.Code, e.Path, e.Message)
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d/%d demo contract(s) invalid", len(failed), len(all))
	}
	fmt.Printf("DEMO-CONTRACTS VALID: %d/%d\n", len(all), len(all))
	return nil
}

// buildIndex walks bundlesDir for pacto.yaml files and indexes each by service
// name and version, keeping the raw bytes for validation. Same walk as genlocks.
func buildIndex(root string) (index, error) {
	idx := index{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != contractFile {
			return nil
		}
		dir := filepath.Dir(p)
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		c, err := contract.Parse(strings.NewReader(string(raw)))
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		name, ver := c.Service.Name, c.Service.Version
		if idx[name] == nil {
			idx[name] = map[string]*svcVersion{}
		}
		idx[name][ver] = &svcVersion{contract: c, raw: raw, dir: dir}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// offlineResolver is a validation.BundleResolver that resolves a dependency /
// config / policy ref to the LOCAL sibling bundle by service name (latest semver),
// NO registry. Mirrors internal/app's bundleResolverAdapter, but the underlying
// resolution is offline-by-name instead of an OCI pull. The returned Bundle
// carries the bundle dir as its FS so ValidateWithResolver can read the referenced
// bundle's own policy/schema.json.
type offlineResolver struct {
	idx index
}

func (r *offlineResolver) ResolveBundle(_ context.Context, ref string) (*contract.Bundle, error) {
	sv, err := latest(r.idx, serviceName(ref))
	if err != nil {
		return nil, err
	}
	return &contract.Bundle{
		Contract: sv.contract,
		RawYAML:  sv.raw,
		FS:       os.DirFS(sv.dir),
	}, nil
}

// latest returns the highest-semver indexed version of a service. Same selection
// as genlocks (semver.Latest, lexical fallback).
func latest(idx index, name string) (*svcVersion, error) {
	versions := idx[name]
	if len(versions) == 0 {
		return nil, fmt.Errorf("service %q not found in demo set", name)
	}
	all := make([]string, 0, len(versions))
	for v := range versions {
		all = append(all, v)
	}
	pick := semver.Latest(all)
	if pick == "" {
		sort.Strings(all)
		pick = all[len(all)-1]
	}
	return versions[pick], nil
}

// serviceName extracts the service name from a ref, identical to genlocks: strip
// scheme, registry path and any tag/digest.
func serviceName(ref string) string {
	loc := graph.ParseDependencyRef(ref).Location
	parts := strings.Split(loc, "/")
	name := parts[len(parts)-1]
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	if i := strings.Index(name, ":"); i > 0 {
		name = name[:i]
	}
	return name
}
