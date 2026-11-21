package fleet

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	anaSubject = "ana@acme.com"
	anaIssuer  = "https://accounts.google.com"
)

var ana = contract.InferredSigner{Subject: anaSubject, Issuer: anaIssuer, Class: contract.SignerClassHuman}

// anaPolicy allows every acme.com person of anaIssuer.
func anaPolicy() *DeclarationSignerPolicy {
	return &DeclarationSignerPolicy{Signers: []PolicySigner{{Issuer: anaIssuer, Subjects: `[^@]+@acme\.com`}}}
}

// anaSigned is a provenance whose verified signers include ana, a person.
func anaSigned() *BundleProvenance {
	return &BundleProvenance{Signature: BundleSignature{Status: SignatureUnverified}, Source: "oci",
		Signers: []VerifiedSigner{{Subject: anaSubject, Issuer: anaIssuer, Class: contract.SignerClassHuman}}}
}

// declaredRecord is a declaration of newsletter by ana from the inferred
// service news/Deployment/newsletter: one folder field, a cluster runtime
// dependency and an accepted proposal, coverage with one partial source and
// hidden counts.
func declaredRecord(t *testing.T, state string, asker contract.InferredSigner) []byte {
	t.Helper()
	b := clusterRecordOf(t, true, contract.Inferred{Field: "workload", Value: "service", Source: "deploy/app.yaml:1", Confidence: contract.ConfidenceLikely},
		clusterRuntimeDep(), clusterProposal())
	rec, err := contract.ParseInferredRecord(b)
	if err != nil {
		t.Fatal(err)
	}
	rec.Coverage[0] = contract.InferredCoverage{Source: rec.Coverage[0].Source, State: contract.CoveragePartial, Reason: "forbidden"}
	rec.Hidden = &contract.InferredHidden{Services: 2, Callers: 1}
	rec.Declaration = &contract.InferredDeclaration{State: state, Asker: asker, InferredFrom: K8sCaller("", "news", "Deployment", "newsletter"),
		Cluster:  "3f1c",
		Baseline: &contract.InferredBaseline{Version: "1.4.0", Bundle: "ghcr.io/acme/newsletter@sha256:" + strings.Repeat("a", 64)}}
	if state == contract.DeclarationDeclared {
		rec.Declaration.Accepted = []contract.InferredAcceptance{{Field: "dependencies[notification-worker].compatibility", Range: "^2.3.0"}}
	}
	out := rec.Marshal()
	if _, err := contract.ParseInferredRecord(out); err != nil {
		t.Fatalf("the declared fixture must parse: %v", err)
	}
	return out
}

// declFleetOpts shapes declFleet.
type declFleetOpts struct {
	record  []byte
	prov    *BundleProvenance
	policy  *DeclarationSignerPolicy
	targets bool // both run in shop, a namespace other than the declaration's
	// workerNS is where notification-worker runs with targets, shop when "".
	workerNS string
}

