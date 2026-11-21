package fleet

import (
	"cmp"
	"math"
	"slices"
)

// The layout constants of the approved fleet map mockup: the distance between
// neighbouring nodes, the sunflower spacing, the golden angle, the node radius
// and the gap between two groups, all in world units.
const (
	mapGapWorld  = 14.0
	mapSpacing   = mapGapWorld / 1.9
	mapNodeR     = 2.6
	mapGroupGap  = mapGapWorld * 5
	mapAngles    = 36
	mapColumnGap = mapGroupGap * 1.6
)

var goldenAngle = math.Pi * (3 - math.Sqrt(5))

// layoutNode is one node a group lays out: its key, how many edges it has,
// whether it is inferred and the sub-cluster it sits in ("" for none).
type layoutNode struct {
	key      string
	degree   int
	inferred bool
	sub      string
}

// nodePos is a node's position relative to its group's centre and the index
// of its sub-cluster (-1 for none).
type nodePos struct {
	key  string
	x, y float64
	sub  int
}

// circle is one group or sub-cluster the packer places.
type circle struct {
	key     string
	r       float64
	special bool
	x, y    float64
}

// groupLayout is the inside of one group: its sub-clusters, its radius and
// its nodes in sunflower order.
type groupLayout struct {
	subs  []*circle
	r     float64
	nodes []nodePos
}

// sunflowerOrder sorts nodes hubs first (by degree), inferred last (the
// least certain sit on the rim), ties by key.
func sunflowerOrder(nodes []layoutNode) {
	slices.SortFunc(nodes, func(a, b layoutNode) int {
		return cmp.Or(cmp.Compare(b2i(a.inferred), b2i(b.inferred)), cmp.Compare(b.degree, a.degree), cmp.Compare(a.key, b.key))
	})
}

// sunflower is the position of the i-th node of a sunflower around (cx, cy).
func sunflower(i int, cx, cy float64) (x, y float64) {
	rr := mapSpacing * math.Sqrt(float64(i)+0.5)
	th := float64(i) * goldenAngle
	return cx + rr*math.Cos(th), cy + rr*math.Sin(th)
}

// sunflowerRadius is the radius a sunflower of n nodes needs.
func sunflowerRadius(n int) float64 { return mapSpacing*math.Sqrt(float64(n)+0.5) + mapNodeR }

// layoutGroup lays out the inside of one group from its members alone, so a
// page of nodes never repeats the whole-fleet packing. With sub-clusters each
// is a sunflower, packed like groups; without, the group is one sunflower.
func layoutGroup(nodes []layoutNode) groupLayout {
	nodes = slices.Clone(nodes)
	sunflowerOrder(nodes)
	subOf := map[string]int{}
	var members [][]layoutNode
	for _, n := range nodes {
		if n.sub == "" {
			continue
		}
		i, ok := subOf[n.sub]
		if !ok {
			i = len(members)
			subOf[n.sub] = i
			members = append(members, nil)
		}
		members[i] = append(members[i], n)
	}
	if len(members) == 0 {
		gl := groupLayout{r: sunflowerRadius(len(nodes)) + mapGapWorld*1.4}
		for i, n := range nodes {
			x, y := sunflower(i, 0, 0)
			gl.nodes = append(gl.nodes, nodePos{key: n.key, x: x, y: y, sub: -1})
		}
		return gl
	}
	return layoutSubs(members)
}

// layoutSubs packs one sunflower per sub-cluster, each member list already in
// sunflower order.
func layoutSubs(members [][]layoutNode) groupLayout {
	var gl groupLayout
	for _, ms := range members {
		gl.subs = append(gl.subs, &circle{key: ms[0].sub, r: sunflowerRadius(len(ms)) + mapGapWorld*0.8})
	}
	packCircles(gl.subs, mapGapWorld*2, nil)
	for i, ms := range members {
		c := gl.subs[i]
		for j, n := range ms {
			x, y := sunflower(j, c.x, c.y)
			gl.nodes = append(gl.nodes, nodePos{key: n.key, x: x, y: y, sub: i})
		}
		gl.r = max(gl.r, math.Hypot(c.x, c.y)+c.r)
	}
	gl.r += mapGapWorld * 1.4
	return gl
}

// circleGrid is a uniform grid of placed circles: two circles closer than
// the cell size sit in adjacent cells, so an overlap check reads nine cells.
type circleGrid struct {
	cell  float64
	cells map[[2]int][]*circle
}

