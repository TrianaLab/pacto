package delivery_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

var linkNow = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// view is a reconciled payments view named name in shop, running as
// Deployment/payments, with labels.
func view(name string, labels map[string]string) delivery.PactoView {
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name, p.Metadata.Labels = "shop", name, labels
	p.Spec.Target = &delivery.PactoTargetView{WorkloadRef: &delivery.WorkloadRefView{Kind: "Deployment", Name: "payments"}}
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", linkNow.Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: "payments", Version: "2.3.0",
		ResolvedRef: "oci://ghcr.io/acme/contracts/payments@sha256:" + strings.Repeat("c", 64)}
	return p
}

func digestLink() fleet.ObservedLink {
	since := linkNow.Add(-time.Hour)
	return fleet.ObservedLink{Signal: fleet.LinkSignalImageDigest, Value: "sha256:" + strings.Repeat("a", 64), Since: &since, OtherVersions: []string{"2.2.0", "2.1.0"}}
}

func invalid(col *fleet.Collection) int {
	n := 0
	for _, l := range col.Limitations {
		if l.Code == fleet.LimitationSourceRecordInvalid {
			n++
		}
	}
	return n
}

// TestLinkLabelsRoundTrip: the labels LinkLabels writes read back as the same
// link, for both signals, with and without the optional labels.
func TestLinkLabelsRoundTrip(t *testing.T) {
	for _, l := range []fleet.ObservedLink{digestLink(), {Signal: fleet.LinkSignalAnnotation, Value: "oci://ghcr.io/acme/contracts/payments:2.3.0"}} {
		col := delivery.LinkedCollection("linked", []delivery.PactoView{view("Deployment/payments", delivery.LinkLabels(l))}, linkNow)
		if len(col.Targets) != 1 || len(col.Limitations) != 0 || !reflect.DeepEqual(*col.Targets[0].Link, l) {
			t.Fatalf("%s: targets %+v limitations %+v", l.Signal, col.Targets, col.Limitations)
		}
		if col.Targets[0].Name != "Deployment/payments" || col.Targets[0].Workload.Name != "payments" {
			t.Fatalf("%s: target %+v", l.Signal, col.Targets[0])
		}
	}
}

// TestBadLinkLabelIsInvalidRecord: a linked target with an unknown signal, a
// bad time or no linked-by is dropped as an invalid record, never read as a
// Pacto target.
func TestBadLinkLabelIsInvalidRecord(t *testing.T) {
	for name, labels := range map[string]map[string]string{
		"unknown signal": {delivery.LinkedByLabel: "guess", delivery.LinkedValueLabel: "x"},
		"bad time":       {delivery.LinkedByLabel: fleet.LinkSignalImageDigest, delivery.LinkedSinceLabel: "yesterday"},
		"no linked-by":   {delivery.LinkedValueLabel: "x"},
	} {
		col := delivery.LinkedCollection("linked", []delivery.PactoView{view("Deployment/payments", labels)}, linkNow)
		if len(col.Targets) != 0 || invalid(col) != 1 || !strings.Contains(col.Limitations[0].Message, "shop/Deployment/payments") {
			t.Errorf("%s: targets %+v limitations %+v", name, col.Targets, col.Limitations)
		}
	}
}

// TestCRWithLinkLabelStaysPactoTarget: link labels on a Pacto-named view, or
// on any view a Pacto source reads, are ignored with SOURCE_RECORD_INVALID;
// the view stays a Pacto target of its workload.
func TestCRWithLinkLabelStaysPactoTarget(t *testing.T) {
	labels := map[string]string{delivery.LinkedByLabel: fleet.LinkSignalImageDigest, "team": "payments"}
	for name, col := range map[string]*fleet.Collection{
		"linked source, Pacto name": delivery.LinkedCollection("linked", []delivery.PactoView{view("payments", labels)}, linkNow),
		"Pacto source, linked name": delivery.PactoCollection("pactos", []delivery.PactoView{view("Deployment/x", labels)}, nil, linkNow),
	} {
		if len(col.Targets) != 1 || col.Targets[0].Link != nil || col.Targets[0].Workload == nil || invalid(col) != 1 {
			t.Errorf("%s: targets %+v limitations %+v", name, col.Targets, col.Limitations)
		}
	}
	plain := delivery.LinkedCollection("linked", []delivery.PactoView{view("payments", map[string]string{"team": "payments"})}, linkNow)
	if len(plain.Targets) != 1 || len(plain.Limitations) != 0 {
		t.Errorf("plain Pacto: %+v %+v", plain.Targets, plain.Limitations)
	}
}

