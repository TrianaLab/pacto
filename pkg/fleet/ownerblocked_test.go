package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// ownedRaw is a revision of service at version owned by team (none when
// empty), a declaration when declared, with check stamped on its provenance.
type ownedRaw struct {
	service, version, team string
	declared               bool
	check                  *OwnerRightCheck
}

var (
	refusedCheck = &OwnerRightCheck{Code: "OWNER_SIGNER_NOT_ALLOWED", Message: "only billing members may set it"}
	allowedCheck = &OwnerRightCheck{Allowed: true}
)

func (o ownedRaw) raw(i int) RawRevision {
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: o.service, Version: o.version, Owner: contract.Owner{Team: o.team}}}
	fs := fstest.MapFS{}
	if o.declared {
		rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "4"},
			Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: contract.InferredSigner{Subject: "eve@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}}}
		fs[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
	}
	r := RawRevision{Digest: fmt.Sprintf("sha256:%s%d", o.service, i), Bundle: &contract.Bundle{Contract: c, FS: fs}}
	if o.check != nil {
		r.Provenance = &BundleProvenance{Signature: BundleSignature{Status: SignatureUnverified}, OwnerRight: o.check}
	}
	return r
}

// ownedFleet builds revs read by one source of kind, each service run by a
// target in the namespace targets names.
func ownedFleet(t *testing.T, kind string, revs []ownedRaw, targets map[string]string) *Query {
	t.Helper()
	col := &Collection{}
	for i, r := range revs {
		col.Revisions = append(col.Revisions, r.raw(i))
	}
	for svc, ns := range targets {
		col.Targets = append(col.Targets, RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: svc, Service: svc, Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource(kind, kind, col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func limitationsOf(q *Query, code string) []string {
	var out []string
	for _, l := range q.snap.Limitations {
		if l.Code == code {
			out = append(out, l.Message)
		}
	}
	return out
}

func TestFleetIgnoresRefusedDeclarationOwner(t *testing.T) {
	q := ownedFleet(t, "oci", []ownedRaw{
		{service: "invoices", version: "1.0.0", team: "billing"},
		{service: "invoices", version: "2.0.0", team: "payments", declared: true, check: refusedCheck},
		{service: "invoices", version: "3.0.0", team: "payments", declared: true, check: refusedCheck},
	}, nil)
	s := invoices(q)
	if s.Owner.Team != "billing" || s.OwnerProvenance != OwnerProvenanceDeclared {
		t.Fatalf("owner = %+v %s", s.Owner, s.OwnerProvenance)
	}
	if m := limitationsOf(q, LimitationOwnerDeclarationRefused); len(m) != 1 || !strings.Contains(m[0], "invoices: 2 registry revision(s)") {
		t.Errorf("limitations = %q", m)
	}
	if ref := q.ownerRevision(s); ref == nil || !strings.Contains(ref.Key, "sha256:invoices0") {
		t.Errorf("owner revision = %+v", ref)
	}
	if q.snap.Completeness == CompletenessPartial {
		t.Error("a refusal is a decided answer, never partial")
	}
	// An attested owner is the fallback when the refused declaration was the only declared claim.
	att := ownedRaw{service: "invoices", version: "1.0.0"}.raw(0)
	att.Bundle.FS = fstest.MapFS{contract.InferredRecordFile: {Data: attRecord("repository:.github/CODEOWNERS:7", "payments", attCommit).Marshal()}}
	att.Provenance = attProvenance(attRepo, attCommit)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{att,
		ownedRaw{service: "invoices", version: "2.0.0", team: "billing", declared: true, check: refusedCheck}.raw(1)}}))
	if err != nil {
		t.Fatal(err)
	}
	if s := snap.Services[rInv]; s.Owner.Team != "payments" || s.OwnerProvenance != OwnerProvenanceAttested {
		t.Errorf("attested fallback = %+v %s", s.Owner, s.OwnerProvenance)
	}
}

func TestFleetUncheckedDeclarationIsUnverified(t *testing.T) {
	for _, kind := range []string{"oci", "cache"} {
		q := ownedFleet(t, kind, []ownedRaw{{service: "invoices", version: "1.0.0", team: "payments", declared: true}}, nil)
		if s := invoices(q); !s.Owner.IsEmpty() {
			t.Errorf("%s: owner = %+v", kind, s.Owner)
		}
		if m := limitationsOf(q, LimitationOwnerDeclarationUnverified); len(m) != 1 {
			t.Errorf("%s: limitations = %q", kind, m)
		}
		if q.snap.Completeness != CompletenessPartial {
			t.Errorf("%s: completeness = %s", kind, q.snap.Completeness)
		}
	}
	// A declaration that claims no owner leaves nothing out.
	q := ownedFleet(t, "oci", []ownedRaw{{service: "invoices", version: "1.0.0", declared: true}}, nil)
	if m := limitationsOf(q, LimitationOwnerDeclarationUnverified); len(m) != 0 || q.snap.Completeness == CompletenessPartial {
		t.Errorf("ownerless declaration = %q %s", m, q.snap.Completeness)
	}
	// A check that could not decide is unverified too.
	q = ownedFleet(t, "oci", []ownedRaw{{service: "invoices", version: "1.0.0", team: "payments", check: &OwnerRightCheck{Code: "OWNER_POLICY_UNVERIFIED"}}}, nil)
	if m := limitationsOf(q, LimitationOwnerDeclarationUnverified); len(m) != 1 || !invoices(q).Owner.IsEmpty() {
		t.Errorf("policy unverified = %q %+v", m, invoices(q).Owner)
	}
}

func TestFleetAllowedDeclarationOwnerKept(t *testing.T) {
	q := ownedFleet(t, "oci", []ownedRaw{{service: "invoices", version: "1.0.0", team: "payments", declared: true, check: allowedCheck}}, nil)
	if s := invoices(q); s.Owner.Team != "payments" {
		t.Errorf("owner = %+v", s.Owner)
	}
	if n := len(limitationsOf(q, LimitationOwnerDeclarationRefused)) + len(limitationsOf(q, LimitationOwnerDeclarationUnverified)); n != 0 {
		t.Errorf("%d owner limitations", n)
	}
}

// TestFleetLocalDeclarationUnaffected: a local folder is its author's own
// files, so no verdict decides its owner.
func TestFleetLocalDeclarationUnaffected(t *testing.T) {
	q := ownedFleet(t, "local", []ownedRaw{{service: "invoices", version: "1.0.0", team: "payments", declared: true, check: refusedCheck}}, nil)
	if s := invoices(q); s.Owner.Team != "payments" || len(limitationsOf(q, LimitationOwnerDeclarationRefused)) != 0 {
		t.Errorf("owner = %+v, limitations %+v", s.Owner, q.snap.Limitations)
	}
}

func TestFleetOwnerFilterIgnoresRefused(t *testing.T) {
	q := ownedFleet(t, "oci", []ownedRaw{
		{service: "invoices", version: "1.0.0", team: "billing"},
		{service: "invoices", version: "2.0.0", team: "payments", declared: true, check: refusedCheck},
	}, nil)
	for owner, want := range map[string]int{"payments": 0, "billing": 1} {
		if res, err := q.Search(SearchFilter{Owner: owner}); err != nil || res.Total != want {
			t.Errorf("owner=%s = %+v, %v", owner, res, err)
		}
	}
}

// TestRestrictRefusedDeclarationLimitation: a restricted view counts the
// refused revisions of the services it shows, never of one it hides.
func TestRestrictRefusedDeclarationLimitation(t *testing.T) {
	q := ownedFleet(t, "oci", []ownedRaw{
		{service: "invoices", version: "1.0.0", team: "payments", declared: true, check: refusedCheck},
		{service: "ledger", version: "1.0.0", team: "payments", declared: true, check: refusedCheck},
	}, map[string]string{"invoices": "shop", "ledger": "ops"})
	if m := limitationsOf(q, LimitationOwnerDeclarationRefused); len(m) != 2 {
		t.Fatalf("unrestricted = %q", m)
	}
	m := limitationsOf(q.Restrict(shopOnly, nil), LimitationOwnerDeclarationRefused)
	if len(m) != 1 || !strings.HasPrefix(m[0], "invoices:") {
		t.Errorf("restricted = %q", m)
	}
}

func TestOwnerRightCheckClone(t *testing.T) {
	p := &BundleProvenance{OwnerRight: &OwnerRightCheck{Code: "x"}}
	c := cloneProvenance(p)
	c.OwnerRight.Code = "y"
	if p.OwnerRight.Code != "x" {
		t.Error("clone aliases the owner right")
	}
}

func TestOwnerView(t *testing.T) {
	raw := ownedRaw{service: "invoices", version: "1.0.0", team: "payments", declared: true, check: allowedCheck}.raw(0)
	v, ok := OwnerView(raw)
	if !ok || v.Service != "invoices" || v.Owner.Team != "payments" || v.Record == nil || v.Record.Declaration == nil || v.Provenance != raw.Provenance {
		t.Fatalf("view = %+v %v", v, ok)
	}
	if _, ok := OwnerView(RawRevision{Bundle: raw.Bundle}); ok {
		t.Error("a revision with no digest has a view")
	}
	if _, ok := OwnerView(RawRevision{Digest: "sha256:x"}); ok {
		t.Error("a revision with no bundle has a view")
	}
}

// TestFleetCatalogDeclarationIsUnverified: a catalog root or the org index
// pulls a declaration from a registry, which no owner pass stamped, so its
// owner is unverified; a catalog root read from a local folder is its
// author's own files.
func TestFleetCatalogDeclarationIsUnverified(t *testing.T) {
	for _, kind := range []string{"catalog", "index"} {
		col := &Collection{Revisions: []RawRevision{ownedRaw{service: "invoices", version: "1.3.0", team: "evil", declared: true}.raw(0)}}
		col.Revisions[0].ResolvedRef = "oci://ghcr.io/acme/invoices@sha256:abc"
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource(kind, kind, col))
		if err != nil {
			t.Fatal(err)
		}
		q := NewQuery(snap)
		if s := q.snap.Services[NewServiceKey("invoices")]; !s.Owner.IsEmpty() {
			t.Errorf("%s: owner = %+v", kind, s.Owner)
		}
		if l := limitationsOf(q, LimitationOwnerDeclarationUnverified); len(l) != 1 {
			t.Errorf("%s: limitations = %v", kind, l)
		}
	}
	q := ownedFleet(t, "catalog", []ownedRaw{{service: "invoices", version: "1.0.0", team: "payments", declared: true}}, nil)
	if s := q.snap.Services[NewServiceKey("invoices")]; s.Owner.Team != "payments" {
		t.Errorf("local catalog owner = %+v", s.Owner)
	}
}

