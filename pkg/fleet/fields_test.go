package fleet

import (
	"cmp"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// fieldsOpts shapes fieldsRev.
type fieldsOpts struct {
	prov     *BundleProvenance
	from     string
	accepted []contract.InferredAcceptance
}

// auditProposal is the range a cluster read proposes for audit.
func auditProposal() contract.Inferred {
	return contract.Inferred{Field: "dependencies[audit].compatibility", Value: "compatibility unknown", Proposed: "^1.4.0",
		Source: "cluster:fleet reported versions 1.4.2", Confidence: contract.ConfidenceLikely,
		AsOf: "2026-07-29T12:00:00Z", Completeness: "partial"}
}

// fieldsRev is invoices 0.1.0, declared by ana from the invoices Deployment:
// it depends on ledger ^2.3.0 (accepted when o.accepted says so) and on
// audit with an unknown range a cluster read proposes ^1.4.0, and its
// record infers one folder field.
func fieldsRev(t *testing.T, o fieldsOpts) RawRevision {
	t.Helper()
	rec, err := contract.ParseInferredRecord(clusterRecordOf(t, false,
		contract.Inferred{Field: "workload", Value: "service", Source: "deploy/app.yaml:1", Confidence: contract.ConfidenceLikely}, auditProposal()))
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana, InferredFrom: cmp.Or(o.from, invoicesName), Accepted: o.accepted}
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "invoices", Version: "0.1.0", Owner: contract.Owner{Team: "billing"}},
		Dependencies: []contract.Dependency{
			{Name: "ledger", Ref: "oci://ghcr.io/acme/ledger", Compatibility: "^2.3.0"},
			{Name: "audit", Ref: "oci://ghcr.io/acme/audit", Compatibility: contract.CompatibilityUnknown},
		}}
	fsys := fstest.MapFS{"deploy/app.yaml": {Data: []byte(recordManifest)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}, Digest: "sha256:invoices-0.1.0", Provenance: o.prov, FetchedAt: new(fixedNow())}
}

var ledgerAccepted = []contract.InferredAcceptance{{Field: "dependencies[ledger].compatibility", Range: "^2.3.0"}}

func fieldsOfInvoices(t *testing.T, q *Query) *FieldsPreview {
	t.Helper()
	v, err := q.GetService("invoices")
	if err != nil {
		t.Fatal(err)
	}
	return v.Fields
}

func fieldNamed(t *testing.T, f *FieldsPreview, field string) FieldProvenance {
	t.Helper()
	i := slices.IndexFunc(f.Items, func(r FieldProvenance) bool { return r.Field == field })
	if i < 0 {
		t.Fatalf("no %s in %+v", field, f.Items)
	}
	return f.Items[i]
}