func linkedTargets() delivery.LinkedTargets {
	return delivery.LinkedTargets{SchemaVersion: delivery.LinkedSchemaVersion, Cluster: "6f1e-uid", AsOf: linkNow.Format(time.RFC3339),
		Completeness: fleet.CompletenessComplete, Targets: []delivery.PactoView{view("Deployment/payments", delivery.LinkLabels(digestLink()))}}
}

func TestDecodeLinkedIsStrict(t *testing.T) {
	b, err := delivery.EncodeLinked(linkedTargets())
	if err != nil {
		t.Fatal(err)
	}
	got, err := delivery.DecodeLinked(b)
	if err != nil || !reflect.DeepEqual(got, linkedTargets()) {
		t.Fatalf("round trip %+v %v", got, err)
	}
	with := func(f func(*delivery.LinkedTargets)) []byte {
		l := linkedTargets()
		f(&l)
		b, _ := json.Marshal(l)
		return b
	}
	for name, b := range map[string][]byte{
		"unknown field": []byte(strings.Replace(string(b), `"cluster"`, `"extra":1,"cluster"`, 1)),
		"schema":        with(func(l *delivery.LinkedTargets) { l.SchemaVersion = "example/linked-targets/v1" }),
		"asOf":          with(func(l *delivery.LinkedTargets) { l.AsOf = "now" }),
		"completeness":  with(func(l *delivery.LinkedTargets) { l.Completeness = "maybe" }),
		"over bound":    make([]byte, delivery.MaxLinkedBytes+1),
	} {
		if _, err := delivery.DecodeLinked(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEncodeLinkedRefusesOverBound(t *testing.T) {
	l := linkedTargets()
	l.Targets[0].Metadata.Labels["big"] = strings.Repeat("x", delivery.MaxLinkedBytes)
	if _, err := delivery.EncodeLinked(l); err == nil {
		t.Fatal("encoded over the bound")
	}
}

// releasedPactoView is PactoView as the released (release B) readers define
// it: a linked target must decode into it with the strict decoder.
type releasedPactoView struct {
	Metadata struct {
		Namespace  string            `json:"namespace"`
		Name       string            `json:"name"`
		Generation int64             `json:"generation"`
		Labels     map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Target *delivery.PactoTargetView `json:"target"`
	} `json:"spec"`
	Status delivery.PactoStatusView `json:"status"`
}

// TestReleasedReaderReadsLinkedSnapshot: a target snapshot whose Pactos hold
// a linked target signs, opens and passes Check, and a released reader's
// strict PactoView decodes the linked view, link labels and all.
func TestReleasedReaderReadsLinkedSnapshot(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, err := signing.FromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	st := delivery.NewTargetSnapshotStatement(delivery.TargetSnapshot{Cluster: snapCluster, AsOf: linkNow.Format(time.RFC3339),
		Completeness: fleet.CompletenessComplete, Publisher: "pacto-system/pacto-operator", Pactos: linkedTargets().Targets})
	b, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), st)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := delivery.OpenTargetSnapshot(b, nil, signing.OpenKey)
	if err != nil || got.Predicate.Check() != nil {
		t.Fatalf("open %v", err)
	}
	raw, _ := json.Marshal(got.Predicate.Pactos)
	var released []releasedPactoView
	if err := strictjson.Unmarshal(raw, &released); err != nil || len(released) != 1 || released[0].Metadata.Labels[delivery.LinkedByLabel] != fleet.LinkSignalImageDigest {
		t.Fatalf("released reader %+v %v", released, err)
	}
}
