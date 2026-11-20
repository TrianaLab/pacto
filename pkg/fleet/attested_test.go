package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	attSigner = "https://github.com/acme/invoices/.github/workflows/ci.yml@refs/heads/main"
	attIssuer = "https://token.actions.githubusercontent.com"
	attRepo   = "https://github.com/acme/invoices"
	attCommit = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
)

// attRecord is a record whose owner file names team at source, read at commit.
func attRecord(source, team, commit string) *contract.InferredRecord {
	file := source[:strings.LastIndexByte(source, ':')]
	return &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "4", Commit: commit,
			Sources: []contract.InferredSourceFile{{Path: file, Digest: "sha256:" + strings.Repeat("0", 64)}}},
		Fields: []contract.Inferred{{Field: "service.owner", Value: team, Source: source, Confidence: contract.ConfidenceLikely,
			Class: contract.InferredClassOwnerFile, Declare: "service:\n  owner:\n    team: " + team + "\n"}}}
}

// attProvenance is a verified CI signature an installation rule accepted.
func attProvenance(repo, commit string) *BundleProvenance {
	return &BundleProvenance{
		Signature: BundleSignature{Status: SignatureVerified, Source: "rule", Signer: attSigner, Issuer: attIssuer, SignerRepository: repo, SignerCommit: commit},
		Signers:   []VerifiedSigner{{Subject: attSigner, Issuer: attIssuer, Class: "ci"}},
	}
}

func TestAttestedOwnerTable(t *testing.T) {
	want := "CODEOWNERS:7 at a1b2c3d, signed by github.com/acme/invoices/.github/workflows/ci.yml@refs/heads/main"
	good := func() *ContractRevision {
		return &ContractRevision{Record: attRecord("repository:.github/CODEOWNERS:7", "payments", attCommit), Provenance: attProvenance(attRepo, attCommit)}
	}
	o, cite, repo, ok := AttestedOwner(good())
	if !ok || o.Team != "payments" || cite != want || repo != attRepo {
		t.Fatalf("attested = %+v %q %q %v", o, cite, repo, ok)
	}
	rows := map[string]func(r *ContractRevision){
		"no record":                        func(r *ContractRevision) { r.Record = nil },
		"no ownerFile class":               func(r *ContractRevision) { r.Record.Fields[0].Class = "" },
		"other field":                      func(r *ContractRevision) { r.Record.Fields[0].Field = "workload" },
		"no declare":                       func(r *ContractRevision) { r.Record.Fields[0].Declare = "" },
		"hand-edited class, no provenance": func(r *ContractRevision) { r.Provenance = nil },
		"unsigned":                         func(r *ContractRevision) { r.Provenance.Signature.Status = SignatureUnsigned },
		"unverified":                       func(r *ContractRevision) { r.Provenance.Signature.Status = SignatureUnverified },
		"unknown":                          func(r *ContractRevision) { r.Provenance.Signature.Status = SignatureUnknown },
		"source inferred":                  func(r *ContractRevision) { r.Provenance.Signature.Source = "inferred" },
		"new-signer": func(r *ContractRevision) {
			r.Provenance.Signature.Status, r.Provenance.Signature.Source = SignatureUnverified, "none"
		},
		"person class":        func(r *ContractRevision) { r.Provenance.Signers[0].Class = "human" },
		"agent class":         func(r *ContractRevision) { r.Provenance.Signers[0].Class = "agent" },
		"signer not listed":   func(r *ContractRevision) { r.Provenance.Signers = nil },
		"commit mismatch":     func(r *ContractRevision) { r.Record.Inputs.Commit = strings.Repeat("f", 40) },
		"empty record commit": func(r *ContractRevision) { r.Record.Inputs.Commit = "" },
		"empty signer commit": func(r *ContractRevision) {
			r.Provenance.Signature.SignerCommit, r.Record.Inputs.Commit = "", ""
		},
		"empty repository": func(r *ContractRevision) { r.Provenance.Signature.SignerRepository = "" },
	}
	for name, mutate := range rows {
		r := good()
		mutate(r)
		if o, cite, repo, ok := AttestedOwner(r); ok || !o.IsEmpty() || cite != "" || repo != "" {
			t.Errorf("%s: attested %+v %q %q", name, o, cite, repo)
		}
	}
	for _, src := range []string{"pin", "policy"} {
		r := good()
		r.Provenance.Signature.Source = src
		if _, _, _, ok := AttestedOwner(r); !ok {
			t.Errorf("source %s: not attested", src)
		}
	}
}

