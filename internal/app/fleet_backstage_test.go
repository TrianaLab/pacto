package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/detect"
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

// detectedCatalog serves the ledger page at the detected name
// backstage.idp.svc:7007, recording each Authorization header, and answers
// status when it is not 200. Every other address dials as usual.
func detectedCatalog(t *testing.T, status int) *[]string {
	t.Helper()
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"kind":"Component","metadata":{"name":"ledger"},"spec":{"owner":"group:default/team-ledger"}}],"pageInfo":{}}`))
	}))
	t.Cleanup(srv.Close)
	prev := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = prev })
	var d net.Dialer
	http.DefaultTransport = &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "backstage.idp.svc:7007" {
			addr = srv.Listener.Addr().String()
		}
		return d.DialContext(ctx, network, addr)
	}}
	return &auth
}

func backstageDetection(t *testing.T, endpoint string) string {
	t.Helper()
	return writeDetected(t, detect.Entry{Kind: "backstage", Value: "Backstage", Completeness: detect.Complete, Endpoint: endpoint,
		Objects: []detect.Object{{Kind: "Service", Namespace: "idp", Name: "backstage"}}})
}

// ledgerRoot is a local root holding ledger.
func ledgerRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeLocalBundle(t, filepath.Join(root, "ledger"), "ledger")
	return root
}

const detectedEndpoint = "http://backstage.idp.svc:7007"

// TestDetectedBackstageEndpointFillsURL: with no URL the dashboard reads the
// detected catalog without the token, keeps its row and never names the
// Service in the source's Ref, which no restricted view redacts.
func TestDetectedBackstageEndpointFillsURL(t *testing.T) {
	auth := detectedCatalog(t, http.StatusOK)
	snap := liveFleet(t, FleetOptions{LocalRoots: []string{ledgerRoot(t)}, Detected: backstageDetection(t, detectedEndpoint),
		LiveEdges: fleetsrc.NewLiveEdges(time.Now), BackstageToken: "reader-token-value"})
	st := sourceState(snap, backstage.SourceKind)
	if st == nil || st.Status != fleet.SourceAvailable || st.Ref != "detected" || !st.Advisory {
		t.Fatalf("source = %+v", st)
	}
	if s := snap.Services["ledger"]; s == nil || s.InferredOwner == nil || s.InferredOwner.Value != "team:team-ledger" {
		t.Fatalf("ledger = %+v", s)
	}
	if len(*auth) != 1 || (*auth)[0] != "" {
		t.Errorf("Authorization headers = %q, want one request with none", *auth)
	}
	if !slices.Contains(rowsOf(snap, "detection"), "backstage") {
		t.Errorf("the backstage row left the detection rows: %v", rowsOf(snap, "detection"))
	}
}

func TestDetectedBackstageRefusedSaysHowToConfigure(t *testing.T) {
	detectedCatalog(t, http.StatusUnauthorized)
	st := sourceState(liveFleet(t, FleetOptions{Detected: backstageDetection(t, detectedEndpoint), LiveEdges: fleetsrc.NewLiveEdges(time.Now)}), backstage.SourceKind)
	if st == nil || st.Status != fleet.SourceUnavailable || st.Ref != "detected" || st.Error == nil ||
		!strings.Contains(st.Error.Message, "set dashboard.backstage.url and tokenSecret") {
		t.Fatalf("source = %+v", st)
	}
}

func TestDetectedBackstageExplicitURLWins(t *testing.T) {
	auth := detectedCatalog(t, http.StatusOK)
	snap := liveFleet(t, FleetOptions{Detected: backstageDetection(t, detectedEndpoint), LiveEdges: fleetsrc.NewLiveEdges(time.Now), BackstageURL: ledgerCatalog(t).URL})
	if st := sourceState(snap, backstage.SourceKind); st == nil || st.Ref != "" || len(*auth) != 0 {
		t.Fatalf("source = %+v, detected requests %q", st, *auth)
	}
}

func TestDetectedBackstageOffClusterEndpointBuildsNothing(t *testing.T) {
	snap := liveFleet(t, FleetOptions{Detected: backstageDetection(t, "http://backstage.example.com:7007"), LiveEdges: fleetsrc.NewLiveEdges(time.Now)})
	if st := sourceState(snap, backstage.SourceKind); st != nil {
		t.Fatalf("source = %+v", st)
	}
}

// TestDetectedBackstageNotReadByTheCLI: without LiveEdges the row shows and
// nothing reads the catalog.
func TestDetectedBackstageNotReadByTheCLI(t *testing.T) {
	auth := detectedCatalog(t, http.StatusOK)
	snap := liveFleet(t, FleetOptions{Detected: backstageDetection(t, detectedEndpoint)})
	if st := sourceState(snap, backstage.SourceKind); st != nil || len(*auth) != 0 {
		t.Fatalf("source = %+v, requests %q", st, *auth)
	}
	if !slices.Contains(rowsOf(snap, "detection"), "backstage") {
		t.Errorf("detection rows = %v", rowsOf(snap, "detection"))
	}
}

// TestConfiguredBackstageHidesNotDetectedRow: with a configured catalog the
// dashboard drops the not-detected row, which would advise setting the URL
// already set; without one the row stays.
func TestConfiguredBackstageHidesNotDetectedRow(t *testing.T) {
	notDetected := writeDetected(t, detect.Backstage(nil, nil))
	snap := liveFleet(t, FleetOptions{Detected: notDetected, LiveEdges: fleetsrc.NewLiveEdges(time.Now), BackstageURL: ledgerCatalog(t).URL})
	if slices.Contains(rowsOf(snap, "detection"), "backstage") {
		t.Errorf("configured: detection rows = %v, want no backstage row", rowsOf(snap, "detection"))
	}
	snap = liveFleet(t, FleetOptions{Detected: notDetected, LiveEdges: fleetsrc.NewLiveEdges(time.Now)})
	if !slices.Contains(rowsOf(snap, "detection"), "backstage") {
		t.Errorf("not configured: detection rows = %v, want the backstage row", rowsOf(snap, "detection"))
	}
}