func TestFieldsOfMergedService(t *testing.T) {
	snap := handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: anaSigned(), accepted: ledgerAccepted})}, nil, true)
	if len(snap.Replacements) != 1 {
		t.Fatalf("the fixture must hand over: %+v %+v", snap.Replacements, snap.Limitations)
	}
	f := fieldsOfInvoices(t, NewQuery(snap))
	if f == nil || f.Total != 4 || f.Count != 4 || f.Truncated {
		t.Fatalf("fields %+v", f)
	}
	checkMergedRows(t, f.Items)
	want := []string{
		"  owner         billing    declared",
		"  ledger range  ^2.3.0     accepted by ana@acme.com",
		"  audit range   unknown    proposed ^1.4.0 (fleet, partial)",
		"  workload      service    inferred from deploy/app.yaml:1",
	}
	if got := f.Lines(); !slices.Equal(got, want) {
		t.Errorf("lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// checkMergedRows checks the owner, ledger, audit and workload rows of
// fieldsRev in that order.
func checkMergedRows(t *testing.T, items []FieldProvenance) {
	t.Helper()
	at, src := new(fixedNow()), "pacto.yaml of invoices@sha256:invoices-0.1.0"
	want := []FieldProvenance{
		{Field: "service.owner", Value: "billing", Provenance: FieldDeclared, Source: src, AsOf: at},
		{Field: "dependencies[ledger].compatibility", Value: "^2.3.0", Provenance: FieldAccepted, By: anaSubject, Source: src, AsOf: at},
		{Field: "dependencies[audit].compatibility", Value: contract.CompatibilityUnknown, Provenance: FieldDeclared,
			Source: "cluster:fleet reported versions 1.4.2", AsOf: at, Completeness: "partial", Proposed: "^1.4.0"},
		{Field: "workload", Value: "service", Provenance: FieldInferred, Source: "deploy/app.yaml:1"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("rows %+v\nwant %+v", items, want)
	}
	if sourceKind(items[2].Source) != "fleet" {
		t.Errorf("kind %q", sourceKind(items[2].Source))
	}
}

func TestFieldsAcceptedNeedsVerifiedMatchingSigner(t *testing.T) {
	signer := func(subject, class string) *BundleProvenance {
		p := anaSigned()
		p.Signers = []VerifiedSigner{{Subject: subject, Issuer: anaIssuer, Class: class}}
		return p
	}
	for name, tc := range map[string]struct {
		prov     *BundleProvenance
		accepted []contract.InferredAcceptance
		want, by string
	}{
		"ana, a person":     {anaSigned(), ledgerAccepted, FieldAccepted, anaSubject},
		"another subject":   {signer("bob@acme.com", contract.SignerClassHuman), ledgerAccepted, FieldDeclared, ""},
		"a CI identity":     {signer(anaSubject, "ci"), ledgerAccepted, FieldDeclared, ""},
		"no provenance":     {nil, ledgerAccepted, FieldDeclared, ""},
		"not in accepted":   {anaSigned(), nil, FieldDeclared, ""},
		"another accepted":  {anaSigned(), []contract.InferredAcceptance{{Field: "dependencies[audit].compatibility", Range: "^1.4.0"}}, FieldDeclared, ""},
		"subject unallowed": {signer("ana@other.example", contract.SignerClassHuman), ledgerAccepted, FieldDeclared, ""},
	} {
		t.Run(name, func(t *testing.T) {
			snap := handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: tc.prov, accepted: tc.accepted})}, nil, false)
			ledger := fieldNamed(t, fieldsOfInvoices(t, NewQuery(snap)), "dependencies[ledger].compatibility")
			if ledger.Provenance != tc.want || ledger.By != tc.by {
				t.Errorf("ledger %+v", ledger)
			}
		})
	}
}

func TestFieldsAcceptedNeedsIssuerMatch(t *testing.T) {
	p := anaSigned()
	p.Signers = []VerifiedSigner{{Subject: anaSubject, Issuer: "https://token.actions.githubusercontent.com", Class: contract.SignerClassHuman}}
	snap := handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: p, accepted: ledgerAccepted})}, nil, false)
	if ledger := fieldNamed(t, fieldsOfInvoices(t, NewQuery(snap)), "dependencies[ledger].compatibility"); ledger.Provenance != FieldDeclared || ledger.By != "" {
		t.Errorf("a same-subject signer of another issuer accepted: %+v", ledger)
	}
}

