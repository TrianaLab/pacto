package fleet

import (
	"slices"
	"testing"
)

// TestAvailabilityLines: each environment says its state, provenance and
// reason, the unlabelled group is named, partial is marked and the bound is
// counted.
func TestAvailabilityLines(t *testing.T) {
	a := Availability{Total: 3, ByEnvironment: []EnvironmentAvailability{
		{Environment: "prod", State: AvailabilityAvailable, Provenance: ProvenanceObserved, Reason: "2 of 2 targets reporting"},
		{State: AvailabilityUnknown, Provenance: provenanceUnknown, Reason: "r", Partial: true},
	}}
	want := []string{
		"prod: available (observed, 2 of 2 targets reporting)",
		"(no environment label): unknown (unknown, r) [partial]",
		"1 more environments",
	}
	if got := a.Lines(); !slices.Equal(got, want) {
		t.Errorf("Lines = %q, want %q", got, want)
	}
}

// TestSuccessorText: a resolved successor names its revision, an unresolved
// one says unknown and why.
func TestSuccessorText(t *testing.T) {
	rev := EntityRef{Label: "postgresql 17.0.0"}
	if got := (&SuccessorInfo{Ref: "oci://x:17", Revision: &rev}).Text(); got != "oci://x:17 (postgresql 17.0.0)" {
		t.Errorf("resolved = %q", got)
	}
	if got := (&SuccessorInfo{Ref: "oci://x:18", Reason: "no match"}).Text(); got != "oci://x:18 (unknown: no match)" {
		t.Errorf("unresolved = %q", got)
	}
}

// TestObservedConsumersLine: stale and uncontracted rows say so, the bound is
// counted and an empty preview renders nothing.
func TestObservedConsumersLine(t *testing.T) {
	if got := (ObservedConsumersPreview{}).Line(); got != "" {
		t.Errorf("empty = %q", got)
	}
	p := ObservedConsumersPreview{Total: 4, Count: 3, Truncated: true, Items: []ObservedConsumer{
		{Consumer: &EntityRef{Label: "a"}, Stale: true}, {Consumer: &EntityRef{Label: "b"}}, {Caller: "c", Uncontracted: true},
	}}
	if got, want := p.Line(), "Also calling a provider (observed, no declaration): a (stale), b, c (no contract, last seen unknown); 1 more"; got != want {
		t.Errorf("Line = %q, want %q", got, want)
	}
}
