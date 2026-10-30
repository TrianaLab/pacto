package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// printFleetTarget must label the revision link with how it was matched, so an
// exact (immutable-digest) link is distinguishable from an inferred, merely
// correlated mutable one (review section S15). The unlabelled/unlinked case is
// covered by TestFleetGetTarget's targets, which carry no revision link.
func TestPrintFleetTarget_LabelsRevisionMatch(t *testing.T) {
	run := func(match string) string {
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		tv := &fleet.TargetView{Target: &fleet.TargetRecord{
			Key: "prod/k8s/x", Service: "x", Compliance: "Compliant",
			ContractRevision: "x@sha256:1", RevisionMatch: match,
		}}
		if err := printFleetTarget(cmd, tv, "text"); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	if out := run("inferred"); !strings.Contains(out, "Revision: x@sha256:1 (inferred)") {
		t.Errorf("inferred link must be labelled:\n%s", out)
	}
	if out := run("exact"); !strings.Contains(out, "Revision: x@sha256:1 (exact)") {
		t.Errorf("exact link must be labelled:\n%s", out)
	}
}

// A finding names the distinct collectors that saw it, and says nothing when none is recorded.
func TestSeenBy(t *testing.T) {
	refs := []finding.EvidenceRef{{Source: "k8s", Collector: "k8s-observer+ebpf"}, {Source: "k8s"}, {Source: "k8s", Collector: "k8s-observer+ebpf"}, {Source: "e", Collector: "pacto-run"}}
	if got := seenBy(refs); got != " (seen by k8s-observer+ebpf, pacto-run)" {
		t.Errorf("seenBy = %q", got)
	}
	if got := seenBy([]finding.EvidenceRef{{Source: "k8s"}}); got != "" {
		t.Errorf("seenBy without collector = %q", got)
	}
}

// The graph answer lists nodes breadth-first; the text view indents by depth,
// so it prints them in path order to keep each node under the parent it was
// reached through.
func TestPrintFleetGraph_IndentsUnderShortestPathParent(t *testing.T) {
	res := &fleet.GraphResult{Root: "r", Direction: fleet.DirectionDependencies, Nodes: []fleet.GraphNode{
		{Key: "a", Name: "a", Depth: 1, Path: []fleet.ServiceKey{"r", "a"}},
		{Key: "z", Name: "z", Depth: 1, Path: []fleet.ServiceKey{"r", "z"}},
		{Key: "b", Name: "b", Depth: 2, Path: []fleet.ServiceKey{"r", "a", "b"}},
		{Key: "t", Name: "t", Depth: 2, Path: []fleet.ServiceKey{"r", "z", "t"}},
	}}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printFleetGraph(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	want := "dependencies of r (4 node(s)):\n" +
		"    a (depth 1)\n" +
		"      b (depth 2)\n" +
		"    z (depth 1)\n" +
		"      t (depth 2)\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	if res.Nodes[1].Key != "z" {
		t.Error("printing must not reorder the caller's answer")
	}
}
