package dashboard

import (
	"strings"

	depgraph "github.com/trianalab/pacto/pkg/graph"
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
}

// GlobalGraph is the full graph of all services and their dependency edges.
type GlobalGraph struct {
	Nodes []GraphNodeData `json:"nodes"`
}

// buildRefAliases builds a mapping from OCI repo names to contract service names.
// For example, if service "my-service" has imageRef "ghcr.io/org/my-service-pacto:1.0.0",
// this maps "my-service-pacto" -> "my-service".
func buildRefAliases(index map[string]*ServiceDetails) map[string]string {
	aliases := make(map[string]string)
	for name, d := range index {
		if d == nil {
			continue
		}
		if d.ImageRef != "" {
			refName := extractServiceNameFromRef(d.ImageRef)
			if refName != name {
				aliases[refName] = name
			}
		}
		if d.ChartRef != "" {
			refName := extractServiceNameFromRef(d.ChartRef)
			if refName != name {
				aliases[refName] = name
			}
		}
	}
	return aliases
}

// stripPactoSuffix removes the conventional "-pacto" suffix from OCI repo names.
func stripPactoSuffix(name string) (string, bool) {
	stripped := strings.TrimSuffix(name, "-pacto")
	return stripped, stripped != name
}

// resolveServiceName resolves a ref-extracted name to an actual service name
// using the index and alias map. As a fallback, strips the common "-pacto"
// suffix from OCI repo names (e.g. "payment-gateway-pacto" → "payment-gateway").
func resolveServiceName(name string, index map[string]*ServiceDetails, aliases map[string]string) string {
	if _, ok := index[name]; ok {
		return name
	}
	if resolved, ok := aliases[name]; ok {
		return resolved
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
	aliases := buildRefAliases(index)

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
				depName := resolveServiceName(extractServiceNameFromRef(dep.Ref), index, aliases)
				_, resolved := index[depName]
				node.Edges = append(node.Edges, GraphEdgeData{
					TargetID:      depName,
					TargetName:    depName,
					Required:      dep.Required,
					Compatibility: dep.Compatibility,
					Resolved:      resolved,
					Type:          depgraph.EdgeDependency,
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
				refName := resolveServiceName(extractServiceNameFromRef(ref), index, aliases)
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
			if details.Configuration != nil {
				addRefEdge(details.Configuration.Ref)
			}
			if details.Policy != nil {
				addRefEdge(details.Policy.Ref)
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

// buildGraph constructs a DependencyGraph rooted at the given service.
func buildGraph(root *ServiceDetails, index map[string]*ServiceDetails, reasonFn unresolvedReasonFunc) *DependencyGraph {
	visited := make(map[string]bool)
	aliases := buildRefAliases(index)
	node := buildGraphNode(root, index, aliases, visited, reasonFn)

	return &DependencyGraph{
		Root: node,
	}
}

func buildGraphNode(svc *ServiceDetails, index map[string]*ServiceDetails, aliases map[string]string, visited map[string]bool, reasonFn unresolvedReasonFunc) *GraphNode {
	if svc == nil {
		return nil
	}

	node := &GraphNode{
		Name:    svc.Name,
		Version: svc.Version,
	}

	if visited[svc.Name] {
		return node
	}
	visited[svc.Name] = true

	for _, dep := range svc.Dependencies {
		edge := GraphEdge{
			Ref:           dep.Ref,
			Required:      dep.Required,
			Compatibility: dep.Compatibility,
		}

		depName := resolveServiceName(extractServiceNameFromRef(dep.Ref), index, aliases)
		if resolved, ok := index[depName]; ok {
			edge.Node = buildGraphNode(resolved, index, aliases, visited, reasonFn)
		} else {
			reason := unresolvedReason(dep.Ref, reasonFn)
			if reason != "" {
				edge.Error = reason
			} else {
				edge.Error = "not resolved"
			}
		}

		node.Dependencies = append(node.Dependencies, edge)
	}

	return node
}

// extractServiceNameFromRef extracts a service name from a dependency ref.
func extractServiceNameFromRef(ref string) string {
	ref = strings.TrimPrefix(ref, "oci://")
	parts := strings.Split(ref, "/")
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

// depRefMatchesName checks if a dependency ref refers to a service name,
// using an alias map to resolve OCI repo names to contract service names.
func depRefMatchesName(ref, name string, aliases map[string]string) bool {
	extracted := extractServiceNameFromRef(ref)
	if extracted == name {
		return true
	}
	if resolved, ok := aliases[extracted]; ok && resolved == name {
		return true
	}
	if stripped, ok := stripPactoSuffix(extracted); ok && stripped == name {
		return true
	}
	return false
}

// computeBlastRadius computes how many services are transitively affected
// if the given service breaks (via required dependency chains).
func computeBlastRadius(name string, index map[string]*ServiceDetails, aliases map[string]string) int {
	// Build reverse dependency map (who depends on me via required deps).
	reverseDeps := make(map[string][]string)
	for svcName, details := range index {
		if details == nil {
			continue
		}
		for _, dep := range details.Dependencies {
			if dep.Required {
				depName := resolveServiceName(extractServiceNameFromRef(dep.Ref), index, aliases)
				reverseDeps[depName] = append(reverseDeps[depName], svcName)
			}
		}
	}

	// BFS from the given service.
	visited := map[string]bool{name: true}
	queue := []string{name}
	count := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range reverseDeps[cur] {
			if !visited[dep] {
				visited[dep] = true
				queue = append(queue, dep)
				count++
			}
		}
	}
	return count
}
