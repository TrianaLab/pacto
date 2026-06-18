// Package main provides a browser/WASM build of the Pacto dashboard backed by
// contracts embedded at compile time, so the whole demo runs client-side with
// no server and no live OCI access.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/dashboard"
	"github.com/trianalab/pacto/pkg/lock"
	"github.com/trianalab/pacto/pkg/semver"
	"github.com/trianalab/pacto/pkg/validation"
)

const contractFile = "pacto.yaml"

// embedSourceName is the source label reported to the dashboard. "local" is one
// of the source types the UI already understands; the data is local to the wasm
// binary, so the label is accurate.
const embedSourceName = "local"

// versionEntry is one parsed contract version held in memory.
type versionEntry struct {
	bundle *contract.Bundle
	hash   string     // sha256 of the raw YAML, used as the version's contract hash
	lock   *lock.Lock // parsed pacto.lock from the bundle dir, nil when absent
}

// EmbedSource implements dashboard.DataSource over an embedded filesystem of
// bundles. Unlike dashboard.LocalSource (which only knows the single version on
// disk), it indexes every versioned directory it finds, so version history and
// cross-version diffs work fully offline. The graph, dependents and cross-ref
// views need no OCI resolver: they are derived from each contract's declared
// dependencies, and every referenced bundle is embedded.
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
		// Read an embedded pacto.lock sitting alongside pacto.yaml. The lockfile is
		// default-ignored by .pactoignore, but //go:embed bypasses that filter, so a
		// committed lock IS present in the embedded FS and read directly from fsys
		// (the bundle's ignore-filtered sub-FS would hide it). A malformed lock is
		// skipped so one bad file never breaks the demo.
		l, err := readEmbeddedLock(fsys, path.Dir(p))
		if err != nil {
			l = nil
		}
		s.byName[name][ver] = &versionEntry{
			bundle: &contract.Bundle{Contract: c, RawYAML: raw, FS: sub},
			hash:   hex.EncodeToString(h[:]),
			lock:   l,
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

// readEmbeddedLock reads pacto.lock from dir inside fsys. Returns (nil, nil) when
// the file is absent (a lock is optional). Mirrors dashboard.readLock, but reads
// from the embedded FS rather than the OS filesystem.
func readEmbeddedLock(fsys fs.FS, dir string) (*lock.Lock, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, lock.FileName))
	if err != nil {
		return nil, nil // absent (or unreadable) → no lock
	}
	return lock.Parse(raw)
}

// versionsDesc returns a service's versions sorted latest-first.
func (s *EmbedSource) versionsDesc(name string) []string {
	m := s.byName[name]
	all := make([]string, 0, len(m))
	for v := range m {
		all = append(all, v)
	}
	return semver.Filter(all) // valid semver, descending
}

// latestEntry returns the highest-semver version entry for a service.
func (s *EmbedSource) latestEntry(name string) (*versionEntry, string, error) {
	m := s.byName[name]
	if len(m) == 0 {
		return nil, "", fmt.Errorf("service %q not found", name)
	}
	all := make([]string, 0, len(m))
	for v := range m {
		all = append(all, v)
	}
	latest := semver.Latest(all)
	if latest == "" {
		// No valid semver tags; fall back to any deterministic choice.
		sort.Strings(all)
		latest = all[len(all)-1]
	}
	return m[latest], latest, nil
}

// ListServices returns one entry per service (its latest version), matching the
// "current fleet" view. ContractStatus is computed the same way the dashboard's
// own LocalSource does (validate the bundle), via the exported validator.
func (s *EmbedSource) ListServices(_ context.Context) ([]dashboard.Service, error) {
	services := make([]dashboard.Service, 0, len(s.names))
	for _, name := range s.names {
		entry, _, err := s.latestEntry(name)
		if err != nil {
			continue
		}
		svc := dashboard.ServiceFromContract(entry.bundle.Contract, embedSourceName)
		svc.ContractStatus = contractStatus(entry.bundle)
		services = append(services, svc)
	}
	return services, nil
}

// GetService returns full details for a service's latest version, with any
// embedded pacto.lock pins applied so the demo dashboard shows resolved digests.
func (s *EmbedSource) GetService(_ context.Context, name string) (*dashboard.ServiceDetails, error) {
	entry, _, err := s.latestEntry(name)
	if err != nil {
		return nil, err
	}
	return s.detailsWithLock(entry), nil
}

