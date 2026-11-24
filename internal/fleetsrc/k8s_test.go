package fleetsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/kubecluster"
	"github.com/trianalab/pacto/v4/pkg/readiness"
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

func (f *fakeK8sClient) PreferredVersions(context.Context, ...string) (map[string]string, error) {
	return nil, errors.New("the CRD source never discovers GitOps APIs")
}

func (f *fakeK8sClient) DiscoverCRD(context.Context) (*k8sclient.CRDDiscovery, error) {
	return f.disc, f.discErr
}

// ListPage reads as a cluster with no workloads.
func (f *fakeK8sClient) ListPage(context.Context, schema.GroupVersionResource, string, string) ([]byte, string, error) {
	return []byte(`{"items":[]}`), "", nil
}

func (f *fakeK8sClient) ListJSON(_ context.Context, crd k8sclient.CRDDiscovery, namespace string) ([]byte, error) {
	f.gotResource, f.gotNS = crd.ResourceName, namespace
	return f.listData, f.listErr
}
func (f *fakeK8sClient) ClusterIdentity(context.Context, string) (kubecluster.Identity, error) {
	return kubecluster.Identity{}, errors.New("the source never reads the identity")
}

func TestK8sSource_IDKind_Default(t *testing.T) {
	s := NewK8sSource("", nil, &fakeK8sClient{}, nil)
	if s.ID() != "k8s" {
		t.Errorf("default ID = %q, want k8s", s.ID())
	}
	if s.Kind() != "kubernetes" {
		t.Errorf("Kind = %q, want kubernetes", s.Kind())
	}
	if got := NewK8sSource("prod", nil, nil, nil).ID(); got != "prod" {
		t.Errorf("ID = %q, want prod", got)
	}
}

func TestK8sSource_Collect_DiscoverError(t *testing.T) {
	s := NewK8sSource("k8s", nil, &fakeK8sClient{discErr: errors.New("unreachable")}, nil)
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestK8sSourceCRDMissingSaysSo(t *testing.T) {
	cluster := &fleet.Cluster{ID: "c1"}
	s := NewK8sSource("k8s", cluster, &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: false}}, nil)
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(col.Targets) != 0 || col.Cluster != cluster {
		t.Errorf("collection = %+v, want no target in the cluster", col)
	}
	if len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationPactoCRDNotInstalled || col.Limitations[0].Source != "k8s" {
		t.Fatalf("limitations = %+v, want PACTO_CRD_NOT_INSTALLED", col.Limitations)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, s)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Sources[0].Status != fleet.SourcePartial {
		t.Errorf("state = %+v, want partial", snap.Sources[0])
	}
}

func TestK8sTargetWorkloadRef(t *testing.T) {
	list := `{"items":[
		{"metadata":{"name":"a","namespace":"shop"},"spec":{"target":{"serviceName":"cart-svc","workloadRef":{"kind":"StatefulSet","name":"cart"}}},"status":{"contract":{"serviceName":"a"}}},
		{"metadata":{"name":"b","namespace":"shop"},"spec":{"target":{"serviceName":"orders"}},"status":{"contract":{"serviceName":"b"}}},
		{"metadata":{"name":"c","namespace":"shop"},"spec":{"target":{"serviceName":"svc","workloadRef":{"name":"worker"}}},"status":{"contract":{"serviceName":"c"}}},
		{"metadata":{"name":"d","namespace":"shop"},"spec":{"target":{}},"status":{"contract":{"serviceName":"d"}}},
		{"metadata":{"name":"e","namespace":"shop"},"status":{"contract":{"serviceName":"e"}}}]}`
	col, err := NewK8sSource("k8s", nil, &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"}, listData: []byte(list)}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]*fleet.WorkloadRef{
		"a": {Kind: "StatefulSet", Name: "cart"},
		"b": {Kind: "Deployment", Name: "orders"},
		"c": {Kind: "Deployment", Name: "worker"},
		"d": nil,
		"e": nil,
	}
	for _, tg := range col.Targets {
		if !reflect.DeepEqual(tg.Workload, want[tg.Name]) {
			t.Errorf("target %s workload = %+v, want %+v", tg.Name, tg.Workload, want[tg.Name])
		}
	}
}

