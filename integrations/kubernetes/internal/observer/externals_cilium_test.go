package observer

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
)

func ciliumScheme() *runtime.Scheme {
	s := extScheme()
	for _, l := range []string{ciliumPolicyList.Kind, ciliumClusterwidePolicyList.Kind} {
		s.AddKnownTypeWithName(ciliumPolicyList.GroupVersion().WithKind(strings.TrimSuffix(l, "List")), &unstructured.Unstructured{})
		s.AddKnownTypeWithName(ciliumPolicyList.GroupVersion().WithKind(l), &unstructured.UnstructuredList{})
	}
	return s
}

// ciliumPolicy is a CiliumNetworkPolicy (ns set) or a cluster-wide one (ns
// empty) whose spec selects selector and allows fqdns in one egress rule.
func ciliumPolicy(ns, name string, selector map[string]any, fqdns ...map[string]any) *unstructured.Unstructured {
	kind := "CiliumNetworkPolicy"
	if ns == "" {
		kind = "CiliumClusterwideNetworkPolicy"
	}
	to := make([]any, 0, len(fqdns))
	for _, f := range fqdns {
		to = append(to, f)
	}
	u := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		"endpointSelector": selector,
		"egress":           []any{map[string]any{"toFQDNs": to}},
	}}}
	u.SetAPIVersion("cilium.io/v2")
	u.SetKind(kind)
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}

func matchApp(app string) map[string]any {
	return map[string]any{"matchLabels": map[string]any{"k8s:app": app}}
}

func ciliumInput() CollectInput {
	in := extInput()
	in.CiliumPolicies = true
	return in
}

func ciliumObserve(t *testing.T, fns interceptor.Funcs, input CollectInput, objs ...client.Object) ([]unversioned.ExternalStatus, unversioned.CallerCoverage) {
	t.Helper()
	objs = append(objs, extDeployment(corev1.Container{}))
	c := fake.NewClientBuilder().WithScheme(ciliumScheme()).WithObjects(objs...).WithInterceptorFuncs(fns).Build()
	return New(c).observeExternals(context.Background(), input)
}

func TestCiliumFQDNMatchNameIsInferredExternal(t *testing.T) {
	got, cov := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(),
		ciliumPolicy(extNS, "egress", matchApp("payments"), map[string]any{"matchName": "api.stripe.com"}))
	if want := "stripe|vendor-api|likely|ciliumFQDN|api.stripe.com|ciliumnetworkpolicy/payments/egress toFQDNs api.stripe.com"; summary(got) != want {
		t.Fatalf("got\n%s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

func TestCiliumFQDNPatternWildcardStripped(t *testing.T) {
	got, cov := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(), ciliumPolicy(extNS, "egress", matchApp("payments"),
		map[string]any{"matchPattern": "*.corp-legacy.com"}, map[string]any{"matchPattern": "**.stripe.com"}, map[string]any{"matchPattern": "api.stripe.com"}))
	// The catalog names api.stripe.com only, so a wildcard over stripe.com is its registrable domain.
	want := "host/corp-legacy.com|legacy|likely|ciliumFQDN|corp-legacy.com|ciliumnetworkpolicy/payments/egress toFQDNs *.corp-legacy.com\n" +
		"host/stripe.com|legacy|likely|ciliumFQDN|stripe.com|ciliumnetworkpolicy/payments/egress toFQDNs **.stripe.com\n" +
		"stripe|vendor-api|likely|ciliumFQDN|api.stripe.com|ciliumnetworkpolicy/payments/egress toFQDNs api.stripe.com"
	if summary(got) != want {
		t.Fatalf("got\n%s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

func TestCiliumFQDNBareStarPartial(t *testing.T) {
	for _, pattern := range []string{"*", "api.*.stripe.com", "*.com", "*.co.uk"} {
		got, cov := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(),
			ciliumPolicy(extNS, "egress", matchApp("payments"), map[string]any{"matchPattern": pattern}))
		if len(got) != 0 {
			t.Fatalf("%s: got %s", pattern, summary(got))
		}
		wantCoverage(t, cov, unversioned.CallerCoveragePartial, "fqdn-pattern-unbounded")
	}
}

func TestCiliumPolicySelectorMustMatchPod(t *testing.T) {
	stripe := map[string]any{"matchName": "api.stripe.com"}
	hostPolicy := ciliumPolicy(extNS, "host", nil, stripe)
	unstructured.RemoveNestedField(hostPolicy.Object, "spec", "endpointSelector")
	badSelector := ciliumPolicy(extNS, "bad", map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "Nope"}}}, stripe)
	unparsable := ciliumPolicy(extNS, "odd", map[string]any{"matchLabels": "app"}, stripe)
	got, _ := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(),
		ciliumPolicy(extNS, "other", matchApp("orders"), stripe), hostPolicy, badSelector, unparsable,
		ciliumPolicy("team-b", "elsewhere", map[string]any{}, stripe))
	if len(got) != 0 {
		t.Fatalf("got %s", summary(got))
	}
	all, _ := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(), ciliumPolicy(extNS, "all", map[string]any{}, stripe))
	if len(all) != 1 {
		t.Fatalf("an empty selector selects every pod of the namespace: %s", summary(all))
	}
}

