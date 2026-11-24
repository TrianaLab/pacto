package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// loadExtensionTrust loads a trust config whose one key, of producer
// acme-backup, carries the given flow-mapping fields.
func loadExtensionTrust(t *testing.T, fields string) (map[string][]string, error) {
	t.Helper()
	dir := t.TempDir()
	if _, err := (&Service{}).GenerateKey(dir, "", "backup", false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "trust.yaml")
	body := "apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n  - {keyId: acme-backup-2026, producerId: acme-backup, publicKeyFile: backup.pub, " + fields + "}\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, err := loadTrustStore(p)
	refs := map[string][]string{}
	for id, e := range ts {
		refs[id] = e.ExtensionRefs
	}
	return refs, err
}

func TestLoadStructuredTrustStore_ExtensionRefs(t *testing.T) {
	refs, err := loadExtensionTrust(t, "allowedObservationKinds: [CapabilityObserved], allowedExtensionRefs: [x.acme.io/backup, x.acme.io/restore]")
	if err != nil || !slices.Equal(refs["acme-backup-2026"], []string{"x.acme.io/backup", "x.acme.io/restore"}) {
		t.Fatalf("valid key: refs %v, err %v", refs, err)
	}
	cases := []struct {
		name, fields, want string
	}{
		{"un-namespaced", "allowedObservationKinds: [CapabilityObserved], allowedExtensionRefs: [acme/backup]", `"acme/backup" is not a namespaced extension capability ref`},
		{"duplicate", "allowedObservationKinds: [CapabilityObserved], allowedExtensionRefs: [x.acme.io/backup, x.acme.io/backup]", `"x.acme.io/backup" is listed twice`},
		{"kinds without CapabilityObserved", "allowedObservationKinds: [ResourceProvisioned], allowedExtensionRefs: [x.acme.io/backup]", "an extension verifier key lists allowedObservationKinds: [CapabilityObserved]"},
		{"kinds with an extra kind", "allowedObservationKinds: [CapabilityObserved, WorkloadObserved], allowedExtensionRefs: [x.acme.io/backup]", "an extension verifier key lists allowedObservationKinds: [CapabilityObserved]"},
		{"empty kinds", "allowedExtensionRefs: [x.acme.io/backup]", "an extension verifier key lists allowedObservationKinds: [CapabilityObserved]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadExtensionTrust(t, tc.fields)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `key "acme-backup-2026"`) {
				t.Errorf("err = %v, want %q naming the key", err, tc.want)
			}
		})
	}
}

// TestTrustConfigBarePubGrantsNoExtension: a key of the directory-of-*.pub
// store can never report an extension.
func TestTrustConfigBarePubGrantsNoExtension(t *testing.T) {
	dir := t.TempDir()
	if _, err := (&Service{}).GenerateKey(dir, "", "acme-backup", false); err != nil {
		t.Fatal(err)
	}
	ts, err := loadTrustStore(dir)
	if err != nil || len(ts) == 0 {
		t.Fatalf("loadTrustStore = %v %v", ts, err)
	}
	for id, e := range ts {
		if len(e.ExtensionRefs) != 0 {
			t.Errorf("%s lists %v", id, e.ExtensionRefs)
		}
	}
}
