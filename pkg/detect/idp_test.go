package detect

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const crossplaneHint = "helm upgrade <release> <chart> --reuse-values --set idp.crossplane.install=true"

func TestCrossplaneEntryControllerRunningV2(t *testing.T) {
	e := Crossplane(CrossplaneFacts{API: "v2", Version: "v2.1.0", Controller: CrossplaneControllerFound}, true)
	if e.Kind != fleet.ProviderCrossplane || e.Value != "Crossplane v2.1.0" || e.Evidence != "runs and serves apiextensions.crossplane.io/v2" ||
		e.Completeness != Complete || e.Scope != fleet.ScopeCluster || e.Hint != "" || len(e.Namespaces)+len(e.Objects) != 0 {
		t.Errorf("entry = %+v", e)
	}
	if strings.Contains(e.Evidence, "crossplane-system") {
		t.Errorf("evidence names a namespace: %q", e.Evidence)
	}
	// A tag that is not a version names the API major instead.
	if e := Crossplane(CrossplaneFacts{API: "v2", Version: "latest", Controller: CrossplaneControllerFound}, false); e.Value != "Crossplane v2" {
		t.Errorf("non-version tag = %+v", e)
	}
}

func TestCrossplaneEntryV1Only(t *testing.T) {
	e := Crossplane(CrossplaneFacts{API: "v1", Version: "1.20.1", Controller: CrossplaneControllerFound}, false)
	if e.Value != "Crossplane 1.20.1" || e.Evidence != "runs and serves apiextensions.crossplane.io/v1" || e.Completeness != Complete {
		t.Errorf("entry = %+v", e)
	}
}

func TestCrossplaneEntryNoneFoundIsComplete(t *testing.T) {
	e := Crossplane(CrossplaneFacts{Controller: CrossplaneControllerNone}, false)
	if e.Value != fleet.CrossplaneNoneFound || e.Completeness != Complete || e.Reason != "" || e.Hint != "" {
		t.Errorf("entry = %+v", e)
	}
}

func TestCrossplaneEntryAPIsWithoutControllerIsPartial(t *testing.T) {
	// The exit path leaves the CRDs: they never read as a running Crossplane.
	e := Crossplane(CrossplaneFacts{API: "v2", Controller: CrossplaneControllerNone}, true)
	if e.Value != "Crossplane APIs served, controller not found" || e.Completeness != Partial || e.Reason == "" || e.Hint != "" {
		t.Errorf("entry = %+v", e)
	}
}

// A core Deployment scaled to zero or crash-looping, with its CRDs still
// served, is neither a running Crossplane nor none found.
func TestCrossplaneEntryControllerNotReadyIsPartial(t *testing.T) {
	for _, api := range []string{"v2", ""} {
		e := Crossplane(CrossplaneFacts{API: api, Version: "v2.1.0", Controller: CrossplaneControllerNotReady}, true)
		if e.Value != "Crossplane not available" || e.Completeness != Partial || e.Reason == "" || e.Hint != "" || e.Scope != fleet.ScopeCluster {
			t.Errorf("api %q: entry = %+v", api, e)
		}
	}
}

