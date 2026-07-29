package fleetsrc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/internal/k8sclient"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// fakeK8sClient is a fake k8sclient.K8sClient driving the source's Collect path.
type fakeK8sClient struct {
	disc        *k8sclient.CRDDiscovery
	discErr     error
	listData    []byte
	listErr     error
	gotResource string
	gotNS       string
}

func (f *fakeK8sClient) Probe(context.Context) error { return nil }
func (f *fakeK8sClient) DiscoverCRD(context.Context) (*k8sclient.CRDDiscovery, error) {
	return f.disc, f.discErr
}
func (f *fakeK8sClient) ListJSON(_ context.Context, resource, namespace string) ([]byte, error) {
	f.gotResource, f.gotNS = resource, namespace
	return f.listData, f.listErr
}
func (f *fakeK8sClient) GetJSON(context.Context, string, string, string) ([]byte, error) {
	return nil, nil
}
func (f *fakeK8sClient) CountResources(context.Context, string, string) (int, error) {
	return 0, nil
}

func TestK8sSource_IDKind_Default(t *testing.T) {
	s := NewK8sSource("", &fakeK8sClient{}, "")
	if s.ID() != "k8s" {
		t.Errorf("default ID = %q, want k8s", s.ID())
	}
	if s.Kind() != "kubernetes" {
		t.Errorf("Kind = %q, want kubernetes", s.Kind())
	}
	if got := NewK8sSource("prod", nil, "").ID(); got != "prod" {
		t.Errorf("ID = %q, want prod", got)
	}
}

func TestK8sSource_Collect_DiscoverError(t *testing.T) {
	s := NewK8sSource("k8s", &fakeK8sClient{discErr: errors.New("unreachable")}, "")
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestK8sSource_Collect_NotFound(t *testing.T) {
	s := NewK8sSource("k8s", &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: false}}, "")
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(col.Targets) != 0 {
		t.Errorf("expected empty collection, got %d targets", len(col.Targets))
	}
}

func TestK8sSource_Collect_ListError(t *testing.T) {
	s := NewK8sSource("k8s", &fakeK8sClient{
		disc:    &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listErr: errors.New("list failed"),
	}, "")
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestK8sSource_Collect_InvalidJSON(t *testing.T) {
	s := NewK8sSource("k8s", &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte("{not json"),
	}, "")
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestK8sSource_Collect_ResourceNameFallback(t *testing.T) {
	fake := &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: ""}, // empty -> fallback
		listData: []byte(`{"items":[]}`),
	}
	s := NewK8sSource("k8s", fake, "team-a")
	if _, err := s.Collect(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.gotResource != "pactos" {
		t.Errorf("resource = %q, want pactos (fallback)", fake.gotResource)
	}
	if fake.gotNS != "team-a" {
		t.Errorf("namespace = %q, want team-a", fake.gotNS)
	}
}

func TestK8sSource_Collect_FullMapping(t *testing.T) {
	list := `{"items":[
	  {
	    "metadata":{"name":"payments-prod","namespace":"prod"},
	    "status":{
	      "contractStatus":"NonCompliant",
	      "contract":{"serviceName":"payments","resolvedRef":"ghcr.io/x/payments:1.2.0@sha256:abcd"},
	      "evaluationCoverage":{"evaluated":3,"required":5},
	      "observedRuntime":{"workloadKind":"Deployment","hasPVC":false},
	      "lastReconciledAt":"2026-07-29T10:00:00Z",
	      "findings":[
	        {"code":"WORKLOAD_MISMATCH","severity":"error","category":"RuntimeDrift","subject":"payments","contractPath":"spec.workload","message":"drift",
	         "evidenceRefs":[{"source":"k8s","observedAt":"2026-07-29T09:59:00Z"}]}
	      ]
	    }
	  }
	]}`
	s := NewK8sSource("prod-cluster", &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, "")
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(col.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(col.Targets))
	}
	tg := col.Targets[0]
	reconciled := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	if tg.ReconciledAt == nil || !tg.ReconciledAt.Equal(reconciled) {
		t.Fatalf("reconciledAt = %v, want %v", tg.ReconciledAt, reconciled)
	}
	tg.ReconciledAt = nil // asserted above; nil out so DeepEqual is location-agnostic
	want := fleet.RawTarget{
		Scope:           "prod",
		Kind:            "kubernetes",
		Name:            "payments-prod",
		Service:         "payments",
		ResolvedRef:     "ghcr.io/x/payments:1.2.0@sha256:abcd",
		Digest:          "sha256:abcd",
		Compliance:      "NonCompliant",
		Coverage:        &fleet.Coverage{Evaluated: 3, Required: 5},
		ObservedRuntime: map[string]any{"workloadKind": "Deployment", "hasPVC": false},
		Findings: []finding.Finding{{
			Code:         "WORKLOAD_MISMATCH",
			Severity:     finding.SeverityError,
			Category:     "RuntimeDrift",
			Subject:      finding.SubjectRef{Name: "payments"},
			ContractPath: "spec.workload",
			Message:      "drift",
			EvidenceRefs: []finding.EvidenceRef{{Source: "k8s", ObservedAt: "2026-07-29T09:59:00Z"}},
		}},
	}
	if !reflect.DeepEqual(tg, want) {
		t.Errorf("target mismapped:\n got %+v\nwant %+v", tg, want)
	}
}

func TestK8sSource_Collect_MinimalItem(t *testing.T) {
	// No contract block, no coverage, no findings, no timestamp: service falls
	// back to the CR name and optional fields stay zero.
	list := `{"items":[{"metadata":{"name":"orphan","namespace":"default"},"status":{"contractStatus":"Unknown"}}]}`
	s := NewK8sSource("k8s", &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, "")
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tg := col.Targets[0]
	if tg.Service != "orphan" {
		t.Errorf("service = %q, want orphan (CR name fallback)", tg.Service)
	}
	if tg.ResolvedRef != "" || tg.Digest != "" || tg.Coverage != nil || tg.ReconciledAt != nil || tg.Findings != nil {
		t.Errorf("expected zero optional fields, got %+v", tg)
	}
}

func TestDigestFromRef(t *testing.T) {
	if got := digestFromRef("repo:tag@sha256:xyz"); got != "sha256:xyz" {
		t.Errorf("digestFromRef = %q", got)
	}
	if got := digestFromRef("repo:tag"); got != "" {
		t.Errorf("digestFromRef no-digest = %q, want empty", got)
	}
}

func TestParseK8sTime(t *testing.T) {
	if parseK8sTime("") != nil {
		t.Error("empty should be nil")
	}
	if parseK8sTime("not-a-time") != nil {
		t.Error("unparseable should be nil")
	}
	got := parseK8sTime("2026-07-29T10:00:00Z")
	if got == nil || !got.Equal(time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("parseK8sTime = %v", got)
	}
}
