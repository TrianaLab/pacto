package graph

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

const fuzzNodes = 6

// fuzzFetcher serves synthetic contracts for nodes "n0".."n5" and counts fetches
// per node so dedup / single-flight can be asserted.
type fuzzFetcher struct {
	deps   [][]int
	mu     sync.Mutex
	counts map[int]int
}

func (ff *fuzzFetcher) Fetch(_ context.Context, dep contract.Dependency) (*contract.Bundle, error) {
	i := fuzzIndex(dep.Ref)
	ff.mu.Lock()
	ff.counts[i]++
	ff.mu.Unlock()
	return &contract.Bundle{Contract: fuzzContract(i, ff.deps[i])}, nil
}

func fuzzIndex(ref string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(ref, "n"))
	return n
}

func fuzzContract(i int, children []int) *contract.Contract {
	c := &contract.Contract{Service: contract.Service{Name: fmt.Sprintf("n%d", i), Version: "1.0.0"}}
	for _, ch := range children {
		ref := fmt.Sprintf("n%d", ch)
		c.Dependencies = append(c.Dependencies, contract.Dependency{Name: ref, Ref: ref, Required: true})
	}
	return c
}

// reachableCyclic reports whether the subgraph reachable from start contains a
// directed cycle (self-loops included).
func reachableCyclic(deps [][]int, start int) bool {
	color := make([]int, len(deps)) // 0 white, 1 gray, 2 black
	var dfs func(u int) bool
	dfs = func(u int) bool {
		color[u] = 1
		for _, v := range deps[u] {
			if color[v] == 1 || (color[v] == 0 && dfs(v)) {
				return true
			}
		}
		color[u] = 2
		return false
	}
	return dfs(start)
}

// FuzzResolveGraph feeds arbitrary directed graphs (adjacency from the fuzz bytes)
// into the recursive resolver and asserts the resolver's guarantees, all of which
// hold deterministically regardless of concurrent scheduling:
//
//   - Termination: the fuzz harness times out on any hang; a cyclic graph must not
//     loop forever (dedup guarantees this).
//   - Single-flight dedup: no node is fetched more than once.
//   - Cycle soundness: every reported cycle is a genuine repeated-ref path.
//   - Cycle completeness: cycles are reported IFF a cycle is reachable from the
//     root. detectCycles runs a deterministic post-resolution DFS over the final
//     graph, so a cycle split across two concurrently-resolved branches (e.g. 2->3
//     and 3->2 on separate branches, once deduped to Shared edges) is still
//     reported. This target originally documented that gap; the biconditional
//     below now pins the fix.
func FuzzResolveGraph(f *testing.F) {
	f.Add([]byte{0, 1, 1, 2})                   // 0->1->2 chain
	f.Add([]byte{0, 1, 1, 0})                   // 0->1->0 cycle
	f.Add([]byte{0, 0})                         // self loop
	f.Add([]byte{0, 1, 0, 2, 1, 3, 2, 3})       // diamond over n3
	f.Add([]byte{0, 1, 0, 3, 1, 2, 2, 3, 3, 2}) // split cycle 2<->3 across two branches
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, adj []byte) {
		deps := make([][]int, fuzzNodes)
		for i := 0; i+1 < len(adj); i += 2 {
			from := int(adj[i]) % fuzzNodes
			to := int(adj[i+1]) % fuzzNodes
			deps[from] = append(deps[from], to)
		}

		ff := &fuzzFetcher{deps: deps, counts: map[int]int{}}
		root := fuzzContract(0, deps[0])
		res := ResolveWithOptions(context.Background(), root, ff, ResolveOptions{})

		// Single-flight: no node is fetched more than once.
		for i, n := range ff.counts {
			if n > 1 {
				t.Fatalf("node n%d fetched %d times, want <=1 (dedup)", i, n)
			}
		}

		// Every reported cycle must be a genuine repeated-ref path.
		for _, cyc := range res.Cycles {
			if len(cyc) < 2 || !repeats(cyc) {
				t.Fatalf("reported cycle is not a real repeated path: %v", cyc)
			}
		}

		// Soundness AND completeness: cycles are reported IFF a cycle is reachable
		// from the root. The resolver only fetches root-reachable nodes, so the
		// resolved closure is exactly that subgraph — its cyclicity must match the
		// static verdict, independent of concurrent traversal order.
		if got, want := len(res.Cycles) > 0, reachableCyclic(deps, 0); got != want {
			t.Fatalf("cycle report mismatch: reported=%v reachableCyclic=%v deps=%v cycles=%v", got, want, deps, res.Cycles)
		}
	})
}

// repeats reports whether a path contains a duplicated element (a true cycle).
func repeats(path []string) bool {
	seen := map[string]bool{}
	for _, p := range path {
		if seen[p] {
			return true
		}
		seen[p] = true
	}
	return false
}