func TestAttestedOwnerFromCatalogInfo(t *testing.T) {
	r := &ContractRevision{Record: attRecord("catalog-info.yaml:7", "team-growth", attCommit), Provenance: attProvenance(attRepo, attCommit)}
	if o, cite, _, ok := AttestedOwner(r); !ok || o.Team != "team-growth" || !strings.HasPrefix(cite, "catalog-info.yaml:7 at a1b2c3d, signed by github.com/") {
		t.Errorf("attested = %+v %q %v", o, cite, ok)
	}
}

// attRev shapes one invoices revision of attestedFleet.
type attRev struct {
	version, declared, attested, repo string
}

// attestedFleet builds the service invoices from revs; each target scope
// runs the revision of the version it names, linked by digest.
func attestedFleet(t *testing.T, revs []attRev, targets map[string]string) *Query {
	t.Helper()
	var raw []RawRevision
	digests := map[string]string{}
	for i, r := range revs {
		c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "invoices", Version: r.version}}
		if r.declared != "" {
			c.Service.Owner = contract.Owner{Team: r.declared}
		}
		d := fmt.Sprintf("sha256:inv%d", i)
		digests[r.version] = d
		rr := RawRevision{Digest: d, Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}}
		if r.attested != "" {
			rr.Bundle.FS = fstest.MapFS{contract.InferredRecordFile: {Data: attRecord("repository:.github/CODEOWNERS:7", r.attested, attCommit).Marshal()}}
			rr.Provenance = attProvenance(r.repo, attCommit)
		}
		raw = append(raw, rr)
	}
	var raws []RawTarget
	for _, scope := range slices.Sorted(func(yield func(string) bool) {
		for k := range targets {
			if !yield(k) {
				return
			}
		}
	}) {
		raws = append(raws, RawTarget{Scope: scope, Kind: SourceKindKubernetes, Name: "invoices-" + scope, Service: "invoices",
			Digest: digests[targets[scope]], Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())})
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("oci", "oci", &Collection{Revisions: raw, Targets: raws}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

var rInv = NewServiceKey("invoices")

func invoices(q *Query) *ServiceRecord { return q.snap.Services[rInv] }

func TestDeriveOwnerDeclaredBeatsAttested(t *testing.T) {
	s := invoices(attestedFleet(t, []attRev{{version: "1.0.0", declared: "billing"}, {version: "2.0.0", attested: "payments", repo: attRepo}}, nil))
	if s.Owner.Team != "billing" || s.OwnerProvenance != OwnerProvenanceDeclared || s.OwnerSource != "" {
		t.Errorf("owner = %+v %s %q", s.Owner, s.OwnerProvenance, s.OwnerSource)
	}
}

func TestDeriveOwnerAttestedBeatsTarget(t *testing.T) {
	q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}}, map[string]string{"eu": "1.0.0"})
	s := invoices(q)
	if s.Owner.Team != "payments" || s.OwnerProvenance != OwnerProvenanceAttested || !strings.HasPrefix(s.OwnerSource, "CODEOWNERS:7 at a1b2c3d") {
		t.Errorf("owner = %+v %s %q", s.Owner, s.OwnerProvenance, s.OwnerSource)
	}
}

func TestDeriveOwnerNewestAttestedRevision(t *testing.T) {
	s := invoices(attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}, {version: "2.0.0", attested: "billing", repo: attRepo}}, nil))
	if s.Owner.Team != "billing" || s.OwnerProvenance != OwnerProvenanceAttested {
		t.Errorf("owner = %+v %s", s.Owner, s.OwnerProvenance)
	}
}

func TestOwnerClaimsAttestedTier(t *testing.T) {
	q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}}, nil)
	if n, key := q.ownershipState(invoices(q)); n != 1 || key != "team:payments" {
		t.Errorf("claims = %d %q", n, key)
	}
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if agg.Ownership.Unowned != 0 || agg.Ownership.Consistent != 1 {
		t.Errorf("ownership = %+v", agg.Ownership)
	}
	d, err := q.EntityDetail(KindOwner, "team:payments")
	if err != nil || d.Owner.Services.Total != 1 || d.Owner.Revisions.Total != 1 {
		t.Fatalf("owner page = %+v, %v", d, err)
	}
	res, err := q.Search(SearchFilter{Owner: "payments"})
	if err != nil || res.Total != 1 {
		t.Errorf("owner=payments = %+v, %v", res, err)
	}
}