func TestK8sTargetCarriesWorkloadAndCreatedAt(t *testing.T) {
	list := `{"items":[
		{"metadata":{"name":"a","namespace":"shop","creationTimestamp":"2026-07-29T11:00:00Z"},"spec":{"target":{"serviceName":"cart"}},"status":{"contract":{"serviceName":"a"}}},
		{"metadata":{"name":"b","namespace":"shop"},"status":{"contract":{"serviceName":"b"}}}]}`
	col, err := NewK8sSource("k8s", nil, &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"}, listData: []byte(list)}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)
	for _, tg := range col.Targets {
		switch tg.Name {
		case "a":
			if !reflect.DeepEqual(tg.Workload, &fleet.WorkloadRef{Kind: "Deployment", Name: "cart"}) || tg.CreatedAt == nil || !tg.CreatedAt.Equal(created) {
				t.Errorf("target a = %+v created %v", tg.Workload, tg.CreatedAt)
			}
		default:
			if tg.CreatedAt != nil {
				t.Errorf("target %s created %v, want unknown", tg.Name, tg.CreatedAt)
			}
		}
	}
	w := `{"metadata":{"name":"cart","namespace":"shop","creationTimestamp":"2026-07-26T12:00:00Z"}}`
	wcol, err := NewWorkloadSource("", nil, &workloadFake{pages: map[string][2]string{"deployments||": {w}}}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if at := wcol.InferredServices[0].CreatedAt; at == nil || !at.Equal(time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("workload created %v", at)
	}
}

func TestK8sSource_Collect_ListError(t *testing.T) {
	s := NewK8sSource("k8s", nil, &fakeK8sClient{
		disc:    &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listErr: errors.New("list failed"),
	}, nil)
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestK8sSource_Collect_InvalidJSON(t *testing.T) {
	s := NewK8sSource("k8s", nil, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte("{not json"),
	}, nil)
	if _, err := s.Collect(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestK8sSource_Collect_FullMapping(t *testing.T) {
	list := `{"items":[
	  {
	    "metadata":{"name":"payments-prod","namespace":"prod","labels":{"app":"payments","delivery.pacto.trianalab.io/environment":"production"}},
	    "status":{
	      "contractStatus":"NonCompliant",
	      "contract":{"serviceName":"payments","domain":"payments","offering":"postgresql","summary":"Primary database","resolvedRef":"ghcr.io/x/payments:1.2.0@sha256:abcd",
	        "owner":{"team":"payments","dri":"alice","contacts":[{"type":"email","value":"pay@x","purpose":"escalation"}]},"ownerDisplay":"payments"},
	      "evaluationCoverage":{"evaluated":3,"required":5},
	      "observedRuntime":{"workloadKind":"Deployment","hasPVC":false},
	      "lastReconciledAt":"2026-07-29T10:00:00Z",
	      "findings":[
	        {"code":"WORKLOAD_MISMATCH","severity":"error","category":"RuntimeDrift","subject":"payments","contractPath":"spec.workload","message":"drift",
	         "evidenceRefs":[{"source":"k8s","observedAt":"2026-07-29T09:59:00Z","collector":"k8s-observer+ebpf","detectedAt":"2026-07-29T09:58:30Z"}]}
	      ]
	    }
	  }
	]}`
	s := NewK8sSource("prod-cluster", nil, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, nil)
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
	// The status has no statusFormat, so it cannot say which externals payments uses.
	if len(tg.Limitations) != 1 || tg.Limitations[0].Code != fleet.LimitationExternalsUnreported {
		t.Fatalf("limitations = %+v", tg.Limitations)
	}
	tg.Limitations = nil
	want := fleet.RawTarget{
		Scope: "prod",
		// Domain is derived from the resolved ref's registry+org, correlating this
		// runtime target with the same service's OCI baseline.
		Domain:          "ghcr.io/x",
		Kind:            "kubernetes",
		Name:            "payments-prod",
		Labels:          map[string]string{fleet.TargetEnvironmentLabel: "production"},
		Service:         "payments",
		ResolvedRef:     "ghcr.io/x/payments:1.2.0@sha256:abcd",
		Digest:          "sha256:abcd",
		Compliance:      "NonCompliant",
		Owner:           contract.Owner{Team: "payments", DRI: "alice", Contacts: []contract.OwnerContact{{Type: "email", Value: "pay@x", Purpose: "escalation"}}},
		BusinessDomain:  "payments",
		Offering:        "postgresql",
		Summary:         "Primary database",
		Coverage:        &fleet.Coverage{Evaluated: 3, Required: 5},
		ObservedRuntime: map[string]any{"workloadKind": "Deployment", "hasPVC": false},
		Findings: []finding.Finding{{
			Code:         "WORKLOAD_MISMATCH",
			Severity:     finding.SeverityError,
			Category:     "RuntimeDrift",
			Subject:      finding.SubjectRef{Name: "payments"},
			ContractPath: "spec.workload",
			Message:      "drift",
			EvidenceRefs: []finding.EvidenceRef{{Source: "k8s", ObservedAt: "2026-07-29T09:59:00Z", Collector: "k8s-observer+ebpf", DetectedAt: "2026-07-29T09:58:30Z"}},
		}},
	}
	if !reflect.DeepEqual(tg, want) {
		t.Errorf("target mismapped:\n got %+v\nwant %+v", tg, want)
	}
}

// The operator derives the readiness assessment in-cluster and publishes it as
// status.readiness. The decoder here did not read it, so TargetRecord.Readiness
// was permanently nil and a Kubernetes target of a contract that DOES declare
// readiness rendered identically to one that declares none.
func TestK8sSource_Collect_Readiness(t *testing.T) {
	list := `{"items":[{"metadata":{"name":"payments-prod","namespace":"prod"},"status":{
	  "readiness":{
	    "score":90,"minScore":80,"passing":true,"totalWeight":40,"earnedWeight":36,
	    "expires":"2027-01-31","expired":false,"daysRemaining":120,
	    "doneCount":3,"partialCount":1,"notDoneCount":1,"deferredCount":2,
	    "claims":[{"id":"runbook","type":"url","category":"operations","status":"done",
	      "evidence":"https://runbooks/payments","description":"on-call runbook",
	      "weight":10,"earnedWeight":10,"excluded":false}]
	  }
	}}]}`
	s := NewK8sSource("prod-cluster", nil, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, nil)
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	days := 120
	want := &readiness.Result{
		Score: 90, MinScore: 80, Passing: true, TotalWeight: 40, EarnedWeight: 36,
		Expires: "2027-01-31", DaysRemaining: &days,
		DoneCount: 3, PartialCount: 1, NotDoneCount: 1, DeferredCount: 2,
		Checks: []readiness.CheckResult{{
			ID: "runbook", Type: "url", Category: "operations", Status: "done",
			Evidence: "https://runbooks/payments", Description: "on-call runbook",
			Weight: 10, EarnedWeight: 10,
		}},
	}
	if !reflect.DeepEqual(col.Targets[0].Readiness, want) {
		t.Errorf("readiness mismapped:\n got %+v\nwant %+v", col.Targets[0].Readiness, want)
	}
}

func TestK8sSource_Collect_MinimalItem(t *testing.T) {
	// No contract block, no coverage, no findings, no timestamp: service falls
	// back to the CR name and optional fields stay zero.
	list := `{"items":[{"metadata":{"name":"orphan","namespace":"default"},"status":{"contractStatus":"Unknown"}}]}`
	s := NewK8sSource("k8s", nil, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, nil)
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tg := col.Targets[0]
	if tg.Service != "orphan" {
		t.Errorf("service = %q, want orphan (CR name fallback)", tg.Service)
	}
	if tg.ResolvedRef != "" || tg.Digest != "" || tg.Coverage != nil || tg.ReconciledAt != nil || tg.Findings != nil || tg.Offering != "" || tg.Summary != "" {
		t.Errorf("expected zero optional fields, got %+v", tg)
	}
}

func TestContractRefs_NoCRD(t *testing.T) {
	// An absent CRD is a readable cluster that declares nothing, not a failure.
	refs, err := ContractRefs(context.Background(), &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: false}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want none", refs)
	}
}

func TestContractRefs_DiscoverError(t *testing.T) {
	_, err := ContractRefs(context.Background(), &fakeK8sClient{discErr: errors.New("unreachable")}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestContractRefs_NoCRs(t *testing.T) {
	fake := &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(`{"items":[]}`),
	}
	refs, err := ContractRefs(context.Background(), fake, []string{"team-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want none", refs)
	}
	if fake.gotNS != "team-a" {
		t.Errorf("namespace = %q, want team-a", fake.gotNS)
	}
}

// TestContractRefs_RefsAndRepos pins what the fleet gets to see: every running
// ref plus the repository behind it, so a pinned digest still resolves to the
// published baseline it was pinned from. Two CRs sharing a repository contribute
// it once, and a CR the operator has not resolved yet contributes nothing.
func TestContractRefs_RefsAndRepos(t *testing.T) {
	list := `{"items":[
	  {"metadata":{"name":"tagged"},"status":{"contract":{"serviceName":"svc","resolvedRef":"ghcr.io/x/payments:1.2.0"}}},
	  {"metadata":{"name":"pinned"},"status":{"contract":{"serviceName":"svc","resolvedRef":"ghcr.io/x/payments:1.3.0@sha256:abcd"}}},
	  {"metadata":{"name":"bare"},"status":{"contract":{"serviceName":"svc","resolvedRef":"ghcr.io/x/orders"}}},
	  {"metadata":{"name":"ported"},"status":{"contract":{"serviceName":"svc","resolvedRef":"localhost:5000/dev/svc:2.0.0"}}},
	  {"metadata":{"name":"unresolved"},"status":{"contract":{"serviceName":"svc","resolvedRef":""}}},
	  {"metadata":{"name":"nocontract"},"status":{"contractStatus":"Unknown"}}
	]}`
	refs, err := ContractRefs(context.Background(), &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"ghcr.io/x/orders",                     // bare repo: already a repository
		"ghcr.io/x/payments",                   // shared by the tagged and the pinned CR, once
		"ghcr.io/x/payments:1.2.0",             //
		"ghcr.io/x/payments:1.3.0@sha256:abcd", //
		"localhost:5000/dev/svc",               // the registry port is not a tag
		"localhost:5000/dev/svc:2.0.0",         //
	}
	if !slices.Equal(refs, want) {
		t.Errorf("refs =\n %v\nwant\n %v", refs, want)
	}
}

func TestRepoFromRef(t *testing.T) {
	// Parity with the dashboard helper this replaced: digest first, then a tag
	// only when the ':' is past the last '/'.
	for ref, want := range map[string]string{
		"ghcr.io/x/payments:1.2.0@sha256:abcd": "ghcr.io/x/payments",
		"ghcr.io/x/payments@sha256:abcd":       "ghcr.io/x/payments",
		"ghcr.io/x/payments:1.2.0":             "ghcr.io/x/payments",
		"ghcr.io/x/payments":                   "ghcr.io/x/payments",
		"localhost:5000/dev/svc":               "localhost:5000/dev/svc",
		"@sha256:abcd":                         "@sha256", // a leading '@' is not a digest separator
		"":                                     "",
	} {
		if got := repoFromRef(ref); got != want {
			t.Errorf("repoFromRef(%q) = %q, want %q", ref, got, want)
		}
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
	src := NewK8sSource("prod-cluster", nil, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, nil)
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

func TestK8sSource_EnvironmentLabelOnly(t *testing.T) {
	if got := environmentLabel(map[string]string{"app": "x"}); got != nil {
		t.Errorf("a CR without the environment label yields labels %v, want nil", got)
	}
	if got := environmentLabel(nil); got != nil {
		t.Errorf("a CR with no labels yields %v", got)
	}
}

// status.contract.domain is omitted when empty, so its absence means "declares
// none" only on a status format that records the domain at all. An older
// operator's status, or one with no contract, leaves the domain unknown.
func TestK8sSource_BusinessDomainKnownOnlyFromDomainFormat(t *testing.T) {
	item := func(format int, contract string) string {
		return fmt.Sprintf(`{"metadata":{"name":"x","namespace":"ns"},"status":{"statusFormat":%d%s}}`, format, contract)
	}
	cases := []struct {
		name, item string
		known      bool
	}{
		{"older operator", item(delivery.StatusFormatGuarantees, `,"contract":{"serviceName":"x"}`), false},
		{"domain format, none declared", item(delivery.StatusFormatDomain, `,"contract":{"serviceName":"x"}`), true},
		{"domain format, no contract", item(delivery.StatusFormatDomain, ""), false},
	}
	for _, c := range cases {
		s := NewK8sSource("c", nil, &fakeK8sClient{
			disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
			listData: []byte(`{"items":[` + c.item + `]}`),
		}, nil)
		col, err := s.Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got := col.Targets[0].BusinessDomainKnown; got != c.known {
			t.Errorf("%s: BusinessDomainKnown = %v, want %v", c.name, got, c.known)
		}
	}
}

// collectList runs Collect over a fake list.
func collectList(t *testing.T, list string) *fleet.Collection {
	t.Helper()
	col, err := NewK8sSource("prod-cluster", nil, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return col
}

func TestK8sSourceCollectsObservedEdges(t *testing.T) {
	col := collectList(t, `{"items":[
	  {"metadata":{"name":"payments","namespace":"prod"},"status":{
	    "contract":{"serviceName":"payments"},
	    "findings":[{"code":"DEPENDENCY_UNDECLARED","subject":"dependency/ledger","evidenceRefs":[{"collector":"ebpf"}]}],
	    "observedCallers":[{"namespace":"billing","kind":"Deployment","name":"legacy"}]}},
	  {"metadata":{"name":"orphan","namespace":"prod"},"status":{"observedCallers":[{"namespace":"x","kind":"Deployment","name":"y"}]}}
	]}`)
	want := []fleet.ObservedEdge{{From: "payments", To: "ledger", Count: 1}, {From: "k8s:billing/Deployment/legacy", To: "payments", Count: 1}}
	if !reflect.DeepEqual(col.Observed, want) {
		t.Fatalf("observed = %+v, want %+v", col.Observed, want)
	}
	if !col.ObservedUndeclaredOnly {
		t.Fatal("the Kubernetes source reports only undeclared calls")
	}
	if len(col.Targets) != 2 || len(col.Limitations) != 0 {
		t.Fatalf("targets = %d, limitations = %+v", len(col.Targets), col.Limitations)
	}
}

func TestK8sSourceCallerCoverageLimitation(t *testing.T) {
	fresh := time.Now().UTC().Format(time.RFC3339)
	item := func(name, state string) string {
		return `{"metadata":{"name":"` + name + `","namespace":"prod"},"status":{"lastReconciledAt":"` + fresh + `",
		  "statusFormat":3,"externalsCoverage":{"state":"complete","refreshSeconds":30},
		  "contract":{"serviceName":"` + name + `"},
		  "observedCallersCoverage":{"state":"` + state + `","reason":"stale-node","refreshSeconds":30}}}`
	}
	col := collectList(t, `{"items":[`+item("partial", "partial")+`,`+item("complete", "complete")+`]}`)
	lims := col.Targets[0].Limitations
	if len(lims) != 1 || lims[0].Code != delivery.LimitationCallersUnseen || lims[0].Source != "prod-cluster" {
		t.Fatalf("partial coverage limitations = %+v", lims)
	}
	if col.Targets[1].Limitations != nil {
		t.Fatalf("complete coverage limitations = %+v", col.Targets[1].Limitations)
	}
}

func TestK8sSourceObservedDecodeIsLenient(t *testing.T) {
	col := collectList(t, `{"items":[
	  {"metadata":{"name":"broken","namespace":"prod"},"status":{"contract":{"serviceName":"broken"},"observedCallers":"oops"}},
	  {"metadata":{"name":"payments","namespace":"prod"},"status":{"contract":{"serviceName":"payments"},
	    "observedCallers":[{"namespace":"billing","kind":"Deployment","name":"legacy"}]}}
	]}`)
	if len(col.Targets) != 2 || col.Targets[0].Service != "broken" {
		t.Fatalf("targets = %+v", col.Targets)
	}
	if len(col.Observed) != 1 || col.Observed[0].To != "payments" {
		t.Fatalf("observed = %+v", col.Observed)
	}
	if len(col.Limitations) != 1 || col.Limitations[0].Code != LimitationPactoStatusUnreadable || !strings.Contains(col.Limitations[0].Message, "prod/broken") ||
		!strings.Contains(col.Limitations[0].Message, "external systems") {
		t.Fatalf("limitations = %+v", col.Limitations)
	}
	// The unreadable target says its callers and externals cannot be ruled out,
	// so an external answer turns partial; the readable one says nothing.
	var codes []string
	for _, l := range col.Targets[0].Limitations {
		codes = append(codes, l.Code)
	}
	if !slices.Equal(codes, []string{delivery.LimitationCallersUnseen, fleet.LimitationExternalsUnreported}) {
		t.Fatalf("broken target limitations = %+v", col.Targets[0].Limitations)
	}
}

// externalsItem is a reconciled Pacto at status format 3 in namespace ns,
// reporting externals (a JSON array) with a fresh complete coverage.
func externalsItem(service, ns, externals string) string {
	fresh := time.Now().UTC().Format(time.RFC3339)
	return `{"metadata":{"name":"` + service + `","namespace":"` + ns + `"},"status":{"statusFormat":3,"lastReconciledAt":"` + fresh + `",
	  "contract":{"serviceName":"` + service + `","resolvedRef":"oci://reg.example/pactos/` + service + `@sha256:` + strings.Repeat("a", 64) + `"},
	  "externalCatalog":"` + externalcatalog.DefaultDigest() + ` (embedded)",
	  "externalsCoverage":{"state":"complete","refreshSeconds":30},
	  "externals":` + externals + `}}`
}

const stripeExternal = `[{"identity":"stripe","class":"vendor-api","confidence":"likely","kind":"env",
  "where":"deployment/payments env STRIPE_API_BASE","host":"api.stripe.com","entry":"stripe"}]`

func TestK8sSourceCollectsExternalUses(t *testing.T) {
	col := collectList(t, `{"items":[`+externalsItem("payments", "prod", stripeExternal)+`,
	  {"metadata":{"name":"old","namespace":"prod"},"status":{"contract":{"serviceName":"old"}}}]}`)
	if len(col.ExternalUses) != 1 {
		t.Fatalf("uses = %+v", col.ExternalUses)
	}
	u := col.ExternalUses[0]
	if u.Service != "payments" || u.Namespace != "prod" || u.Domain != "reg.example/pactos" || u.Identity != "stripe" ||
		u.Kind != "env" || u.Host != "api.stripe.com" || u.Catalog != externalcatalog.DefaultDigest() || u.ObservedAt == nil {
		t.Fatalf("use = %+v", u)
	}
	if col.Targets[0].Limitations != nil {
		t.Fatalf("a complete report adds no limitation: %+v", col.Targets[0].Limitations)
	}
	lims := col.Targets[1].Limitations
	if len(lims) != 1 || lims[0].Code != fleet.LimitationExternalsUnreported || lims[0].Source != "prod-cluster" || !strings.Contains(lims[0].Message, "status-format") {
		t.Fatalf("an older operator's status = %+v", lims)
	}
}

// buildK8s builds a snapshot from one Kubernetes source over list.
func buildK8s(t *testing.T, list string) *fleet.FleetSnapshot {
	t.Helper()
	src := NewK8sSource("prod-cluster", nil, &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"}, listData: []byte(list)}, nil)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestK8sExternalUsesRestrictedByNamespace(t *testing.T) {
	vault := strings.ReplaceAll(stripeExternal, "deployment/payments", "deployment/vault")
	snap := buildK8s(t, `{"items":[`+externalsItem("payments", "prod", stripeExternal)+`,`+externalsItem("vault", "secret", vault)+`]}`)
	if v, err := fleet.NewQuery(snap).GetExternal("stripe"); err != nil || len(v.External.Consumers) != 2 {
		t.Fatalf("unrestricted stripe = %+v, %v", v, err)
	}
	r := fleet.NewQuery(snap).Restrict(func(_, ns string) bool { return ns == "prod" }, nil)
	v, err := r.GetExternal("stripe")
	if err != nil || len(v.External.Consumers) != 1 || v.External.HiddenConsumers != 1 {
		t.Fatalf("restricted stripe = %+v, %v", v, err)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "vault") {
		t.Fatalf("the restricted answer names the hidden caller: %s", b)
	}
}

// TestSnapshotJSONCarriesNoCredential: a status built from what the operator
// reports for a credential URL, a JWT and a dotted password (plus fields no
// operator writes, carrying the raw values) yields a snapshot holding the
// normalized host only.
func TestSnapshotJSONCarriesNoCredential(t *testing.T) {
	const credURL = "postgres://user:pass@db.xyz.rds.amazonaws.com/app?password=x"
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJl"
	cat := externalcatalog.Default()
	if len(cat.Classify(jwt, "API_TOKEN")) != 0 || len(cat.Classify("s3cr3t.v4lue", "auth.password")) != 0 {
		t.Fatal("a value under a secret-like key is never classified")
	}
	var entries []map[string]string
	for kind, where := range map[string]string{"env": "deployment/payments env DATABASE_URL", "configMap": "configmap/settings DB_URL"} {
		for _, m := range cat.Classify(credURL, "DATABASE_URL") {
			entries = append(entries, map[string]string{"identity": string(m.Identity), "class": m.Class, "confidence": m.Confidence,
				"kind": kind, "where": where, "host": m.Host, "entry": m.Entry, "value": credURL, "token": jwt, "password": "s3cr3t.v4lue"})
		}
	}
	ext, _ := json.Marshal(entries)
	snap := buildK8s(t, `{"items":[`+externalsItem("payments", "prod", string(ext))+`]}`)
	b, _ := json.Marshal(snap)
	if !strings.Contains(string(b), "db.xyz.rds.amazonaws.com") {
		t.Fatalf("the snapshot lost the host: %s", b)
	}
	for _, bad := range []string{"user:", "pass@", "/app", "password=", "?password", jwt, "s3cr3t"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("snapshot carries %q", bad)
		}
	}
}

// A reconciled contract with no service name cannot come from the operator, which
// refuses one; the source reports the record instead of guessing the CR name.
// The item becomes no target, so Restrict never learns its namespace: the
// message must name neither the namespace nor the Pacto.
func TestK8sSource_Collect_NamelessContractIsARecordGap(t *testing.T) {
	list := `{"items":[
	  {"metadata":{"name":"nameless","namespace":"prod"},"status":{"contract":{"serviceName":"","resolvedRef":"ghcr.io/x/a:1.0.0"}}},
	  {"metadata":{"name":"payments","namespace":"prod"},"status":{"contract":{"serviceName":"payments"}}}
	]}`
	col, err := NewK8sSource("prod", nil, &fakeK8sClient{
		disc:     &k8sclient.CRDDiscovery{Found: true, ResourceName: "pactos"},
		listData: []byte(list),
	}, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Targets) != 1 || col.Targets[0].Service != "payments" {
		t.Fatalf("targets = %+v", col.Targets)
	}
	if len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationSourceRecordInvalid ||
		strings.Contains(col.Limitations[0].Message, "prod") || strings.Contains(col.Limitations[0].Message, "nameless") ||
		!strings.Contains(col.Limitations[0].Message, "1 Pacto(s)") {
		t.Fatalf("limitations = %+v", col.Limitations)
	}
}

// providerRow is the row of id the snapshot holds, nil when none.
func providerRow(snap *fleet.FleetSnapshot, id string) *fleet.Provider {
	if i := slices.IndexFunc(snap.Providers, func(p fleet.Provider) bool { return p.ID == id }); i >= 0 {
		return &snap.Providers[i]
	}
	return nil
}

func hasCode(lims []fleet.Limitation, code string) bool {
	return slices.ContainsFunc(lims, func(l fleet.Limitation) bool { return l.Code == code })
}

func TestK8sCRDRowServed(t *testing.T) {
	src := NewK8sSource("prod", &fleet.Cluster{ID: "c1", Name: "eu-prod"}, &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: true, Version: "v1", ResourceName: "pactos"}, listData: []byte(pactoIn("shop", "cart"))}, nil)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	row := providerRow(snap, fleet.ProviderPactoCRD)
	if row == nil {
		t.Fatalf("providers = %+v, want the Pacto CRD row", snap.Providers)
	}
	// The evidence carries no count: the count is the source's targetCount,
	// which a restricted view recounts (one target read, no "1" anywhere).
	if row.Evidence != "served (pacto.trianalab.io/v1)" || row.Name != "Pacto CRD" {
		t.Errorf("row = %+v", row)
	}
	if !slices.Equal(row.UsedFor, []string{fleet.UseDeclaredTargets}) || row.Status != fleet.SourceAvailable || row.Source != "prod" || row.Cluster != "eu-prod" {
		t.Errorf("row = %+v", row)
	}
	if !slices.Equal(row.Objects, []fleet.ProviderObject{{Kind: "CustomResourceDefinition", Name: "pactos.pacto.trianalab.io"}}) || len(row.Limitations) != 0 {
		t.Errorf("objects %+v limitations %+v", row.Objects, row.Limitations)
	}
}

func TestK8sCRDRowNotInstalled(t *testing.T) {
	src := NewK8sSource("prod", nil, &fakeK8sClient{disc: &k8sclient.CRDDiscovery{Found: false}}, nil)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	row := providerRow(snap, fleet.ProviderPactoCRD)
	if row == nil || row.Evidence != "not installed" || row.Status != fleet.SourcePartial || !hasCode(row.Limitations, fleet.LimitationPactoCRDNotInstalled) || len(row.Objects) != 0 {
		t.Fatalf("row = %+v", row)
	}
}

func TestK8sDiscoveryErrorIsNotNotInstalled(t *testing.T) {
	src := NewK8sSource("prod", nil, &fakeK8sClient{discErr: errors.New("dial tcp: connection refused")}, nil)
	if _, err := src.Collect(context.Background()); err == nil {
		t.Fatal("an unreachable cluster collected without an error")
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 0 || snap.Sources[0].Status != fleet.SourceUnavailable {
		t.Fatalf("providers %+v sources %+v; want no row and the source unavailable", snap.Providers, snap.Sources)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "not installed") {
		t.Error("an unreachable cluster reads as not installed")
	}
}

func TestK8sCRDRowNamespaceList(t *testing.T) {
	c := &nsFakeClient{found: true, errs: map[string]error{"ops": errors.New("boom")}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, NewK8sSource("c1", nil, c, []string{"shop", "ops"}))
	if err != nil {
		t.Fatal(err)
	}
	row := providerRow(snap, fleet.ProviderPactoCRD)
	if row == nil || row.Status != fleet.SourcePartial || !hasCode(row.Limitations, fleet.LimitationProviderSourceDegraded) || !hasCode(row.Limitations, fleet.LimitationK8sNamespaceUnreadable) {
		t.Fatalf("row = %+v; want partial through its source, with the unreadable namespace", row)
	}
	if hasCode(row.Limitations, fleet.LimitationK8sNamespaceScoped) {
		t.Errorf("row repeats the scope limitation its source states: %+v", row.Limitations)
	}
}