func (g *circleGrid) at(x, y float64) [2]int {
	return [2]int{int(math.Floor(x / g.cell)), int(math.Floor(y / g.cell))}
}

func (g *circleGrid) add(c *circle) { k := g.at(c.x, c.y); g.cells[k] = append(g.cells[k], c) }

// free reports whether a circle of radius r at (x, y) keeps gap from every
// placed circle.
func (g *circleGrid) free(x, y, r, gap float64) bool {
	k := g.at(x, y)
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for _, p := range g.cells[[2]int{k[0] + dx, k[1] + dy}] {
				m := p.r + r + gap - 0.01
				if (x-p.x)*(x-p.x)+(y-p.y)*(y-p.y) < m*m {
					return false
				}
			}
		}
	}
	return true
}

// packCircles places cs (already in placement order) each at the tangent
// candidate, 36 angles around every placed circle, that scores lowest:
// close to the centre (slightly wider than tall), specials further out, and
// close to the weighted centroid of the placed circles weight links it to.
// Overlap checks read a grid, so a placement costs O(placed x 36 x k) with k
// the circles in nine cells.
func packCircles(cs []*circle, gap float64, weight func(i, j int) int) {
	var maxR float64
	for _, c := range cs {
		maxR = max(maxR, c.r)
	}
	grid := &circleGrid{cell: 2*maxR + gap, cells: map[[2]int][]*circle{}}
	for i, c := range cs {
		if i > 0 {
			cx, cy, linked := centroid(cs[:i], i, weight)
			c.x, c.y = bestCandidate(cs[:i], c, gap, grid, func(x, y float64) float64 {
				if c.special {
					return math.Hypot(x*0.75, y)
				}
				s := math.Hypot(x*0.75, y) * 0.6
				if linked {
					s += math.Hypot(x-cx, y-cy) * 0.5
				}
				return s
			})
		}
		grid.add(c)
	}
}

// centroid is the weighted centroid of placed by their weight to circle i.
func centroid(placed []*circle, i int, weight func(i, j int) int) (x, y float64, linked bool) {
	if weight == nil {
		return 0, 0, false
	}
	var w float64
	for j, p := range placed {
		if wj := float64(weight(i, j)); wj > 0 {
			x, y, w = x+p.x*wj, y+p.y*wj, w+wj
		}
	}
	if w == 0 {
		return 0, 0, false
	}
	return x / w, y / w, true
}

// bestCandidate is the free tangent candidate around placed with the lowest
// score; candidates are tried in a fixed order, so ties are deterministic.
func bestCandidate(placed []*circle, c *circle, gap float64, grid *circleGrid, score func(x, y float64) float64) (float64, float64) {
	// No free candidate (never seen, kept for safety): right of everything.
	bx, by, best := 0.0, 0.0, math.Inf(1)
	for _, p := range placed {
		bx = max(bx, p.x+p.r+gap+c.r)
	}
	for _, p := range placed {
		d := p.r + c.r + gap
		for a := range mapAngles {
			th := float64(a) / mapAngles * 2 * math.Pi
			x, y := p.x+d*math.Cos(th), p.y+d*math.Sin(th)
			if s := score(x, y); s < best && grid.free(x, y, c.r, gap) {
				bx, by, best = x, y, s
			}
		}
	}
	return bx, by
}

// packColumns places groups in columns left to right by depth (nil depth
// last), each column centred vertically, largest first.
func packColumns(cs []*circle, depth []*int) {
	type col struct {
		depth int
		cs    []*circle
	}
	var cols []*col
	at := map[int]*col{}
	last := math.MaxInt
	for i, c := range cs {
		d := last
		if depth[i] != nil {
			d = *depth[i]
		}
		if at[d] == nil {
			at[d] = &col{depth: d}
			cols = append(cols, at[d])
		}
		at[d].cs = append(at[d].cs, c)
	}
	slices.SortFunc(cols, func(a, b *col) int { return cmp.Compare(a.depth, b.depth) })
	x := 0.0
	for _, cl := range cols {
		w, total := 0.0, -mapGroupGap
		for _, c := range cl.cs {
			w, total = max(w, c.r), total+2*c.r+mapGroupGap
		}
		y := -total / 2
		for _, c := range cl.cs {
			c.x, c.y = x+w, y+c.r
			y += 2*c.r + mapGroupGap
		}
		x += 2*w + mapColumnGap
	}
}

// round1 rounds a coordinate to 0.1, the precision every answer carries.
func round1(v float64) float64 { return math.Round(v*10) / 10 }
