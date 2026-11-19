package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestPactoCollectionSetsTargetWorkload(t *testing.T) {
	cases := []struct {
		name   string
		target *PactoTargetView
		want   *fleet.WorkloadRef
	}{
		{"workloadRef", &PactoTargetView{ServiceName: "payments", WorkloadRef: &WorkloadRefView{Kind: "StatefulSet", Name: "ledger"}}, &fleet.WorkloadRef{Kind: "StatefulSet", Name: "ledger"}},
		{"workloadRef kind only", &PactoTargetView{ServiceName: "payments", WorkloadRef: &WorkloadRefView{Kind: "StatefulSet"}}, &fleet.WorkloadRef{Kind: "StatefulSet", Name: "payments"}},
		{"serviceName only", &PactoTargetView{ServiceName: "payments"}, &fleet.WorkloadRef{Kind: "Deployment", Name: "payments"}},
		{"no name", &PactoTargetView{}, nil},
		{"no spec.target", nil, nil},
	}
	for _, c := range cases {
		p := paymentsPacto(complete())
		p.Spec.Target = c.target
		col := PactoCollection("snap", []PactoView{p}, nil, now)
		if len(col.Targets) != 1 || !reflect.DeepEqual(col.Targets[0].Workload, c.want) {
			t.Errorf("%s: targets %+v, want workload %+v", c.name, col.Targets, c.want)
		}
	}
}

func TestPactoCollectionLimitationSource(t *testing.T) {
	var unreconciled PactoView
	unreconciled.Metadata.Namespace, unreconciled.Metadata.Name = "prod", "fresh"
	stale := paymentsPacto(&CallerCoverageView{State: "partial", RefreshSeconds: 300})
	col := PactoCollection("targets-c1", []PactoView{unreconciled, stale}, errors.New("boom"), now)
	codes := map[string]bool{}
	for _, l := range col.Limitations {
		codes[l.Code] = true
		if l.Source != "targets-c1" {
			t.Errorf("limitation %+v names another source", l)
		}
	}
	if !codes["PACTO_NOT_RECONCILED"] || !codes["PACTOS_UNREADABLE"] {
		t.Errorf("limitations %+v", col.Limitations)
	}
	if len(col.Targets) != 1 || len(col.Targets[0].Limitations) == 0 {
		t.Fatalf("targets %+v", col.Targets)
	}
	for _, l := range col.Targets[0].Limitations {
		if l.Source != "targets-c1" {
			t.Errorf("target limitation %+v names another source", l)
		}
	}
}

// TestDeliverySnapshotUnchangedByPactoCollection rebuilds the delivery
// snapshot the way Snapshot did before PactoCollection was split out and
// compares the two byte for byte.
func TestDeliverySnapshotUnchangedByPactoCollection(t *testing.T) {
	var unreconciled PactoView
	unreconciled.Metadata.Namespace, unreconciled.Metadata.Name = "prod", "fresh"
	pactos := []PactoView{paymentsPacto(complete(), legacy), externalsPacto(complete(), stripeEnv), unreconciled}
	from, to := minorSides()
	sides := []Revision{from, to, {}}
	listErr := errors.New("page 2 failed")

	got, err := Snapshot(context.Background(), pactos, listErr, sides, now)
	if err != nil {
		t.Fatal(err)
	}
	col := &fleet.Collection{Limitations: []fleet.Limitation{{Code: "PACTOS_UNREADABLE", Source: SnapshotSourceID, Message: "the Pacto objects could not be listed: " + listErr.Error()}}}
	for _, side := range sides {
		if side.Bundle == nil {
			continue
		}
		repo, digest, _ := strings.Cut(contract.TrimOCIScheme(side.ContractRef), "@")
		col.Revisions = append(col.Revisions, fleet.RawRevision{Bundle: side.Bundle, Domain: contract.OCIDomain(side.ContractRef),
			RequestedRef: side.ContractRef, ResolvedRef: repo + "@" + digest, Digest: digest})
	}
	for _, p := range pactos {
		c := p.Status.Contract
		if c == nil {
			col.Limitations = append(col.Limitations, fleet.Limitation{Code: "PACTO_NOT_RECONCILED", Source: SnapshotSourceID,
				Message: "Pacto " + p.Metadata.Namespace + "/" + p.Metadata.Name + " has no reconciled contract yet"})
			continue
		}
		rev, digest := revisionFromStatus(p)
		col.Revisions = append(col.Revisions, rev)
		reconciled := parseTime(p.Status.LastReconciledAt)
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: p.Metadata.Namespace, Kind: fleet.SourceKindKubernetes, Name: p.Metadata.Name,
			Service: c.ServiceName, Domain: rev.Domain, ResolvedRef: c.ResolvedRef, Digest: digest,
			Compliance: p.Status.ContractStatus, EvidenceAt: reconciled, ReconciledAt: reconciled,
			Limitations: append(CallerLimitations(p, reconciled, now), ExternalLimitations(p, reconciled, now)...)})
		col.Observed = append(col.Observed, ObservedEdges(p)...)
		col.ExternalUses = append(col.ExternalUses, ExternalUses(p, reconciled)...)
	}
	want, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }}, fleet.NewMemorySource(SnapshotSourceID, fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("snapshot changed:\n got %s\nwant %s", g, w)
	}
}

func TestDecodePactosReadsSpecTarget(t *testing.T) {
	pactos, err := DecodePactos([]byte(`{"items":[{"metadata":{"namespace":"prod","name":"ledger"},
		"spec":{"target":{"serviceName":"ledger-svc","workloadRef":{"kind":"StatefulSet","name":"ledger"}}}},
		{"metadata":{"namespace":"prod","name":"bare"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := &PactoTargetView{ServiceName: "ledger-svc", WorkloadRef: &WorkloadRefView{Kind: "StatefulSet", Name: "ledger"}}
	if len(pactos) != 2 || !reflect.DeepEqual(pactos[0].Spec.Target, want) || pactos[1].Spec.Target != nil {
		t.Errorf("pactos %+v", pactos)
	}
}

// TestDeclaredTargetFromPactoCollectionHidesInferred: the workload a Pacto
// targets is the target, never also an inferred service.
func TestDeclaredTargetFromPactoCollectionHidesInferred(t *testing.T) {
	p := paymentsPacto(complete())
	p.Spec.Target = &PactoTargetView{ServiceName: "payments"}
	workloads := &fleet.Collection{InferredServices: []fleet.RawInferredService{
		{Namespace: "prod", Kind: "Deployment", Name: "payments"}, {Namespace: "prod", Kind: "Deployment", Name: "legacy"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("targets", fleet.SourceKindKubernetes, PactoCollection("targets", []PactoView{p}, nil, now)),
		fleet.NewMemorySource("targets-workloads", fleet.SourceKindKubernetesWorkloads, workloads))
	if err != nil {
		t.Fatal(err)
	}
	if s := snap.Services[fleet.NewServiceKey("k8s:prod/Deployment/payments")]; s != nil {
		t.Errorf("the targeted workload was inferred: %+v", s)
	}
	if s := snap.Services[fleet.NewServiceKey("k8s:prod/Deployment/legacy")]; s == nil || !s.Inferred() {
		t.Errorf("legacy is not inferred: %v", snap.Services)
	}
}