func TestFieldsOwnerProvenanceMapping(t *testing.T) {
	snap := handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: anaSigned()})}, nil, false)
	ownerRec := contract.Inferred{Field: "service.owner", Value: "team-x", Source: "CODEOWNERS:1"}
	for prov, want := range map[string]FieldProvenance{
		OwnerProvenanceDeclaredViaTarget: {Field: "service.owner", Value: "billing", Provenance: FieldDeclared, Source: "Pacto target"},
		OwnerProvenanceAttested:          {Field: "service.owner", Value: "billing", Provenance: FieldAttested, Source: "CODEOWNERS:3 at abc"},
		OwnerProvenanceInferred:          {Field: "service.owner", Value: "team-x", Provenance: FieldInferred, Source: "CODEOWNERS:1"},
	} {
		s := *snap.Services[invoicesKey]
		s.OwnerProvenance, s.OwnerSource = prov, "CODEOWNERS:3 at abc"
		rows := NewQuery(snap).ownerRow(&s, map[string]contract.Inferred{"service.owner": ownerRec})
		if len(rows) != 1 || rows[0] != want {
			t.Errorf("%s: %+v", prov, rows)
		}
	}
	// No owner of any tier: no row.
	s := *snap.Services[invoicesKey]
	s.OwnerProvenance = ""
	if rows := NewQuery(snap).ownerRow(&s, nil); rows != nil {
		t.Errorf("rows %+v", rows)
	}
	declared := fieldsOfInvoices(t, NewQuery(snap)).Items[0]
	if declared.Provenance != FieldDeclared || !strings.HasPrefix(declared.Source, "pacto.yaml of ") {
		t.Errorf("declared %+v", declared)
	}
	if got := (FieldProvenance{Field: "service.owner", Value: "billing", Provenance: FieldAttested}).Line(); got != "  owner         billing    attested" {
		t.Errorf("line %q", got)
	}
}

func TestFieldsAbsentWithoutRecordOrReplacement(t *testing.T) {
	snap := handSnap(t, []RawRevision{handoverRev(t, declOf{noDeclaration: true})}, nil, false)
	snap.Revisions[NewRevisionKey(invoicesKey, "sha256:invoices-0.1.0")].Record = nil
	v, err := NewQuery(snap).GetService("invoices")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if v.Fields != nil || strings.Contains(string(b), `"fields"`) {
		t.Errorf("fields %s", b)
	}
	// A service with no revision has no fields either.
	if f := NewQuery(snap).fieldsOf(&ServiceRecord{Key: "x"}); f != nil {
		t.Errorf("fields %+v", f)
	}
	// A record without a declaration is inferredFields' alone.
	rev := snap.Revisions[NewRevisionKey(invoicesKey, "sha256:invoices-0.1.0")]
	rev.Record = &contract.InferredRecord{Fields: []contract.Inferred{{Field: "service.owner", Value: "billing", Source: "CODEOWNERS"}}}
	if f := NewQuery(snap).fieldsOf(snap.Services[invoicesKey]); f != nil {
		t.Errorf("fields of a plain record %+v", f)
	}
	rev.Record = nil
	// A Pacto target's takeover alone keeps 509-S4's answer.
	snap.Replacements = append(snap.Replacements, Replacement{To: invoicesKey, Link: linkPactoTarget})
	if f := NewQuery(snap).fieldsOf(snap.Services[invoicesKey]); f != nil {
		t.Errorf("fields of a Pacto target takeover %+v", f)
	}
	// A hidden Pacto target's takeover counts in replacesHidden alone: every
	// viewer gets the same shape.
	snap.hiddenReplacements = map[ServiceKey]int{invoicesKey: 1}
	if f := NewQuery(snap).fieldsOf(snap.Services[invoicesKey]); f != nil {
		t.Errorf("fields of a hidden Pacto target takeover %+v", f)
	}
	// A hidden hand-over keeps the block a viewer who sees it gets.
	snap.hiddenHandovers = map[ServiceKey]bool{invoicesKey: true}
	if f := NewQuery(snap).fieldsOf(snap.Services[invoicesKey]); f == nil {
		t.Error("no fields with a hidden hand-over")
	}
	snap.hiddenReplacements, snap.hiddenHandovers = nil, nil
	// Any other replacement is enough: its owner and dependency rows.
	snap.Replacements = append(snap.Replacements, Replacement{To: invoicesKey, Link: linkInferredFrom})
	if f := NewQuery(snap).fieldsOf(snap.Services[invoicesKey]); f == nil || f.Items[0].Field != "service.owner" {
		t.Errorf("fields %+v", f)
	}
	// An unreadable contract has no dependency rows.
	snap.Revisions[NewRevisionKey(invoicesKey, "sha256:invoices-0.1.0")].Contract = nil
	if f := NewQuery(snap).fieldsOf(snap.Services[invoicesKey]); f == nil || slices.ContainsFunc(f.Items, func(r FieldProvenance) bool { return r.Field != "service.owner" }) {
		t.Errorf("fields %+v", f)
	}
}

