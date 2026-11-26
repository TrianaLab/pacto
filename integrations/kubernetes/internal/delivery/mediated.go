/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"slices"
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

// mediated is the Mediated condition for the Stage of environment env
// applying through a.
func (c Cluster) mediated(ctx context.Context, ns, env string, a applier) (delivery.Condition, error) {
	if o := a.object(); !labelled(o) {
		return notMediated("ApplyRefUnguarded", "the apply object "+o.GetName()+" has no "+ApplyRefLabel+" label, so admission does not guard its pointer"), nil
	}
	return a.mediated(ctx, c, ns, env)
}

// argoApplier.mediated accepts one values selection: valueFiles holding only
// values-<env>.yaml, or the overlay overlays/<env>. Any other is
// OtherEnvironmentValues.
func (a argoApplier) mediated(_ context.Context, _ Cluster, ns, env string) (delivery.Condition, error) {
	if s, _, _ := unstructured.NestedSlice(a.obj.Object, "spec", "sources"); len(s) > 0 {
		return notMediated("SeveralSources", "Application "+a.obj.GetName()+" sets spec.sources"), nil
	}
	sel := delivery.SelectedValues(delivery.ApplyRef{Kind: delivery.ApplierArgoCD, Name: a.obj.GetName()}, a.obj.Object, nil, ns, env)
	if sel.Problem != "" {
		return notMediated(reasonOtherEnvironmentValues, sel.Problem), nil
	}
	paths := argoOverrides
	if sel.Form == delivery.FormHelmValues {
		paths = slices.DeleteFunc(slices.Clone(argoOverrides), func(p []string) bool { return slices.Equal(p, []string{"helm", "valueFiles"}) })
	}
	if set := setPaths(a.obj.Object, []string{"spec", "source"}, paths); len(set) > 0 {
		return notMediated("ApplierOverrides", "Application "+a.obj.GetName()+" sets "+strings.Join(set, ", ")), nil
	}
	return withValues(pointerOnly, sel, env), nil
}

// reasonOtherEnvironmentValues is the Mediated reason of an applier that
// selects values other than the Stage's own environment's.
const reasonOtherEnvironmentValues = "OtherEnvironmentValues"

// withValues is the accepted condition c naming the values selection sel
// applies for env, when it names one.
func withValues(c delivery.Condition, sel delivery.ValuesSelection, env string) delivery.Condition {
	if sel.File != "" {
		c.Message += ", with " + sel.File + " for environment " + env
	}
	return c
}

func (a fluxApplier) mediated(ctx context.Context, c Cluster, ns, env string) (delivery.Condition, error) {
	if ignore, _, _ := unstructured.NestedString(a.obj.Object, "spec", "ignore"); ignore != "" {
		return notMediated("ApplierOverrides", "OCIRepository "+a.obj.GetName()+" sets spec.ignore, which leaves files of the version out"), nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvkKustomization)
	if err := c.cached(delivery.ApplierFlux).List(ctx, list); err != nil {
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
		return notMediated("NoApplier", "no Kustomization applies OCIRepository "+a.obj.GetName()), nil
	case 1:
	default:
		names := make([]string, len(appliers))
		for i, k := range appliers {
			names[i] = k.GetNamespace() + "/" + k.GetName()
		}
		return notMediated("SeveralAppliers", "Kustomizations "+strings.Join(names, " and ")+" all apply OCIRepository "+a.obj.GetName()), nil
	}
	b, _ := (&unstructured.UnstructuredList{Items: appliers}).MarshalJSON()
	// ponytail: the apiserver's own JSON, under the Flux CRD schema, decodes.
	ks, _ := delivery.DecodeKustomizations(b)
	sel := delivery.SelectedValues(delivery.ApplyRef{Kind: delivery.ApplierFlux, Name: a.obj.GetName()}, nil, ks, ns, env)
	if sel.Problem != "" {
		return notMediated(reasonOtherEnvironmentValues, sel.Problem), nil
	}
	med := kustomizationMediated(&appliers[0])
	if med.Status == statusTrue {
		med = withValues(med, sel, env)
	}
	return med, nil
}

// pointerOnly is the Mediated condition of a Stage whose pointer alone
// decides what runs.
var pointerOnly = delivery.Condition{Type: ConditionMediated, Status: statusTrue, Reason: "PointerOnly",
	Message: "only the pointer decides what runs, and admission guards it"}

// notMediated is the Mediated condition of a Stage where something besides
// the pointer decides what runs.
func notMediated(reason, msg string) delivery.Condition {
	return delivery.Condition{Type: ConditionMediated, Status: statusFalse, Reason: reason,
		Message: msg + ", so the pointer is not all that decides what runs and records name only the pointer"}
}

// kustomizationMediated is the Mediated condition for the one Kustomization
// k that applies the Stage's OCIRepository.
func kustomizationMediated(k *unstructured.Unstructured) delivery.Condition {
	if !labelled(k) {
		return notMediated("ApplierUnguarded", "Kustomization "+k.GetName()+" has no "+ApplyRefLabel+" label, so admission does not guard its sourceRef, path, images or patches")
	}
	if sa, _, _ := unstructured.NestedString(k.Object, "spec", "serviceAccountName"); sa == "" {
		// A version can then carry a ClusterRoleBinding and have it applied:
		// an auto level judges the contract, never the manifests.
		return notMediated("ApplierUnscoped", "Kustomization "+k.GetName()+" sets no serviceAccountName, so Flux applies each version with "+
			"kustomize-controller's own rights (cluster-admin in a default install) and a version can grant itself anything")
	}
	paths := make([][]string, len(kustomizationOverrides))
	for i, f := range kustomizationOverrides {
		paths[i] = []string{f}
	}
	if set := setPaths(k.Object, []string{"spec"}, paths); len(set) > 0 {
		return notMediated("ApplierOverrides", "Kustomization "+k.GetName()+" sets "+strings.Join(set, ", "))
	}
	return pointerOnly
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
