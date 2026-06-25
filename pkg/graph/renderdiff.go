package graph

import (
	"fmt"
	"slices"
	"strings"
)

// DiffColors holds optional colorizer functions applied to diff tree labels.
// A nil field is treated as identity, so the zero value renders plain text.
type DiffColors struct {
	Name    func(string) string // node name (recommended nil — alignment safety)
	Added   func(string) string
	Removed func(string) string
	Changed func(string) string
}

// RenderDiffTree renders a graph diff as a tree-style string,
// showing only nodes that have changes in their subtree.
// Uses the same tree connectors as RenderTree for consistency.
func RenderDiffTree(d *GraphDiff) string {
	return RenderDiffTreeColored(d, DiffColors{})
}

// RenderDiffTreeColored renders a graph diff as a tree-style string with
// optional colorizers applied to change labels.
func RenderDiffTreeColored(d *GraphDiff, col DiffColors) string {
	if d == nil || len(d.Changes) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintln(&b, apply(col.Name, d.Root.Name))
	renderDiffChildren(&b, d.Root.Children, "", col)
	return b.String()
}

func renderDiffChildren(b *strings.Builder, children []DiffNode, prefix string, col DiffColors) {
	// Filter to only children that have changes in their subtree.
	var relevant []DiffNode
	for _, child := range children {
		if hasChanges(child) {
			relevant = append(relevant, child)
		}
	}

	for i, child := range relevant {
		connector, childPrefix := treeConnectors(i == len(relevant)-1)

		label := formatDiffLabel(child, col)
		fmt.Fprintf(b, "%s%s%s\n", prefix, connector, label)

		renderDiffChildren(b, child.Children, prefix+childPrefix, col)
	}
}

// diffLabelWidth is the left-aligned column width for service names in the diff
// tree, sized so the version transition that follows lines up across rows.
const diffLabelWidth = 14

func formatDiffLabel(n DiffNode, col DiffColors) string {
	var label string
	var picker func(string) string
	if n.Change == nil {
		label = n.Name
		picker = col.Name
	} else {
		switch n.Change.ChangeType {
		case VersionChanged:
			label = fmt.Sprintf("%-*s%s → %s", diffLabelWidth, n.Name, n.Change.OldVersion, n.Change.NewVersion)
			picker = col.Changed
		case AddedNode:
			label = fmt.Sprintf("%-*s+%s", diffLabelWidth, n.Name, n.Change.NewVersion)
			picker = col.Added
		case RemovedNode:
			label = fmt.Sprintf("%-*s-%s", diffLabelWidth, n.Name, n.Change.OldVersion)
			picker = col.Removed
		default:
			label = n.Name
			picker = col.Name
		}
	}
	return apply(picker, label)
}

// hasChanges returns true if the node or any of its descendants have a change.
func hasChanges(n DiffNode) bool {
	return n.Change != nil || slices.ContainsFunc(n.Children, hasChanges)
}
