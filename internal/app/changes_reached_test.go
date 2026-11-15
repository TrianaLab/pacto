package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// refuseStages makes the Stage list of each of nss ("" cluster-wide) fail with
// a 403.
func refuseStages(e *recordEnv, nss ...string) {
	e.c.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
		for _, ns := range nss {
			if a.GetNamespace() == ns {
				return true, nil, apierrors.NewForbidden(gvrStages.GroupResource(), "", errors.New("no"))
			}
		}
		return false, nil, nil
	})
}

func TestReachedReadsSignedArrivals(t *testing.T) {
	e := newRecordEnv(t)
	_, r2 := e.twoRecords()
	got := e.d.Reached(context.Background(), "payments")
	rs := got.ByContract[dlC]
	if got.Times != fleet.TimesRead || len(got.Unreadable) != 0 || len(rs) != 1 {
		t.Fatalf("reached = %+v", got)
	}
	want := fleet.ChangeReach{Namespace: "prod", Stage: "payments-prod", Environment: "prod", At: "2026-10-21T10:00:00Z", Record: r2, Verified: true}
	if rs[0] != want {
		t.Fatalf("reach = %+v, want %+v", rs[0], want)
	}
}

func TestReachedForbiddenHidden(t *testing.T) {
	e := newRecordEnv(t)
	refuseStages(e, "")
	e.c.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(gvrNamespaces.GroupResource(), "", errors.New("no"))
	})
	if got := e.d.Reached(context.Background(), "payments"); got.Times != fleet.TimesHidden || got.Reason == "" || got.ByContract != nil {
		t.Fatalf("reached = %+v", got)
	}
	// Namespaces listed, every one refused: hidden too.
	e = newRecordEnv(t, obj("v1", "Namespace", "", "prod", nil))
	refuseStages(e, "", "prod")
	if got := e.d.Reached(context.Background(), "payments"); got.Times != fleet.TimesHidden || len(got.Unreadable) != 1 {
		t.Fatalf("reached = %+v", got)
	}
}

func TestReachedPartialNamespaces(t *testing.T) {
	e := newRecordEnv(t, obj("v1", "Namespace", "", "prod", nil), obj("v1", "Namespace", "", "test", nil))
	e.twoRecords()
	refuseStages(e, "", "test")
	got := e.d.Reached(context.Background(), "payments")
	if got.Times != fleet.TimesPartial || len(got.Unreadable) != 1 || !strings.Contains(got.Unreadable[0], "test") || len(got.ByContract[dlC]) != 1 {
		t.Fatalf("reached = %+v", got)
	}
	// A Stage whose history cannot be read is named too.
	e = newRecordEnv(t)
	failOperations(e.c)
	if got := e.d.Reached(context.Background(), "payments"); got.Times != fleet.TimesPartial || !strings.Contains(got.Unreadable[0], "payments-prod") {
		t.Fatalf("reached = %+v", got)
	}
}

func TestReachedNotServedNotRead(t *testing.T) {
	e := newRecordEnv(t)
	e.c.PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the server could not find the requested resource")
	})
	got := e.d.Reached(context.Background(), "payments")
	if got.Times != fleet.TimesNotRead || !strings.Contains(got.Reason, "could not find") {
		t.Fatalf("reached = %+v", got)
	}
}
