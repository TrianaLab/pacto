package workloadmanager

import (
	"strings"
	"testing"
)

func deploy() Object {
	return Object{Group: "apps", Kind: "Deployment", Namespace: "payments", Name: "payments"}
}

func with(labels, annotations map[string]string, managers ...string) Object {
	o := deploy()
	o.Labels, o.Annotations = labels, annotations
	for _, m := range managers {
		o.Managers = append(o.Managers, FieldManager{Manager: m})
	}
	return o
}

func tracking(id string) Object {
	return with(nil, map[string]string{argoTrackingID: id})
}

func check(t *testing.T, got, want Manager) {
	t.Helper()
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestDetectArgoTrackingID(t *testing.T) {
	check(t, Detect(tracking("payments:apps/Deployment:payments/payments")), Manager{
		Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application", OwnerName: "payments", Evidence: "tracking annotation",
	})
	check(t, Detect(tracking("staging_payments:apps/Deployment:payments/payments")), Manager{
		Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application", OwnerNamespace: "staging", OwnerName: "payments", Evidence: "tracking annotation",
	})
	for _, id := range []string{"payments", "payments:apps/Deployment", "payments:Deployment:payments/payments", "payments:apps/Deployment:payments", ":apps/Deployment:payments/payments"} {
		check(t, Detect(tracking(id)), Manager{
			Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application", Evidence: "malformed tracking annotation " + id,
		})
	}
}

func TestDetectArgoTrackingIDOfAnotherObject(t *testing.T) {
	for _, id := range []string{
		"payments:batch/Deployment:payments/payments",
		"payments:apps/StatefulSet:payments/payments",
		"payments:apps/Deployment:orders/payments",
		"payments:apps/Deployment:payments/orders",
	} {
		check(t, Detect(tracking(id)), Manager{Name: Unsupported, Product: "Argo CD", Evidence: "tracking id of another object"})
	}
}

func TestDetectManagedFieldsFallback(t *testing.T) {
	check(t, Detect(with(nil, nil, "kubectl", "kustomize-controller")), Manager{
		Name: FluxKustomization, Product: "Flux", OwnerKind: "Kustomization", Evidence: "field manager kustomize-controller",
	})
	check(t, Detect(with(nil, nil, "helm-controller")), Manager{
		Name: FluxHelmRelease, Product: "Flux", OwnerKind: "HelmRelease", Evidence: "field manager helm-controller",
	})
	check(t, Detect(with(nil, nil, "argocd-application-controller")), Manager{
		Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application", Evidence: "field manager argocd-application-controller",
	})
}

func TestDetectEvidenceIsPrintableAndBounded(t *testing.T) {
	for _, v := range []string{
		"pay\x1b[31mments", "pay\u202ements", strings.Repeat("a", 10*1024), "x:\x1b]8;;evil\x07",
	} {
		for _, o := range []Object{tracking(v), with(nil, nil, "argocd-"+v), with(map[string]string{kustomizeName: v, kustomizeNamespace: v}, nil)} {
			m := Detect(o)
			for _, s := range []string{m.Evidence, m.OwnerName, m.OwnerNamespace, Describe(m)} {
				if len(s) > 3*maxValue {
					t.Errorf("%q: %d bytes", s[:64], len(s))
				}
				for i := range len(s) {
					if s[i] < 0x20 || s[i] > 0x7e {
						t.Fatalf("%q holds byte 0x%02x", s, s[i])
					}
				}
			}
		}
	}
	if got := sanitize(strings.Repeat("a", 10*1024)); len(got) != maxValue {
		t.Errorf("sanitize kept %d bytes, want %d", len(got), maxValue)
	}
	if got := sanitize("pay\x1bments"); got != unprintable {
		t.Errorf("sanitize = %q, want %q", got, unprintable)
	}
}

func TestDetectArgoInstanceLabelNeedsArgoManager(t *testing.T) {
	labels := map[string]string{instanceLabel: "payments"}
	check(t, Detect(with(labels, nil)), Manager{Name: None})
	check(t, Detect(with(labels, nil, "argocd-controller")), Manager{
		Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application", OwnerName: "payments", Evidence: "instance label",
	})
	// An Application outside the Argo CD namespace labels its workloads
	// `<appNamespace>_<app>`, as it writes the tracking id.
	check(t, Detect(with(map[string]string{instanceLabel: "staging_payments"}, nil, "argocd-controller")), Manager{
		Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application", OwnerNamespace: "staging", OwnerName: "payments", Evidence: "instance label",
	})
}

func TestDetectFluxKustomization(t *testing.T) {
	check(t, Detect(with(map[string]string{kustomizeName: "payments", kustomizeNamespace: "flux-system"}, nil, "kustomize-controller")), Manager{
		Name: FluxKustomization, Product: "Flux", OwnerKind: "Kustomization", OwnerNamespace: "flux-system", OwnerName: "payments", Evidence: "kustomize-controller labels",
	})
}

func TestDetectFluxHelmReleaseOverHelmAnnotations(t *testing.T) {
	o := with(
		map[string]string{helmReleaseName: "payments", helmReleaseNamespace: "payments"},
		map[string]string{helmName: "payments", helmNamespace: "payments"},
		"helm-controller",
	)
	check(t, Detect(o), Manager{
		Name: FluxHelmRelease, Product: "Flux", OwnerKind: "HelmRelease", OwnerNamespace: "payments", OwnerName: "payments", Evidence: "helm-controller labels",
	})
}

func TestDetectPlainHelm(t *testing.T) {
	check(t, Detect(with(nil, map[string]string{helmName: "payments", helmNamespace: "payments"})), Manager{
		Name: Helm, Product: "Helm", OwnerKind: "release", OwnerNamespace: "payments", OwnerName: "payments", Evidence: "release annotations",
	})
	check(t, Detect(with(nil, nil, "helm")), Manager{
		Name: Helm, Product: "Helm", OwnerKind: "release", Evidence: "field manager helm",
	})
}

func TestDetectUnsupportedFingerprints(t *testing.T) {
	owned := deploy()
	owned.Controller = &OwnerRef{Kind: "RedisCluster", Name: "cache"}
	for _, tc := range []struct {
		o    Object
		want Manager
	}{
		{owned, Manager{Name: Unsupported, Product: "Operator", OwnerKind: "RedisCluster", OwnerName: "cache", Evidence: "controller ownerReference"}},
		{with(map[string]string{"kapp.k14s.io/app": "1"}, nil), Manager{Name: Unsupported, Product: "kapp", Evidence: "app label"}},
		{with(nil, map[string]string{"objectset.rio.cattle.io/id": "x"}), Manager{Name: Unsupported, Product: "Rancher Fleet", Evidence: "bundle annotation"}},
		{with(nil, map[string]string{"moniker.spinnaker.io/application": "x"}), Manager{Name: Unsupported, Product: "Spinnaker", Evidence: "moniker annotation"}},
		{with(map[string]string{"skaffold.dev/run-id": "x"}, nil), Manager{Name: Unsupported, Product: "Skaffold", Evidence: "run-id label"}},
		{with(nil, nil, "Terraform"), Manager{Name: Unsupported, Product: "Terraform", Evidence: "field manager Terraform"}},
		{with(nil, nil, "pulumi-kubernetes-a1b2"), Manager{Name: Unsupported, Product: "Pulumi", Evidence: "field manager pulumi-kubernetes-a1b2"}},
	} {
		check(t, Detect(tc.o), tc.want)
	}
}

func TestDetectAmbiguous(t *testing.T) {
	o := with(map[string]string{kustomizeName: "payments", kustomizeNamespace: "flux-system"}, map[string]string{argoTrackingID: "payments:apps/Deployment:payments/payments"})
	check(t, Detect(o), Manager{Name: Ambiguous, Evidence: "Argo CD tracking annotation and Flux kustomize-controller labels"})
	o = with(
		map[string]string{helmReleaseName: "payments", helmReleaseNamespace: "payments"},
		map[string]string{argoTrackingID: "payments:apps/Deployment:payments/payments", helmName: "payments", helmNamespace: "payments"},
	)
	check(t, Detect(o), Manager{Name: Ambiguous, Evidence: "Argo CD tracking annotation and Flux helm-controller labels"})
}

func TestDetectNone(t *testing.T) {
	check(t, Detect(deploy()), Manager{Name: None})
	check(t, Detect(with(map[string]string{instanceLabel: "x"}, nil, "kubectl-client-side-apply")), Manager{Name: None})
}

func TestDetectIgnoresStatusSubresourceManagers(t *testing.T) {
	o := deploy()
	o.Managers = []FieldManager{{Manager: "kube-controller-manager", Subresource: "status"}, {Manager: "argocd-controller", Subresource: "status"}}
	check(t, Detect(o), Manager{Name: None})
}

func TestDescribe(t *testing.T) {
	for _, tc := range []struct {
		m    Manager
		want string
	}{
		{Detect(tracking("payments:apps/Deployment:payments/payments")), "Argo CD (Application payments, tracking annotation)"},
		{Detect(tracking("staging_payments:apps/Deployment:payments/payments")), "Argo CD (Application staging/payments, tracking annotation)"},
		{Detect(with(map[string]string{kustomizeName: "payments", kustomizeNamespace: "flux-system"}, nil)), "Flux (Kustomization flux-system/payments, kustomize-controller labels)"},
		{Detect(with(map[string]string{helmReleaseName: "payments", helmReleaseNamespace: "payments"}, nil)), "Flux (HelmRelease payments/payments, helm-controller labels)"},
		{Detect(with(nil, map[string]string{helmName: "payments", helmNamespace: "payments"})), "Helm (release payments, release annotations)"},
		{Detect(with(nil, nil, "kustomize-controller")), "Flux (Kustomization, field manager kustomize-controller)"},
		{Detect(with(nil, map[string]string{"objectset.rio.cattle.io/id": "x"})), "Rancher Fleet (bundle annotation), not supported"},
		{Manager{Name: Unsupported, Product: "Operator", OwnerKind: "RedisCluster", OwnerName: "cache", Evidence: "controller ownerReference"}, "Operator (RedisCluster cache, controller ownerReference), not supported"},
		{Manager{Name: Ambiguous, Evidence: "Argo CD tracking annotation and Flux kustomize-controller labels"}, "ambiguous (Argo CD tracking annotation and Flux kustomize-controller labels)"},
		{Manager{Name: None}, "unmanaged"},
		{Manager{}, "unmanaged"},
	} {
		if got := Describe(tc.m); got != tc.want {
			t.Errorf("Describe(%+v) = %q, want %q", tc.m, got, tc.want)
		}
	}
}
