// Package main provides a browser/WASM build of the Pacto dashboard backed by
// contracts embedded at compile time, so the whole demo runs client-side with
// no server and no live OCI access.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

const contractFile = "pacto.yaml"

// versionEntry is one parsed contract version held in memory.
type versionEntry struct {
	bundle *contract.Bundle
	hash   string // sha256 of the raw YAML, used as the version's contract hash
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
		c, err := contract.Parse(bytes.NewReader(raw))
		if err != nil {
			return nil // skip invalid
		}
		// Root the bundle FS at the contract's directory so validation can
		// resolve sibling files (referenced schemas, policies, docs).
		sub, err := fs.Sub(fsys, path.Dir(p))
		if err != nil {
			return nil
		}
		h := sha256.Sum256(raw)
		name, ver := c.Service.Name, c.Service.Version
		if s.byName[name] == nil {
			s.byName[name] = make(map[string]*versionEntry)
		}
		// The sub-FS is rooted at the contract dir, so whatever the bundle ships
		// beside pacto.yaml travels with it. The demo commits no pacto.lock — the
		// publish script regenerates one against the live coordinate — so there are
		// no pins to apply here.
		s.byName[name][ver] = &versionEntry{
			bundle: &contract.Bundle{Contract: c, RawYAML: raw, FS: sub},
			hash:   hex.EncodeToString(h[:]),
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
