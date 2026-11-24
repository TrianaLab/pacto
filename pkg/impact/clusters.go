package impact

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// attributeClusters says, on an answer over two or more clusters, which
// clusters it read and where each consumer runs, and copies the unread
// cluster count. A consumer with any stale or unavailable cluster loses a
// compatible verdict. With one cluster the answer is unchanged.
func (r *Result) attributeClusters(snap *fleet.FleetSnapshot, q *fleet.Query, meta fleet.ProductMeta, clustersOf func(AffectedConsumer) []string) {
	r.UnreadClusters = snap.UnreadClusters
	if len(meta.Clusters) < 2 {
		return
	}
	r.Clusters = meta.Clusters
	// The uncapped list: the meta cap never hides a stale cluster.
	statuses := map[string]fleet.ClusterSummary{}
	for _, c := range q.ClusterSummaries() {
		statuses[c.ID] = c
	}
	for i := range r.Consumers {
		c := &r.Consumers[i]
		c.Clusters = clustersOf(*c)
		staleClustersUnknown(c, statuses, snap.GeneratedAt)
	}
}

// targetClusters is the clusters of a contract consumer: those its targets
// sit in.
func targetClusters(snap *fleet.FleetSnapshot) func(AffectedConsumer) []string {
	return func(c AffectedConsumer) []string { return consumerClusters(snap, c.Targets) }
}

// consumerClusters are the sorted ids of the clusters the targets sit in.
func consumerClusters(snap *fleet.FleetSnapshot, targets []string) []string {
	var out []string
	for _, tk := range targets {
		if t := snap.Targets[fleet.TargetKey(tk)]; t != nil && t.Cluster != "" && !slices.Contains(out, t.Cluster) {
			out = append(out, t.Cluster)
		}
	}
	slices.Sort(out)
	return out
}

// staleClustersUnknown turns a compatible verdict unknown when any cluster c
// runs in is stale or unavailable: the instance there may run another
// revision whose range refuses the change. A cluster with no status counts as
// unavailable. An incompatible verdict or an unmet guarantee stays.
func staleClustersUnknown(c *AffectedConsumer, statuses map[string]fleet.ClusterSummary, at time.Time) {
	if c.CompatibilityVerdict != CompatibilityCompatible && c.rangeVerdict != CompatibilityCompatible {
		return
	}
	i := slices.IndexFunc(c.Clusters, func(id string) bool {
		sum, ok := statuses[id]
		return !ok || sum.Status == fleet.SourceStale || sum.Status == fleet.SourceUnavailable
	})
	if i < 0 {
		return
	}
	if c.CompatibilityVerdict == CompatibilityCompatible {
		c.CompatibilityVerdict = CompatibilityUnknown
	}
	if c.rangeVerdict == CompatibilityCompatible {
		c.rangeVerdict = CompatibilityUnknown
	}
	id := c.Clusters[i]
	sum := statuses[id]
	// With no summary, the uid prefix: the display name of last resort (514 D2).
	note := "cluster " + cmp.Or(sum.Name, id[:min(8, len(id))]) + " " + publishedAgo(sum.AsOf, at) + ", so Pacto cannot tell which revision of " + c.Service + " runs there now"
	if c.Note != "" {
		note = c.Note + "; " + note
	}
	c.Note = note
}

// publishedAgo says how old a cluster's last read is.
func publishedAgo(asOf *time.Time, at time.Time) string {
	if asOf == nil {
		return "could not be read"
	}
	d := max(at.Sub(*asOf), 0)
	switch {
	case d < time.Hour:
		return "last published " + strconv.Itoa(int(d/time.Minute)) + "m ago"
	case d < 48*time.Hour:
		return "last published " + strconv.Itoa(int(d/time.Hour)) + "h ago"
	}
	return "last published " + strconv.Itoa(int(d/(24*time.Hour))) + "d ago"
}

// unreadSentence says that n clusters are not members, so consumers there
// are unknown, and what to do.
func unreadSentence(n int) string {
	return strconv.Itoa(n) + " " + plural(n, "cluster publishes", "clusters publish") + " to this hub and " + plural(n, "is not a member", "are not members") +
		", so consumers there are unknown. Approve or remove " + plural(n, "it", "them") + " (pacto cluster list) and run the analysis again"
}