func TestFieldsHideClusterSourcesWhenReplacementHidden(t *testing.T) {
	billingTarget := workload("billing", "invoices")
	for name, tc := range map[string]struct {
		prov    *BundleProvenance
		targets []RawTarget
		// accepted: the build keeps the record's cluster facts, so the
		// restricted view hides and counts them; otherwise there are none.
		accepted bool
	}{
		"replacement hidden":        {anaSigned(), []RawTarget{billingTarget}, true},
		"a Pacto target won":        {anaSigned(), []RawTarget{billingTarget, invoicesTarget("orders")}, true},
		"an unverified link":        {nil, []RawTarget{billingTarget}, false},
		"a signer the rule refuses": {&BundleProvenance{Source: "oci", Signers: []VerifiedSigner{{Subject: anaSubject, Issuer: anaIssuer, Class: "ci"}}}, []RawTarget{billingTarget}, false},
	} {
		t.Run(name, func(t *testing.T) {
			full := NewQuery(handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: tc.prov, accepted: ledgerAccepted})}, tc.targets, true))
			if f := fieldsOfInvoices(t, full); slices.ContainsFunc(f.Items, func(r FieldProvenance) bool { return clusterFact(r.Source) }) != tc.accepted {
				t.Fatalf("cluster rows for a viewer who may list the workload: %+v", f)
			}
			q := full.Restrict(inBilling, noDeployments)
			f := fieldsOfInvoices(t, q)
			if f == nil || (f.Hidden == 1) != tc.accepted || slices.ContainsFunc(f.Items, func(r FieldProvenance) bool { return clusterFact(r.Source) }) {
				t.Fatalf("fields %+v", f)
			}
			if !slices.ContainsFunc(f.Items, func(r FieldProvenance) bool { return r.Field == "workload" }) {
				t.Errorf("a folder row was hidden: %+v", f.Items)
			}
			if got := f.Lines(); tc.accepted && got[len(got)-1] != "  1 fields hidden by your permissions" {
				t.Errorf("lines %q", got)
			}
			if a := handoverAnswers(t, q); strings.Contains(a, "billing/Deployment/invoices") {
				t.Errorf("a restricted answer names the workload: %s", a)
			}
		})
	}
}

func TestExplainServiceReplacesReasons(t *testing.T) {
	q := NewQuery(handSnap(t, []RawRevision{fieldsRev(t, fieldsOpts{prov: anaSigned()})}, nil, true))
	e, err := q.Explain("invoices")
	if err != nil {
		t.Fatal(err)
	}
	var got []Reason
	for _, r := range e.Reasons {
		if r.Code == ReasonReplacesInferred {
			got = append(got, r)
		}
	}
	want := q.snap.Replacements[0].Line(q.snap.GeneratedAt)
	if len(got) != 1 || got[0].Message != want || got[0].Source != "invoices@sha256:invoices-0.1.0" || e.Fields == nil || e.ReplacesHidden != 0 {
		t.Errorf("reasons %+v fields %+v", got, e.Fields)
	}
	// A restricted viewer gets the count, never the reason.
	r, err := q.Restrict(inBilling, noDeployments).Explain("invoices")
	if err == nil && (slices.ContainsFunc(r.Reasons, func(r Reason) bool { return r.Code == ReasonReplacesInferred })) {
		t.Errorf("reasons %+v", r.Reasons)
	}
	// A Pacto target's reason cites the target.
	p := NewQuery(replaceSnap(t, hoursAgo(72), []RawTarget{cartTarget("cart-svc", hoursAgo(1))}))
	if e, err := p.Explain("cart-svc"); err != nil || !slices.ContainsFunc(e.Reasons, func(r Reason) bool {
		return r.Code == ReasonReplacesInferred && strings.HasPrefix(r.Message, "Replaces ") && r.Source != ""
	}) {
		t.Errorf("explain %+v %v", e, err)
	}
}