// declFleet is newsletter (declared, from a registry) and notification-worker.
func declFleet(t *testing.T, o declFleetOpts) *Query {
	t.Helper()
	news := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "newsletter", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	worker := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "notification-worker", Version: "2.3.1", Owner: contract.Owner{Team: "t"}}}
	fsys := fstest.MapFS{"deploy/app.yaml": {Data: []byte(recordManifest)}, contract.InferredRecordFile: {Data: o.record}}
	col := &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: news, FS: fsys}, Digest: "sha256:news", Provenance: o.prov},
		{Bundle: &contract.Bundle{Contract: worker, FS: fstest.MapFS{}}, Digest: "sha256:worker"},
	}}
	if o.targets {
		k := SourceKindKubernetes
		col.Targets = []RawTarget{
			{Scope: "shop", Kind: k, Name: "newsletter", Service: "newsletter", Compliance: StatusCompliant},
			{Scope: cmp.Or(o.workerNS, "shop"), Kind: k, Name: "notification-worker", Service: "notification-worker", Compliance: StatusCompliant},
		}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, DeclarationSigners: o.policy}, NewMemorySource("oci", "oci", col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func signedOpts(t *testing.T) declFleetOpts {
	return declFleetOpts{record: declaredRecord(t, contract.DeclarationDeclared, ana), prov: anaSigned(), policy: anaPolicy()}
}

// newsRevision is newsletter's revision detail in q.
func newsRevision(t *testing.T, q *Query) *RevisionDetailData {
	t.Helper()
	d, err := q.EntityDetail(KindRevision, revKeyOf(t, q, "newsletter"))
	if err != nil {
		t.Fatal(err)
	}
	return d.Revision
}

// clusterFields counts the cluster-sourced fields of a detail.
func clusterFields(d *RevisionDetailData) int {
	n := 0
	for _, f := range d.Inferred {
		if strings.HasPrefix(f.Source, "cluster:") {
			n++
		}
	}
	return n
}

func TestBuildAcceptsSignedDeclarationClusterFacts(t *testing.T) {
	q := declFleet(t, signedOpts(t))
	s := q.SnapshotForSerialization()
	if s.Completeness != CompletenessComplete {
		t.Errorf("completeness %s, limitations %+v", s.Completeness, s.Limitations)
	}
	d := newsRevision(t, q)
	if clusterFields(d) != 2 || len(d.InferredCoverage) != len(contract.CoverageSources) {
		t.Fatalf("cluster facts were not shown: %+v %+v", d.Inferred, d.InferredCoverage)
	}
	if inf := newsletterEdges(s)[ProvenanceInferred]; len(inf) != 1 || ConsumesEdge(inf[0]) {
		t.Errorf("the accepted runtime dependency is one inferred, non-consuming edge: %+v", inf)
	}
}

func TestRevisionDetailCarriesCoverageAndDeclaration(t *testing.T) {
	d := newsRevision(t, declFleet(t, signedOpts(t)))
	var proposal InferredField
	for _, f := range d.Inferred {
		if f.Proposed != "" {
			proposal = f
		}
	}
	if proposal.Proposed != "^2.3.0" || proposal.AsOf != "2026-11-14T09:58:12Z" || proposal.Completeness != contract.CoverageComplete {
		t.Errorf("proposal field = %+v", proposal)
	}
	if d.InferredCoverage[0].State != contract.CoveragePartial || d.InferredCoverage[0].Reason != "forbidden" {
		t.Errorf("coverage = %+v", d.InferredCoverage)
	}
	if d.InferredHidden == nil || *d.InferredHidden != (InferredHiddenCounts{Services: 2, Callers: 1}) {
		t.Errorf("hidden = %+v", d.InferredHidden)
	}
	want := DeclarationView{State: contract.DeclarationDeclared, Signer: &VerifiedSigner{Subject: anaSubject, Issuer: anaIssuer, Class: contract.SignerClassHuman},
		SignatureChecked: true, InferredFrom: "k8s:news/Deployment/newsletter", Cluster: "3f1c", Baseline: "1.4.0",
		Accepted: []string{"dependencies[notification-worker].compatibility ^2.3.0"}}
	got, _ := json.Marshal(d.Declaration)
	if exp, _ := json.Marshal(want); string(got) != string(exp) {
		t.Errorf("declaration = %s\nwant %s", got, exp)
	}
}

// wantClusterFactsDropped: folder fields stay, every cluster fact is gone,
// the snapshot is partial and names code.
func wantClusterFactsDropped(t *testing.T, q *Query, code string) *RevisionDetailData {
	t.Helper()
	s := q.SnapshotForSerialization()
	if !hasLimitation(s.Limitations, code) || s.Completeness != CompletenessPartial {
		t.Errorf("completeness %s, limitations %+v", s.Completeness, s.Limitations)
	}
	d := newsRevision(t, q)
	if clusterFields(d) != 0 || d.InferredCoverage != nil || d.InferredHidden != nil || len(d.Inferred) != 1 || d.Inferred[0].Field != "workload" {
		t.Errorf("cluster facts reached the detail: %+v %+v %+v", d.Inferred, d.InferredCoverage, d.InferredHidden)
	}
	if d.Declaration == nil {
		t.Fatal("the declaration line must stay when its cluster facts are dropped")
	}
	if n := len(newsletterEdges(s)[ProvenanceInferred]); n != 0 {
		t.Errorf("a dropped cluster dependency projected %d edges", n)
	}
	return d
}

func TestUnverifiedDeclarationDropsClusterFacts(t *testing.T) {
	o := signedOpts(t)
	o.prov.Signers[0].Subject = "mallory@acme.com"
	wantClusterFactsDropped(t, declFleet(t, o), LimitationDeclarationSignerUnverified)
}

func TestNoProvenanceNoClusterFacts(t *testing.T) {
	o := signedOpts(t)
	o.prov = nil
	wantClusterFactsDropped(t, declFleet(t, o), LimitationDeclarationSignerUnverified)
}

func TestNonHumanSignerDropsClusterFacts(t *testing.T) {
	o := signedOpts(t)
	o.prov.Signers[0].Class = contract.SignerClassCI
	wantClusterFactsDropped(t, declFleet(t, o), LimitationDeclarationSignerUnverified)
}

func TestDeclarationSignerNotInInstallationPolicyDropped(t *testing.T) {
	for name, pol := range map[string]*DeclarationSignerPolicy{
		"other issuer":  {Signers: []PolicySigner{{Issuer: "https://login.example", Subjects: ".*"}}},
		"other subject": {Signers: []PolicySigner{{Issuer: anaIssuer, Subjects: `[^@]+@example\.com`}}},
		"bad regexp":    {Signers: []PolicySigner{{Issuer: anaIssuer, Subjects: `(`}}},
	} {
		t.Run(name, func(t *testing.T) {
			o := signedOpts(t)
			o.policy = pol
			d := wantClusterFactsDropped(t, declFleet(t, o), LimitationDeclarationSignerNotAllowed)
			if !d.Declaration.SignatureChecked {
				t.Error("the signer matched, so the signature was checked")
			}
			wantNotAccepted(t, d)
		})
	}
}

func TestUnreadSignerPolicyMarksPartial(t *testing.T) {
	for name, pol := range map[string]*DeclarationSignerPolicy{"unread": {Unread: "forbidden"}, "none given": nil} {
		t.Run(name, func(t *testing.T) {
			o := signedOpts(t)
			o.policy = pol
			wantNotAccepted(t, wantClusterFactsDropped(t, declFleet(t, o), LimitationDeclarationSignerNotAllowed))
		})
	}
}

// wantNotAccepted: a declarer the reader does not accept is never named as
// the signer, only claimed, and the view says why.
func wantNotAccepted(t *testing.T, d *RevisionDetailData) {
	t.Helper()
	if v := d.Declaration; v.Signer != nil || v.ClaimedSubject != anaSubject || v.NotAccepted == "" {
		t.Errorf("a declarer the policy refuses was named: %+v", v)
	}
}

// Even with no cluster fact to drop, a signer the policy refuses is never
// named as the declarer.
func TestDeclarationWithoutClusterFactsNotAllowedOmitsSigner(t *testing.T) {
	rec, err := contract.ParseInferredRecord(newsletterRecord())
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana}
	d := newsRevision(t, declFleet(t, declFleetOpts{record: rec.Marshal(), prov: anaSigned()}))
	wantNotAccepted(t, d)
	if !d.Declaration.SignatureChecked || d.Declaration.NotAccepted != noSignerPolicy {
		t.Errorf("declaration = %+v", d.Declaration)
	}
}

