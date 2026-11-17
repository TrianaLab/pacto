package detect

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Node is what the rules read of one node.
type Node struct {
	ProviderID    string
	Labels        map[string]string
	KernelVersion string
}

// nfdBTF is the Node Feature Discovery label for a kernel built with BTF.
const nfdBTF = "feature.node.kubernetes.io/kernel-config.DEBUG_INFO_BTF"

// Cloud is the provider most nodes name in their providerID, refined by the
// managed-service node labels. Mixed providers name the majority, partial.
func Cloud(ns []Node) Entry {
	t := tallies{}
	for _, n := range ns {
		if c := cloudOf(n); c != "" {
			t.add(c, "")
		}
	}
	if len(t) == 0 {
		return UnknownEntry("cloud", "no node carries a providerID")
	}
	ranked := t.ranked()
	e := Entry{Kind: "cloud", Value: ranked[0].name, Evidence: fmt.Sprintf("on %d of %d nodes", ranked[0].n, len(ns)),
		Source: "node providerID", Completeness: Complete, Scope: fleet.ScopeCluster, Details: nodeDetails(ranked)}
	if ranked[0].n < len(ns) {
		return partial(e, "nodes name more than one provider or none")
	}
	return e
}

// cloudOf names the provider of one node, empty when it names none.
func cloudOf(n Node) string {
	scheme, _, ok := strings.Cut(n.ProviderID, "://")
	if !ok || scheme == "" {
		return ""
	}
	has := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := n.Labels[k]; ok {
				return true
			}
		}
		return false
	}
	switch scheme {
	case "gce":
		return pick(has("cloud.google.com/gke-nodepool"), "GKE", "GCE")
	case "aws":
		return pick(has("eks.amazonaws.com/nodegroup", "alpha.eksctl.io/cluster-name"), "EKS", "AWS")
	case "azure":
		return pick(has("kubernetes.azure.com/cluster"), "AKS", "Azure")
	}
	return scheme
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// BTF levels, worst last: one node decides down, never up.
var btfLevels = []string{"likely", "unknown", "unlikely"}

// Kernel is the distribution of node kernels (major.minor) and whether BTF
// is likely. It is always partial: nothing here is confirmed on the node, so
// BTF is likely, unlikely or unknown and never more.
func Kernel(ns []Node) Entry {
	if len(ns) == 0 {
		return UnknownEntry("nodes", "no nodes found")
	}
	t, btf := tallies{}, 0
	for _, n := range ns {
		v, level := kernelOf(n)
		t.add(v, "")
		btf = max(btf, level)
	}
	ranked := t.ranked()
	return partial(Entry{Kind: "nodes", Value: fmt.Sprintf("kernel %s on %d of %d nodes", ranked[0].name, ranked[0].n, len(ns)),
		Evidence: "BTF " + btfLevels[btf] + " (unconfirmed)", Source: "nodes", Scope: fleet.ScopeCluster, Details: nodeDetails(ranked)},
		"not confirmed on the node")
}

// kernelOf is the major.minor of a node's kernel ("unknown" when unparsable)
// and its BTF level: likely with the NFD label or kernel 5.8 or newer.
func kernelOf(n Node) (string, int) {
	major, minor, ok := majorMinor(n.KernelVersion)
	switch {
	case n.Labels[nfdBTF] == "true" && !ok:
		return "unknown", 0
	case !ok:
		return "unknown", 1
	case n.Labels[nfdBTF] == "true", major > 5 || major == 5 && minor >= 8:
		return fmt.Sprintf("%d.%d", major, minor), 0
	}
	return fmt.Sprintf("%d.%d", major, minor), 2
}

// majorMinor parses the leading major.minor of a kernel version. Only
// [0-9A-Za-z.+_-] of it is read: kubelets write it, and it reaches terminals.
func majorMinor(version string) (int, int, bool) {
	safe := strings.Map(func(r rune) rune {
		if r < 0x80 && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune(".+_-", r)) {
			return r
		}
		return -1
	}, version)
	majorS, rest, _ := strings.Cut(safe, ".")
	major, err1 := strconv.Atoi(majorS)
	minor, err2 := strconv.Atoi(rest[:len(rest)-len(strings.TrimLeft(rest, "0123456789"))])
	return major, minor, err1 == nil && err2 == nil
}

// nodeDetails is one "<name>: <n> nodes" line per ranked candidate, capped.
func nodeDetails(ranked []*tally) []string {
	var out []string
	for _, c := range capped(ranked, MaxDetails) {
		out = append(out, fmt.Sprintf("%s: %d nodes", c.name, c.n))
	}
	return out
}
