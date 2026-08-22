package fleetsrc

import (
	"context"
	"os"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/otelobserver"
)

// ObservationSource reads runtime-observed dependency edges from an OTLP/JSON
// trace file and contributes them as [fleet.Collection.Observed]. It is the real
// observation pipeline into a snapshot: [fleet.Build] resolves each raw observed
// endpoint name to a unique domain-qualified service and folds the resolved edges
// in as observed relationships, so the operational graph, impact and reconciliation
// all see runtime evidence — not a test fixture. An endpoint that resolves to zero
// or multiple services is preserved as an explicit limitation, never coerced to a
// domain.
//
// This adapter carries NO deployed targets or evaluation results: it is purely a
// source of observed edges. Combine it with definition/target sources to reconcile
// intent against reality.
type ObservationSource struct {
	id   string
	root string
	path string
}

// NewObservationSource returns an observation source reading an OTLP/JSON trace
// file at path.
//
// root, when set, is the directory the source may read inside and nothing
// outside: path is then resolved relative to it through [os.Root], so no symlink
// the file's storage happens to contain can make the read leave it. That is what
// a declared source root means for storage Pacto does not own — a mounted volume
// whose contents someone else produces. Symlinks are not banned, only escapes:
// the internal indirection a projected Kubernetes ConfigMap volume is built from
// resolves normally.
//
// root empty means path is read as given, which is the ad-hoc command line: a
// path a person typed names whatever it names, and there is no declared root for
// it to escape from.
func NewObservationSource(id, root, path string) *ObservationSource {
	if id == "" {
		id = "observation"
	}
	return &ObservationSource{id: id, root: root, path: path}
}

// read returns the trace bytes, rooted when the source declares a root.
//
// The rooted branch is not a check followed by a read — there is nothing to
// re-resolve between them. [os.Root] performs the resolution itself and refuses
// the open the moment a component would leave the root, so a path that escapes
// yields no bytes at all rather than bytes plus a verdict.
func (s *ObservationSource) read() ([]byte, error) {
	if s.root == "" {
		return os.ReadFile(s.path)
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(s.path)
}

// ID implements [fleet.Source].
func (s *ObservationSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *ObservationSource) Kind() string { return "observation" }

// Collect reads and parses the trace file into observed dependency edges. A
// missing, unreadable, escaping or unparseable file is a source error (the source
// becomes unavailable, with its own limitation, and every other source still
// answers); endpoint resolution and cross-domain safety happen later in
// [fleet.Build].
func (s *ObservationSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := s.read()
	if err != nil {
		return nil, err
	}
	td, err := otelobserver.ParseTraces(data)
	if err != nil {
		return nil, err
	}
	edges := otelobserver.DependencyEdges(td)
	col := &fleet.Collection{Observed: make([]fleet.ObservedEdge, 0, len(edges))}
	for _, e := range edges {
		col.Observed = append(col.Observed, fleet.ObservedEdge{
			From: e.From, To: e.To, Count: e.Count, FirstSeen: e.FirstSeen, LastSeen: e.LastSeen,
		})
	}
	return col, nil
}