func TestBoundSignersKeepsTheDeclarer(t *testing.T) {
	o := signedOpts(t)
	for _, sub := range []string{"a1", "a2", "a3", "a4", "a5", "a6"} {
		o.prov.Signers = append([]VerifiedSigner{{Subject: sub + "@gmail.com", Issuer: anaIssuer, Class: contract.SignerClassHuman}}, o.prov.Signers...)
	}
	q := declFleet(t, o)
	d := newsRevision(t, q)
	if clusterFields(d) != 2 || d.Declaration.Signer == nil {
		t.Errorf("extra signatures pushed the declarer out: %+v", d.Declaration)
	}
	if n := len(q.SnapshotForSerialization().Revisions[RevisionKey(revKeyOf(t, q, "newsletter"))].Provenance.Signers); n != MaxProvenanceSigners {
		t.Errorf("signers = %d, want the bound", n)
	}
	boundSigners(nil, nil)
}

func TestRestrictHidesDeclarationOfUnlistableWorkloadKind(t *testing.T) {
	q := declFleet(t, signedOpts(t))
	if sc := q.InferredWorkloadScopes(); !slices.Contains(sc, WorkloadScope{Namespace: "news", Kind: "Deployment"}) {
		t.Fatalf("the declaration's workload is not reviewed: %v", sc)
	}
	r := q.Restrict(anyCluster(func(string) bool { return true }), anyClusterKind(func(ns, kind string) bool { return ns != "news" || kind != "Deployment" }))
	if r == q {
		t.Fatal("a workload kind the viewer may not list must restrict the view")
	}
	wantInferredFromHidden(t, r)
}

