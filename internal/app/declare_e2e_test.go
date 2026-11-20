package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// TestDeclareEndToEndFleetShowsDeclaration: ana's declaration, read back
// from the registry by the fleet with a checker that trusts the private
// Sigstore and an installation signer policy that lists ana's issuer, shows
// who declared it, the signature that names her and the cluster facts.
func TestDeclareEndToEndFleetShowsDeclaration(t *testing.T) {
	e := newDeclareEnv(t)
	t.Setenv("PACTO_INSECURE_REGISTRIES", e.host)
	e.login(personToken("ana@acme.com"))
	res := e.declare(t, e.req("ledger"))

	// The private Sigstore keeps no transparency log, so the reader requires
	// none, as the provenance tests do.
	oldRekor := rekorURL
	t.Cleanup(func() { rekorURL = oldRekor })
	rekorURL = ""
	pc := newProvenanceChecker(publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), e.host))
	pc.root, pc.rootAt = e.ss.Root, time.Now()
	trustingIn(pc, Installation{Signers: []delivery.PersonSigner{{Issuer: personIssuer, Subjects: `.+@acme\.com`}}}, nil)
	svc := NewService(oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.WithInsecureRegistries(e.host)), nil)
	snap, err := svc.Fleet(t.Context(), FleetOptions{OCIRefs: []string{"oci://" + e.host + "/acme/invoices:" + res.Tag}, Provenance: pc})
	if err != nil {
		t.Fatal(err)
	}
	wantDeclarationShown(t, snap)
}

// wantDeclarationShown: the invoices revision shows ana's checked
// declaration and its cluster facts, and nothing about it was dropped.
func wantDeclarationShown(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	q := fleet.NewQuery(snap)
	view, err := q.GetService("invoices")
	if err != nil || len(view.Revisions) != 1 {
		t.Fatalf("%v %+v", err, view)
	}
	detail, err := q.EntityDetail(fleet.KindRevision, string(view.Revisions[0].Key))
	if err != nil {
		t.Fatal(err)
	}
	r := detail.Revision
	want := fleet.DeclarationView{State: contract.DeclarationDeclared, SignatureChecked: true, InferredFrom: "k8s:billing/Deployment/invoices",
		Signer: &fleet.VerifiedSigner{Subject: "ana@acme.com", Issuer: personIssuer, Class: contract.SignerClassHuman}, Cluster: "3f1c-uid",
		Accepted: []string{"dependencies[ledger].compatibility ^2.3.0"}}
	got, _ := json.Marshal(r.Declaration)
	if exp, _ := json.Marshal(want); string(got) != string(exp) {
		t.Fatalf("declaration %s\nwant %s\nlimitations %+v", got, exp, snap.Limitations)
	}
	cluster := 0
	for _, f := range r.Inferred {
		if strings.HasPrefix(f.Source, "cluster:") {
			cluster++
		}
	}
	if cluster == 0 || len(r.InferredCoverage) == 0 {
		t.Fatalf("cluster facts were not shown: %+v %+v", r.Inferred, r.InferredCoverage)
	}
	for _, l := range snap.Limitations {
		if strings.HasPrefix(l.Code, "DECLARATION_") {
			t.Errorf("limitation %+v", l)
		}
	}
}
