package contractview

import (
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	depgraph "github.com/trianalab/pacto/v3/pkg/graph"
)

// DependentInfo describes a service that depends on another service.
type DependentInfo struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
	ContractStatus string `json:"contractStatus,omitempty"`
	Required       bool   `json:"required"`
	Compatibility  string `json:"compatibility,omitempty"`
}

// CrossReference describes a cross-reference between services via config/policy refs.
type CrossReference struct {
	Name           string `json:"name"`
	RefType        string `json:"refType"` // "config" or "policy"
	Ref            string `json:"ref,omitempty"`
	ContractStatus string `json:"contractStatus,omitempty"`
}

// CrossReferences contains both outgoing references and incoming "referenced by".
type CrossReferences struct {
	References   []CrossReference `json:"references"`
	ReferencedBy []CrossReference `json:"referencedBy"`
}

// GraphNodeData is a flat representation of a graph node for the D3 visualization.
type GraphNodeData struct {
	ID          string          `json:"id"`
	ServiceName string          `json:"serviceName"`
	Status      string          `json:"status"`
	Version     string          `json:"version,omitempty"`
	Source      string          `json:"source,omitempty"`
	Reason      string          `json:"reason,omitempty"` // why unresolved: non_oci_ref, auth_failed, no_semver_tags, not_found, pull_failed, discovering
	Edges       []GraphEdgeData `json:"edges,omitempty"`
}

// GraphEdgeData is a flat representation of a graph edge for D3.
type GraphEdgeData struct {
	TargetID      string `json:"targetId"`
	TargetName    string `json:"targetName"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility,omitempty"`
	Resolved      bool   `json:"resolved"`
	Type          string `json:"type"` // "dependency" or "reference"
	// Lock pins from pacto.lock, carried on dependency edges when a lockfile is present.
	LockedDigest  string `json:"lockedDigest,omitempty"`
	LockedVersion string `json:"lockedVersion,omitempty"`
	DriftStatus   string `json:"driftStatus,omitempty"`
}

// GlobalGraph is the full graph of all services and their dependency edges.
type GlobalGraph struct {
	Nodes []GraphNodeData `json:"nodes"`
}

// stripPactoSuffix removes the conventional "-pacto" suffix from OCI repo names.
func stripPactoSuffix(name string) (string, bool) {
	stripped := strings.TrimSuffix(name, "-pacto")
	return stripped, stripped != name
}

// resolveServiceName resolves a ref-extracted name to an actual service name
// using the index. As a fallback, strips the common "-pacto" suffix from OCI
// repo names (e.g. "payment-gateway-pacto" → "payment-gateway").
func resolveServiceName(name string, index map[string]*ServiceDetails) string {
	if _, ok := index[name]; ok {
		return name
	}
	if stripped, ok := stripPactoSuffix(name); ok {
		if _, exists := index[stripped]; exists {
			return stripped
		}
	}
	return name
}

// unresolvedReasonFunc returns a human-readable reason why a dependency ref
// could not be resolved. Returns "" if no specific reason is available.
type unresolvedReasonFunc func(depRef string) string

