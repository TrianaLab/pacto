package kubecluster

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestInfraReadsCrossplaneCategories(t *testing.T) {
	dc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{
		{GroupVersion: "platform.example.org/v1", APIResources: []metav1.APIResource{
			{Kind: "PostgresClaim", Categories: []string{"crossplane", "claim"}},
			{Kind: "XPostgres", Categories: []string{"crossplane", "composite"}},
			{Kind: "Settings"},
		}},
		{GroupVersion: "rds.aws.example.io/v1beta1", APIResources: []metav1.APIResource{{Kind: "Instance", Categories: []string{"crossplane", "managed", "aws"}}}},
	}}}
	infra := Infra(dc)
	for _, c := range []struct {
		group, kind  string
		infra, known bool
	}{
		{"platform.example.org", "PostgresClaim", true, true},
		{"platform.example.org", "XPostgres", true, true},
		{"rds.aws.example.io", "Instance", true, true},
		{"platform.example.org", "Settings", false, true},
		{"unlisted.example.org", "Widget", false, false},
	} {
		if got, known := infra(c.group, c.kind); got != c.infra || known != c.known {
			t.Errorf("%s/%s: infra %v known %v", c.group, c.kind, got, known)
		}
	}
}
