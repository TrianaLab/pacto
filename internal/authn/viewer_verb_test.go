package authn

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestReviewObjectCarriesVerbAndName: a review asks list by default, and the
// verb and object name it is given.
func TestReviewObjectCarriesVerbAndName(t *testing.T) {
	attrs := func(res ResourceAttributes) map[string]any {
		m, _, _ := unstructured.NestedMap(reviewObject("SubjectAccessReview", Person{User: "u"}, "", res).Object, "spec", "resourceAttributes")
		return m
	}
	if a := attrs(PactoResource); a["verb"] != "list" || a["name"] != "" {
		t.Errorf("default = %+v", a)
	}
	res := ClusterJoinResource
	res.Name = "9f1c4e2a"
	if a := attrs(res); a["verb"] != "approve" || a["name"] != "9f1c4e2a" || a["resource"] != "clusterjoins" || a["group"] != "pacto.trianalab.io" {
		t.Errorf("approve = %+v", a)
	}
}

// TestAskCacheKeyHoldsVerbAndName: a cached list answer never answers an
// approve question, nor an approval of one member another's.
func TestAskCacheKeyHoldsVerbAndName(t *testing.T) {
	var asked []ResourceAttributes
	w := &clusterWork{key: "c", review: func(_ context.Context, _ Person, _ string, res ResourceAttributes) (bool, error) {
		asked = append(asked, res)
		return res.Verb == "", nil
	}}
	v := NewViewer(nil)
	a, b := ClusterJoinResource, ClusterJoinResource
	a.Name, b.Name = "a", "b"
	list := ResourceAttributes{Group: a.Group, Resource: a.Resource}
	for _, res := range []ResourceAttributes{list, a, b, list, a} {
		ok, err := v.ask(context.Background(), w, Person{User: "u"}, "u", "", res)
		if err != nil || ok != (res.Verb == "") {
			t.Fatalf("%+v: %v %v", res, ok, err)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("asked %d reviews, want 3: %+v", len(asked), asked)
	}
}
