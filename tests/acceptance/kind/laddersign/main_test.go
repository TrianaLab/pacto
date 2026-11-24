package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// The record the kind scenario writes is one the promoter reads as it does:
// listed on the cluster's ladder stream, verified under the root the command
// wrote as the person it names, confirming the declared chain and the
// proposed rung as a leaf bound to the proposal digest.
func TestRunWritesWhatThePromoterReads(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	repoPath := strings.TrimPrefix(srv.URL, "http://") + "/acme/pacto-delivery-approvals"
	proposal := delivery.LadderProposal{{Name: "preview", Source: "namespaces *-preview, 2 found", Provenance: delivery.RungInferred}}
	view := delivery.EnvironmentsView{
		Declared: []delivery.LadderRung{{Name: "staging", Provenance: delivery.RungDeclared},
			{Name: "production", Upstream: "staging", Provenance: delivery.RungDeclared}},
		Proposal: proposal, ProposalDigest: delivery.ProposalDigest(proposal), Repository: repoPath}
	dir := t.TempDir()
	viewFile, rootFile := filepath.Join(dir, "view.json"), filepath.Join(dir, "trusted_root.json")
	b, _ := json.Marshal(view)
	if err := os.WriteFile(viewFile, b, 0o600); err != nil {
		t.Fatal(err)
	}
	req := request{root: rootFile, view: viewFile, cluster: "cluster-uid", email: "alice@acme.test", issuer: "https://idp.acme.test"}

	var out bytes.Buffer
	if err := run(t.Context(), testutil.New(t), req, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "confirmed 3 rungs of cluster-uid as alice@acme.test; recorded ") {
		t.Errorf("output %q", out.String())
	}
	repo, _ := ocireferrer.Open(repoPath, ocireferrer.Options{})
	recs, cut, _, err := (&deliveryrecord.Reader{}).ListLadder(t.Context(), repo, "cluster-uid")
	if err != nil || cut || len(recs) != 1 {
		t.Fatalf("ListLadder: %d records, cut %v, %v", len(recs), cut, err)
	}
	root, _ := os.ReadFile(rootFile)
	var signer delivery.PublisherIdentity
	st, at, err := delivery.OpenLadderRecord(recs[0].Bundle, func(b []byte) (time.Time, error) {
		id, t, err := signing.VerifyPerson(b, req.issuer, string(root))
		signer = id
		return t, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Predicate.Check(at); err != nil {
		t.Fatal(err)
	}
	p := st.Predicate
	if signer.Subject != req.email || p.Signer != signer || p.ClusterID != "cluster-uid" || p.ProposalDigest != view.ProposalDigest {
		t.Fatalf("record %+v signed by %+v", p, signer)
	}
	rungs, _, _ := delivery.ConfirmedLadder([]delivery.VerifiedLadder{{Record: p, SignedAt: at}}, "cluster-uid", []string{"staging", "production"})
	if len(rungs) != 1 || rungs[0].Name != "preview" || rungs[0].Provenance != delivery.RungConfirmed || rungs[0].Upstream != "" {
		t.Fatalf("confirmed %+v", rungs)
	}

	for _, bad := range []request{{}, {root: rootFile, view: filepath.Join(dir, "missing.json"), cluster: "c", email: "e"}} {
		if err := run(t.Context(), testutil.New(t), bad, io.Discard); err == nil {
			t.Errorf("%+v ran", bad)
		}
	}
	view.Repository = ""
	b, _ = json.Marshal(view)
	_ = os.WriteFile(viewFile, b, 0o600)
	if err := run(t.Context(), testutil.New(t), req, io.Discard); err == nil || !strings.Contains(err.Error(), "no approvals repository") {
		t.Errorf("a view with no repository: %v", err)
	}
}
