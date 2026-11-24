package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// manifestLayer is a gzipped tar of one manifests file.
func manifestLayer(t *testing.T, yaml string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "deploy.yaml", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(yaml))})
	_, _ = tw.Write([]byte(yaml))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

const crbYAML = "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: orders-admin\n"

func inspectorPolicy() runtime.Object {
	return obj("v1", "ConfigMap", "pacto-delivery", delivery.PolicyConfigMap, map[string]any{
		"data": map[string]any{delivery.PolicyKey: `{"agentGroups":["pacto:agents"],"manifestInspector":true}`}})
}

func TestDeliveryPlanInspectsManifests(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, Reason: "ship", OnBehalfOf: "alice"}
	d, _, reg := deliveryFixture(t, "system:serviceaccount:agents:bot", []string{"pacto:agents"}, nil, inspectorPolicy())
	d.PolicyNamespace = "pacto-delivery"
	reg.layerType, reg.layer = delivery.FluxContentMediaType, manifestLayer(t, crbYAML)
	plan, _, err := d.Plan(ctx, req)
	if err != nil || plan.Predicted != delivery.PredictRejected || !hasVerdict(plan.Verdicts, delivery.GateManifests, delivery.Fail) {
		t.Fatalf("a ClusterRoleBinding is refused: %+v %v", plan, err)
	}
	// A served kind is read through the discovery scope.
	d.Scope = func(group, kind string) (bool, bool) { return true, group == "example.com" }
	reg.layer = manifestLayer(t, "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n")
	if plan, _, err = d.Plan(ctx, req); err != nil || plan.Predicted != delivery.PredictProceeds || !hasVerdict(plan.Verdicts, delivery.GateManifests, delivery.Pass) {
		t.Fatalf("a served namespaced kind passes: %+v %v", plan, err)
	}
	// Without the published flag nothing is read.
	d, _, reg = deliveryFixture(t, "system:serviceaccount:agents:bot", []string{"pacto:agents"}, nil)
	reg.layerType, reg.layer = delivery.FluxContentMediaType, manifestLayer(t, crbYAML)
	if plan, _, err = d.Plan(ctx, req); err != nil || plan.Predicted != delivery.PredictProceeds || hasVerdict(plan.Verdicts, delivery.GateManifests, delivery.Fail) {
		t.Fatalf("the inspector is off without the policy flag: %+v %v", plan, err)
	}
}

// TestDeliveryPlanUnreadablePolicyAsksAboutTheInspector: a policy the client
// cannot read leaves whether the promoter inspects unknown, so the plan asks
// a person instead of planning as if the inspector were off.
func TestDeliveryPlanUnreadablePolicyAsksAboutTheInspector(t *testing.T) {
	t.Parallel()
	req := delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, Reason: "ship", OnBehalfOf: "alice"}
	d, _, reg := deliveryFixture(t, "system:serviceaccount:agents:bot", []string{"pacto:agents"}, nil)
	d.PolicyNamespace = "pacto-delivery"
	reg.layerType, reg.layer = delivery.FluxContentMediaType, manifestLayer(t, crbYAML)
	plan, _, err := d.Plan(context.Background(), req)
	if err != nil || plan.Predicted != delivery.PredictHeld || !hasVerdict(plan.Verdicts, delivery.GateManifests, delivery.Unknown) ||
		!strings.Contains(plan.Question, "whether the manifest inspector runs is unknown") {
		t.Fatalf("an unreadable policy: %+v %v", plan, err)
	}
}

// TestDeliveryPlanRefusesRawInfraUnderIDP: a claim the discovery categories
// call infrastructure is refused under the published refuseRawInfra (profile
// idp), and asks a person without it.
func TestDeliveryPlanRefusesRawInfraUnderIDP(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, Reason: "ship", OnBehalfOf: "alice"}
	claim := "apiVersion: platform.example.org/v1\nkind: PostgresClaim\nmetadata:\n  name: db\n"
	scope := func(string, string) (bool, bool) { return true, true }
	infra := func(group, kind string) (bool, bool) { return kind == "PostgresClaim", true }
	for _, c := range []struct {
		policy string
		want   delivery.Prediction
		result delivery.Result
	}{
		{`{"agentGroups":["pacto:agents"],"manifestInspector":true}`, delivery.PredictHeld, delivery.Unknown},
		{`{"agentGroups":["pacto:agents"],"manifestInspector":true,"refuseRawInfra":true}`, delivery.PredictRejected, delivery.Fail},
	} {
		d, _, reg := deliveryFixture(t, "system:serviceaccount:agents:bot", []string{"pacto:agents"}, nil,
			obj("v1", "ConfigMap", "pacto-delivery", delivery.PolicyConfigMap, map[string]any{"data": map[string]any{delivery.PolicyKey: c.policy}}))
		d.PolicyNamespace, d.Scope, d.Infra = "pacto-delivery", scope, infra
		reg.layerType, reg.layer = delivery.FluxContentMediaType, manifestLayer(t, claim)
		if plan, _, err := d.Plan(ctx, req); err != nil || plan.Predicted != c.want || !hasVerdict(plan.Verdicts, delivery.GateManifests, c.result) {
			t.Fatalf("%s: %+v %v", c.policy, plan, err)
		}
	}
}