// TestEntitySearchSkipsRefusedRevisionOwner: entity search reads a
// revision's owner as the owner page does, so a refused claim never matches.
func TestEntitySearchSkipsRefusedRevisionOwner(t *testing.T) {
	q := ownedFleet(t, "oci", []ownedRaw{
		{service: "invoices", version: "1.0.0", team: "payments"},
		{service: "invoices", version: "1.3.0", team: "evil", declared: true, check: refusedCheck},
	}, nil)
	for _, f := range []EntityFilter{{Kinds: []EntityKind{KindRevision}, OwnerKey: "team:evil"}, {Kinds: []EntityKind{KindRevision}, Owner: "evil"}} {
		l, err := q.Entities(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Entities) != 0 {
			t.Errorf("%+v matched %+v", f, l.Entities)
		}
	}
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindRevision}, OwnerKey: "team:payments"})
	if err != nil || len(l.Entities) != 1 {
		t.Errorf("payments revisions = %+v %v", l, err)
	}
}

// TestRevisionOwnershipMarksBlockedOwner: a revision page shows the owner its
// pacto.yaml sets, and says when its service does not take it.
func TestRevisionOwnershipMarksBlockedOwner(t *testing.T) {
	q := ownedFleet(t, "oci", []ownedRaw{
		{service: "invoices", version: "1.0.0", team: "billing"},
		{service: "invoices", version: "2.0.0", team: "payments", declared: true, check: refusedCheck},
		{service: "invoices", version: "3.0.0", team: "payments", declared: true},
	}, nil)
	want := map[string]string{"1.0.0": "", "2.0.0": LimitationOwnerDeclarationRefused, "3.0.0": LimitationOwnerDeclarationUnverified}
	for _, r := range q.snap.Revisions {
		info := revisionOwnership(r)
		if info.Blocked != want[r.Version] || info.Owner == "" {
			t.Errorf("%s: blocked = %q, owner %q", r.Version, info.Blocked, info.Owner)
		}
	}
}