func TestAttestedConflict(t *testing.T) {
	q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}, {version: "2.0.0", attested: "billing", repo: attRepo}},
		map[string]string{"eu": "1.0.0", "us": "2.0.0"})
	if m := ownerConflictMessages(q); len(m) != 1 || m[0] != "targets of invoices run revisions that declare different owners" {
		t.Errorf("conflicts = %q", m)
	}
	sentence := "attested revisions of invoices were signed from different repositories"
	for name, other := range map[string]string{"another repository of the org signs this service": "payments", "two attested repositories disagree": "billing"} {
		q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}, {version: "2.0.0", attested: other, repo: "https://github.com/acme/other"}}, nil)
		s := invoices(q)
		if !s.Owner.IsEmpty() || s.OwnerProvenance != "" || s.OwnerSource != "" {
			t.Errorf("%s: owner = %+v %s", name, s.Owner, s.OwnerProvenance)
		}
		if m := ownerConflictMessages(q); len(m) != 1 || m[0] != sentence {
			t.Errorf("%s: conflicts = %q", name, m)
		}
		own := serviceOwnership(q.snap, s)
		if own.ConflictReason != sentence || own.Conflicts.Total != 2 || !strings.Contains(strings.Join(own.Conflicts.Items, ","), "payments (Team)") ||
			!strings.Contains(strings.Join(own.Conflicts.Items, ","), other+" (Team)") {
			t.Errorf("%s: preview = %+v", name, own)
		}
		if b, _ := q.ownershipBucket(s); b != OwnershipConflicting {
			t.Errorf("%s: the tally counts it %s, the limitation says conflicting", name, b)
		}
	}
}

func TestOwnerRevisionAttested(t *testing.T) {
	q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}, {version: "2.0.0"}}, nil)
	ref := q.ownerRevision(invoices(q))
	if ref == nil || !strings.Contains(ref.Key, "sha256:inv0") {
		t.Errorf("owner revision = %+v", ref)
	}
}

// A golden path shows a restricted caller only its template revisions: the
// owner its older, attested revision gave it goes with that revision.
func TestRestrictAttestedOwnerFromHiddenRevision(t *testing.T) {
	path := templated("http-service", "1.0.0")
	path.PactoVersion, path.Service.Owner = "2.5", contract.Owner{}
	old := usesPostgres("http-service", "0.9.0")
	old.PactoVersion, old.Service.Owner = "2.5", contract.Owner{}
	oldRev := ociRev(acme, old, fstest.MapFS{contract.InferredRecordFile: {Data: attRecord("repository:.github/CODEOWNERS:7", "payments", attCommit).Marshal()}})
	oldRev.Provenance = attProvenance(attRepo, attCommit)
	k := SourceKindKubernetes
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{ociRev(acme, path, pathFiles), oldRev, postgres()}}),
		NewMemorySource("k8s", k, &Collection{Targets: []RawTarget{{Scope: "shop", Kind: k, Name: "postgresql", Service: "postgresql", Domain: acme, Compliance: StatusCompliant}}}))
	if err != nil {
		t.Fatal(err)
	}
	q, key := NewQuery(snap), NewServiceKeyDomain(acme, "http-service")
	if s := q.snap.Services[key]; s.OwnerProvenance != OwnerProvenanceAttested || s.OwnerSource == "" {
		t.Fatalf("unrestricted owner = %+v %s %q", s.Owner, s.OwnerProvenance, s.OwnerSource)
	}
	s := q.Restrict(shopOnly, nil).snap.Services[key]
	if s == nil || !s.Owner.IsEmpty() || s.OwnerSource != "" || s.OwnerProvenance != "" {
		t.Errorf("restricted owner = %+v", s)
	}
}

func TestOwnershipInfoSource(t *testing.T) {
	q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}}, nil)
	d, err := q.EntityDetail(KindService, string(rInv))
	if err != nil {
		t.Fatal(err)
	}
	o := d.Service.Ownership
	if !o.Declared || o.Provenance != OwnerProvenanceAttested || !strings.HasPrefix(o.Source, "CODEOWNERS:7 at a1b2c3d, signed by ") {
		t.Errorf("ownership = %+v", o)
	}
}

func TestOwnerSourceJSON(t *testing.T) {
	b, _ := json.Marshal(ServiceRecord{OwnerSource: "CODEOWNERS:7"})
	if !strings.Contains(string(b), `"ownerSource":"CODEOWNERS:7"`) {
		t.Errorf("json = %s", b)
	}
	b, _ = json.Marshal(ServiceRecord{})
	if strings.Contains(string(b), "ownerSource") {
		t.Errorf("empty source serialized: %s", b)
	}
	b, _ = json.Marshal(OwnershipInfo{Source: "x"})
	if !strings.Contains(string(b), `"source":"x"`) {
		t.Errorf("ownership json = %s", b)
	}
}

