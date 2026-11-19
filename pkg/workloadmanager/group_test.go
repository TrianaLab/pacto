package workloadmanager

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// grouped is a Deployment shop/cart with labels l and annotations a.
func grouped(l, a map[string]string, managers ...FieldManager) Object {
	return Object{Group: "apps", Kind: "Deployment", Namespace: "shop", Name: "cart", Labels: l, Annotations: a, Managers: managers}
}

const cartTracking = "cart:apps/Deployment:shop/cart"

func TestGroupPrecedence(t *testing.T) {
	argo := map[string]string{argoTrackingID: "shop_" + cartTracking}
	fluxL := map[string]string{kustomizeName: "apps", kustomizeNamespace: "flux-system"}
	helmA := map[string]string{helmName: "cart-rel", helmNamespace: "shop"}
	both := func(ms ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	partOf := map[string]string{partOfLabel: "storefront"}
	inst := map[string]string{instanceLabel: "cart-1"}
	for _, tc := range []struct {
		name string
		o    Object
		want Grouping
	}{
		{"argo beats flux", grouped(both(fluxL, partOf, inst), both(argo, helmA)), Grouping{Rule: ArgoCD, Namespace: "shop", Name: "cart", From: "annotation " + argoTrackingID}},
		{"flux beats helm", grouped(both(fluxL, partOf, inst), helmA), Grouping{Rule: FluxKustomization, Namespace: "flux-system", Name: "apps", From: "labels " + kustomizeName + " and namespace"}},
		{"helm beats part-of", grouped(both(partOf, inst), helmA), Grouping{Rule: Helm, Namespace: "shop", Name: "cart-rel", From: "annotations " + helmName + " and release-namespace"}},
		{"part-of beats instance", grouped(both(partOf, inst), nil), Grouping{Rule: PartOf, Namespace: "shop", Name: "storefront", From: "label " + partOfLabel}},
		{"instance beats workload", grouped(inst, nil), Grouping{Rule: Instance, Namespace: "shop", Name: "cart-1", From: "label " + instanceLabel}},
		{"workload", grouped(nil, nil), Grouping{Rule: Workload, Namespace: "shop", Name: "Deployment/cart", From: NoGroupingFrom}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Group(tc.o); got != tc.want {
				t.Errorf("Group = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestGroupArgoNamespacePrefix(t *testing.T) {
	if g := Group(grouped(nil, map[string]string{argoTrackingID: "shop_" + cartTracking})); g.Namespace != "shop" || g.Name != "cart" {
		t.Errorf("prefixed = %+v", g)
	}
	if g := Group(grouped(nil, map[string]string{argoTrackingID: cartTracking})); g.Rule != ArgoCD || g.Namespace != "" || g.Name != "cart" {
		t.Errorf("unprefixed = %+v", g)
	}
	if g := Group(grouped(map[string]string{argoInstanceKey: "shop_cart"}, nil)); g.Rule != ArgoCD || g.Namespace != "shop" || g.From != "label "+argoInstanceKey {
		t.Errorf("instance label = %+v", g)
	}
}

func TestGroupArgoInstanceLabelAgreesWithTracking(t *testing.T) {
	g := Group(grouped(map[string]string{argoInstanceKey: "cart"}, map[string]string{argoTrackingID: cartTracking}))
	want := Grouping{Rule: ArgoCD, Name: "cart", From: "annotation " + argoTrackingID + " and label " + argoInstanceKey}
	if g != want {
		t.Errorf("Group = %+v, want %+v", g, want)
	}
}

func TestGroupAmbiguousWithinRule(t *testing.T) {
	g := Group(grouped(map[string]string{argoInstanceKey: "checkout"}, map[string]string{argoTrackingID: cartTracking}))
	if g.Rule != Workload || g.Name != "Deployment/cart" || g.Namespace != "shop" ||
		g.Ambiguous != "annotation "+argoTrackingID+" names cart and label "+argoInstanceKey+" names checkout" {
		t.Errorf("argo = %+v", g)
	}
	g = Group(grouped(map[string]string{kustomizeName: "apps", kustomizeNamespace: "flux-system", helmReleaseName: "cart", helmReleaseNamespace: "shop"}, nil))
	if g.Rule != Workload || !strings.Contains(g.Ambiguous, "names flux-system/apps and labels "+helmReleaseName+" and namespace names shop/cart") {
		t.Errorf("flux = %+v", g)
	}
	g = Group(grouped(map[string]string{instanceLabel: "cart-chart"}, map[string]string{argoTrackingID: cartTracking}, FieldManager{Manager: "argocd-controller"}))
	if g.Rule != ArgoCD || g.Name != "cart" || g.Ambiguous != "" {
		t.Errorf("weak instance = %+v", g)
	}
	long1, long2 := strings.Repeat("a", 253), strings.Repeat("b", 253)
	g = Group(grouped(map[string]string{argoInstanceKey: long1}, map[string]string{argoTrackingID: long2 + ":apps/Deployment:shop/cart"}))
	if !strings.Contains(g.Ambiguous, strings.Repeat("b", 120)+"...") || strings.Contains(g.Ambiguous, strings.Repeat("a", 121)) ||
		utf8.RuneCountInString(g.Ambiguous) >= 512 {
		t.Errorf("long = %q (%d runes)", g.Ambiguous, utf8.RuneCountInString(g.Ambiguous))
	}
}

func TestGroupFluxHelmReleaseHidesHelm(t *testing.T) {
	g := Group(grouped(map[string]string{helmReleaseName: "cart", helmReleaseNamespace: "flux-system"}, map[string]string{helmName: "shop-cart", helmNamespace: "shop"}))
	want := Grouping{Rule: FluxHelmRelease, Namespace: "flux-system", Name: "cart", From: "labels " + helmReleaseName + " and namespace"}
	if g != want {
		t.Errorf("Group = %+v, want %+v", g, want)
	}
}

func TestGroupIgnoresForeignTrackingID(t *testing.T) {
	for _, id := range []string{"cart:apps/Deployment:shop/other", "not-a-tracking-id", "shop_:apps/Deployment:shop/cart"} {
		g := Group(grouped(map[string]string{partOfLabel: "storefront"}, map[string]string{argoTrackingID: id}))
		if g.Rule != PartOf {
			t.Errorf("%s: Group = %+v, want part-of", id, g)
		}
	}
	if g := Group(grouped(map[string]string{argoInstanceKey: "shop_"}, nil)); g.Rule != Workload {
		t.Errorf("empty instance name = %+v", g)
	}
	if g := Group(grouped(map[string]string{instanceLabel: "shop_"}, nil, FieldManager{Manager: "argocd-controller"})); g.Rule != Instance {
		t.Errorf("empty weak instance name = %+v", g)
	}
	if g := Group(grouped(map[string]string{kustomizeName: "", kustomizeNamespace: "flux-system"}, nil)); g.Rule != Workload {
		t.Errorf("empty Kustomization name = %+v", g)
	}
}

func TestGroupAppInstanceNeedsArgoManagerForArgo(t *testing.T) {
	l := map[string]string{instanceLabel: "shop_cart"}
	if g := Group(grouped(l, nil)); g.Rule != Instance || g.Name != "shop_cart" {
		t.Errorf("label alone = %+v", g)
	}
	g := Group(grouped(l, nil, FieldManager{Manager: "argocd-controller"}))
	want := Grouping{Rule: ArgoCD, Namespace: "shop", Name: "cart", From: "label " + instanceLabel + " and field manager argocd-controller"}
	if g != want {
		t.Errorf("with manager = %+v, want %+v", g, want)
	}
	if g := Group(grouped(l, nil, FieldManager{Manager: "argocd-controller", Subresource: "status"})); g.Rule != Instance {
		t.Errorf("status manager = %+v", g)
	}
}

func TestGroupSanitizesValues(t *testing.T) {
	if g := Group(grouped(map[string]string{partOfLabel: "shop\x1b[31m"}, nil)); g.Name != unprintable {
		t.Errorf("control = %+v", g)
	}
	if g := Group(grouped(map[string]string{partOfLabel: strings.Repeat("x", 300)}, nil)); len(g.Name) != maxValue {
		t.Errorf("long = %d bytes", len(g.Name))
	}
	g := Group(grouped(map[string]string{instanceLabel: "cart"}, nil, FieldManager{Manager: "argocd-" + strings.Repeat("m", 300)}))
	if g.Rule != ArgoCD || utf8.RuneCountInString(g.From) > 256 {
		t.Errorf("long manager = %+v", g)
	}
}

func TestGroupWorkloadFallback(t *testing.T) {
	want := Grouping{Rule: Workload, Namespace: "shop", Name: "Deployment/cart", From: NoGroupingFrom}
	if g := Group(grouped(map[string]string{partOfLabel: ""}, map[string]string{helmName: "", helmNamespace: "shop"})); g != want {
		t.Errorf("Group = %+v, want %+v", g, want)
	}
}