// buildGlobalGraph constructs the flat graph representation used by the D3 visualization.
// reasonFn is optional (may be nil); when provided, it populates GraphNodeData.Reason
// for unresolved nodes so the UI can distinguish auth failures from missing repos, etc.
func buildGlobalGraph(services []Service, index map[string]*ServiceDetails, reasonFn unresolvedReasonFunc) *GlobalGraph {
	graph := &GlobalGraph{}

	// Track which names we've added as nodes.
	nodeSet := make(map[string]bool)

	for _, svc := range services {
		details := index[svc.Name]
		node := GraphNodeData{
			ID:          svc.Name,
			ServiceName: svc.Name,
			Status:      string(svc.ContractStatus),
			Version:     svc.Version,
			Source:      svc.Source,
		}

		if details != nil {
			// Dependency edges
			for _, dep := range details.Dependencies {
				depName := resolveServiceName(extractServiceNameFromRef(dep.Ref), index)
				_, resolved := index[depName]
				node.Edges = append(node.Edges, GraphEdgeData{
					TargetID:      depName,
					TargetName:    depName,
					Required:      dep.Required,
					Compatibility: dep.Compatibility,
					Resolved:      resolved,
					Type:          depgraph.EdgeDependency,
					LockedDigest:  dep.LockedDigest,
					LockedVersion: dep.LockedVersion,
					DriftStatus:   dep.DriftStatus,
				})

				// Add unresolved dependency targets as external nodes.
				if !resolved && !nodeSet[depName] {
					nodeSet[depName] = true
					graph.Nodes = append(graph.Nodes, GraphNodeData{
						ID:          depName,
						ServiceName: depName,
						Status:      "external",
						Reason:      unresolvedReason(dep.Ref, reasonFn),
					})
				}
			}

			// Reference edges — config/policy refs to other services
			addRefEdge := func(ref string) {
				if ref == "" {
					return
				}
				refName := resolveServiceName(extractServiceNameFromRef(ref), index)
				if refName == svc.Name {
					return // skip self-references
				}
				_, resolved := index[refName]
				node.Edges = append(node.Edges, GraphEdgeData{
					TargetID:   refName,
					TargetName: refName,
					Resolved:   resolved,
					Type:       depgraph.EdgeReference,
				})
				if !resolved && !nodeSet[refName] {
					nodeSet[refName] = true
					graph.Nodes = append(graph.Nodes, GraphNodeData{
						ID:          refName,
						ServiceName: refName,
						Status:      "external",
						Reason:      unresolvedReason(ref, reasonFn),
					})
				}
			}
			for _, cfg := range details.Configurations {
				addRefEdge(cfg.Ref)
			}
			for _, pol := range details.Policies {
				addRefEdge(pol.Ref)
			}
		}

		nodeSet[svc.Name] = true
		graph.Nodes = append(graph.Nodes, node)
	}

	return graph
}

// unresolvedReason classifies why a dependency ref could not be resolved.
func unresolvedReason(depRef string, reasonFn unresolvedReasonFunc) string {
	if !strings.HasPrefix(depRef, "oci://") {
		return "non_oci_ref"
	}
	if reasonFn != nil {
		if r := reasonFn(depRef); r != "" {
			return r
		}
	}
	return ""
}

// GlobalGraphFromResult builds the flat D3 GlobalGraph (as served at /api/graph)
// from a resolved dependency graph, for the offline single-service doc export.
// root is the already-built snapshot for gr.Root (carries real status + lock pins);
// dependency nodes are built from their resolved contracts and validated through
// the bundle FS, so they carry a real status. A node the resolver could not fetch
// a contract for stays Unknown.
func GlobalGraphFromResult(gr *depgraph.Result, root *ServiceDetails) *GlobalGraph {
	if gr == nil || gr.Root == nil {
		return &GlobalGraph{}
	}
	index := map[string]*ServiceDetails{}
	var services []Service
	seen := map[string]bool{}
	var walk func(n *depgraph.Node, isRoot bool)
	walk = func(n *depgraph.Node, isRoot bool) {
		if n == nil {
			return
		}
		var d *ServiceDetails
		switch {
		case isRoot && root != nil:
			d = root
		case n.Contract != nil:
			d = ServiceDetailsFromBundle(&contract.Bundle{Contract: n.Contract, FS: n.FS}, "local")
		default:
			d = &ServiceDetails{Service: Service{Name: n.Name, Version: n.Version, ContractStatus: StatusUnknown}}
		}
		// Dedupe AFTER resolving d so the root's real details win even when a
		// dependency shares its name.
		if seen[d.Name] {
			return
		}
		seen[d.Name] = true
		index[d.Name] = d
		services = append(services, d.Service)
		for _, e := range n.Dependencies {
			walk(e.Node, false) // nil Node (unresolved/cycle) is skipped by the guard above
		}
	}
	walk(gr.Root, true)
	return buildGlobalGraph(services, index, nil)
}

// extractServiceNameFromRef extracts a service name from a dependency ref.
// Scheme stripping uses the shared CLI parser (graph.ParseDependencyRef) so
// oci:// and file:// are handled identically to `pacto graph`. The bare-name
// extraction is dashboard-specific: it labels refs we couldn't resolve to a
// contract (the CLI names nodes from the resolved contract instead).
func extractServiceNameFromRef(ref string) string {
	loc := depgraph.ParseDependencyRef(ref).Location
	parts := strings.Split(loc, "/")
	name := parts[len(parts)-1]
	// Strip digest (@sha256:...) before tag (:version).
	if idx := strings.Index(name, "@"); idx > 0 {
		name = name[:idx]
	}
	if idx := strings.Index(name, ":"); idx > 0 {
		name = name[:idx]
	}
	return name
}
