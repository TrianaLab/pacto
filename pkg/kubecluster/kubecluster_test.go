package kubecluster

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testUID = "9f1c4e2a-0b1d-4c2e-8f3a-1b2c3d4e5f60"

func namespace(name, uid string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Namespace")
	u.SetName(name)
	u.SetUID(types.UID(uid))
	return u
}

func node(name string, labels map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Node")
	u.SetName(name)
	u.SetLabels(labels)
	return u
}

func fakeClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "namespaces"}: "NamespaceList",
			{Version: "v1", Resource: "nodes"}:      "NodeList",
		}, objs...)
}

func TestIDReadsKubeSystemUID(t *testing.T) {
	id, err := ID(context.Background(), fakeClient(namespace("kube-system", testUID), namespace("default", "other")))
	if err != nil || id != testUID {
		t.Fatalf("ID = %q, %v; want %q", id, err, testUID)
	}
}

func TestIDErrorNamesTheRead(t *testing.T) {
	_, err := ID(context.Background(), fakeClient())
	if err == nil || !strings.HasPrefix(err.Error(), "reading the cluster id: ") || !apierrors.IsNotFound(errors.Unwrap(err)) {
		t.Fatalf("err = %v; want a wrapped not-found naming the read", err)
	}
}

func TestIDEmptyUIDIsAnError(t *testing.T) {
	_, err := ID(context.Background(), fakeClient(namespace("kube-system", "")))
	if err == nil || !strings.Contains(err.Error(), "reading the cluster id") {
		t.Fatalf("err = %v; want an error for an empty uid", err)
	}
}

func TestIdentifyErrorWhenIDFails(t *testing.T) {
	if _, err := Identify(context.Background(), fakeClient(), "ctx"); err == nil {
		t.Fatal("want the ID error")
	}
}

func identify(t *testing.T, contextName string, objs ...runtime.Object) Identity {
	t.Helper()
	got, err := Identify(context.Background(), fakeClient(append(objs, namespace("kube-system", testUID))...), contextName)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != testUID {
		t.Fatalf("ID = %q", got.ID)
	}
	return got
}

func TestIdentifyNameFromEksctlLabel(t *testing.T) {
	got := identify(t, "arn:aws:eks:ctx", node("n1", map[string]string{"alpha.eksctl.io/cluster-name": "eu-prod"}))
	if got.Name != "eu-prod" || got.NameSource != NameFromNodeLabel {
		t.Fatalf("got %+v", got)
	}
}

func TestIdentifyNameFromAKSNodeResourceGroup(t *testing.T) {
	got := identify(t, "ctx", node("n1", map[string]string{"kubernetes.azure.com/cluster": "MC_aks_myAKSCluster_westus2"}))
	if got.Name != "myAKSCluster" || got.NameSource != NameFromNodeLabel {
		t.Fatalf("got %+v", got)
	}
}

func TestIdentifyAKSGroupWithUnderscoreFallsThrough(t *testing.T) {
	for _, v := range []string{"MC_my_rg_cluster_westus2", "custom-node-rg", "MC__cluster_westus2", "XX_rg_cluster_westus2"} {
		got := identify(t, "ctx", node("n1", map[string]string{"kubernetes.azure.com/cluster": v}))
		if got.Name != "ctx" || got.NameSource != NameFromContext {
			t.Fatalf("%q: got %+v", v, got)
		}
	}
}

func TestIdentifyEmptyEksctlLabelFallsThrough(t *testing.T) {
	got := identify(t, "ctx", node("n1", map[string]string{"alpha.eksctl.io/cluster-name": ""}))
	if got.NameSource != NameFromContext {
		t.Fatalf("got %+v", got)
	}
}

func TestIdentifyNodesForbiddenFallsToContext(t *testing.T) {
	c := fakeClient(namespace("kube-system", testUID), node("n1", map[string]string{"alpha.eksctl.io/cluster-name": "eu-prod"}))
	c.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("no"))
	})
	got, err := Identify(context.Background(), c, "eu-prod-ctx")
	if err != nil || got.Name != "eu-prod-ctx" || got.NameSource != NameFromContext {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestIdentifyNoContextFallsToUIDPrefix(t *testing.T) {
	got := identify(t, "")
	if got.Name != "9f1c4e2a" || got.NameSource != NameFromUID {
		t.Fatalf("got %+v", got)
	}
	short, err := Identify(context.Background(), fakeClient(namespace("kube-system", "abc")), "")
	if err != nil || short.Name != "abc" {
		t.Fatalf("short uid: got %+v, %v", short, err)
	}
}

func TestIdentifyNoNodesFallsToContext(t *testing.T) {
	got := identify(t, "kind-dev")
	if got.Name != "kind-dev" || got.NameSource != NameFromContext {
		t.Fatalf("got %+v", got)
	}
}