func TestRestrictHidesClusterDependencyOnHiddenService(t *testing.T) {
	o := signedOpts(t)
	o.targets, o.workerNS = true, "internal"
	r := declFleet(t, o).Restrict(anyCluster(func(ns string) bool { return ns != "internal" }), anyClusterKind(func(string, string) bool { return true }))
	d := newsRevision(t, r)
	if d.Declaration.InferredFromHidden || len(d.Inferred) != 1 || d.InferredHidden == nil || d.InferredHidden.Fields != 2 {
		t.Errorf("the hidden service's cluster facts reached the viewer: %+v %+v", d.Inferred, d.InferredHidden)
	}
	if db, _ := json.Marshal(d); strings.Contains(string(db), "2.3.1") {
		t.Errorf("the hidden service's versions leaked: %s", db)
	}
}

func TestRevisionDetailUnverifiedDeclarationOmitsSigner(t *testing.T) {
	o := signedOpts(t)
	o.prov = nil
	d := newsRevision(t, declFleet(t, o))
	if d.Declaration.Signer != nil || d.Declaration.SignatureChecked || d.Declaration.ClaimedSubject != anaSubject {
		t.Errorf("declaration = %+v", d.Declaration)
	}
}

func TestBuildIgnoresDraftRecord(t *testing.T) {
	o := signedOpts(t)
	o.record = declaredRecord(t, contract.DeclarationDraft, contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent})
	s := declFleet(t, o).SnapshotForSerialization()
	if s.Services[rNews] != nil || !hasLimitation(s.Limitations, LimitationDeclarationDraftIgnored) || s.Completeness != CompletenessComplete {
		t.Errorf("a draft is no version: services %v, completeness %s, limitations %+v", s.Services, s.Completeness, s.Limitations)
	}
}

// A declaration with no cluster fact keeps its record whoever signed it.
func TestDeclarationWithoutClusterFactsKept(t *testing.T) {
	rec, err := contract.ParseInferredRecord(newsletterRecord())
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration = &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana}
	q := declFleet(t, declFleetOpts{record: rec.Marshal()})
	if s := q.SnapshotForSerialization(); s.Completeness != CompletenessComplete {
		t.Errorf("completeness %s, limitations %+v", s.Completeness, s.Limitations)
	}
	if d := newsRevision(t, q); len(d.Inferred) != 4 || d.Declaration.SignatureChecked || d.Declaration.Baseline != "" || d.InferredHidden != nil {
		t.Errorf("detail = %+v %+v", d.Inferred, d.Declaration)
	}
}

func TestDeclarationNamesDraft(t *testing.T) {
	rec, err := contract.ParseInferredRecord(declaredRecord(t, contract.DeclarationDeclared, ana))
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration.Draft = &contract.InferredDraft{Tag: "draft-3", Digest: "ghcr.io/acme/newsletter@sha256:" + strings.Repeat("b", 64),
		Asker: contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}}
	o := signedOpts(t)
	o.record = rec.Marshal()
	if d := newsRevision(t, declFleet(t, o)); d.Declaration.Draft != "draft-3" {
		t.Errorf("draft = %q", d.Declaration.Draft)
	}
}

