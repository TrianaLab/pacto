package k8sclient

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

func TestGoClientClusterIdentity(t *testing.T) {
	ns := &unstructured.Unstructured{}
	ns.SetAPIVersion("v1")
	ns.SetKind("Namespace")
	ns.SetName("kube-system")
	ns.SetUID("9f1c4e2a-0b1d-4c2e-8f3a-1b2c3d4e5f60")
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{{Version: "v1", Resource: "nodes"}: "NodeList"}, ns)

	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) { return &rest.Config{Host: "https://test:6443"}, nil }
	b.dynamic = func(*rest.Config) (dynamic.Interface, error) { return dyn, nil }
	client, err := b.goClient()
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.ClusterIdentity(context.Background(), "eu-prod")
	want := kubecluster.Identity{ID: "9f1c4e2a-0b1d-4c2e-8f3a-1b2c3d4e5f60", Name: "eu-prod", NameSource: kubecluster.NameFromContext}
	if err != nil || got != want {
		t.Fatalf("ClusterIdentity = %+v, %v; want %+v", got, err, want)
	}
}
