package kubecluster

import (
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
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

// TestInfraReadsAKindServedLater: a kind added to a group the cache already
// listed is read after one invalidation, and a kind still missing is
// unknown, never "not infrastructure".
func TestInfraReadsAKindServedLater(t *testing.T) {
	fake := &clienttesting.Fake{Resources: []*metav1.APIResourceList{
		{GroupVersion: "platform.example.org/v1", APIResources: []metav1.APIResource{{Name: "buckets", Kind: "Bucket", Categories: []string{"claim"}}}},
	}}
	infra := Infra(memory.NewMemCacheClient(&fakediscovery.FakeDiscovery{Fake: fake}))
	if got, known := infra("platform.example.org", "Bucket"); !got || !known {
		t.Fatalf("Bucket: %v %v", got, known)
	}
	if got, known := infra("platform.example.org", "PostgresClaim"); got || known {
		t.Fatalf("a kind discovery does not list is unknown: %v %v", got, known)
	}
	fake.Resources[0].APIResources = append(fake.Resources[0].APIResources, metav1.APIResource{Name: "postgresclaims", Kind: "PostgresClaim", Categories: []string{"claim"}})
	if got, known := infra("platform.example.org", "PostgresClaim"); !got || !known {
		t.Fatalf("a kind served later: %v %v", got, known)
	}
}

// TestInfraFailedGroupVersionIsUnknown: a group one of whose versions failed
// to list cannot say a kind is not infrastructure.
func TestInfraFailedGroupVersionIsUnknown(t *testing.T) {
	dc := failingDiscovery{&fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{Resources: []*metav1.APIResourceList{
		{GroupVersion: "platform.example.org/v1", APIResources: []metav1.APIResource{{Kind: "Settings"}}},
	}}}}
	if got, known := Infra(dc)("platform.example.org", "Settings"); got || known {
		t.Fatalf("Settings with v2 failed: %v %v", got, known)
	}
	if got, known := Infra(dc)("other.example.org", "Widget"); got || known {
		t.Fatalf("an unlisted group: %v %v", got, known)
	}
}

type failingDiscovery struct{ *fakediscovery.FakeDiscovery }

func (f failingDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	g, l, _ := f.FakeDiscovery.ServerGroupsAndResources()
	return g, l, &discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{{Group: "platform.example.org", Version: "v2"}: errors.New("boom")}}
}