// restrictNews is q as a viewer who may read every namespace but news.
func restrictNews(q *Query) *Query {
	return q.Restrict(anyCluster(func(ns string) bool { return ns != "news" }), anyClusterKind(func(string, string) bool { return true }))
}

// wantInferredFromHidden: no inferredFrom, cluster field or coverage, the
// cluster-field count instead, and no source text naming news.
func wantInferredFromHidden(t *testing.T, r *Query) {
	t.Helper()
	d := newsRevision(t, r)
	if d.Declaration == nil || d.Declaration.InferredFrom != "" || !d.Declaration.InferredFromHidden {
		t.Fatalf("declaration = %+v", d.Declaration)
	}
	if clusterFields(d) != 0 || d.InferredCoverage != nil || d.InferredHidden == nil || *d.InferredHidden != (InferredHiddenCounts{Fields: 2}) {
		t.Errorf("cluster facts reached a viewer who may not read news: %+v %+v %+v", d.Inferred, d.InferredCoverage, d.InferredHidden)
	}
	b, _ := json.Marshal(r.SnapshotForSerialization())
	db, _ := json.Marshal(d)
	for _, out := range [][]byte{b, db} {
		if strings.Contains(string(out), "news/Deployment") {
			t.Errorf("the hidden namespace leaked in a source: %s", out)
		}
	}
}

func TestRestrictHidesInferredFromOfHiddenNamespace(t *testing.T) {
	o := signedOpts(t)
	o.targets = true
	wantInferredFromHidden(t, restrictNews(declFleet(t, o)))
}

func TestRestrictedViewerSeesNoForeignNamespaceInClusterSource(t *testing.T) {
	o := signedOpts(t)
	q := declFleet(t, o)
	if inf := newsletterEdges(q.SnapshotForSerialization())[ProvenanceInferred]; len(inf) != 1 || !strings.Contains(inf[0].InferredSources[0].Where, "news/") {
		t.Fatalf("the unrestricted edge must name its cluster source: %+v", inf)
	}
	r := restrictNews(q)
	if n := len(newsletterEdges(r.SnapshotForSerialization())[ProvenanceInferred]); n != 0 {
		t.Errorf("an edge read from the hidden namespace's cluster reached the viewer: %d", n)
	}
	wantInferredFromHidden(t, r)
}

func TestRestrictHidesInferredFromWithNoTargetInNamespace(t *testing.T) {
	q := declFleet(t, signedOpts(t)) // no Kubernetes target or source anywhere
	if got := namespaceNames(q.Namespaces()); len(got) != 1 || got[0] != "news" {
		t.Fatalf("Namespaces = %v, want the declaration's", got)
	}
	r := restrictNews(q)
	if r == q {
		t.Fatal("a hidden declaration namespace must restrict the view")
	}
	wantInferredFromHidden(t, r)
}

func TestRestrictDoesNotMutateSharedRevision(t *testing.T) {
	q := declFleet(t, signedOpts(t))
	_ = newsRevision(t, restrictNews(q))
	d := newsRevision(t, q)
	if d.Declaration.InferredFrom == "" || d.Declaration.InferredFromHidden || clusterFields(d) != 2 || d.InferredCoverage == nil {
		t.Errorf("Restrict changed the shared revision: %+v", d.Declaration)
	}
}

func TestRestrictVisibleDeclarationNamespaceUnchanged(t *testing.T) {
	q := declFleet(t, signedOpts(t))
	if q.Restrict(anyCluster(func(string) bool { return true }), anyClusterKind(func(string, string) bool { return true })) != q {
		t.Error("a viewer who may read news sees the snapshot unchanged")
	}
}

// A declaration whose cluster facts name no hidden service is kept as is.
func TestHideClusterDependenciesKeepsUnrelatedFields(t *testing.T) {
	q := declFleet(t, signedOpts(t))
	rev := q.SnapshotForSerialization().Revisions[RevisionKey(revKeyOf(t, q, "newsletter"))]
	if hideClusterDependencies(q.SnapshotForSerialization(), rev, map[ServiceKey]bool{"elsewhere": true}) != nil {
		t.Error("a clone without any hidden dependency")
	}
}
