// Package main provides a browser/WASM build of the Pacto dashboard backed by
// contracts embedded at compile time, so the whole demo runs client-side with
// no server and no live OCI access.
package main

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

const contractFile = "pacto.yaml"

// versionEntry is one parsed contract version held in memory.
type versionEntry struct {
	bundle *contract.Bundle
	doc    *contract.Document // the YAML decoded once, handed to the fleet build to validate
	hash   string             // hex of lock.HashFS over the bundle dir, used as the version's content digest
}

// EmbedSource is the demo's contract index over an embedded filesystem of
// bundles: every versioned directory it finds, keyed by service name and
// version. It is the input [buildDemoFleet] turns into fleet revisions, which is
// how the browser demo answers version history, diffs and the operational graph
// with no server and no OCI access.
type EmbedSource struct {
	byName map[string]map[string]*versionEntry // service name -> version -> entry
	names  []string                            // sorted service names
}

// NewEmbedSource walks fsys for pacto.yaml files and indexes them by service
// name and version. Both layouts are supported: flat (bundles/<svc>/pacto.yaml)
// and versioned (bundles/<svc>/vX.Y.Z/pacto.yaml). Unparseable files are skipped
// so one bad bundle never breaks the whole demo.
func NewEmbedSource(fsys fs.FS) (*EmbedSource, error) {
	s := &EmbedSource{byName: make(map[string]map[string]*versionEntry)}

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != contractFile {
			return nil
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil // skip unreadable
		}
		c, doc, err := contract.ParseDocument(raw)
		if err != nil {
			return nil // skip invalid
		}
		// Root the bundle FS at the contract's directory so validation can
		// resolve sibling files (referenced schemas, policies, docs).
		sub, err := fs.Sub(fsys, path.Dir(p))
		if err != nil {
			return nil
		}
		// The content identity `pacto fleet --local` gives a bundle dir, so a
		// committed pacto.lock pins the same revision here as on the CLI.
		h, err := lock.HashFS(sub)
		if err != nil {
			return nil
		}
		name, ver := c.Service.Name, c.Service.Version
		if s.byName[name] == nil {
			s.byName[name] = make(map[string]*versionEntry)
		}
		// The sub-FS is rooted at the contract dir, so whatever the bundle ships
		// beside pacto.yaml travels with it, pacto.lock included. The demo commits a
		// lock only where a reference must resolve (catalog-service 1.3.0 to
		// postgresql 16.1.0); the fleet build reads it from the bundle FS.
		s.byName[name][ver] = &versionEntry{
			bundle: &contract.Bundle{Contract: c, RawYAML: raw, FS: sub},
			doc:    doc,
			hash:   strings.TrimPrefix(h, "sha256:"),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("indexing embedded bundles: %w", err)
	}

	for name := range s.byName {
		s.names = append(s.names, name)
	}
	sort.Strings(s.names)
	return s, nil
}
