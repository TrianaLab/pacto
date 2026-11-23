package delivery

import (
	"reflect"
	"strings"
	"testing"
)

var idpPublisher = PublisherIdentity{Issuer: "https://token.actions.githubusercontent.com",
	Subject: "https://github.com/acme/payments/.github/workflows/ci.yml@refs/heads/main"}

func dig(o map[string]any, path ...string) any {
	var v any = o
	for _, p := range path {
		m, _ := v.(map[string]any)
		v = m[p]
	}
	return v
}

func TestFluxApplierObjectsManifests(t *testing.T) {
	objs := FluxApplierObjects(FluxApplierInput{Service: "payments", Environment: "staging", Namespace: "payments-staging",
		URL: "oci://ghcr.io/acme/deploy/payments", Publisher: idpPublisher})
	if len(objs) != 2 || objs[0]["kind"] != "OCIRepository" || objs[1]["kind"] != "Kustomization" ||
		objs[1]["apiVersion"] != "kustomize.toolkit.fluxcd.io/v1" {
		t.Fatalf("objects %v", objs)
	}
	checkSuspendedManaged(t, objs, "payments-staging")
	p := objs[0]
	if _, ok := dig(p, "spec").(map[string]any)["ref"]; ok || dig(p, "spec", "url") != "oci://ghcr.io/acme/deploy/payments" ||
		dig(p, "spec", "layerSelector") != nil {
		t.Fatalf("pointer %v", p)
	}
	checkPinned(t, p)
	k := objs[1]
	if dig(k, "spec", "serviceAccountName") != "payments-deployer" || dig(k, "spec", "deletionPolicy") != "Orphan" ||
		dig(k, "spec", "prune") != true || dig(k, "spec", "targetNamespace") != "payments-staging" ||
		!reflect.DeepEqual(dig(k, "spec", "sourceRef"), map[string]any{"kind": "OCIRepository", "name": "payments-staging"}) {
		t.Fatalf("kustomization %v", k)
	}
}

// checkSuspendedManaged checks each object is name in namespace name,
// labelled managed and apply-ref, and suspended.
func checkSuspendedManaged(t *testing.T, objs []map[string]any, name string) {
	t.Helper()
	for _, o := range objs {
		labels, _ := dig(o, "metadata", "labels").(map[string]any)
		if dig(o, "metadata", "name") != name || dig(o, "metadata", "namespace") != name ||
			labels[ManagedLabel] != "pacto" || labels[ApplyRefLabel] != name || dig(o, "spec", "suspend") != true {
			t.Errorf("%s: %v", o["kind"], o)
		}
	}
}

// checkPinned checks the pointer verifies the publisher's keyless identity,
// anchored.
func checkPinned(t *testing.T, p map[string]any) {
	t.Helper()
	id := dig(p, "spec", "verify", "matchOIDCIdentity").([]any)[0].(map[string]any)
	if !PinnedPattern(id["issuer"].(string)) || !PinnedPattern(id["subject"].(string)) || !strings.Contains(id["subject"].(string), `ci\.yml@refs/heads/main`) {
		t.Fatalf("verify %v", id)
	}
}

func TestFluxApplierObjectsChart(t *testing.T) {
	objs := FluxApplierObjects(FluxApplierInput{Service: "payments", Environment: "production", Namespace: "production",
		URL: "ghcr.io/acme/charts/payments", Chart: true, Publisher: idpPublisher})
	p, h := objs[0], objs[1]
	if dig(p, "spec", "url") != "oci://ghcr.io/acme/charts/payments" || dig(p, "spec", "layerSelector", "operation") != "copy" {
		t.Fatalf("pointer %v", p)
	}
	if h["kind"] != "HelmRelease" || h["apiVersion"] != "helm.toolkit.fluxcd.io/v2" || dig(h, "spec", "suspend") != true ||
		dig(h, "spec", "serviceAccountName") != "payments-deployer" || dig(h, "spec", "chartRef", "name") != "payments-production" ||
		dig(h, "spec", "sourceRef") != nil {
		t.Fatalf("helmrelease %v", h)
	}
}

func TestDeployerObjects(t *testing.T) {
	objs := DeployerObjects("payments", "payments-staging", "d-pacto-operator-delivery-deployer")
	sa, rb := objs[0], objs[1]
	if sa["kind"] != "ServiceAccount" || dig(sa, "metadata", "name") != "payments-deployer" || dig(sa, "metadata", "namespace") != "payments-staging" ||
		dig(sa, "metadata", "labels").(map[string]any)[ManagedLabel] != "pacto" {
		t.Fatalf("service account %v", sa)
	}
	want := map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "d-pacto-operator-delivery-deployer"}
	subject := map[string]any{"kind": "ServiceAccount", "name": "payments-deployer", "namespace": "payments-staging"}
	if rb["kind"] != "RoleBinding" || dig(rb, "metadata", "name") != "payments-deployer" || !reflect.DeepEqual(rb["roleRef"], want) ||
		!reflect.DeepEqual(rb["subjects"], []any{subject}) || dig(rb, "metadata", "labels").(map[string]any)[ManagedLabel] != "pacto" {
		t.Fatalf("role binding %v", rb)
	}
}

func TestPlacementQuestion(t *testing.T) {
	p := Placement{Namespace: "payments-staging", Evidence: "Argo CD source github.com/acme/payments"}
	if got := PlacementQuestion("payments", p); got != "deploy payments to namespace payments-staging (Argo CD source github.com/acme/payments)?" {
		t.Fatal(got)
	}
	if got := PlacementQuestion("payments", Placement{Namespace: "staging"}); got != "deploy payments to namespace staging?" {
		t.Fatal(got)
	}
}

func TestNamespacesDigest(t *testing.T) {
	a, b := NamespacesDigest([]string{"b", "a"}), NamespacesDigest([]string{"a", "b"})
	if a != b || !strings.HasPrefix(a, "sha256:") || len(a) != 71 || NamespacesDigest(nil) != "" || a == NamespacesDigest([]string{"a"}) {
		t.Fatalf("%s %s", a, b)
	}
}
