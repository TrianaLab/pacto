package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
)

// runningPacto is a reconciled Pacto in ns running service at version.
func runningPacto(host, ns, name, service, version, hex string) delivery.PactoView {
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = ns, name
	p.Spec.Target = &delivery.PactoTargetView{ServiceName: service}
	p.Status.StatusFormat = delivery.StatusFormatGuarantees
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", time.Now().Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: service, Version: version,
		ResolvedRef: "oci://" + host + "/acme/" + service + "@sha256:" + strings.Repeat(hex, 64)}
	// The operator records the manifest digest it evaluated, so the fleet links the target.
	p.Status.EvaluatedRevision = &delivery.EvaluatedView{ContractDigest: "sha256:" + strings.Repeat(hex, 64)}
	return p
}

// reportsByName depends on ledger by name and announces an undated break.
const reportsByName = "dependencies:\n  - name: ledger\n    required: true\nbreaks:\n  - consumers: [web]\n    reason: drop v1\n"

// proposalEnv publishes ledger 2.4.0 (indexed) and reports 1.0.0, then
// changes reports, so the plan runs impact over the target snapshot.
func proposalEnv(t *testing.T) *ciPubEnv {
	t.Helper()
	e := newCIPub(t)
	e.svc.sys.refusalRepo = anonRepo
	e.write(t, map[string]string{"b/pacto.yaml": e.contract("ledger", "2.4.0", "", "")})
	if p := e.ciPublish(t, nil); p.Exit != 0 {
		t.Fatalf("plan %+v", p)
	}
	e.write(t, map[string]string{"a/pacto.yaml": e.contract("reports", "1.0.0", reportsByName, "")})
	e.publishAs(t, "a", "r1")
	e.write(t, map[string]string{"a/pacto.yaml": e.contract("reports", "", reportsByName, ""), "a/notes.md": "# reports\n"})
	return e
}

// recordFields are the fields of the record in fsys by key.
func recordFields(t *testing.T, fsys fs.FS) map[string]contract.Inferred {
	t.Helper()
	rec, err := contract.ReadInferredRecord(fsys)
	if err == nil && rec != nil {
		err = contract.VerifyInferredRecord(fsys, rec)
	}
	if err != nil || rec == nil {
		t.Fatalf("record %v: %v", rec, err)
	}
	out := map[string]contract.Inferred{}
	for _, f := range rec.Fields {
		out[f.Field] = f
	}
	return out
}

func TestCIProposesFromIndexAndSnapshot(t *testing.T) {
	t.Parallel()
	e := proposalEnv(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	snap := delivery.TargetSnapshot{Cluster: snapA, AsOf: time.Now().Format(time.RFC3339), Completeness: fleet.CompletenessComplete,
		Publisher: "pacto-system/pacto-operator-delivery-recorder", Pactos: []delivery.PactoView{
			runningPacto(e.host, "shop", "reports", "reports", "1.0.0", "a"),
			runningPacto(e.host, "shop", "ledger-a", "ledger", "2.4.0", "b"),
			runningPacto(e.host, "shop", "ledger-b", "ledger", "2.3.1", "c"),
			runningPacto(e.host, "other", "ledger", "ledger", "1.0.0", "d"), // another namespace: never proposed from
		}}
	desc := pushAt(t, e.host+"/acme/"+delivery.TargetSnapshotRepository, key, snapA, snap)
	trust := trustFile(t, delivery.TrustedKey{Name: "rec", PublicKey: pemOf(t, key), Streams: []string{snapA + "/*/*"}})
	bundles := map[string]*contract.Bundle{}
	p, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme", TargetSnapshotAuto: true, Bundles: bundles,
		Fleet: FleetOptions{TargetSnapshotTrustFile: trust}, revision: "0123456789abcdef0123",
		Getenv: mapEnv(map[string]string{"SOURCE_DATE_EPOCH": "1794614400"})})
	if err != nil {
		t.Fatal(err)
	}
	fields := recordFields(t, bundles["a"].FS)
	tag, _ := orgindex.Tag(fleet.NewServiceKeyDomain(e.host+"/acme", "ledger"), "2.4.0")
	if ref := fields["dependencies[ledger].ref"]; ref.Value != "oci://"+e.host+"/acme/ledger" || ref.Source != "index:"+e.host+"/acme/pacto-index:"+tag {
		t.Errorf("ref = %+v", ref)
	}
	snapSrc := "snapshot:" + e.host + "/acme/" + delivery.TargetSnapshotRepository + "@" + desc.Digest.String()
	if r := fields["dependencies[ledger].compatibility"]; r.Proposed != "^2.3.0" || r.Source != snapSrc {
		t.Errorf("range = %+v, want ^2.3.0 from %s", r, snapSrc)
	}
	if x := fields["breaks[0].expires"]; x.Value != "2026-12-14" || x.Source != "commit:0123456789ab +30d" {
		t.Errorf("expiry = %+v", x)
	}
	if r := row(t, p, "a"); r.Inferred != len(fields) {
		t.Errorf("row inferred %d, record holds %d", r.Inferred, len(fields))
	}
}

