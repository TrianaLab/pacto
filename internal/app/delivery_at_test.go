package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func TestHistoryAtNamesTheSignedRevision(t *testing.T) {
	t.Parallel()
	e := newRecordEnv(t)
	_, r2 := e.twoRecords()
	ctx := context.Background()
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	a, err := e.d.HistoryAt(ctx, "prod", delivery.ServiceRef{}, "payments-prod", at)
	if err != nil || len(a.Stages) != 1 || a.At != "2026-10-22T00:00:00Z" {
		t.Fatalf("at = %+v, %v", a, err)
	}
	s := a.Stages[0]
	if s.State != delivery.AtKnown || s.Digest != dlA || s.Record != r2 || s.Revision != paymentsKey() || a.Completeness != "complete" {
		t.Fatalf("stage at = %+v (%+v)", s, a)
	}
	expectVersion(t, s, "1.0.0")
	// The walk answers the same; before any record the Stage is unknown.
	a, err = e.d.HistoryAt(ctx, "", delivery.ServiceRef{Name: "payments"}, "", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(a.Stages) != 1 || a.Stages[0].State != delivery.AtUnknown || a.Completeness != "partial" {
		t.Fatalf("walk at = %+v, %v", a, err)
	}
	if _, err := e.d.HistoryAt(ctx, "prod", delivery.ServiceRef{}, "nope", at); err == nil {
		t.Fatal("a missing Stage is an error")
	}
	e.c.PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the server could not find the requested resource")
	})
	if _, err := e.d.HistoryAt(ctx, "", delivery.ServiceRef{}, "", at); err == nil {
		t.Fatal("an unserved Stage list is an error")
	}
}

func TestAtUnreadStageUnknown(t *testing.T) {
	t.Parallel()
	e := newRecordEnv(t)
	failOperations(e.c)
	a, err := e.d.HistoryAt(context.Background(), "prod", delivery.ServiceRef{}, "", time.Now())
	if err != nil || len(a.Stages) != 1 || a.Stages[0].State != delivery.AtUnknown ||
		!strings.Contains(a.Stages[0].Reason, "its history could not be read") || a.Completeness != "partial" {
		t.Fatalf("at = %+v, %v", a, err)
	}
}

func TestAtForbiddenNamespacePartial(t *testing.T) {
	t.Parallel()
	e := newRecordEnv(t, obj("v1", "Namespace", "", "prod", nil), obj("v1", "Namespace", "", "test", nil))
	e.twoRecords()
	refuseStages(e, "", "test")
	a, err := e.d.HistoryAt(context.Background(), "", delivery.ServiceRef{}, "", time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC))
	if err != nil || len(a.Stages) != 1 || a.Stages[0].State != delivery.AtKnown {
		t.Fatalf("at = %+v, %v", a, err)
	}
	if a.Completeness != "partial" || len(a.Unreadable) != 1 || !strings.Contains(a.Unreadable[0], "test") || !strings.Contains(a.Note, "others were not read") {
		t.Fatalf("a refused namespace makes the answer partial: %+v", a)
	}
}

// expectVersion fails t unless s names version, read from the bundle its digest carries.
func expectVersion(t *testing.T, s delivery.StageAt, version string) {
	t.Helper()
	if s.Version != version {
		t.Fatalf("version = %q, want %q", s.Version, version)
	}
}
