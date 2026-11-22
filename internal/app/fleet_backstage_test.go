package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ledgerCatalog serves one by-query page naming ledger, owned by team-ledger.
func ledgerCatalog(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"kind":"Component","metadata":{"name":"ledger"},"spec":{"owner":"group:default/team-ledger","system":"payments"}}],"pageInfo":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBackstageSourceAloneWithLocal(t *testing.T) {
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "ledger"), "ledger")
	const token = "reader-token-value"
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{LocalRoots: []string{root}, BackstageURL: ledgerCatalog(t).URL, BackstageToken: token})
	if err != nil {
		t.Fatal(err)
	}
	s := snap.Services["ledger"]
	if s == nil || s.InferredOwner == nil || s.InferredOwner.Value != "team:team-ledger" || s.InferredOwner.Source != backstage.SourceKind {
		t.Fatalf("services = %+v, want ledger with an inferred owner from %s", snap.Services, backstage.SourceKind)
	}
	st := sourceState(snap, backstage.SourceKind)
	if st == nil || st.Kind != backstage.SourceKind || st.Status != fleet.SourceAvailable || !st.Advisory {
		t.Fatalf("source = %+v, want an available advisory %s", st, backstage.SourceKind)
	}
	data, _ := json.Marshal(snap)
	if strings.Contains(string(data), token) {
		t.Error("the snapshot carries the reader token")
	}
}

func TestNoBackstageURLNoSource(t *testing.T) {
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "ledger"), "ledger")
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{LocalRoots: []string{root}, BackstageToken: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	if sourceState(snap, backstage.SourceKind) != nil {
		t.Errorf("sources = %+v, want no %s source", snap.Sources, backstage.SourceKind)
	}
}

func TestFleetRejectsBackstageTokenOverPlainHTTP(t *testing.T) {
	const token = "reader-token-value"
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{BackstageURL: "http://backstage.example.com", BackstageToken: token})
	if err == nil || snap != nil || strings.Contains(err.Error(), token) {
		t.Fatalf("snap=%v err=%v, want no snapshot and an error without the token", snap, err)
	}
}