// TestCIPlanAndPublishCarrySameProposals: the bundle pushed holds the record
// the plan judged, proposals included.
func TestCIPlanAndPublishCarrySameProposals(t *testing.T) {
	t.Parallel()
	e := proposalEnv(t)
	bundles := map[string]*contract.Bundle{}
	if _, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: bundles, revision: "r2"}); err != nil {
		t.Fatal(err)
	}
	p := e.ciPublish(t, nil)
	r := pubRow(t, p, "reports")
	if r.State != ci.StatePublished {
		t.Fatalf("row %+v", r)
	}
	repoTag, dg, _ := strings.Cut(r.Published, "@")
	pulled, err := oci.NewClient(anonymous, oci.WithInsecureRegistries(e.host)).Pull(t.Context(), repoTag[:strings.LastIndex(repoTag, ":")]+"@"+dg)
	if err != nil {
		t.Fatal(err)
	}
	// Publish adds the derived version's field; every planned field is there as planned.
	planned, got := recordFields(t, bundles["a"].FS), recordFields(t, pulled.FS)
	if _, ok := planned["dependencies[ledger].ref"]; !ok {
		t.Fatalf("no proposal planned: %+v", planned)
	}
	for k, f := range planned {
		if !reflect.DeepEqual(got[k], f) {
			t.Errorf("%s: planned %+v, published %+v", k, f, got[k])
		}
	}
}

// TestCIProposalOnlyDifferenceNotRepublished: a proposal alone never makes a
// service changed, so nothing is written for it.
func TestCIProposalOnlyDifferenceNotRepublished(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.svc.sys.refusalRepo = anonRepo
	e.write(t, map[string]string{"a/pacto.yaml": e.contract("reports", "", reportsByName, ""), "b/pacto.yaml": e.contract("ledger", "2.4.0", "", "")})
	e.publishAs(t, "a", "r1") // published before ledger is indexed: no proposal
	p := e.ciPublish(t, nil)  // ledger publishes and is indexed; reports is planned with no proposal
	if r := pubRow(t, p, "reports"); r.State != ci.StateUnchanged {
		t.Fatalf("row %+v", r)
	}
	e.svc.indexListings = indexMemo{} // a new run reads the index again, ledger's entry included
	before := e.reg.writes.Load()
	p = e.ciPublish(t, nil) // reports now gets a ref proposal
	bundles := map[string]*contract.Bundle{}
	if _, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: bundles, revision: "r2"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordFields(t, bundles["a"].FS)["dependencies[ledger].ref"]; !ok {
		t.Fatal("no proposal planned")
	}
	if r := pubRow(t, p, "reports"); r.State != ci.StateUnchanged || e.reg.writes.Load() != before {
		t.Fatalf("row %+v, %d writes", r, e.reg.writes.Load()-before)
	}
}

