package authn

import (
	"context"
	"errors"
	"testing"
)

func TestReviewObjectCarriesVerbAndName(t *testing.T) {
	attrs := func(res ResourceAttributes) map[string]any {
		spec := reviewObject("SubjectAccessReview", Person{User: "ana"}, "billing", res).Object["spec"].(map[string]any)
		return spec["resourceAttributes"].(map[string]any)
	}
	if a := attrs(PactoResource); a["verb"] != "list" || a["name"] != nil {
		t.Errorf("a list review = %v", a)
	}
	a := attrs(ResourceAttributes{Group: "apps", Resource: "deployments", Verb: "update", Name: "invoices"})
	if a["verb"] != "update" || a["name"] != "invoices" || a["namespace"] != "billing" || a["resource"] != "deployments" {
		t.Errorf("an update review = %v", a)
	}
	join := ClusterJoinResource
	join.Name = "9f1c4e2a"
	if a := attrs(join); a["verb"] != "approve" || a["name"] != "9f1c4e2a" || a["resource"] != "clusterjoins" || a["group"] != "pacto.trianalab.io" {
		t.Errorf("an approve review = %v", a)
	}
}

func TestAskCacheKeyHoldsVerbAndName(t *testing.T) {
	var asked []ResourceAttributes
	review := func(_ context.Context, _ Person, _ string, res ResourceAttributes) (bool, error) {
		asked = append(asked, res)
		return res.Verb == "", nil // list allowed, everything else refused
	}
	v := NewViewer(func(context.Context) map[string]Review { return map[string]Review{"": review} })
	w := &clusterWork{key: "", review: review}
	ctx := context.Background()
	dep := workloadResources["Deployment"]
	if ok, _ := v.ask(ctx, w, alice, reviewer(alice), "billing", dep); !ok {
		t.Fatal("list refused")
	}
	upd := dep
	upd.Verb, upd.Name = "update", "invoices"
	if ok, _ := v.ask(ctx, w, alice, reviewer(alice), "billing", upd); ok {
		t.Error("a cached list allow answered an update question")
	}
	other := upd
	other.Name = "ledger"
	_, _ = v.ask(ctx, w, alice, reviewer(alice), "billing", other)
	if len(asked) != 3 {
		t.Errorf("asked %v, want three distinct reviews", asked)
	}
}

func TestMayUpdateWorkloadUsesClusterReviewer(t *testing.T) {
	var got []string
	reviewIn := func(cluster string) Review {
		return func(_ context.Context, _ Person, ns string, res ResourceAttributes) (bool, error) {
			got = append(got, cluster+"/"+ns+"/"+res.Resource+"/"+res.Verb+"/"+res.Name)
			return cluster == "dev", nil
		}
	}
	v := NewViewer(func(context.Context) map[string]Review {
		return map[string]Review{"prod": reviewIn("prod"), "dev": reviewIn("dev")}
	})
	ctx := context.Background()
	if ok, err := v.MayUpdateWorkload(ctx, alice, "dev", "billing", "Deployment", "invoices"); !ok || err != nil {
		t.Errorf("dev: %v %v", ok, err)
	}
	if ok, err := v.MayUpdateWorkload(ctx, alice, "prod", "billing", "StatefulSet", "invoices"); ok || err != nil {
		t.Errorf("prod: %v %v", ok, err)
	}
	if len(got) != 2 || got[0] != "dev/billing/deployments/update/invoices" || got[1] != "prod/billing/statefulsets/update/invoices" {
		t.Errorf("reviews = %v", got)
	}
	if _, err := v.MayUpdateWorkload(ctx, alice, "edge", "billing", "Deployment", "invoices"); !errors.Is(err, ErrClusterNotReviewable) {
		t.Errorf("an unreviewed cluster: %v", err)
	}
	if ok, err := v.MayUpdateWorkload(ctx, alice, "dev", "billing", "Pod", "invoices"); ok || err != nil {
		t.Errorf("a kind Pacto does not read: %v %v", ok, err)
	}
	// A Pacto resource stands in for a workload Pacto does not know.
	if ok, err := v.MayUpdateWorkload(ctx, alice, "dev", "billing", PactoKind, "invoices"); !ok || err != nil || got[len(got)-1] != "dev/billing/pactos/update/invoices" {
		t.Errorf("a Pacto: %v %v %v", ok, err, got)
	}
}

func TestViewerMayUpdateReviewsResource(t *testing.T) {
	var got []string
	v := NewViewer(func(context.Context) map[string]Review {
		return map[string]Review{"c1": func(_ context.Context, _ Person, ns string, res ResourceAttributes) (bool, error) {
			got = append(got, ns+"/"+res.Group+"/"+res.Resource+"/"+res.Verb+"/"+res.Name)
			return true, nil
		}}
	})
	res := ResourceAttributes{Group: "example.io", Resource: "widgets", Verb: "update", Name: "xbuckets.storage.acme.io"}
	if ok, err := v.MayUpdate(context.Background(), alice, "c1", "", res); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if len(got) != 1 || got[0] != "/example.io/widgets/update/xbuckets.storage.acme.io" {
		t.Fatalf("reviews = %v", got)
	}
	if _, err := v.MayUpdate(context.Background(), alice, "edge", "", res); !errors.Is(err, ErrClusterNotReviewable) {
		t.Fatalf("an unreviewed cluster: %v", err)
	}
}

func TestViewerMayUpdateMemberClusterNotReviewable(t *testing.T) {
	member := MemberReview(&fakeLink{uid: "m1"}, nil, nil)
	failing := func(context.Context, Person, string, ResourceAttributes) (bool, error) {
		return false, errors.New("boom")
	}
	v := NewViewer(func(context.Context) map[string]Review { return map[string]Review{"m1": member, "c2": failing} })
	res := ResourceAttributes{Group: "example.io", Resource: "widgets", Verb: "update", Name: "x"}
	if ok, err := v.MayUpdate(context.Background(), alice, "m1", "", res); ok || !errors.Is(err, ErrClusterNotReviewable) {
		t.Fatalf("a member cluster answered %v %v", ok, err)
	}
	if ok, err := v.MayUpdate(context.Background(), alice, "c2", "", res); ok || err == nil || errors.Is(err, ErrClusterNotReviewable) {
		t.Fatalf("a failed review answered %v %v", ok, err)
	}
}