func TestReplacementLineByLink(t *testing.T) {
	now := fixedNow()
	at := now.Add(-3 * time.Minute)
	p := Replacement{From: NewServiceKey(cartName), To: "cart-svc", Link: linkPactoTarget, Target: NewTargetKey("shop", "kubernetes", "cart"), At: &at}
	if got := p.Line(now); got != "Replaces "+cartName+" 3m ago (linked by Pacto shop/cart target)" {
		t.Errorf("line %q", got)
	}
	if got := p.ReplacedLine(now); got != cartName+" was replaced by cart-svc 3m ago (linked by Pacto shop/cart target)" {
		t.Errorf("replaced line %q", got)
	}
	i := Replacement{From: NewServiceKey(invoicesName), To: invoicesKey, Link: linkInferredFrom, By: anaSubject, Revision: "invoices@sha256:x"}
	want := "Declared by ana@acme.com from inferred " + invoicesName + " (revision invoices@sha256:x); Pacto read no workload for it here"
	if i.Line(now) != want || i.ReplacedLine(now) != want {
		t.Errorf("line %q", i.Line(now))
	}
	p.At = nil // never panics
	if got := p.Line(now); !strings.HasPrefix(got, "Replaces ") {
		t.Errorf("line %q", got)
	}
}

func TestFieldProvenanceLineForms(t *testing.T) {
	for _, tc := range []struct {
		f    FieldProvenance
		want string
	}{
		{FieldProvenance{Field: "dependencies[audit].compatibility", Value: "unknown", Proposed: "^1.4.0", Source: "deploy/app.yaml:3"}, "  audit range   unknown    proposed ^1.4.0"},
		{FieldProvenance{Field: "dependencies[audit].compatibility", Value: "unknown", Proposed: "^1.4.0", Source: "cluster:fleet reported 1.4.2"}, "  audit range   unknown    proposed ^1.4.0 (fleet)"},
		{FieldProvenance{Field: "image.digest[app]", Value: "sha256:ab", Provenance: FieldInferred, Source: "cluster:runtime pods"}, "  image.digest[app] sha256:ab  inferred from cluster:runtime pods"},
	} {
		if got := tc.f.Line(); got != tc.want {
			t.Errorf("line %q, want %q", got, tc.want)
		}
	}
	if parseAsOf("") != nil || sourceKind("deploy/app.yaml:1") != "" {
		t.Error("a field with no read time or no cluster source")
	}
	p := &FieldsPreview{Items: []FieldProvenance{{Field: "workload", Value: "service", Provenance: FieldInferred, Source: "x"}}, Count: 1, Total: 3}
	if got := p.Lines(); len(got) != 2 || got[1] != "  2 more" {
		t.Errorf("lines %q", got)
	}
}

// Every owner row carries when its source was read.
func TestFieldsOwnerRowAsOf(t *testing.T) {
	at := fixedNow().Add(-time.Hour)
	q := ownerProvenanceFleet(t, contract.Owner{}, payTarget("a", contract.Owner{Team: "pay"}))
	s := q.snap.Services[NewServiceKey("pay")]
	q.snap.Targets[s.Targets[0]].ReconciledAt = &at
	if rows := q.ownerRow(s, nil); len(rows) != 1 || rows[0].AsOf == nil || !rows[0].AsOf.Equal(at) {
		t.Errorf("via target rows %+v", rows)
	}
	a := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}}, nil)
	as := a.snap.Services[rInv]
	for _, rk := range as.Revisions {
		a.snap.Revisions[rk].FetchedAt = &at
	}
	if rows := a.ownerRow(as, nil); len(rows) != 1 || rows[0].Provenance != FieldAttested || rows[0].AsOf == nil || !rows[0].AsOf.Equal(at) {
		t.Errorf("attested rows %+v", rows)
	}
}
