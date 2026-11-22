package fleet

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// CodeClusterIssuerDiffers is the attention code of an issuer whose signed-in
// people member clusters review under a group mapping, or not at all.
const CodeClusterIssuerDiffers = "CLUSTER_ISSUER_DIFFERS"

// maxIssuerClusters is how many clusters an issuer item names before it
// counts the rest.
const maxIssuerClusters = 5

// clusterLinkAttention is one item per token issuer that some member reviews
// under a group mapping, or refuses with none approved, naming those members.
// Approvals from the hub are disabled for them, so a person must know.
func (q *Query) clusterLinkAttention() []AttentionItem {
	type issuerClusters struct {
		clusters []ClusterRecord
		models   []IssuerModel
	}
	by := map[string]*issuerClusters{}
	for _, c := range q.snap.Clusters {
		if c.Link == nil {
			continue
		}
		for _, m := range c.Link.Issuers {
			if m.Model != IssuerGroupMapping && m.Model != IssuerNotAccepted {
				continue
			}
			g := by[m.Issuer]
			if g == nil {
				g = &issuerClusters{}
				by[m.Issuer] = g
			}
			g.clusters, g.models = append(g.clusters, c), append(g.models, m)
		}
	}
	issuers := make([]string, 0, len(by))
	for iss := range by {
		issuers = append(issuers, iss)
	}
	slices.Sort(issuers)
	out := make([]AttentionItem, 0, len(issuers))
	for _, iss := range issuers {
		out = append(out, q.issuerDiffersItem(iss, by[iss].clusters, by[iss].models))
	}
	return out
}

// issuerDiffersItem is the item of issuer over clusters, models[i] being how
// clusters[i] reviews its people. It is anchored on the first cluster's
// first source.
func (q *Query) issuerDiffersItem(issuer string, clusters []ClusterRecord, models []IssuerModel) AttentionItem {
	names := make([]string, len(clusters))
	for i, c := range clusters {
		names[i] = cmp.Or(c.Name, c.ID)
	}
	list := boundedNames(names)
	next := "Reads in " + list + " use the group mapping approved by " + models[0].MappedBy
	if models[0].MappedAt != nil {
		next += " on " + models[0].MappedAt.UTC().Format("2006-01-02")
	}
	next += ". Approvals for these clusters stay with each cluster's own sign-in."
	for i, m := range models {
		if m.MappedBy == "" {
			next = "Signed-in readers do not see " + list + " until a person approves a group mapping: pacto cluster map-groups " +
				clusters[i].ID + " --issuer " + issuer + " --map <hub group>=<cluster group>"
			break
		}
	}
	return AttentionItem{
		Entity: clusterSourceRef(clusters[0]), Label: list, Severity: severityWarning,
		Code: CodeClusterIssuerDiffers, Category: categoryCluster,
		Summary:  "Member clusters do not accept this hub's sign-in tokens",
		Reason:   "the apiservers of " + list + " refuse tokens from " + issuer + ", or this hub exchanges none, so people signed in with it are read there only under an approved group mapping",
		NextStep: next,
	}
}

// clusterSourceRef is the source entity of the first source that reported c.
func clusterSourceRef(c ClusterRecord) EntityRef {
	id := c.Sources[0] // a cluster record has the source that reported it
	return EntityRef{Kind: KindSource, Key: id, Label: id}
}

// boundedNames joins at most maxIssuerClusters names and counts the rest.
func boundedNames(names []string) string {
	if len(names) > maxIssuerClusters {
		return strings.Join(names[:maxIssuerClusters], ", ") + " and " + strconv.Itoa(len(names)-maxIssuerClusters) + " more"
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
