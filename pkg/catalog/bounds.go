package catalog

// Every bound here stops work rather than trimming an answer that was already
// paid for. MaxRoots, MaxRevisions, MaxEdges, MaxDepth, MaxPathLength and
// MaxPaths are all checked BEFORE the resolver is called or the walk descends,
// so a catalog built against a hostile or merely enormous closure performs a
// bounded number of resolutions -- which permanent tests assert by counting
// resolver calls, not by measuring the output.
//
// MaxUnresolved, MaxConflicts and MaxLimitations bound derived reporting only.
// Refusing to resolve because too many OTHER references already failed would
// hide healthy parts of the closure, so those three cap their lists and say so.

// Bound defaults. A zero or negative field takes the default; a field above its
// ceiling is clamped to the ceiling. The result is exposed as [Meta.Bounds], so
// what actually applied is always reviewable.
const (
	DefaultMaxRoots       = 50
	DefaultMaxRevisions   = 500
	DefaultMaxEdges       = 2000
	DefaultMaxDepth       = 10
	DefaultMaxPaths       = 20
	DefaultMaxPathLength  = 10
	DefaultMaxUnresolved  = 200
	DefaultMaxConflicts   = 200
	DefaultMaxLimitations = 100
)

// Bound ceilings. They exist so a caller cannot ask for an unbounded catalog.
const (
	CeilingMaxRoots       = 200
	CeilingMaxRevisions   = 5000
	CeilingMaxEdges       = 20000
	CeilingMaxDepth       = 25
	CeilingMaxPaths       = 200
	CeilingMaxPathLength  = 25
	CeilingMaxUnresolved  = 2000
	CeilingMaxConflicts   = 2000
	CeilingMaxLimitations = 1000
)

// Bounds is the explicit, reviewable work budget for one catalog.
type Bounds struct {
	// MaxRoots caps how many requested roots are resolved at all.
	MaxRoots int `json:"maxRoots"`
	// MaxRevisions caps distinct resolved revisions. At the cap, a reference that
	// is not already resolved is refused without calling the resolver.
	MaxRevisions int `json:"maxRevisions"`
	// MaxEdges caps distinct dependency edges. At the cap, a dependency is
	// refused without calling the resolver.
	MaxEdges int `json:"maxEdges"`
	// MaxDepth caps how many declarations deep the walk descends.
	MaxDepth int `json:"maxDepth"`
	// MaxPaths caps retained root-to-revision paths per revision. A revision at
	// the cap is not entered again, so its subtree is not re-walked either.
	MaxPaths int `json:"maxPaths"`
	// MaxPathLength caps the length of a retained path. A retained path's length
	// is its depth, so this bound and MaxDepth meet at the same place and the
	// smaller of the two is what actually stops the walk. Both are kept because
	// both are independently reviewable.
	MaxPathLength int `json:"maxPathLength"`
	// MaxUnresolved caps recorded unresolved dependencies.
	MaxUnresolved int `json:"maxUnresolved"`
	// MaxConflicts caps recorded conflicts.
	MaxConflicts int `json:"maxConflicts"`
	// MaxLimitations caps recorded distinct limitations.
	MaxLimitations int `json:"maxLimitations"`
}

// effective fills defaults and applies ceilings.
func (b Bounds) effective() Bounds {
	return Bounds{
		MaxRoots:       clampBound(b.MaxRoots, DefaultMaxRoots, CeilingMaxRoots),
		MaxRevisions:   clampBound(b.MaxRevisions, DefaultMaxRevisions, CeilingMaxRevisions),
		MaxEdges:       clampBound(b.MaxEdges, DefaultMaxEdges, CeilingMaxEdges),
		MaxDepth:       clampBound(b.MaxDepth, DefaultMaxDepth, CeilingMaxDepth),
		MaxPaths:       clampBound(b.MaxPaths, DefaultMaxPaths, CeilingMaxPaths),
		MaxPathLength:  clampBound(b.MaxPathLength, DefaultMaxPathLength, CeilingMaxPathLength),
		MaxUnresolved:  clampBound(b.MaxUnresolved, DefaultMaxUnresolved, CeilingMaxUnresolved),
		MaxConflicts:   clampBound(b.MaxConflicts, DefaultMaxConflicts, CeilingMaxConflicts),
		MaxLimitations: clampBound(b.MaxLimitations, DefaultMaxLimitations, CeilingMaxLimitations),
	}
}

func clampBound(v, def, ceiling int) int {
	if v <= 0 {
		return def
	}
	return min(v, ceiling)
}
