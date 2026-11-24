package fleet

import (
	"cmp"
	"slices"
	"strings"
)

// codeManagedGroupUnread is the attention code of a managed-resource
// group the operator may not list.
const codeManagedGroupUnread = "MANAGED_GROUP_UNREAD"

// unreadPrefix starts a managed-resources row detail that names a kind the
// operator may not list: "unread <group>/<plural>".
const unreadPrefix = "unread "

// managedGroupAttention reports one MANAGED_GROUP_UNREAD item per cluster
// and group a partial managed-resources row names as unread. Only what was
// observed is said: a provider group served after install is granted by the
// row's helm upgrade, while a claim or composite group of a helm template
// install stays unread, since only helm install or upgrade looks it up.
func (q *Query) managedGroupAttention() []AttentionItem {
	type groupKey struct{ cluster, group string }
	seen := map[groupKey]bool{}
	var out []AttentionItem
	for _, p := range q.snap.Providers {
		if p.ID != ProviderManagedResources || p.Status != SourcePartial {
			continue
		}
		for _, d := range p.Details {
			kind, ok := strings.CutPrefix(d, unreadPrefix)
			if !ok {
				continue
			}
			group, _, _ := strings.Cut(kind, "/")
			if k := (groupKey{p.Cluster, group}); !seen[k] {
				seen[k] = true
				out = append(out, managedGroupItem(p, group))
			}
		}
	}
	slices.SortFunc(out, func(a, b AttentionItem) int {
		return cmp.Or(cmp.Compare(a.Label, b.Label), cmp.Compare(a.Reason, b.Reason))
	})
	return out
}

// managedGroupItem is the MANAGED_GROUP_UNREAD item of group on p's cluster.
func managedGroupItem(p Provider, group string) AttentionItem {
	where := ""
	if p.Cluster != "" {
		where = " in cluster " + p.Cluster
	}
	return AttentionItem{
		Entity: EntityRef{Kind: KindSource, Key: p.Source, Label: p.Source}, Severity: severityInfo,
		Code: codeManagedGroupUnread, Category: categoryKnowledge, Source: p.Source,
		Label:   group + ": Pacto may not list it",
		Summary: "Managed resources Pacto cannot read",
		Reason: "the operator may not list the managed resources of " + group + where + ": helm upgrade grants a provider group served after the chart was installed; " +
			"a claim or composite group stays unread on a helm template install, which cannot look it up",
		NextStep: p.Hint,
	}
}
