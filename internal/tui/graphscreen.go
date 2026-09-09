package tui

import (
	"fmt"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/graph"
)

// graphScreen shows the bounded neighborhood around one entity as a tree, with
// depth and direction under the reader's control.
type graphScreen struct {
	ref     fleet.EntityRef
	vp      viewport.Model
	depth   int
	dirIx   int
	body    string
	nb      *fleet.Neighborhood
	loadErr error
}

// graphDirections is the cycle order for the direction toggle.
func graphDirections() []fleet.Direction {
	return []fleet.Direction{fleet.DirectionBoth, fleet.DirectionDependencies, fleet.DirectionDependents}
}

func newGraphScreen(c *Context, ref fleet.EntityRef) screen {
	g := &graphScreen{ref: ref, vp: viewport.New(), depth: fleet.DefaultNeighborhoodDepth}
	g.refresh(c)
	return g
}

func (g *graphScreen) refresh(c *Context) {
	nb, err := c.Query.Neighborhood(fleet.NeighborhoodQuery{
		Kind:      g.ref.Kind,
		Key:       g.ref.Key,
		Direction: graphDirections()[g.dirIx],
		Depth:     g.depth,
		MaxNodes:  fleet.DefaultMaxNodes,
		MaxEdges:  fleet.DefaultMaxEdges,
	})
	g.loadErr, g.nb = err, nb
	if err != nil {
		g.body = ""
		return
	}
	g.body = graph.RenderTreeColored(neighborhoodToTree(nb), treeColors())
}

func (g *graphScreen) selected() (fleet.EntityRef, bool) { return g.ref, true }

func (g *graphScreen) Title() string { return "Graph: " + label(g.ref) }

func (g *graphScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "+", "=":
			if g.depth < fleet.MaxNeighborhoodDepth {
				g.depth++
				g.refresh(c)
			}
			return g, nil
		case "-", "_":
			if g.depth > 1 {
				g.depth--
				g.refresh(c)
			}
			return g, nil
		case "d":
			g.dirIx = (g.dirIx + 1) % len(graphDirections())
			g.refresh(c)
			return g, nil
		}
	}
	g.resize(c)
	vp, cmd := g.vp.Update(msg)
	g.vp = vp
	return g, cmd
}

func (g *graphScreen) resize(c *Context) {
	g.vp.SetWidth(c.Width)
	h := c.Height - 4
	if h < 3 {
		h = 3
	}
	g.vp.SetHeight(h)
	g.vp.SetContent(g.body)
}

func (g *graphScreen) View(c *Context) string {
	if g.loadErr != nil {
		return errorStyle.Render("neighborhood query failed: " + g.loadErr.Error())
	}
	g.resize(c)
	return g.bar() + "\n" + g.vp.View()
}

// bar states the bounds the projection actually used. EffectiveDepth can be
// smaller than the requested depth (a target projection is always one hop), so
// showing the requested number alone would be a lie.
func (g *graphScreen) bar() string {
	if g.nb == nil {
		return ""
	}
	s := fmt.Sprintf("depth %d (evaluated %d)   direction %s   %d nodes",
		g.depth, g.nb.EffectiveDepth, g.nb.Direction, len(g.nb.Nodes))
	line := dimStyle.Render(s + "   +/- depth   d direction")
	if g.nb.Truncated {
		line += "  " + warnStyle.Render("truncated")
	}
	return line
}
