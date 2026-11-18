package fleetsrc

import (
	"maps"
	"slices"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// groupingLabels and groupingAnnotations are the only metadata keys grouping
// reads; every other label and annotation, last-applied-configuration
// included, stays behind.
var (
	groupingLabels = []string{
		"argocd.argoproj.io/instance", "app.kubernetes.io/instance", "app.kubernetes.io/part-of",
		"kustomize.toolkit.fluxcd.io/name", "kustomize.toolkit.fluxcd.io/namespace",
		"helm.toolkit.fluxcd.io/name", "helm.toolkit.fluxcd.io/namespace",
	}
	groupingAnnotations = []string{"argocd.argoproj.io/tracking-id", "meta.helm.sh/release-name", "meta.helm.sh/release-namespace"}
)

// groupingObject is the workloadmanager view of one workload: its identity
// (Argo CD's tracking id names all four), the allowlisted labels and
// annotations and its managedFields manager names, never their fieldsV1.
func groupingObject(gvr schema.GroupVersionResource, kind, ns, name string, meta map[string]any) workloadmanager.Object {
	o := workloadmanager.Object{Group: gvr.Group, Kind: kind, Namespace: ns, Name: name,
		Labels: allowlisted(meta["labels"], groupingLabels), Annotations: allowlisted(meta["annotations"], groupingAnnotations)}
	fields, _ := meta["managedFields"].([]any)
	for _, f := range fields {
		entry, _ := f.(map[string]any)
		manager, _ := entry["manager"].(string)
		sub, _ := entry["subresource"].(string)
		o.Managers = append(o.Managers, workloadmanager.FieldManager{Manager: manager, Subresource: sub})
	}
	return o
}

// allowlisted copies the string values of keys out of a metadata map.
func allowlisted(v any, keys []string) map[string]string {
	all, _ := v.(map[string]any)
	out := map[string]string{}
	for _, k := range keys {
		if s, ok := all[k].(string); ok {
			out[k] = s
		}
	}
	return out
}

func toFleetGroup(g workloadmanager.Grouping) *fleet.InferredGroup {
	return &fleet.InferredGroup{Rule: g.Rule, Namespace: g.Namespace, Name: g.Name, From: g.From, Ambiguous: g.Ambiguous}
}

// helmRow is the Helm row of col's services: the distinct releases their
// groups name, counted in the namespace of each workload that names one, so a
// viewer is reviewed in the namespace the count was read from, never the one
// a release annotation claims. No Helm group, no row. Pacto
// reads no release Secret, so the row names no repository. A workload read
// col's limitations cut short may have missed a release, so the row is then
// partial with them, like the workloads row.
func helmRow(col *fleet.Collection) (fleet.Provider, bool) {
	releases := map[[3]string]bool{}
	perNS := map[string]int{}
	for _, in := range col.InferredServices {
		if g := in.Group; g != nil && g.Rule == workloadmanager.Helm && !releases[[3]string{in.Namespace, g.Namespace, g.Name}] {
			releases[[3]string{in.Namespace, g.Namespace, g.Name}] = true
			perNS[in.Namespace]++
		}
	}
	if len(releases) == 0 {
		return fleet.Provider{}, false
	}
	row := fleet.Provider{ID: fleet.ProviderHelm, Name: "Helm", UsedFor: []string{fleet.UseServiceGrouping}}
	for _, ns := range slices.Sorted(maps.Keys(perNS)) {
		row.Counts = append(row.Counts, fleet.ProviderCount{Kind: fleet.HelmReleaseKind, Namespace: ns, N: perNS[ns]})
	}
	if len(col.Limitations) > 0 {
		row.Status, row.Limitations = fleet.SourcePartial, slices.Clone(col.Limitations)
	}
	return row, true
}
