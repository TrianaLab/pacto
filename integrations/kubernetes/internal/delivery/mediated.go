/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// ConditionMediated is True when the pointer alone decides what runs: one
// applier takes the version from the apply object as it is, and admission
// guards everything that could change that. False names what else can.
const ConditionMediated = "Mediated"

// kustomizationOverrides are the Kustomization fields that change what runs
// beyond the bytes the pointer names, where or whether it runs. The applier
// policy guards them, with sourceRef and serviceAccountName, on a labelled
// Kustomization.
var kustomizationOverrides = []string{"images", "patches", "components", "postBuild", "namePrefix", "nameSuffix",
	"kubeConfig", "suspend", "decryption"}

// argoOverrides are the paths under an Application's spec.source that change
// what runs beyond the bytes targetRevision names.
var argoOverrides = [][]string{
	{"kustomize", "images"}, {"kustomize", "patches"}, {"kustomize", "components"},
	{"helm", "parameters"}, {"helm", "values"}, {"helm", "valuesObject"}, {"helm", "fileParameters"}, {"helm", "valueFiles"},
	{"plugin"},
}

// mediated is the Mediated condition for the Stage applying through a.
func (c Cluster) mediated(ctx context.Context, ns string, a *applyRef) (delivery.Condition, error) {
	yes := delivery.Condition{Type: ConditionMediated, Status: statusTrue, Reason: "PointerOnly",
		Message: "only the pointer decides what runs, and admission guards it"}
	no := func(reason, msg string) delivery.Condition {
		return delivery.Condition{Type: ConditionMediated, Status: statusFalse, Reason: reason,
			Message: msg + ", so the pointer is not all that decides what runs and records name only the pointer"}
	}
	if !labelled(a.obj) {
		return no("ApplyRefUnguarded", "the apply object "+a.obj.GetName()+" has no "+ApplyRefLabel+" label, so admission does not guard its pointer"), nil
	}
	if ignore, _, _ := unstructured.NestedString(a.obj.Object, "spec", "ignore"); !a.argo && ignore != "" {
		return no("ApplierOverrides", "OCIRepository "+a.obj.GetName()+" sets spec.ignore, which leaves files of the version out"), nil
	}
	if a.argo {
		if s, _, _ := unstructured.NestedSlice(a.obj.Object, "spec", "sources"); len(s) > 0 {
			return no("SeveralSources", "Application "+a.obj.GetName()+" sets spec.sources"), nil
		}
		if set := setPaths(a.obj.Object, []string{"spec", "source"}, argoOverrides); len(set) > 0 {
			return no("ApplierOverrides", "Application "+a.obj.GetName()+" sets "+strings.Join(set, ", ")), nil
		}
		return yes, nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkKustomization)
	if err := c.cached(false).List(ctx, list); err != nil {
		return delivery.Condition{}, err
	}
	var appliers []unstructured.Unstructured
	for _, k := range list.Items {
		if appliesRepo(&k, ns, a.obj.GetName()) {
			appliers = append(appliers, k)
		}
	}
	switch len(appliers) {
	case 0:
		return no("NoApplier", "no Kustomization applies OCIRepository "+a.obj.GetName()), nil
	case 1:
	default:
		names := make([]string, len(appliers))
		for i, k := range appliers {
			names[i] = k.GetNamespace() + "/" + k.GetName()
		}
		return no("SeveralAppliers", "Kustomizations "+strings.Join(names, " and ")+" all apply OCIRepository "+a.obj.GetName()), nil
	}
	k := appliers[0]
	if !labelled(&k) {
		return no("ApplierUnguarded", "Kustomization "+k.GetName()+" has no "+ApplyRefLabel+" label, so admission does not guard its sourceRef, path, images or patches"), nil
	}
	if sa, _, _ := unstructured.NestedString(k.Object, "spec", "serviceAccountName"); sa == "" {
		// A version can then carry a ClusterRoleBinding and have it applied:
		// an auto level judges the contract, never the manifests.
		return no("ApplierUnscoped", "Kustomization "+k.GetName()+" sets no serviceAccountName, so Flux applies each version with "+
			"kustomize-controller's own rights (cluster-admin in a default install) and a version can grant itself anything"), nil
	}
	paths := make([][]string, len(kustomizationOverrides))
	for i, f := range kustomizationOverrides {
		paths[i] = []string{f}
	}
	if set := setPaths(k.Object, []string{"spec"}, paths); len(set) > 0 {
		return no("ApplierOverrides", "Kustomization "+k.GetName()+" sets "+strings.Join(set, ", ")), nil
	}
	return yes, nil
}

// setPaths names the paths under base that obj sets to something non-empty.
func setPaths(obj map[string]any, base []string, paths [][]string) []string {
	var set []string
	for _, p := range paths {
		v, found, _ := unstructured.NestedFieldNoCopy(obj, append(append([]string{}, base...), p...)...)
		if found && !empty(v) {
			set = append(set, strings.Join(append(append([]string{}, base...), p...), "."))
		}
	}
	return set
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x // suspend: false suspends nothing
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}
