/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package targetsnapshot

import (
	"context"
	"strings"
	"testing"
	"time"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// linkedView is a linked target of service, named <Kind>/<name>.
func linkedView(name string) core.PactoView {
	var v core.PactoView
	v.Metadata.Namespace, v.Metadata.Name = ns, name
	v.Metadata.Labels = core.LinkLabels(fleet.ObservedLink{Signal: fleet.LinkSignalImageDigest, Value: "sha256:ab"})
	v.Status.Contract = &core.ContractInfoView{ServiceName: "payments", Version: "2.3.0", ResolvedRef: "oci://reg.example/pactos/payments@sha256:" + strings.Repeat("b", 64)}
	return v
}

func publishLinked(t *testing.T, l core.LinkedTargets) core.TargetSnapshot {
	t.Helper()
	e := newTargetsEnv(t, fakeReader(snapshotObjects()...))
	e.p.Linked = func() core.LinkedTargets { return l }
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := e.pushed(t)
	return s
}

func TestSnapshotCarriesLinkedTargets(t *testing.T) {
	s := publishLinked(t, core.LinkedTargets{AsOf: "2026-11-17T09:59:00Z", Completeness: fleet.CompletenessComplete,
		Limitations: []fleet.Limitation{{Code: core.LimitationLinkDigestOff, Message: "annotation only"}},
		Targets:     []core.PactoView{linkedView("Deployment/payments")}})
	if len(s.Pactos) != 2 || s.Pactos[1].Metadata.Name != "Deployment/payments" ||
		s.Pactos[1].Metadata.Labels[core.LinkedByLabel] != fleet.LinkSignalImageDigest {
		t.Fatalf("pactos: %+v", s.Pactos)
	}
	if sec := s.Sources[0]; sec.Completeness != fleet.CompletenessComplete || len(sec.Limitations) != 0 {
		t.Fatalf("the informational digest-off limitation marked the section: %+v", sec)
	}
	col := core.LinkedCollection("snap", s.Pactos, time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC))
	if len(col.Targets) != 2 || col.Targets[1].Link == nil {
		t.Fatalf("a reader lost the link: %+v", col.Targets)
	}
}

func TestSnapshotLinkedPartialMakesSectionPartial(t *testing.T) {
	capped := fleet.Limitation{Code: "LINKED_TARGETS_CAPPED", Message: "kept 200 linked workloads"}
	for name, l := range map[string]core.LinkedTargets{
		"partial":   {AsOf: "2026-11-17T09:59:00Z", Completeness: fleet.CompletenessPartial, Limitations: []fleet.Limitation{capped}},
		"stale":     {AsOf: "2026-11-17T09:00:00Z", Completeness: fleet.CompletenessComplete},
		"never run": {},
	} {
		s := publishLinked(t, l)
		if sec := s.Sources[0]; sec.Completeness != fleet.CompletenessPartial || s.Completeness != fleet.CompletenessPartial {
			t.Errorf("%s: section %+v", name, sec)
		}
		if name == "partial" && (len(s.Sources[0].Limitations) != 1 || s.Sources[0].Limitations[0].Code != capped.Code) {
			t.Errorf("limitations: %+v", s.Sources[0].Limitations)
		}
	}
}

// A Pacto CR carrying the link labels reaches the snapshot without them: only
// the linker writes them.
func TestSnapshotStripsLinkLabelsFromCRs(t *testing.T) {
	objs := snapshotObjects()
	objs[0].SetLabels(map[string]string{core.LinkedByLabel: fleet.LinkSignalAnnotation, "team": "pay"})
	e := newTargetsEnv(t, fakeReader(objs...))
	if err := e.p.publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := e.pushed(t)
	if l := s.Pactos[0].Metadata.Labels; l[core.LinkedByLabel] != "" || l["team"] != "pay" {
		t.Fatalf("labels: %v", l)
	}
}