// TestRunningProvidersReadsLinkedTargets: a target linked to its revision
// counts, one outside the consumer's namespaces, reported by no target
// snapshot or left unlinked does not, and an unresolved dependency proposes
// nothing.
func TestRunningProvidersReadsLinkedTargets(t *testing.T) {
	t.Parallel()
	const reports, ledger fleet.ServiceKey = "acme/reports", "acme/ledger"
	rev := &fleet.ContractRevision{Version: "2.5.0"}
	snap := &fleet.FleetSnapshot{
		Sources: []fleet.SourceState{{ID: "snap", Kind: fleet.SourceKindTargetSnapshot, Ref: "r.io/acme/pacto-targets@sha256:" + strings.Repeat("a", 64)},
			{ID: "file", Kind: "target-state"}},
		Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{"acme/ledger@x": rev, fleet.NewRevisionKey(ledger, "y"): {Version: "9.0.0"}},
		Targets: map[fleet.TargetKey]*fleet.TargetRecord{
			"shop/k/reports": {Key: "shop/k/reports", ServiceKey: reports, Source: "snap"},
			"shop/k/ledger":  {Key: "shop/k/ledger", ServiceKey: ledger, Source: "snap", ContractRevision: "acme/ledger@x"},
			"else/k/ledger":  {Key: "else/k/ledger", ServiceKey: ledger, Source: "snap", ContractRevision: "acme/ledger@x"},
			"shop/k/ledger2": {Key: "shop/k/ledger2", ServiceKey: ledger, Source: "file", ContractRevision: "acme/ledger@x"},
			// The fleet made no link (an inconsistent or ambiguous identity): never a version.
			"shop/k/ledger3": {Key: "shop/k/ledger3", ServiceKey: ledger, Source: "snap", Digest: "y"},
		},
		Relationships: []fleet.Relationship{
			{FromService: reports, To: "ledger", ToService: ledger, Type: fleet.RelationshipDependency, Resolved: true},
			{FromService: reports, To: "audit", Type: fleet.RelationshipDependency},
			{FromService: reports, To: "billing", ToService: "acme/billing", Type: fleet.RelationshipDependency, Resolved: true},
			{FromService: "acme/web", To: "ledger", ToService: ledger, Type: fleet.RelationshipDependency, Resolved: true},
		},
	}
	got := runningProviders(snap, reports)
	if len(got) != 1 || got["ledger"].Value != "2.5.0" || got["ledger"].Source != "snapshot:"+snap.Sources[0].Ref {
		t.Fatalf("running = %+v", got)
	}
	if runningProviders(nil, reports) != nil {
		t.Fatal("no snapshot proposes")
	}
}

// TestCIProposesNoRefWithoutAnIndex: an index that cannot be read, or
// --index none, proposes no ref and fails nothing.
func TestCIProposesNoRefWithoutAnIndex(t *testing.T) {
	t.Parallel()
	e := newCIPub(t)
	e.write(t, map[string]string{"a/pacto.yaml": e.contract("reports", "1.0.0", "dependencies:\n  - name: ledger\n    required: true\n", "")})
	for _, fo := range []FleetOptions{{}, {Index: IndexNone}} {
		e.svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("denied") }
		bundles := map[string]*contract.Bundle{}
		if _, _, err := e.svc.CIPlan(t.Context(), CIOptions{Root: e.root, Registry: e.host + "/acme", Bundles: bundles, Fleet: fo}); err != nil {
			t.Fatal(err)
		}
		if rec, _ := contract.ReadInferredRecord(bundles["a"].FS); rec != nil {
			t.Fatalf("%+v: record %+v", fo, rec)
		}
	}
}

