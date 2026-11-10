package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

var regressedB = impact.Event{Kind: impact.EventRegressed, Service: "payments", Revision: dlB}

// setStageStatus writes fields into the payments-prod Stage's status.
func setStageStatus(t *testing.T, c *dynamicfake.FakeDynamicClient, fields map[string]any) {
	t.Helper()
	ri := c.Resource(gvrStages).Namespace("prod")
	u, _ := ri.Get(context.Background(), "payments-prod", metav1.GetOptions{})
	for k, v := range fields {
		_ = unstructured.SetNestedField(u.Object, v, "status", k)
	}
	if _, err := ri.Update(context.Background(), u, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestRegressionVersionNeedsDigest(t *testing.T) {
	d, _, _ := deliveryFixture(t, "alice", nil, nil)
	reg := d.RegressionEvidence(context.Background(), impact.Event{Kind: impact.EventRegressed, Service: "payments", Revision: "1.0.0"})
	if len(reg.Evidence) != 0 || !strings.Contains(reg.Unavailable, "name the artifact digest") {
		t.Fatalf("a version reads nothing: %+v", reg)
	}
}

// TestRegressionReadsStages: the failed list, the re-check's Fail verdicts
// and a rollback off the digest are cited, and a complete read says nothing
// is missing.
func TestRegressionReadsStages(t *testing.T) {
	rollback := opFixture("rb1", map[string]any{"type": delivery.TypeRollback, "stage": "payments-prod",
		"params": map[string]any{"from": dlB, "to": dlA}, "actor": map[string]any{"user": "bot"}, "cause": map[string]any{"reason": "health failed"}},
		map[string]any{"phase": "Concluded", "outcome": "Succeeded", "seq": int64(4), "pointer": map[string]any{"before": dlB, "after": dlA}})
	d, c, _ := deliveryFixture(t, "alice", nil, nil, rollback)
	setStageStatus(t, c, map[string]any{"failedDigests": []any{dlB}, "lastCheck": map[string]any{"operation": "uid-p", "digest": dlB,
		"at": "2026-10-21T10:00:00Z", "verdicts": []any{map[string]any{"gate": "health", "result": "Fail", "message": "5xx"}}}})
	reg := d.RegressionEvidence(context.Background(), regressedB)
	if reg.Unavailable != "" {
		t.Fatalf("a complete read names nothing missing: %q", reg.Unavailable)
	}
	kinds := map[string]bool{}
	for _, e := range reg.Evidence {
		kinds[e.Kind] = e.Namespace == "prod" && e.Stage == "payments-prod"
	}
	if !kinds[impact.RegressionFailed] || !kinds[impact.RegressionCheck] || !kinds[impact.RegressionRollback] {
		t.Fatalf("evidence = %+v", reg.Evidence)
	}
}

// TestRegressionForbiddenNamespaceNamed: a namespace the caller may not read
// is named, and what was read is kept.
func TestRegressionForbiddenNamespaceNamed(t *testing.T) {
	d, c, _ := deliveryFixture(t, "alice", nil, nil, obj("v1", "Namespace", "", "prod", nil), obj("v1", "Namespace", "", "test", nil))
	setStageStatus(t, c, map[string]any{"failedDigests": []any{dlB}})
	c.PrependReactor("list", "stages", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if ns := a.GetNamespace(); ns == "" || ns == "test" {
			return true, nil, apierrors.NewForbidden(gvrStages.GroupResource(), "", errors.New("no"))
		}
		return false, nil, nil
	})
	reg := d.RegressionEvidence(context.Background(), regressedB)
	if len(reg.Evidence) != 1 || !strings.Contains(reg.Unavailable, "evidence from these namespaces was not read") || !strings.Contains(reg.Unavailable, "test") {
		t.Fatalf("forbidden namespace = %+v", reg)
	}
	// A history that cannot be read is named too.
	c.PrependReactor("list", "operations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("gone")
	})
	if reg := d.RegressionEvidence(context.Background(), regressedB); !strings.Contains(reg.Unavailable, "the histories of these Stages were not read") {
		t.Fatalf("unreadable history = %+v", reg)
	}
}

func TestRegressionNotServed(t *testing.T) {
	d, c, _ := deliveryFixture(t, "alice", nil, nil)
	c.PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(gvrStages.GroupResource(), "")
	})
	if reg := d.RegressionEvidence(context.Background(), regressedB); !strings.Contains(reg.Unavailable, "does not serve delivery.pacto.trianalab.io") {
		t.Fatalf("not served = %+v", reg)
	}
	c.PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})
	if reg := d.RegressionEvidence(context.Background(), regressedB); !strings.Contains(reg.Unavailable, "could not be read: ") || !strings.HasSuffix(reg.Unavailable, "boom") {
		t.Fatalf("error = %+v", reg)
	}
}
