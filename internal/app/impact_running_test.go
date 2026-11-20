package app

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// runReg is the registry domain the running fixtures publish under.
const runReg = "reg.example/acme"

// payBundle is payments at version; a 1.x revision has an interface the
// local folders run() writes have not, so judging a running 1.x is BREAKING.
func payBundle(version string) *contract.Bundle {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments", Version: version}}
	if !strings.HasPrefix(version, "1.") {
		return &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
	}
	c.Interfaces = []contract.Interface{{Name: "api", Type: "openapi", Ref: "openapi.yaml"}}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{"openapi.yaml": {Data: []byte("openapi: 3.0.0\ninfo: {title: p, version: \"1\"}\npaths: {}\n")}}}
}

// payRev is a published payments revision at version with digest.
func payRev(version, digest string) fleet.RawRevision {
	return fleet.RawRevision{Bundle: payBundle(version), Domain: runReg, ResolvedRef: runReg + "/payments:" + version, Digest: digest}
}

// payTarget runs the payments revision with digest on name.
func payTarget(name, digest string) fleet.RawTarget {
	return fleet.RawTarget{Scope: "prod", Kind: "deployment", Name: name, Service: "payments", Domain: runReg, Digest: digest, Compliance: fleet.StatusCompliant, EvidenceAt: freshNow()}
}

// ledgerRevs is ledger depending on payments with rng, running on a target.
func ledgerRevs(rng string) ([]fleet.RawRevision, []fleet.RawTarget) {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "ledger", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://" + runReg + "/payments", Required: true, Compatibility: rng}}}
	return []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: runReg, Digest: "sha256:l1"}},
		[]fleet.RawTarget{{Scope: "prod", Kind: "deployment", Name: "ledger", Service: "ledger", Domain: runReg, Digest: "sha256:l1", Compliance: fleet.StatusCompliant, EvidenceAt: freshNow()}}
}

// runEnv is a --running fixture: the fleet sources and the store pulls.
type runEnv struct {
	col    fleet.Collection
	extra  []fleet.Source
	pulled []string
	fail   bool
}

// withLedger adds ledger depending on payments with rng.
func (e *runEnv) withLedger(rng string) *runEnv {
	revs, targets := ledgerRevs(rng)
	e.col.Revisions = append(e.col.Revisions, revs...)
	e.col.Targets = append(e.col.Targets, targets...)
	return e
}

// run judges payments at version (a local folder) with --running.
func (e *runEnv) run(t *testing.T, version string, mutate ...func(*ImpactOptions)) (*impact.Result, error) {
	t.Helper()
	dir := t.TempDir()
	writeImpactService(t, dir, "payments", version)
	store := &mockBundleStore{PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
		e.pulled = append(e.pulled, ref)
		if e.fail {
			return nil, errors.New("401 unauthorized for token s3cr3t")
		}
		return payBundle(ref[strings.LastIndex(ref, ":")+1:]), nil
	}}
	col := e.col
	opts := ImpactOptions{NewPath: dir, RunningRegistries: []string{"reg.example"},
		Fleet: FleetOptions{Index: IndexNone, extraSources: append([]fleet.Source{fleet.NewMemorySource("mem", "memory", &col)}, e.extra...)}}
	for _, m := range mutate {
		m(&opts)
	}
	return NewService(store, nil).ImpactRunning(context.Background(), opts, nil)
}

