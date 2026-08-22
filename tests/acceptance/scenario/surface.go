package scenario

import (
	"fmt"
	"slices"
	"strings"
)

// A surface is a place the canonical scenario is deployed. Two exist: the
// Kubernetes surface the Kind harness installs through the operator chart, and
// the Docker Compose surface the clone-free demo artifact runs.
//
// They are NOT equivalent, and the difference is declared here rather than
// hidden in whichever harness happens to skip a step. Compose has no controller,
// so nothing reconciles a Pacto CR into an operational target; every other fact
// the fixture obliges — published revisions, both OCI sources, the observation
// export, the declared/observed/reconciled edge, the signed external evidence
// target — holds identically on both.
//
// Declaring the gap as data is what lets the Product gate REPORT it. A gate that
// simply skipped the target checks on Compose would print the same "fixture
// proved" line for a strictly smaller fixture, which is the failure this type
// exists to make impossible.

// Capability is something a surface must be able to do for the fixture to be
// fully provable on it.
type Capability string

// CapabilityOperationalTarget is a reconciled runtime target: a controller that
// resolves a declared contract reference against something actually running and
// publishes the link. Only Kubernetes has one.
const CapabilityOperationalTarget Capability = "operational-target"

// allCapabilities is every capability a surface can be measured against.
var allCapabilities = []Capability{CapabilityOperationalTarget}

// Surface is one deployment surface of the canonical scenario.
type Surface string

const (
	// SurfaceKubernetes is the operator-managed cluster surface (the Kind harness).
	SurfaceKubernetes Surface = "kubernetes"
	// SurfaceCompose is the Docker Compose surface (the clone-free demo artifact).
	SurfaceCompose Surface = "compose"
)

// surfaceCapabilities lists what each surface PROVIDES. Positive, not negative:
// an unknown surface — including the reachable-by-accident zero value — then
// provides nothing, instead of inheriting the reference surface's answers by
// having no absences recorded against it.
var surfaceCapabilities = map[Surface][]Capability{
	SurfaceKubernetes: {CapabilityOperationalTarget},
	SurfaceCompose:    {},
}

// Surfaces returns every declared surface, in a stable order.
func Surfaces() []Surface { return []Surface{SurfaceKubernetes, SurfaceCompose} }

// Valid reports whether s is a declared surface.
func (s Surface) Valid() bool {
	_, ok := surfaceCapabilities[s]
	return ok
}

// Has reports whether s provides the capability.
func (s Surface) Has(c Capability) bool {
	return slices.Contains(surfaceCapabilities[s], c)
}

// Missing is every capability s does NOT provide, so a gate can name the gap in
// its own output rather than leaving a shorter run looking like a complete one.
func (s Surface) Missing() []Capability {
	var out []Capability
	for _, c := range allCapabilities {
		if !s.Has(c) {
			out = append(out, c)
		}
	}
	return out
}

// ParseSurface resolves a surface name supplied from outside — a flag, an
// environment variable — and refuses anything else by name.
func ParseSurface(name string) (Surface, error) {
	s := Surface(name)
	if s.Valid() {
		return s, nil
	}
	known := make([]string, 0, len(surfaceCapabilities))
	for _, k := range Surfaces() {
		known = append(known, string(k))
	}
	return "", fmt.Errorf("unknown surface %q (known: %s)", name, strings.Join(known, ", "))
}