func TestCrossplaneEntryControllerWithoutAPIsIsStarting(t *testing.T) {
	// A Crossplane still creating its CRDs is never none found, so idp
	// proposes no bootstrap and no "no provisioner found".
	e := Crossplane(CrossplaneFacts{Version: "v2.4.3", Controller: CrossplaneControllerFound}, true)
	if e.Value != "Crossplane starting" || e.Completeness != Partial || e.Reason == "" || e.Hint != "" || e.Scope != fleet.ScopeCluster {
		t.Errorf("entry = %+v", e)
	}
	rows := ProviderRows(Result{AsOf: time.Now(), Entries: []Entry{e}}, "detected", time.Now())
	if strings.HasPrefix(rows[0].Evidence, fleet.CrossplaneNoneFound+" ") || rows[0].Status == fleet.SourceAvailable {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestCrossplaneEntryControllerUnreadIsPartial(t *testing.T) {
	e := Crossplane(CrossplaneFacts{API: "v2", Controller: CrossplaneControllerUnread}, false)
	if e.Value != "Crossplane v2 APIs served" || e.Completeness != Partial || e.Reason == "" {
		t.Errorf("entry = %+v", e)
	}
	rows := ProviderRows(Result{AsOf: time.Now(), Entries: []Entry{e}}, "detected", time.Now())
	if len(rows[0].Limitations) != 1 || rows[0].Limitations[0].Code != "DETECTION_CROSSPLANE_PARTIAL" {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestCrossplaneEntryDiscoveryErrorIsUnknown(t *testing.T) {
	e := Crossplane(CrossplaneFacts{APIErr: errors.New("forbidden"), Controller: CrossplaneControllerNone}, true)
	if e.Completeness != Unknown || e.Value != "" || !strings.Contains(e.Reason, "forbidden") || e.Hint != "" {
		t.Errorf("entry = %+v", e)
	}
}

func TestCrossplaneHintOnlyUnderIDPWhenNoneFound(t *testing.T) {
	if e := Crossplane(CrossplaneFacts{Controller: CrossplaneControllerNone}, true); e.Hint != crossplaneHint {
		t.Errorf("idp none found hint = %q", e.Hint)
	}
	for _, f := range []CrossplaneFacts{
		{API: "v2", Version: "v2.1.0", Controller: CrossplaneControllerFound},
		{API: "v2", Controller: CrossplaneControllerNone},
		{API: "v2", Controller: CrossplaneControllerUnread},
	} {
		if e := Crossplane(f, true); e.Hint != "" {
			t.Errorf("%+v: hint %q", f, e.Hint)
		}
	}
}

func TestProviderRowsCrossplaneNoneFoundPrefix(t *testing.T) {
	asOf := time.Now()
	rows := ProviderRows(Result{AsOf: asOf, Entries: []Entry{Crossplane(CrossplaneFacts{Controller: CrossplaneControllerNone}, false)}}, "detected", asOf)
	if !strings.HasPrefix(rows[0].Evidence, fleet.CrossplaneNoneFound+" ") || rows[0].Status != fleet.SourceAvailable {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestEvidencePieceKeepsSecretOutOfEvidence(t *testing.T) {
	secret := Object{Kind: "Secret", Namespace: "pacto", Name: "pacto-evidence-trust"}
	e := EvidencePiece("on", "zot.acme.internal serves OCI Referrers; the trust Secret holds 2 keys", []Object{secret}, Complete, "")
	rows := ProviderRows(Result{AsOf: time.Now(), Entries: []Entry{e}}, "detected", time.Now())
	p := rows[0]
	if p.ID != "evidence" || strings.Contains(p.Evidence, secret.Name) || strings.Contains(p.Evidence, "pacto ") ||
		!slices.Contains(p.Objects, fleet.ProviderObject(secret)) || !strings.HasPrefix(p.Evidence, "on: zot.acme.internal") {
		t.Errorf("row = %+v", p)
	}
}

func TestEvidencePieceUnknownCarriesReason(t *testing.T) {
	e := EvidencePiece("unknown", "", nil, Unknown, "no target snapshot pushed yet to probe zot.acme.internal")
	rows := ProviderRows(Result{AsOf: time.Now(), Entries: []Entry{e}}, "detected", time.Now())
	if rows[0].Status != fleet.SourceUnavailable || len(rows[0].Limitations) != 1 ||
		rows[0].Limitations[0].Code != "DETECTION_EVIDENCE_UNKNOWN" || !strings.Contains(rows[0].Limitations[0].Message, "no target snapshot") {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestRecorderPieceEveryState(t *testing.T) {
	want := map[string]string{
		"on":                "signs every concluded operation",
		"off":               "delivery.recorder.enabled is false (an upgrade with --reuse-values carries the false an earlier chart defaulted to); set it to null to let profile idp decide",
		"no-signer":         "no signing method is set; set one of delivery.recorder.signing",
		"no-streams":        "no streams repository; set registry",
		"signer-incomplete": "the signing method fails a recorder check; see helm template with delivery.recorder.enabled=true",
		"delivery-off":      "profile hardened renders no Pacto delivery",
	}
	if len(RecorderStates) != len(want) {
		t.Fatalf("RecorderStates = %v", RecorderStates)
	}
	for _, s := range RecorderStates {
		e := RecorderPiece(s)
		value := "off"
		if s == "on" {
			value = "on"
		}
		if e.Kind != "recorder" || e.Value != value || e.Evidence != want[s] || e.Completeness != Complete {
			t.Errorf("%s = %+v", s, e)
		}
	}
}

func TestRecorderPieceRejectsUnknownStateAsUnknown(t *testing.T) {
	e := RecorderPiece("maybe")
	if e.Kind != "recorder" || e.Completeness != Unknown || e.Value != "" || !strings.Contains(e.Reason, "maybe") {
		t.Errorf("entry = %+v", e)
	}
}

func TestProviderRowsMapsIDPPiecesInferred(t *testing.T) {
	asOf := time.Now()
	rows := ProviderRows(Result{AsOf: asOf, Entries: []Entry{
		Crossplane(CrossplaneFacts{API: "v2", Version: "v2.1.0", Controller: CrossplaneControllerFound}, true),
		EvidencePiece("off", "zot.acme.internal does not serve OCI Referrers", nil, Complete, ""),
		RecorderPiece("no-signer"),
	}}, "detected", asOf)
	want := []struct{ id, name, used string }{
		{"crossplane", "Crossplane", "provisioning engine; profile idp offers its XRDs once accepted"},
		{"evidence", "Evidence", "Evidence Server for provisioning reports (profile idp)"},
		{"recorder", "Recorder", "signed delivery records (profile idp)"},
	}
	for i, w := range want {
		p := rows[i]
		if p.ID != w.id || p.Name != w.name || !slices.Equal(p.UsedFor, []string{w.used}) || p.Provenance != fleet.ProvenanceInferred || p.AsOf == nil {
			t.Errorf("row %d = %+v", i, p)
		}
	}
}