func mustRun(t *testing.T, e *runEnv, version string, mutate ...func(*ImpactOptions)) *impact.Result {
	t.Helper()
	res, err := e.run(t, version, mutate...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func onePayments() *runEnv {
	return &runEnv{col: fleet.Collection{Revisions: []fleet.RawRevision{payRev("1.0.0", "sha256:p1")}, Targets: []fleet.RawTarget{payTarget("pay", "sha256:p1")}}}
}

func TestImpactRunningJudgesRunningRevision(t *testing.T) {
	res := mustRun(t, onePayments().withLedger("^1"), "2.0.0")
	if res.Gate != impact.GateBlocked || res.Running == nil || res.Running.Judged != "oci://"+runReg+"/payments:1.0.0" || res.OldVersion != "1.0.0" || res.Running.FirstVersion {
		t.Fatalf("gate %s %q running %+v old %s", res.Gate, res.GateReason, res.Running, res.OldVersion)
	}
}

func TestImpactRunningWorstGateWins(t *testing.T) {
	e := onePayments().withLedger("^1")
	e.col.Revisions = append(e.col.Revisions, payRev("2.0.0", "sha256:p2"))
	e.col.Targets = append(e.col.Targets, payTarget("pay-canary", "sha256:p2"))
	res := mustRun(t, e, "2.0.0")
	if res.Gate != impact.GateBlocked || res.Running.Judged != "oci://"+runReg+"/payments:1.0.0" || len(res.Running.Refs) != 2 {
		t.Fatalf("gate %s running %+v", res.Gate, res.Running)
	}
}

func TestImpactRunningNoTargetIsFirstVersion(t *testing.T) {
	e := &runEnv{col: fleet.Collection{Revisions: []fleet.RawRevision{payRev("1.0.0", "sha256:p1")}}}
	res := mustRun(t, e.withLedger("^2"), "2.0.0")
	if res.Gate != impact.GateClear || res.Running.Refs == nil || len(res.Running.Refs) != 0 || !res.Running.FirstVersion || res.Running.Judged != "" {
		t.Fatalf("gate %s %q running %+v", res.Gate, res.GateReason, res.Running)
	}
}

// unread runs e and checks the answer is unverified and names want.
func unread(t *testing.T, e *runEnv, want string) *impact.Result {
	t.Helper()
	res := mustRun(t, e, "1.1.0")
	if res.Gate != impact.GateUnverified || res.GateCode() != impact.GateCodeRunningUnread ||
		!slices.ContainsFunc(res.Running.Unread, func(s string) bool { return strings.Contains(s, want) }) {
		t.Fatalf("gate %s %s %q running %+v", res.Gate, res.GateCode(), res.GateReason, res.Running)
	}
	return res
}

func TestImpactRunningInlineContractIsUnread(t *testing.T) {
	e := onePayments()
	e.col.Revisions[0].ResolvedRef = ""
	unread(t, e, "runs a revision with no registry ref")
}

func TestImpactRunningUnlinkedTargetIsUnread(t *testing.T) {
	e := onePayments()
	e.col.Targets[0].Digest = "sha256:other"
	unread(t, e, "runs a revision Pacto could not link")
	// A link to a revision the snapshot does not hold reads the same.
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("mem", "memory", &onePayments().col))
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range snap.Targets {
		tr.ContractRevision = "missing"
	}
	if _, lines := runningRefs(snap, fleet.NewServiceKeyDomain(runReg, "payments"), "payments", func(string) bool { return true }); len(lines) != 1 ||
		!strings.Contains(lines[0], "could not link") {
		t.Fatalf("unread %v", lines)
	}
}

func TestImpactRunningInferredLinkNeverClears(t *testing.T) {
	e := onePayments()
	// The target names the tag only: a mutable correlation, never proof.
	e.col.Targets[0].Digest, e.col.Targets[0].ResolvedRef = "", runReg+"/payments:1.0.0"
	res := unread(t, e, "by tag, not digest")
	if res.Running.Judged == "" {
		t.Fatalf("a tag link is still judged: %+v", res.Running)
	}
	if res := mustRun(t, e.withLedger("^1"), "2.0.0"); res.Gate != impact.GateBlocked {
		t.Fatalf("incompatible: %s", res.Gate)
	}
}

func TestImpactRunningOnlyUnreadStillBlocksIncompatible(t *testing.T) {
	e := onePayments().withLedger("^1")
	e.col.Revisions[0].ResolvedRef = ""
	res := mustRun(t, e, "2.0.0")
	if res.Gate != impact.GateBlocked || !res.Running.FirstVersion {
		t.Fatalf("gate %s running %+v", res.Gate, res.Running)
	}
}

// inferredPayments is a workload source that runs payments with no contract.
func inferredPayments() fleet.Source {
	return fleet.NewMemorySource("k"+workloadsSuffix, fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
		InferredServices: []fleet.RawInferredService{{Namespace: "pay", Kind: "Deployment", Name: "payments"}}})
}

func TestImpactRunningInferredWorkloadIsUnread(t *testing.T) {
	e := onePayments()
	e.extra = []fleet.Source{inferredPayments()}
	unread(t, e, "pay/payments runs an undeclared revision of payments")
}

func TestFirstVersionNotSetWhenInferredRuns(t *testing.T) {
	e := &runEnv{col: fleet.Collection{Revisions: []fleet.RawRevision{payRev("1.0.0", "sha256:p1")}}, extra: []fleet.Source{inferredPayments()}}
	res := mustRun(t, e, "2.0.0")
	if res.Gate != impact.GateUnverified || res.GateCode() != impact.GateCodeRunningUnread {
		t.Fatalf("gate %s %s %q", res.Gate, res.GateCode(), res.GateReason)
	}
}

func TestImpactRunningPullFailureIsUnread(t *testing.T) {
	e := onePayments()
	e.fail = true
	res := unread(t, e, "payments:1.0.0: could not be pulled")
	if strings.Contains(strings.Join(res.Running.Unread, " ")+res.GateReason, "s3cr3t") {
		t.Fatalf("error text leaked: %+v", res.Running)
	}
}

func TestImpactRunningForeignHostIsUnread(t *testing.T) {
	e := onePayments()
	e.col.Revisions[0].ResolvedRef = "evil.example/acme/payments:1.0.0"
	unread(t, e, "oci://evil.example/acme/payments:1.0.0: registry not allowed")
	if len(e.pulled) != 0 {
		t.Fatalf("pulled %v", e.pulled)
	}
}