// A dangling revision key or no service claims nothing.
func TestOwnerTierSkipsMissingRevisions(t *testing.T) {
	q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}}, nil)
	s := *invoices(q)
	s.Revisions = append([]RevisionKey{"invoices@sha256:gone"}, s.Revisions...)
	if tier, repos := ownerTier(q.snap, &s); tier != tierAttested || len(repos) != 1 {
		t.Errorf("tier = %v %v", tier, repos)
	}
	if l := ownerBlockedLimitations(q.snap, &s); len(l) != 0 {
		t.Errorf("limitations = %+v", l)
	}
	if q.ownerClaims(nil, func(contract.Owner) bool { return true }) {
		t.Error("no service has a claim")
	}
}

// A refused verdict on a visible revision was judged against the owner a
// hidden revision attested: the restricted copy names neither that owner nor
// its commit or repository, and keeps the code.
func TestRestrictOwnerRightMessageFromHiddenRevision(t *testing.T) {
	path := templated("http-service", "1.0.0")
	path.PactoVersion, path.Service.Owner = "2.5", contract.Owner{Team: "billing"}
	old := usesPostgres("http-service", "0.9.0")
	old.PactoVersion, old.Service.Owner = "2.5", contract.Owner{}
	oldRev := ociRev(acme, old, fstest.MapFS{contract.InferredRecordFile: {Data: attRecord("repository:.github/CODEOWNERS:7", "payments", attCommit).Marshal()}})
	oldRev.Provenance = attProvenance(attRepo, attCommit)
	pathRev := ociRev(acme, path, pathFiles)
	leak := "service.owner is attested as team payments (CODEOWNERS:7 at " + attCommit[:7] + ", signed by " + attRepo + "); only payments members may set it"
	pathRev.Provenance = &BundleProvenance{Signature: BundleSignature{Status: SignatureVerified},
		OwnerRight: &OwnerRightCheck{Code: "OWNER_SIGNER_NOT_ALLOWED", Message: leak}}
	k := SourceKindKubernetes
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{pathRev, oldRev, postgres()}}),
		NewMemorySource("k8s", k, &Collection{Targets: []RawTarget{{Scope: "shop", Kind: k, Name: "postgresql", Service: "postgresql", Domain: acme, Compliance: StatusCompliant}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	var full *ContractRevision
	for _, r := range q.snap.Revisions {
		if r.Service == "http-service" && r.Version == "1.0.0" {
			full = r
		}
	}
	if full == nil || full.Provenance.OwnerRight.Message != leak {
		t.Fatalf("unrestricted revision = %+v", full)
	}
	r := q.Restrict(shopOnly, nil).snap.Revisions[full.Key]
	if r == nil || r.Provenance == nil || r.Provenance.OwnerRight == nil {
		t.Fatalf("restricted revision = %+v", r)
	}
	if got := r.Provenance.OwnerRight; got.Code != "OWNER_SIGNER_NOT_ALLOWED" || got.Allowed {
		t.Errorf("verdict = %+v", got)
	}
	for _, s := range []string{"payments", attCommit[:7], attRepo, "CODEOWNERS"} {
		if strings.Contains(r.Provenance.OwnerRight.Message, s) {
			t.Errorf("restricted message %q names %q", r.Provenance.OwnerRight.Message, s)
		}
	}
	if full.Provenance.OwnerRight.Message != leak {
		t.Error("restriction changed the unrestricted snapshot")
	}
}

// TestInferredOwnerRowAttested: the revision page's inferred owner row says
// the owner is attested when the revision's signature attests it, so the
// service page and the revision page agree; an unattested row stays plain.
func TestInferredOwnerRowAttested(t *testing.T) {
	q := attestedFleet(t, []attRev{{version: "1.0.0", attested: "payments", repo: attRepo}, {version: "2.0.0", attested: "payments"}}, nil)
	for rk, r := range q.snap.Revisions {
		d, err := q.EntityDetail(KindRevision, string(rk))
		if err != nil {
			t.Fatal(err)
		}
		want := r.Version == "1.0.0"
		var rows int
		for _, f := range d.Revision.Inferred {
			if f.Class == contract.InferredClassOwnerFile {
				rows++
				if f.Attested != want {
					t.Errorf("%s: attested = %v, want %v", r.Version, f.Attested, want)
				}
			}
		}
		if rows != 1 {
			t.Errorf("%s: %d owner rows", r.Version, rows)
		}
	}
	for _, r := range q.snap.Revisions {
		if items := q.inferredPreview(r).Items; len(items) != 1 || items[0].Attested != (r.Version == "1.0.0") {
			t.Errorf("%s: service preview = %+v", r.Version, items)
		}
	}
}
