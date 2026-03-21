package graph

import "sort"

// ChangeType indicates the kind of dependency graph change.
type ChangeType string

const (
	AddedNode      ChangeType = "added"
	RemovedNode    ChangeType = "removed"
	VersionChanged ChangeType = "version_changed"
)

// GraphChange represents a single change in the dependency graph.
type GraphChange struct {
	Name       string     `json:"name"`
	ChangeType ChangeType `json:"changeType"`
	OldVersion string     `json:"oldVersion,omitempty"`
	NewVersion string     `json:"newVersion,omitempty"`
}

// DiffNode represents a node in the diff tree, carrying its own change
// (if any) and the changes in its subtree.
type DiffNode struct {
	Name     string       `json:"name"`
	Version  string       `json:"version,omitempty"`
	Change   *GraphChange `json:"change,omitempty"`
	Children []DiffNode   `json:"children,omitempty"`
}

// GraphDiff holds the result of comparing two dependency graphs.
type GraphDiff struct {
	Root    DiffNode      `json:"root"`
	Changes []GraphChange `json:"changes,omitempty"`
}

// DiffGraphs compares two resolved dependency graphs by walking the tree
// structure and detecting added, removed, and version-changed nodes at
// each level. A node removed as a direct dependency is detected even if
// it remains transitively reachable through another path.
func DiffGraphs(old, new *Result) *GraphDiff {
	if (old == nil || old.Root == nil) && (new == nil || new.Root == nil) {
		return &GraphDiff{}
	}
	if old == nil || old.Root == nil {
		root := markAll(new.Root, AddedNode, map[string]bool{})
		changes := collectTreeChanges(root)
		sortChanges(changes)
		return &GraphDiff{Root: root, Changes: changes}
	}
	if new == nil || new.Root == nil {
		root := markAll(old.Root, RemovedNode, map[string]bool{})
		changes := collectTreeChanges(root)
		sortChanges(changes)
		return &GraphDiff{Root: root, Changes: changes}
	}

	// Build a full-node index for the old graph so that diffTrees can
	// look up the fully-resolved version of shared (shallow) nodes.
	// Without this, non-deterministic concurrent resolution order causes
	// phantom added/removed changes when a node is shared in the old
	// graph but fully resolved in the new graph at the same tree position.
	oldFull := fullNodeIndex(old.Root)
	root := diffTrees(old.Root, new.Root, oldFull, map[string]bool{})
	changes := collectTreeChanges(root)
	sortChanges(changes)
	return &GraphDiff{Root: root, Changes: changes}
}

// diffTrees recursively compares two graph nodes and builds a DiffNode tree
// annotating added, removed, and version-changed children at each level.
// oldFull maps node names to their fully-resolved versions from the old
// graph, used to replace shared (shallow) copies before recursion.
func diffTrees(oldNode, newNode *Node, oldFull map[string]*Node, visited map[string]bool) DiffNode {
	dn := DiffNode{Name: newNode.Name, Version: newNode.Version}

	oldByName := childMap(oldNode)
	newByName := childMap(newNode)

	// Children present in new graph.
	for _, edge := range newNode.Dependencies {
		if edge.Node == nil {
			continue
		}
		name := edge.Node.Name

		if oldChild, exists := oldByName[name]; exists {
			child := DiffNode{Name: name, Version: edge.Node.Version}
			if oldChild.Version != edge.Node.Version {
				child.Change = &GraphChange{
					Name:       name,
					ChangeType: VersionChanged,
					OldVersion: oldChild.Version,
					NewVersion: edge.Node.Version,
				}
			}
			if !edge.Shared && !visited[name] {
				visited[name] = true
				// Use the fully-resolved old node for recursion.
				// Shared (shallow) copies lack Dependencies, which
				// would cause phantom added/removed changes.
				if full, ok := oldFull[name]; ok {
					oldChild = full
				}
				sub := diffTrees(oldChild, edge.Node, oldFull, visited)
				child.Children = sub.Children
			}
			dn.Children = append(dn.Children, child)
		} else {
			child := DiffNode{
				Name:    name,
				Version: edge.Node.Version,
				Change: &GraphChange{
					Name:       name,
					ChangeType: AddedNode,
					NewVersion: edge.Node.Version,
				},
			}
			dn.Children = append(dn.Children, child)
		}
	}

	// Children only in old graph → removed at this level.
	for _, edge := range oldNode.Dependencies {
		if edge.Node == nil {
			continue
		}
		if _, exists := newByName[edge.Node.Name]; !exists {
			child := DiffNode{
				Name:    edge.Node.Name,
				Version: edge.Node.Version,
				Change: &GraphChange{
					Name:       edge.Node.Name,
					ChangeType: RemovedNode,
					OldVersion: edge.Node.Version,
				},
			}
			dn.Children = append(dn.Children, child)
		}
	}

	return dn
}

// markAll builds a DiffNode tree marking every dependency as the given
// change type. Used when one side of the diff is nil.
func markAll(node *Node, ct ChangeType, visited map[string]bool) DiffNode {
	dn := DiffNode{Name: node.Name, Version: node.Version}
	for _, edge := range node.Dependencies {
		if edge.Node == nil {
			continue
		}
		child := DiffNode{
			Name:    edge.Node.Name,
			Version: edge.Node.Version,
			Change:  &GraphChange{Name: edge.Node.Name, ChangeType: ct},
		}
		if ct == AddedNode {
			child.Change.NewVersion = edge.Node.Version
		} else {
			child.Change.OldVersion = edge.Node.Version
		}
		if !edge.Shared && !visited[edge.Node.Name] {
			visited[edge.Node.Name] = true
			sub := markAll(edge.Node, ct, visited)
			child.Children = sub.Children
		}
		dn.Children = append(dn.Children, child)
	}
	return dn
}

// fullNodeIndex collects all fully-resolved nodes from a dependency
// graph, indexed by name. When a node appears both as a fully-resolved
// edge and as a shared (shallow) edge, the full version is kept.
func fullNodeIndex(root *Node) map[string]*Node {
	idx := map[string]*Node{}
	fullNodeIndexRec(root, idx)
	return idx
}

func fullNodeIndexRec(node *Node, idx map[string]*Node) {
	if node == nil {
		return
	}
	prev, seen := idx[node.Name]
	if seen && len(prev.Dependencies) > 0 {
		return
	}
	idx[node.Name] = node
	for _, edge := range node.Dependencies {
		fullNodeIndexRec(edge.Node, idx)
	}
}

// childMap indexes a node's direct dependency children by name.
func childMap(node *Node) map[string]*Node {
	m := map[string]*Node{}
	if node == nil {
		return m
	}
	for _, edge := range node.Dependencies {
		if edge.Node != nil {
			m[edge.Node.Name] = edge.Node
		}
	}
	return m
}

// collectTreeChanges walks the diff tree and returns unique changes
// (deduplicated by name, keeping the first occurrence).
func collectTreeChanges(root DiffNode) []GraphChange {
	seen := map[string]bool{}
	var changes []GraphChange
	collectChangesRec(root, &changes, seen)
	return changes
}

func collectChangesRec(node DiffNode, changes *[]GraphChange, seen map[string]bool) {
	if node.Change != nil && !seen[node.Name] {
		*changes = append(*changes, *node.Change)
		seen[node.Name] = true
	}
	for _, child := range node.Children {
		collectChangesRec(child, changes, seen)
	}
}

func sortChanges(changes []GraphChange) {
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Name < changes[j].Name
	})
}
