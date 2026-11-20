package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
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
// counts, one outside the consumer's namespaces or reported by no target
// snapshot does not, and an unresolved dependency proposes nothing.
func TestRunningProvidersReadsLinkedTargets(t *testing.T) {
	const reports, ledger fleet.ServiceKey = "acme/reports", "acme/ledger"
	rev := &fleet.ContractRevision{Version: "2.5.0"}
	snap := &fleet.FleetSnapshot{
		Sources: []fleet.SourceState{{ID: "snap", Kind: fleet.SourceKindTargetSnapshot, Ref: "r.io/acme/pacto-targets@sha256:" + strings.Repeat("a", 64)},
			{ID: "file", Kind: "target-state"}},
		Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{"acme/ledger@x": rev},
		Targets: map[fleet.TargetKey]*fleet.TargetRecord{
			"shop/k/reports": {Key: "shop/k/reports", ServiceKey: reports, Source: "snap"},
			"shop/k/ledger":  {Key: "shop/k/ledger", ServiceKey: ledger, Source: "snap", ContractRevision: "acme/ledger@x"},
			"else/k/ledger":  {Key: "else/k/ledger", ServiceKey: ledger, Source: "snap", ContractRevision: "acme/ledger@x"},
			"shop/k/ledger2": {Key: "shop/k/ledger2", ServiceKey: ledger, Source: "file", ContractRevision: "acme/ledger@x"},
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