func TestCiliumClusterwidePolicyRead(t *testing.T) {
	multi := ciliumPolicy("", "vendors", nil)
	multi.Object["specs"] = []any{
		map[string]any{"endpointSelector": map[string]any{"matchLabels": map[string]any{"k8s:io.kubernetes.pod.namespace": "orders"}},
			"egress": []any{map[string]any{"toFQDNs": []any{map[string]any{"matchName": "api.twilio.com"}}}}},
		map[string]any{"endpointSelector": map[string]any{"matchLabels": map[string]any{"k8s:io.kubernetes.pod.namespace": extNS}},
			"egress": []any{map[string]any{"toFQDNs": []any{map[string]any{"matchName": "api.stripe.com"}}}}},
	}
	delete(multi.Object, "spec")
	got, cov := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(), multi)
	// A cluster-wide policy is never named on a namespaced service: its name is not the namespace's.
	if want := "stripe|vendor-api|likely|ciliumFQDN|api.stripe.com|ciliumclusterwidenetworkpolicy (cluster-wide) toFQDNs api.stripe.com"; summary(got) != want {
		t.Fatalf("got\n%s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

// TestCiliumServiceAccountSelectorMatches: Cilium labels every endpoint with its pod's service account,
// so a cluster-wide policy that selects it selects the target's pods (default when the pod spec names none).
func TestCiliumServiceAccountSelectorMatches(t *testing.T) {
	bySA := func(sa string) map[string]any {
		return map[string]any{"matchLabels": map[string]any{"k8s:io.cilium.k8s.policy.serviceaccount": sa}}
	}
	got, cov := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(),
		ciliumPolicy("", "pci", bySA("default"), map[string]any{"matchName": "api.stripe.com"}),
		ciliumPolicy("", "other", bySA("orders"), map[string]any{"matchName": "api.twilio.com"}))
	if want := "stripe|vendor-api|likely|ciliumFQDN|api.stripe.com|ciliumclusterwidenetworkpolicy (cluster-wide) toFQDNs api.stripe.com"; summary(got) != want {
		t.Fatalf("got\n%s", summary(got))
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

// TestCiliumUnmodelledDerivedLabelPartial: a selector on a label Cilium derives that the scan does not
// model (a namespace label, the cluster) is never read as "does not select": the list is partial.
func TestCiliumUnmodelledDerivedLabelPartial(t *testing.T) {
	for _, sel := range []map[string]any{
		{"matchLabels": map[string]any{"k8s:io.cilium.k8s.namespace.labels.team": "payments"}},
		{"matchExpressions": []any{map[string]any{"key": "io.cilium.k8s.policy.cluster", "operator": "Exists"}}},
	} {
		got, cov := ciliumObserve(t, interceptor.Funcs{}, ciliumInput(), ciliumPolicy("", "team", sel, map[string]any{"matchName": "api.stripe.com"}))
		if len(got) != 0 {
			t.Fatalf("got %s", summary(got))
		}
		wantCoverage(t, cov, unversioned.CallerCoveragePartial, "cilium-selector-unevaluated")
	}
}

// TestCiliumNotServedLeavesCoverageComplete: without the Cilium API no
// toFQDNs rule can exist, so nothing is listed and the coverage stays as it
// was.
func TestCiliumNotServedLeavesCoverageComplete(t *testing.T) {
	listed := 0
	fns := interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		if u, ok := list.(*unstructured.UnstructuredList); ok && u.GroupVersionKind().Group == "cilium.io" {
			listed++
		}
		return c.List(ctx, list, opts...)
	}}
	got, cov := ciliumObserve(t, fns, extInput(), ciliumPolicy(extNS, "egress", matchApp("payments"), map[string]any{"matchName": "api.stripe.com"}))
	if len(got) != 0 || listed != 0 {
		t.Fatalf("got %s, listed %d", summary(got), listed)
	}
	wantCoverage(t, cov, unversioned.CallerCoverageComplete, "")
}

func TestCiliumUnreadablePartial(t *testing.T) {
	for _, kind := range []string{ciliumPolicyList.Kind, ciliumClusterwidePolicyList.Kind} {
		fns := interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if u, ok := list.(*unstructured.UnstructuredList); ok && u.GroupVersionKind().Kind == kind {
				return errors.New("forbidden")
			}
			return c.List(ctx, list, opts...)
		}}
		_, cov := ciliumObserve(t, fns, ciliumInput())
		wantCoverage(t, cov, unversioned.CallerCoveragePartial, "cilium-unreadable")
	}
}
