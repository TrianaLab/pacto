package scenario

import (
	"strings"
	"testing"
)

func TestSurface_CapabilitiesAreDeclared(t *testing.T) {
	if !SurfaceKubernetes.Has(CapabilityOperationalTarget) {
		t.Error("the Kubernetes surface must provide operational targets; the operator is what produces them")
	}
	if SurfaceCompose.Has(CapabilityOperationalTarget) {
		t.Error("the Compose surface claims operational targets, but nothing there reconciles a Pacto CR")
	}
}

// The gap is DATA, so a gate can name it. A surface that quietly skipped the
// checks it cannot satisfy would report a smaller fixture as a fully proved one.
func TestSurface_MissingNamesTheGap(t *testing.T) {
	if got := SurfaceKubernetes.Missing(); len(got) != 0 {
		t.Errorf("the Kubernetes surface reports %v missing; it is the reference surface", got)
	}
	got := SurfaceCompose.Missing()
	if len(got) != 1 || got[0] != CapabilityOperationalTarget {
		t.Errorf("Compose reports missing %v, want exactly [%s]", got, CapabilityOperationalTarget)
	}
}

// An unknown surface provides nothing rather than everything: the zero value of
// a string type is reachable by accident, and a surface that answered "yes" to
// every capability would make the reference surface the silent default.
func TestSurface_UnknownProvidesNothing(t *testing.T) {
	for _, s := range []Surface{"", "swarm", "nomad"} {
		if s.Valid() {
			t.Errorf("surface %q reports itself valid", s)
		}
		if s.Has(CapabilityOperationalTarget) {
			t.Errorf("unknown surface %q claims a capability", s)
		}
		if len(s.Missing()) != len(allCapabilities) {
			t.Errorf("unknown surface %q reports %v missing, want every capability", s, s.Missing())
		}
	}
}

func TestParseSurface(t *testing.T) {
	for _, name := range []string{"kubernetes", "compose"} {
		s, err := ParseSurface(name)
		if err != nil {
			t.Fatalf("ParseSurface(%q): %v", name, err)
		}
		if string(s) != name {
			t.Errorf("ParseSurface(%q) = %q", name, s)
		}
	}
	_, err := ParseSurface("swarm")
	if err == nil {
		t.Fatal("an unknown surface name was accepted")
	}
	// The refusal has to be actionable: a typo should print the surfaces that do
	// exist rather than only the one that does not.
	for _, want := range []string{"swarm", "kubernetes", "compose"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}

// Every declared surface must be parseable and every parseable surface declared,
// or a surface exists that no gate can be pointed at.
func TestSurfaces_AreTheParseableOnes(t *testing.T) {
	for _, s := range Surfaces() {
		if !s.Valid() {
			t.Errorf("Surfaces() lists %q, which is not valid", s)
		}
		if _, err := ParseSurface(string(s)); err != nil {
			t.Errorf("Surfaces() lists %q, which ParseSurface refuses: %v", s, err)
		}
	}
	if len(Surfaces()) != len(surfaceCapabilities) {
		t.Errorf("Surfaces() lists %d surfaces, %d are declared", len(Surfaces()), len(surfaceCapabilities))
	}
}
