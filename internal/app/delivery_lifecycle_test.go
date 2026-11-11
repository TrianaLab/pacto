package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestLifecycleByVersion: a version is resolved over the artifacts the
// service's Stages know, and each Stage says what happened to it there.
func TestLifecycleByVersion(t *testing.T) {
	d, _ := statusFixture(t, opFixture("ship", map[string]any{"type": delivery.TypePromote, "stage": "payments-prod",
		"params": map[string]any{"from": dlC, "to": dlA}, "actor": map[string]any{"user": "bot"}},
		map[string]any{"phase": "Concluded", "outcome": "Succeeded", "verification": "Verified", "seq": int64(1),
			"pointer": map[string]any{"before": dlC, "after": dlA}}))
	ctx := context.Background()
	l, err := d.Lifecycle(ctx, delivery.LifecycleQuery{Service: "payments", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(l.Digest, l.Version, l.Contract, l.Service, l.AsOf != "", len(l.Stages), l.Completeness),
		fmt.Sprint(dlA, "1.0.0", "oci://reg/pactos/payments@"+dlC, "payments", true, 1, "partial"); got != want {
		t.Fatalf("lifecycle %s, want %s", got, want)
	}
	s := l.Stages[0]
	if len(s.Events) != 1 {
		t.Fatalf("events %+v", s.Events)
	}
	if got, want := fmt.Sprint(s.Stage, s.Running, s.Verified, s.Events[0].Role, s.Events[0].Operation, s.Source),
		fmt.Sprint("payments-prod", true, "yes", "arrived", "ship", delivery.SourceStatus); got != want {
		t.Fatalf("stage %s, want %s", got, want)
	}
}

// TestLifecycleRefusals: what the query does not name is an error.
func TestLifecycleRefusals(t *testing.T) {
	d, _ := statusFixture(t)
	ctx := context.Background()
	if _, err := d.Lifecycle(ctx, delivery.LifecycleQuery{Service: "payments", Version: "9.9.9"}); err == nil ||
		err.Error() != "no version 9.9.9 of payments is known to its Stages" {
		t.Fatalf("an unknown version: %v", err)
	}
	if _, err := d.Lifecycle(ctx, delivery.LifecycleQuery{Version: "1.0.0"}); err == nil || err.Error() != "name the service" {
		t.Fatalf("no service: %v", err)
	}
	if l, err := d.Lifecycle(ctx, delivery.LifecycleQuery{Service: "payments", Digest: dlA, Namespace: "prod"}); err != nil || l.Digest != dlA || l.Version != "1.0.0" {
		t.Fatalf("by digest %+v %v", l, err)
	}
}

// TestLifecycleForbiddenNamespace: a namespace the caller cannot list makes
// the answer partial and is named; a named one is not walked around.
func TestLifecycleForbiddenNamespace(t *testing.T) {
	d, c := statusFixture(t, obj("v1", "Namespace", "", "prod", nil), obj("v1", "Namespace", "", "test", nil))
	c.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if ns := a.GetNamespace(); ns == "" || ns == "test" {
			return true, nil, forbid("stages")
		}
		return false, nil, nil
	})
	l, err := d.Lifecycle(context.Background(), delivery.LifecycleQuery{Service: "payments", Digest: dlA})
	if err != nil || l.Completeness != "partial" || len(l.Unreadable) != 1 || !strings.Contains(l.Unreadable[0], "in test") || len(l.Stages) != 1 {
		t.Fatalf("lifecycle %+v %v", l, err)
	}
	if _, err := d.Lifecycle(context.Background(), delivery.LifecycleQuery{Service: "payments", Version: "9.9.9"}); err == nil ||
		!strings.Contains(err.Error(), "Stages not listed: ") || !strings.Contains(err.Error(), "it may be among those") {
		t.Fatalf("a miss beside a forbidden namespace is unknown: %v", err)
	}
	if _, err := d.Lifecycle(context.Background(), delivery.LifecycleQuery{Service: "payments", Digest: dlA, Namespace: "test"}); err == nil {
		t.Fatal("a named namespace is not walked around")
	}
}
