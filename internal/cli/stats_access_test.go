package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// accessReviews is a caller's client whose SelfSubjectAccessReviews allow
// the namespaces in allowed (or fail with err) and are recorded.
type accessReviews struct {
	mu      sync.Mutex
	asked   []map[string]any
	allowed map[string]bool
	err     error
}

func (a *accessReviews) client() *app.Delivery {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dyn.PrependReactor("create", "selfsubjectaccessreviews", func(act k8stesting.Action) (bool, runtime.Object, error) {
		o := act.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured)
		attrs, _, _ := unstructured.NestedMap(o.Object, "spec", "resourceAttributes")
		a.mu.Lock()
		defer a.mu.Unlock()
		a.asked = append(a.asked, attrs)
		if a.err != nil {
			return true, nil, a.err
		}
		ns, _ := attrs["namespace"].(string)
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": a.allowed[ns]}}}, nil
	})
	return &app.Delivery{Dynamic: dyn}
}

// namespacesFleet is a snapshot with one Kubernetes target in each of nss.
func namespacesFleet(t *testing.T, nss ...string) *fleet.Query {
	t.Helper()
	var targets []fleet.RawTarget
	for _, ns := range nss {
		targets = append(targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: "orders", Service: "orders"})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{Targets: targets}))
	expect(t, err == nil, "fleet: %v", err)
	return fleet.NewQuery(snap)
}

func TestOperationAccessAsksListOperations(t *testing.T) {
	a := &accessReviews{allowed: map[string]bool{"prod-a": true}}
	visible, err := operationAccess(a.client(), namespacesFleet(t, "prod-a"))(context.Background(), []string{"prod-a"})
	expect(t, err == nil && visible("prod-a"), "prod-a: %v", err)
	expect(t, len(a.asked) == 1, "reviews = %+v", a.asked)
	got := a.asked[0]
	expect(t, got["verb"] == "list" && got["group"] == "delivery.pacto.trianalab.io" && got["resource"] == "operations" && got["namespace"] == "prod-a",
		"review = %+v", got)
}

func TestOperationAccessIgnoresUnknownNamespaces(t *testing.T) {
	a := &accessReviews{allowed: map[string]bool{"prod-a": true, "prod-b": true, "ns-7": true}}
	nss := []string{"prod-a", "prod-b"}
	for i := range 1000 {
		nss = append(nss, fmt.Sprintf("ns-%d", i))
	}
	visible, err := operationAccess(a.client(), namespacesFleet(t, "prod-a", "prod-b"))(context.Background(), nss)
	expect(t, err == nil, "access: %v", err)
	expect(t, len(a.asked) == 2, "reviews = %d", len(a.asked))
	expect(t, visible("prod-a") && visible("prod-b") && !visible("ns-7"), "a namespace outside the snapshot is refused")
}

func TestOperationAccessErrorIsError(t *testing.T) {
	a := &accessReviews{err: errors.New("apiserver down")}
	visible, err := operationAccess(a.client(), namespacesFleet(t, "prod-a"))(context.Background(), []string{"prod-a"})
	expect(t, visible == nil && errors.Is(err, errOperationReview) && !strings.Contains(err.Error(), "prod-a"), "err = %v", err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = operationAccess(a.client(), namespacesFleet(t, "prod-a"))(ctx, []string{"prod-a"})
	expect(t, errors.Is(err, context.Canceled), "a cancelled review is the context's error: %v", err)
}

// A fleet-only server reviews its caller with their own client for
// pacto_stats and never hands them a delivery client, which would grant the
// delivery tools to any code that reads it.
func TestFleetOnlyReviewGivesNoDeliveryClient(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dyn.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{"username": "alice"}}}}, nil
	})
	who, _, err := remoteCaller{}.review(context.Background(), dyn)
	expect(t, err == nil && who.person.User == "alice", "review: %v", err)
	expect(t, who.own != nil && who.own.Dynamic == dyn && who.delivery == nil, "reviewed = %+v", who)
}
