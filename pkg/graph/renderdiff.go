package graph

import (
	"fmt"
	"slices"
	"strings"
)

// RenderDiffTree renders a graph diff as a tree-style string,
// showing only nodes that have changes in their subtree.
// Uses the same tree connectors as RenderTree for consistency.
func RenderDiffTree(d *GraphDiff) string {
	if d == nil || len(d.Changes) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintln(&b, d.Root.Name)
	renderDiffChildren(&b, d.Root.Children, "")
	return b.String()
}

func renderDiffChildren(b *strings.Builder, children []DiffNode, prefix string) {
	// Filter to only children that have changes in their subtree.
	var relevant []DiffNode
	for _, child := range children {
		if hasChanges(child) {
			relevant = append(relevant, child)
		}
	}

	for i, child := range relevant {
		connector, childPrefix := treeConnectors(i == len(relevant)-1)

		label := formatDiffLabel(child)
		fmt.Fprintf(b, "%s%s%s\n", prefix, connector, label)

		renderDiffChildren(b, child.Children, prefix+childPrefix)
	}
}

func formatDiffLabel(n DiffNode) string {
	if n.Change == nil {
		return n.Name
	}
	switch n.Change.ChangeType {
	case VersionChanged:
		return fmt.Sprintf("%-14s%s → %s", n.Name, n.Change.OldVersion, n.Change.NewVersion)
	case AddedNode:
		return fmt.Sprintf("%-14s+%s", n.Name, n.Change.NewVersion)
	case RemovedNode:
		return fmt.Sprintf("%-14s-%s", n.Name, n.Change.OldVersion)
	default:
		return n.Name
	}
}

// hasChanges returns true if the node or any of its descendants have a change.
func hasChanges(n DiffNode) bool {
	return n.Change != nil || slices.ContainsFunc(n.Children, hasChanges)
}
