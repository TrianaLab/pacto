package pactify

import (
	"context"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

const ledgerRef = "oci://reg.example/finance/ledger"

// ledgerContract is ledger at version with the given workload: 3.0.0 runs as
// a job, a breaking change beside the version.
func ledgerContract(version, workload string) string {
	return `pactoVersion: "2.1"
service: {name: ledger, version: ` + version + `}
workload: ` + workload + `
publish:
  bundle: ` + ledgerRef + `
  artifacts:
    - {name: api, type: image, ref: oci://reg.example/finance/ledger-api, build: {dockerfile: {context: .}}}
`
}

// proposedSnapshot plans invoices with ledger proposed from 2.3.1 and 2.4.0
// and builds a fleet holding that draft and ledger 2.3.1, 2.4.0 and 3.0.0.
func proposedSnapshot(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	inferred := Inferred{Name: "ledger", Ref: ledgerRef, Versions: []string{"2.3.1", "2.4.0"}, Where: "calls seen to the Service finance/ledger"}
	r, err := Init(context.Background(), Options{Dir: fixtureDir(t, "invoices"), DescribeOnly: true, Plan: true, Inferred: []Inferred{inferred}}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	text := file(t, r, "pacto.yaml").Content
	if !strings.Contains(text, "compatibility: unknown   # proposed ^2.3.0") {
		t.Fatalf("pacto.yaml:\n%s", text)
	}
	if _, err := contract.Parse(strings.NewReader(text)); err != nil {
		t.Fatalf("the written contract does not parse: %v\n%s", err, text)
	}
	drafts := writeTree(t, t.TempDir(), map[string]string{"invoices/pacto.yaml": text})
	providers := writeTree(t, t.TempDir(), map[string]string{
		"ledger-2.3.1/pacto.yaml": ledgerContract("2.3.1", "service"),
		"ledger-2.4.0/pacto.yaml": ledgerContract("2.4.0", "service"),
		"ledger-3.0.0/pacto.yaml": ledgerContract("3.0.0", "job"),
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleetsrc.NewLocalSource("drafts", drafts), fleetsrc.NewLocalSource("providers", providers))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestProposedRangeReadsUnknownInFleet: the fleet reads a proposal pactify
// wrote as a range that is not declared, never as satisfied.
func TestProposedRangeReadsUnknownInFleet(t *testing.T) {
	snap := proposedSnapshot(t)
	i := slices.IndexFunc(snap.Relationships, func(rel fleet.Relationship) bool {
		return strings.HasSuffix(string(rel.FromService), "invoices") && strings.HasSuffix(string(rel.ToService), "ledger")
	})
	if i < 0 {
		t.Fatalf("no invoices -> ledger relationship: %+v", snap.Relationships)
	}
	if rel := snap.Relationships[i]; rel.Compatibility != contract.CompatibilityUnknown || rel.RangeStatus != fleet.RangeUnknown {
		t.Fatalf("invoices -> ledger: compatibility %q, rangeStatus %q", rel.Compatibility, rel.RangeStatus)
	}
	q := fleet.NewQuery(snap)
	svc := snap.Service("invoices")
	if svc == nil {
		t.Fatal("invoices is not in the fleet")
	}
	d, err := q.EntityDetail(fleet.KindService, string(svc.Key))
	if err != nil {
		t.Fatal(err)
	}
	var claims int
	for _, e := range d.Service.Relationships.Items {
		if e.Relation != "dependency" || !strings.HasSuffix(e.To.Key, "ledger") {
			continue
		}
		for _, c := range e.DeclaredClaims.Items {
			claims++
			if c.Compatibility != contract.CompatibilityUnknown || c.RangeStatus != fleet.RangeUnknown {
				t.Fatalf("service detail claim: %+v", c)
			}
		}
	}
	if claims == 0 {
		t.Fatalf("service detail lists no ledger dependency claim: %+v", d.Service.Relationships.Items)
	}
}

// TestProposedRangeNeverClearsImpactGate: a BREAKING change to ledger leaves
// the gate unverified for invoices, whose range is only proposed, and no
// change ever reads invoices as compatible.
func TestProposedRangeNeverClearsImpactGate(t *testing.T) {
	snap := proposedSnapshot(t)
	parse := func(text string) (*contract.Contract, fs.FS) {
		c, err := contract.Parse(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return c, fstest.MapFS{"pacto.yaml": {Data: []byte(text)}}
	}
	analyze := func(from, to string) (*impact.Result, impact.AffectedConsumer) {
		old, oldFS := parse(ledgerContract("2.3.1", "service"))
		workload := "service"
		if to == "3.0.0" {
			workload = "job"
		}
		next, newFS := parse(ledgerContract(to, workload))
		res := impact.Analyze(context.Background(), old, next, oldFS, newFS, snap, impact.Options{})
		i := slices.IndexFunc(res.Consumers, func(c impact.AffectedConsumer) bool { return c.Service == "invoices" })
		if i < 0 {
			t.Fatalf("%s -> %s: invoices is no consumer: %+v", from, to, res.Consumers)
		}
		return res, res.Consumers[i]
	}

	res, c := analyze("2.3.1", "3.0.0")
	if res.Classification != "BREAKING" {
		t.Fatalf("2.3.1 -> 3.0.0 classification %q, want BREAKING", res.Classification)
	}
	if res.Gate != impact.GateUnverified {
		t.Fatalf("2.3.1 -> 3.0.0 gate %q, want %q", res.Gate, impact.GateUnverified)
	}
	if c.CompatibilityVerdict != impact.CompatibilityUnknown || c.Confidence != impact.ConfidenceDeclared {
		t.Fatalf("2.3.1 -> 3.0.0 invoices: verdict %q, confidence %q", c.CompatibilityVerdict, c.Confidence)
	}

	_, c = analyze("2.3.1", "2.4.0")
	if c.CompatibilityVerdict == impact.CompatibilityCompatible || c.Confidence == impact.ConfidenceContractual {
		t.Fatalf("2.3.1 -> 2.4.0 invoices: verdict %q, confidence %q", c.CompatibilityVerdict, c.Confidence)
	}
}