// TestProposeRefAmbiguousIsFinding pins why a dependency named without a ref
// needs no INDEX_AMBIGUOUS finding: the index keeps one newest entry per tag
// prefix, and an entry is used only under its own service's prefix. A second,
// otherwise confirmable ledger entry written under billing's prefix is
// refused, so the index names exactly one ledger and one ref is proposed,
// from ledger's own tag.
func TestProposeRefAmbiguousIsFinding(t *testing.T) {
	t.Parallel()
	e := newIdxEnv(t)
	e.push(t, "ledger", "1.0.0", interfaceYAML, "")
	v2 := e.push(t, "ledger", "2.0.0", interfaceYAML, IndexNone)
	key := e.key("ledger")
	forged := orgindex.Entry{SchemaVersion: orgindex.SchemaVersion, Service: string(key), Name: "ledger", Version: "2.0.0",
		Bundle: e.ir.host + "/acme/ledger@" + v2.Digest}
	payload, _ := orgindex.Encode(forged)
	other, _ := orgindex.Tag(e.key("billing"), "2.0.0")
	e.forge(t, ocireferrer.Build(orgindex.ArtifactType, orgindex.MediaType, payload, nil, nil), other)

	candidates := indexCandidates("", e.ir.host+"/acme/reports")
	rd := e.svc.IndexEntries(t.Context(), candidates)
	n := 0
	for _, en := range rd.entries {
		if en.Service == string(key) {
			n++
		}
	}
	if n != 1 || !strings.Contains(limMessage(rd.lims, orgindex.LimitationEntryInvalid), "names another service or version") {
		t.Fatalf("%d ledger entries used; limitations %+v", n, rd.lims)
	}
	refs := e.svc.indexRefs(t.Context(), candidates, e.ir.host+"/acme", []string{"ledger"})
	tag, _ := orgindex.Tag(key, "1.0.0")
	want := map[string]discover.Sourced{"ledger": {Value: "oci://" + e.ir.host + "/acme/ledger", Source: contract.SourceIndex + e.index() + ":" + tag}}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %+v, want %+v", refs, want)
	}
}

// TestIndexRefsIgnoresAConsumerOfTheName: an indexed service that only
// depends on ledger proposes no ref for ledger; the name matches the
// entry's own service, never its dependencies.
func TestIndexRefsIgnoresAConsumerOfTheName(t *testing.T) {
	t.Parallel()
	e := newIdxEnv(t)
	e.push(t, "billing", "1.0.0", e.dependsOnLedger(), "")
	refs := e.svc.indexRefs(t.Context(), indexCandidates("", e.ir.host+"/acme/reports"), e.ir.host+"/acme", []string{"ledger"})
	if len(refs) != 0 {
		t.Fatalf("refs = %+v, want none", refs)
	}
}

// TestCIRangesOverWalkedServices: pacto ci counts every walked service's
// dependencies on other services: declared in pacto.yaml, accepted when a
// carried acceptance pinned the range, else unknown with the proposal.
func TestCIRangesOverWalkedServices(t *testing.T) {
	t.Parallel()
	dep := func(name, compat string) contract.Dependency {
		return contract.Dependency{Name: name, Compatibility: compat}
	}
	written := &contract.Bundle{Contract: &contract.Contract{Dependencies: []contract.Dependency{dep("ledger", "^2.0.0"), dep("billing", "unknown"), dep("audit", "unknown")}}}
	rec := acceptRecordOf(contract.Inferred{Field: "dependencies[audit].compatibility", Value: "compatibility unknown", Proposed: "^3.1.0", Source: snapSrc, Confidence: contract.ConfidenceLikely})
	judged := &contract.Bundle{Contract: &contract.Contract{Dependencies: []contract.Dependency{dep("ledger", "^2.0.0"), dep("billing", "^1.4.0"), dep("audit", "unknown"),
		{Name: "stripe", External: true}}}, FS: contract.WithInferredRecord(fstest.MapFS{}, rec.Marshal())}
	empty := &contract.Bundle{Contract: &contract.Contract{}, FS: fstest.MapFS{}}
	folders := []*ciFolder{
		{row: ci.Row{Service: "reports"}, asWritten: written, judged: judged},
		{row: ci.Row{Service: "broken"}},
		{row: ci.Row{Service: "ledger"}, asWritten: empty, judged: empty},
	}
	cov := ciRanges(folders, ".")
	want := impact.RangeCoverage{Scope: "dependencies of 2 services in .", Edges: 3, Declared: 1, Accepted: 1, UnknownTotal: 1,
		Unknown: []impact.UnknownRange{{From: "reports", To: "audit", Proposed: "^3.1.0"}}}
	if !reflect.DeepEqual(*cov, want) {
		t.Fatalf("got %+v", *cov)
	}
	if got := ciRanges(folders[2:], "repo").Scope; got != "dependencies of 1 service in repo" {
		t.Errorf("scope %q", got)
	}
}
