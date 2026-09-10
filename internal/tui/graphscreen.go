package tui

import (
	"fmt"
	"time"

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
	// walkStart is the frame the current traversal reveal began on. Every
	// refresh restarts it, so changing the depth or the direction re-walks the
	// tree rather than swapping one finished picture for another.
	walkStart int
}

// walkDuration is how long the tree takes to draw itself, root outward. It is a
// fixed duration rather than a per-line delay: a two-node neighborhood and a
// two-hundred-node one both finish in the same beat, and the big one simply
// unrolls faster, which is also the honest signal about which is which.
const walkDuration = 600 * time.Millisecond

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
	g.walkStart = c.Frame
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

// bindings are the keys Update handles below, in the order the help lists them.
// The aliases are listed rather than hidden: = and _ are the unshifted keys,
// and a reader who finds them by accident should see them named.
func (g *graphScreen) bindings() []binding {
	return []binding{
		{Key: "+ or =", Help: "one hop deeper"},
		{Key: "- or _", Help: "one hop shallower"},
		{Key: "tab", Help: "next direction: both, dependencies, dependents"},
		{Key: "shift+tab", Help: "previous direction"},
	}
}

func (g *graphScreen) Update(c *Context, msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		// g is the key that opened this screen, and a graph screen's selection is
		// its own root, so letting it reach dispatchVerb pushes an identical screen
		// onto the stack. The reader sees nothing change and then has to press q
		// once per accidental press to get back out.
		if k.String() == graphVerbKey {
			return g, status("already showing the graph of " + label(g.ref))
		}
		if cmd, handled := dispatchVerb(c, g, k); handled {
			return g, cmd
		}
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
		case "tab":
			g.dirIx = (g.dirIx + 1) % len(graphDirections())
			g.refresh(c)
			return g, nil
		case "shift+tab":
			n := len(graphDirections())
			g.dirIx = (g.dirIx - 1 + n) % n
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
	g.vp.SetContent(g.walk(c))
}

// walk is the tree as far as the traversal has drawn it. The rendered tree is
// already in root-outward order, so revealing it line by line IS the traversal:
// the root appears, then its dependencies, then theirs.
func (g *graphScreen) walk(c *Context) string {
	if !c.Anim {
		return g.body
	}
	return revealLines(g.body, g.walkProgress(c))
}

func (g *graphScreen) walkProgress(c *Context) float64 {
	return progressAt(c.Frame, g.walkStart, framesFor(walkDuration))
}

// animating is true while the traversal is still unrolling.
func (g *graphScreen) animating(c *Context) bool { return c.Anim && g.walkProgress(c) < 1 }

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
	// Defensive: Query.Neighborhood is not documented to exclude nil when err is nil,
	// so guard against it to avoid a nil-deref panic in production.
	if g.nb == nil {
		return ""
	}
	s := fmt.Sprintf("depth %d (evaluated %d)   direction %s   %d nodes",
		g.depth, g.nb.EffectiveDepth, g.nb.Direction, len(g.nb.Nodes))
	line := dimStyle.Render(s + "   +/- depth   tab direction")
	if g.nb.Truncated {
		line += "  " + warnStyle.Render("truncated")
	}
	return line
}
