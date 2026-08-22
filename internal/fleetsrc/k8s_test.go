package fleetsrc

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
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
	// One reconcile pass both observes the cluster and evaluates it, so the same
	// instant answers both questions -- see TestK8sSource_ReconciledEvidenceIsEvidence.
	if tg.EvidenceAt == nil || !tg.EvidenceAt.Equal(reconciled) {
		t.Fatalf("evidenceAt = %v, want the reconcile time %v", tg.EvidenceAt, reconciled)
	}
	tg.ReconciledAt, tg.EvidenceAt = nil, nil // asserted above; nil out so DeepEqual is location-agnostic
	want := fleet.RawTarget{
		Scope: "prod",
		// Domain is derived from the resolved ref's registry+org, correlating this
		// runtime target with the same service's OCI baseline.
		Domain:          "ghcr.io/x",
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

// TestK8sSource_ReconciledEvidenceIsEvidence pins what a reconcile timestamp
// means. The operator observes the live cluster and evaluates it in the same
// pass, so lastReconciledAt is when this target's evidence was COLLECTED, not
// merely when Pacto accepted somebody else's. The source used to map it to
// ReconciledAt alone and leave EvidenceAt nil, and one record then carried two
// false statements at once: a target reporting the operator's own coverage and
// findings was annotated "no evidence has been observed for this target", and no
// Kubernetes target could ever be stale — staleness is measured from EvidenceAt,
// so an operator wedged for a month still read as freshly observed.
func TestK8sSource_ReconciledEvidenceIsEvidence(t *testing.T) {
	reconciled := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	list := `{"items":[{
	  "metadata":{"name":"payments-prod","namespace":"prod"},
	  "status":{
	    "contractStatus":"Unknown",
	    "contract":{"serviceName":"payments","resolvedRef":"ghcr.io/x/payments:1.2.0@sha256:abcd"},
	    "evaluationCoverage":{"evaluated":9,"required":10},
	    "lastReconciledAt":"2026-07-29T10:00:00Z"
	  }
	}]}`
	src := NewK8sSource("prod-cluster", &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, "")
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := col.Targets[0].EvidenceAt; got == nil || !got.Equal(reconciled) {
		t.Fatalf("EvidenceAt = %v, want the reconcile time %v", got, reconciled)
	}

	// Both consequences, read off the built record a week later.
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{
		Now:             func() time.Time { return reconciled.Add(7 * 24 * time.Hour) },
		FreshnessWindow: time.Hour,
	}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(snap.Targets))
	}
	rec := snap.Targets[fleet.TargetKey("prod/kubernetes/payments-prod")]
	if rec == nil {
		t.Fatalf("target key not found among %v", slices.Sorted(maps.Keys(snap.Targets)))
	}
	if !rec.Stale {
		t.Error("a target last reconciled a week ago is not stale under a one-hour window")
	}
	for _, l := range rec.Limitations {
		if l.Code == fleet.LimitationEvidenceMissing {
			t.Errorf("target reports coverage %+v yet claims no evidence: %s", rec.Coverage, l.Message)
		}
	}
}
