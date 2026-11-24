package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// extensionFixture is a registry-pushed svc-a bundle declaring the extension
// capability x.acme.io/backup, and a structured trust config with the granted
// key acme-backup and the ungranted key other.
type extensionFixture struct {
	svc         *Service
	bundle      *contract.Bundle
	contractRef string
	digest      string
	repo        string
	trustPath   string
	keys        map[string]KeyPair
}

// extensionFixtureOn pushes the bundle to host and writes the trust config.
func extensionFixtureOn(t *testing.T, host string) extensionFixture {
	t.Helper()
	y := append(mxRenderYAML("svc-a", "1.0.0", nil), "capabilities:\n  - type: extension\n    ref: x.acme.io/backup\n"...)
	c, err := contract.Parse(strings.NewReader(string(y)))
	if err != nil {
		t.Fatal(err)
	}
	b := mxBundle(t, "svc-a", "1.0.0")
	b.Contract, b.RawYAML = c, y
	b.FS.(fstest.MapFS)["pacto.yaml"].Data = y
	svc, client := mxService(mxHostKeychain{})
	digest, err := client.Push(context.Background(), host+"/svc-a:1.0.0", b)
	if err != nil {
		t.Fatal(err)
	}
	f := extensionFixture{svc: svc, bundle: b, contractRef: "oci://" + host + "/svc-a@" + digest, digest: digest, repo: host + "/svc-a",
		trustPath: filepath.Join(t.TempDir(), "trust.yaml"), keys: map[string]KeyPair{}}
	dir := filepath.Dir(f.trustPath)
	for _, p := range []string{"acme-backup", "other"} {
		if f.keys[p], err = (&Service{}).GenerateKey(dir, p, p, false); err != nil {
			t.Fatal(err)
		}
	}
	cfg := "apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n" +
		"  - {keyId: acme-backup, producerId: acme-backup, publicKeyFile: " + filepath.Base(f.keys["acme-backup"].PublicKeyPath) +
		", allowedObservationKinds: [CapabilityObserved], allowedExtensionRefs: [x.acme.io/backup]}\n" +
		"  - {keyId: other, producerId: other, publicKeyFile: " + filepath.Base(f.keys["other"].PublicKeyPath) + ", allowedObservationKinds: [CapabilityObserved]}\n"
	if err := os.WriteFile(f.trustPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// post fills the fixture file (present or absent), signs it as producer with
// seq and posts it to base.
func (f extensionFixture) post(t *testing.T, base, producer string, present bool, seq uint64) SendResult {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "evidence", "extension-backup.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.NewReplacer("SUBJECT", f.repo+"/prod/extension/backup", "CONTRACT_REF", f.contractRef,
		"OBSERVED_AT", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)).Replace(string(data))
	if !present {
		body = strings.Replace(body, `"present": true`, `"present": false`, 1)
	}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	kp := f.keys[producer]
	env, err := f.svc.SignEvidence(SignOptions{EvidencePath: path, KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: producer, Sequence: seq, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.PostEnvelope(context.Background(), base+"/api/evidence/v1/envelopes", env)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// build reads base through the HTTP evidence source beside the bundle's
// revision and returns the snapshot as of now.
func (f extensionFixture) build(t *testing.T, base string, now time.Time) (*fleet.FleetSnapshot, *fleet.ServiceRecord, *fleet.ContractRevision) {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{{
			Bundle: f.bundle, Domain: contract.OCIDomain(f.contractRef), ResolvedRef: f.contractRef, Digest: f.digest,
		}}}),
		fleetsrc.NewEvidenceHTTPSource("evidence", base))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Services) != 1 || len(snap.Revisions) != 1 {
		t.Fatalf("snapshot = %d services %d revisions, want one of each", len(snap.Services), len(snap.Revisions))
	}
	// Each map holds exactly one entry.
	for _, s := range snap.Services {
		for _, r := range snap.Revisions {
			return snap, s, r
		}
	}
	return nil, nil, nil
}

// TestServeEvidenceExtensionFixture is the end-to-end proof of an extension
// report: a granted producer's signed fixture reads covered in the fleet, the
// absent variant reads failing and NonCompliant naming the producer, the same
// report a day later reads unknown, and a key without the grant is refused.
func TestServeEvidenceExtensionFixture(t *testing.T) {
	t.Parallel()
	host, _ := referrersRegistry(t)
	f := extensionFixtureOn(t, host)
	base := serveEvidence(t, f.svc, ServeOptions{TrustPath: f.trustPath, Subjects: []string{f.contractRef}, Producers: []string{"acme-backup", "other"}})

	if res := f.post(t, base, "acme-backup", true, 1); res.StatusCode != http.StatusAccepted || !strings.Contains(res.Body, `"compliance":"Compliant"`) {
		t.Fatalf("present = %d %s, want 202 Compliant", res.StatusCode, res.Body)
	}
	_, _, rev := f.build(t, base, time.Now())
	check := wantExtensionCheck(t, rev, "covered", false)
	if check.Producer != "acme-backup" || string(check.Details) != `{"lastBackup":"2026-11-22T03:00:00Z"}` {
		t.Errorf("covered check = %+v, want acme-backup and its details", check)
	}

	if res := f.post(t, base, "acme-backup", false, 2); res.StatusCode != http.StatusAccepted || !strings.Contains(res.Body, `"compliance":"NonCompliant"`) {
		t.Fatalf("absent = %d %s, want 202 NonCompliant", res.StatusCode, res.Body)
	}
	snap, svc, rev := f.build(t, base, time.Now())
	wantExtensionCheck(t, rev, "failing", false)
	if svc.Status != fleet.StatusNonCompliant || !explainNamesProducer(t, snap, svc, "acme-backup") {
		t.Errorf("status %s, want NonCompliant with a reason naming acme-backup", svc.Status)
	}

	_, _, rev = f.build(t, base, time.Now().Add(25*time.Hour))
	wantExtensionCheck(t, rev, "unknown", true)

	if res := f.post(t, base, "other", true, 1); res.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Body, "unauthorized_producer") {
		t.Errorf("ungranted key = %d %s, want 401 unauthorized_producer", res.StatusCode, res.Body)
	}
}

// wantExtensionCheck asserts rev has exactly one check, in state and stale.
func wantExtensionCheck(t *testing.T, rev *fleet.ContractRevision, state string, stale bool) fleet.ExtensionCheck {
	t.Helper()
	if len(rev.Extensions) != 1 || rev.Extensions[0].State != state || rev.Extensions[0].Stale != stale {
		t.Fatalf("checks = %+v, want one %s (stale %v)", rev.Extensions, state, stale)
	}
	return rev.Extensions[0]
}

// explainNamesProducer reports whether Explain gives svc a CAPABILITY_ABSENT
// reason naming producer.
func explainNamesProducer(t *testing.T, snap *fleet.FleetSnapshot, svc *fleet.ServiceRecord, producer string) bool {
	t.Helper()
	res, err := fleet.NewQuery(snap).Explain(string(svc.Key))
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(res.Reasons, func(r fleet.Reason) bool {
		return r.Code == "CAPABILITY_ABSENT" && strings.Contains(r.Message, "reported absent by "+producer)
	})
}
