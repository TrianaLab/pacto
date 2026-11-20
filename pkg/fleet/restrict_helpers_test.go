package fleet

import "slices"

// anyCluster is a Restrict namespace predicate that ignores the cluster.
func anyCluster(f func(string) bool) func(string, string) bool {
	return func(_, ns string) bool { return f(ns) }
}

// anyClusterKind is a Restrict workload predicate that ignores the cluster;
// nil stays nil.
func anyClusterKind(f func(string, string) bool) func(string, string, string) bool {
	if f == nil {
		return nil
	}
	return func(_, ns, kind string) bool { return f(ns, kind) }
}

// namespaceNames is the namespaces of pairs, sorted and unique: what
// Namespaces returned before it named the cluster.
func namespaceNames(pairs []ClusterNamespace) []string {
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.Namespace)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