func TestImpactRunningBound(t *testing.T) {
	e := &runEnv{}
	for i := range MaxRunningRevisions + 1 {
		v, d := "1.0."+strconv.Itoa(i), "sha256:p"+strconv.Itoa(i)
		e.col.Revisions = append(e.col.Revisions, payRev(v, d))
		e.col.Targets = append(e.col.Targets, payTarget("pay"+strconv.Itoa(i), d))
	}
	res := unread(t, e, "1 more running revisions were not judged")
	if len(res.Running.Refs) != MaxRunningRevisions || len(e.pulled) != MaxRunningRevisions {
		t.Fatalf("refs %d pulled %d", len(res.Running.Refs), len(e.pulled))
	}
}

func TestImpactRunningDomainFromRunningRef(t *testing.T) {
	res := mustRun(t, onePayments().withLedger("^1"), "1.1.0")
	if res.Classification != "BREAKING" || res.Gate != impact.GateUnverified || len(res.Consumers) != 1 || res.Consumers[0].Service != "ledger" {
		t.Fatalf("gate %s %q consumers %+v", res.Gate, res.GateReason, res.Consumers)
	}
}

func TestImpactRunningFirstVersionLocalPathFindsRegistryConsumers(t *testing.T) {
	e := &runEnv{col: fleet.Collection{Revisions: []fleet.RawRevision{payRev("1.0.0", "sha256:p1")}}}
	res := mustRun(t, e.withLedger("^1"), "2.0.0")
	if res.Gate != impact.GateBlocked || !strings.Contains(res.GateReason, "ledger") || !res.Running.FirstVersion {
		t.Fatalf("gate %s %q", res.Gate, res.GateReason)
	}
}

func TestImpactRunningAmbiguousDomainRefused(t *testing.T) {
	other := payRev("1.0.0", "sha256:o1")
	other.Domain, other.ResolvedRef = "other.example/team", "other.example/team/payments:1.0.0"
	e := &runEnv{col: fleet.Collection{Revisions: []fleet.RawRevision{payRev("1.0.0", "sha256:p1"), other}}}
	_, err := e.run(t, "2.0.0")
	if amb := (*fleet.AmbiguousError)(nil); !errors.As(err, &amb) {
		t.Fatalf("err %v", err)
	}
}

func TestImpactRunningNoDomainIsUnverified(t *testing.T) {
	res := mustRun(t, &runEnv{}, "2.0.0")
	if res.Gate != impact.GateUnverified || !strings.Contains(res.GateReason, "payments is in no fleet domain") || !res.Running.FirstVersion {
		t.Fatalf("gate %s %q", res.Gate, res.GateReason)
	}
}

func TestImpactRunningOCINewKeepsItsDomain(t *testing.T) {
	e := onePayments().withLedger("^1")
	store := &mockBundleStore{PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
		return payBundle(ref[strings.LastIndex(ref, ":")+1:]), nil
	}}
	var resolved bool
	res, err := NewService(store, nil).ImpactRunning(context.Background(), ImpactOptions{NewPath: "oci://" + runReg + "/payments:2.0.0",
		OnResolved: func(old, _ *contract.Bundle) { resolved = old.Contract.Service.Version == "1.0.0" },
		Fleet:      FleetOptions{Index: IndexNone, extraSources: []fleet.Source{fleet.NewMemorySource("mem", "memory", &e.col)}}}, nil)
	if err != nil || res.Gate != impact.GateBlocked || !resolved {
		t.Fatalf("gate %v resolved %v err %v", res, resolved, err)
	}
}

func TestImpactRunningErrors(t *testing.T) {
	if _, err := NewService(nil, nil).ImpactRunning(context.Background(), ImpactOptions{NewPath: "/nonexistent"}, nil); err == nil {
		t.Fatal("bad new path")
	}
	dir := t.TempDir()
	writeImpactService(t, dir, "payments", "1.0.0")
	dup := []ObservationSourceSpec{{ID: "x"}, {ID: "x"}}
	if _, err := NewService(nil, nil).ImpactRunning(context.Background(), ImpactOptions{NewPath: dir, Fleet: FleetOptions{ObservationSources: dup}}, nil); err == nil {
		t.Fatal("bad fleet")
	}
	if _, err := onePayments().run(t, "2.0.0", func(o *ImpactOptions) { o.Traces = []byte("{") }); err == nil {
		t.Fatal("bad traces")
	}
}

// freshNow is an evidence time a snapshot built now reads as fresh.
func freshNow() *time.Time {
	now := time.Now()
	return &now
}

func TestImpactRunningUnreadIsBounded(t *testing.T) {
	e := &runEnv{col: fleet.Collection{Revisions: []fleet.RawRevision{payRev("1.0.0", "sha256:p1")}}}
	for i := range maxUnreadLines + 5 {
		e.col.Targets = append(e.col.Targets, payTarget("pay"+strconv.Itoa(i), "sha256:none"))
	}
	res := mustRun(t, e, "2.0.0")
	if u := res.Running.Unread; len(u) != maxUnreadLines || u[len(u)-1] != "6 more running revisions Pacto could not read" {
		t.Fatalf("unread %d %q", len(u), u[len(u)-1])
	}
}