// detailsWithLock builds ServiceDetails for an entry and applies its lock (if any)
// through the exported dashboard hook, so the demo surfaces pins via the same code
// path the on-disk LocalSource uses. The demo has no k8s runtime, so DriftStatus
// stays empty (pins are shown, no drift assertion) — which is correct offline.
func (s *EmbedSource) detailsWithLock(entry *versionEntry) *dashboard.ServiceDetails {
	details := dashboard.ServiceDetailsFromBundle(entry.bundle, embedSourceName)
	dashboard.ApplyLock(details, entry.lock)
	return details
}

// GetVersions returns every indexed version for a service, latest first, each
// annotated with its contract hash and the diff classification against the
// immediately preceding version (BREAKING / POTENTIAL_BREAKING / NON_BREAKING).
// The server marks the current version separately via GetService.
func (s *EmbedSource) GetVersions(_ context.Context, name string) ([]dashboard.Version, error) {
	desc := s.versionsDesc(name)
	if len(desc) == 0 {
		return nil, fmt.Errorf("service %q not found", name)
	}
	out := make([]dashboard.Version, 0, len(desc))
	for i, v := range desc {
		entry := s.byName[name][v]
		ver := dashboard.Version{
			Version:      v,
			ContractHash: entry.hash,
			Source:       embedSourceName,
		}
		// Classify against the previous (older) version, which sits at i+1 in a
		// descending list. The oldest version has no predecessor.
		if i+1 < len(desc) {
			prev := desc[i+1]
			prevEntry := s.byName[name][prev]
			d := dashboard.ComputeDiff(
				dashboard.Ref{Name: name, Version: prev},
				dashboard.Ref{Name: name, Version: v},
				prevEntry.bundle, entry.bundle,
			)
			if d != nil {
				ver.Classification = d.Classification
			}
		}
		out = append(out, ver)
	}
	return out, nil
}

// GetDiff compares two specific service versions. An empty Ref.Version means the
// service's latest version.
func (s *EmbedSource) GetDiff(_ context.Context, a, b dashboard.Ref) (*dashboard.DiffResult, error) {
	entryA, refA, err := s.resolveRef(a)
	if err != nil {
		return nil, fmt.Errorf("loading %q: %w", a.Name, err)
	}
	entryB, refB, err := s.resolveRef(b)
	if err != nil {
		return nil, fmt.Errorf("loading %q: %w", b.Name, err)
	}
	return dashboard.ComputeDiff(refA, refB, entryA.bundle, entryB.bundle), nil
}

func (s *EmbedSource) GetServiceVersion(_ context.Context, ref dashboard.Ref) (*dashboard.ServiceDetails, error) {
	entry, _, err := s.resolveRef(ref)
	if err != nil {
		return nil, fmt.Errorf("loading %q version %q: %w", ref.Name, ref.Version, err)
	}
	return s.detailsWithLock(entry), nil
}

// resolveRef finds the bundle for a ref, defaulting an empty version to latest,
// and returns the concrete ref (with the resolved version filled in).
func (s *EmbedSource) resolveRef(r dashboard.Ref) (*versionEntry, dashboard.Ref, error) {
	if r.Version == "" {
		entry, latest, err := s.latestEntry(r.Name)
		if err != nil {
			return nil, r, err
		}
		r.Version = latest
		return entry, r, nil
	}
	m := s.byName[r.Name]
	entry, ok := m[r.Version]
	if !ok {
		return nil, r, fmt.Errorf("version %q of %q not found", r.Version, r.Name)
	}
	return entry, r, nil
}

// contractStatus mirrors the dashboard's unexported contractStatusFromBundle:
// validate the bundle and map the result to a ContractStatus. It uses the
// exported validator so no upstream change is needed.
func contractStatus(b *contract.Bundle) dashboard.ContractStatus {
	if b.RawYAML == nil {
		return dashboard.StatusUnknown
	}
	res := validation.Validate(b.Contract, b.RawYAML, b.FS)
	if res.IsValid() {
		return dashboard.StatusCompliant
	}
	return dashboard.StatusNonCompliant
}