func hasVerdict(vs []delivery.Verdict, gate string, r delivery.Result) bool {
	for _, v := range vs {
		if v.Gate == gate && v.Result == r {
			return true
		}
	}
	return false
}

// TestPlanSourcesAgreeOnInspection: the client inspects exactly what a source
// shaped like the promoter's (the applier object, the Kustomizations, the
// apply repository and the same scope) does.
func TestPlanSourcesAgreeOnInspection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ks := obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "prod", "payments", map[string]any{
		"spec": map[string]any{"sourceRef": map[string]any{"kind": "OCIRepository", "name": "payments"}, "postBuild": map[string]any{}}})
	d, c, reg := deliveryFixture(t, "bob", nil, nil, ks)
	reg.layerType, reg.layer = delivery.FluxContentMediaType, manifestLayer(t, crbYAML)
	scope := func(string, string) (bool, bool) { return true, true }
	d.Scope = scope
	stage, err := d.Stage(ctx, "prod", "payments-prod")
	if err != nil {
		t.Fatal(err)
	}
	got := (planSources{d}).Manifests(ctx, *stage, dlB).Verdict()
	// The promoter-shaped source.
	repo, _ := c.Resource(gvrOCIRepository).Namespace("prod").Get(ctx, "payments", metav1.GetOptions{})
	kss, _ := d.kustomizations(ctx)
	want := delivery.InspectRevision(ctx, reg, "reg.example/apps/payments", dlB, delivery.ManifestTargetOf(*stage, repo.Object, kss, nil),
		delivery.ChainScope(delivery.BuiltinScope, scope)).Verdict()
	if !reflect.DeepEqual(got, want) || got.Result != delivery.Fail || !strings.Contains(got.Citation["unread"], "Kustomization payments transforms the artifact: spec.postBuild") {
		t.Fatalf("client %+v, promoter %+v", got, want)
	}
	// An applier the client cannot read is Unknown, and says why.
	stage.Spec.ApplyRef = delivery.ApplyRef{Kind: delivery.ApplierHelmRelease, Name: "gone"}
	if in := (planSources{d}).Manifests(ctx, *stage, dlB); in.Verdict().Result != delivery.Unknown || !strings.Contains(in.Unread[0], "the applier: ") {
		t.Fatalf("unreadable applier: %+v", in)
	}
	// The applier object is read by its kind.
	for _, kind := range []string{delivery.ApplierArgoCD, delivery.ApplierHelmRelease} {
		if _, err := d.applierObject(ctx, "prod", delivery.ApplyRef{Kind: kind, Name: "gone"}); err == nil {
			t.Fatalf("a missing %s", kind)
		}
	}
}

func TestDiscoveryScopeFallsBackToBuiltin(t *testing.T) {
	t.Parallel()
	builtin := reflect.ValueOf(delivery.ScopeFunc(delivery.BuiltinScope)).Pointer()
	for name, cfg := range map[string]*rest.Config{
		"none":    nil,
		"bad TLS": {Host: "https://k", TLSClientConfig: rest.TLSClientConfig{CAFile: "/nonexistent/ca.crt"}},
	} {
		scope, infra := DiscoveryScope(cfg)
		if got := reflect.ValueOf(scope).Pointer(); got != builtin || infra != nil {
			t.Errorf("%s: want the built-in scope and no infra", name)
		}
	}
	// A cluster that does not answer knows no kind, and cannot tell
	// infrastructure.
	scope, infra := DiscoveryScope(&rest.Config{Host: "http://127.0.0.1:1"})
	if _, known := scope("example.com", "Widget"); known {
		t.Fatal("an unreachable discovery knows nothing")
	}
	if _, known := infra("example.com", "Widget"); known {
		t.Fatal("an unreachable discovery tells no infrastructure")
	}
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "example.com", Version: "v1"}})
	m.Add(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}, meta.RESTScopeNamespace)
	m.Add(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Gadget"}, meta.RESTScopeRoot)
	s := mapperScope(m)
	if ns, known := s("example.com", "Widget"); !ns || !known {
		t.Fatal("namespaced")
	}
	if ns, known := s("example.com", "Gadget"); ns || !known {
		t.Fatal("cluster-scoped")
	}
}
