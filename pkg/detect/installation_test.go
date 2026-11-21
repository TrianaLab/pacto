package detect

import (
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// installation is a configured registry with a stats repository outside it.
func installation(last *PushResult) *Installation {
	return &Installation{Registry: "ghcr.io/acme", Targets: "ghcr.io/acme/pacto-targets", Index: "ghcr.io/acme/pacto-index",
		Inferred: "ghcr.io/acme/pacto-inferred", SnapshotKey: "AAEC", SnapshotKeyID: "sha256:abc",
		Pushes:   []string{"ghcr.io/acme/pacto-targets", "ghcr.io/acme/pacto-index", "ghcr.io/acme/pacto-inferred", "quay.io/stats/pacto"},
		LastPush: last}
}

var pushed = &PushResult{At: asOf, Ref: "ghcr.io/acme/pacto-targets:cluster-uid"}

func TestInstallationRowIsDeclared(t *testing.T) {
	rows := ProviderRows(Result{AsOf: asOf, Cluster: "uid", Installation: installation(pushed), Entries: []Entry{{Kind: "registry", Completeness: Complete}}}, "detection", asOf)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	inst := row(t, rows, "installation")
	if inst.Provenance != fleet.ProvenanceDeclared || inst.Name != "Installation" || inst.AsOf == nil || inst.Status != fleet.SourceAvailable ||
		inst.Scope != "" || inst.Objects != nil || inst.Hint != "" || inst.ClusterID != "uid" ||
		inst.Evidence != "registry ghcr.io/acme; tags only under the listed prefixes, referrers by digest elsewhere" {
		t.Errorf("installation row = %+v", inst)
	}
	if r := row(t, rows, "registry"); r.Provenance != fleet.ProvenanceInferred {
		t.Errorf("an entry row is not inferred: %+v", r)
	}
	if !strings.Contains(strings.Join(inst.Details, "\n"), "snapshot key sha256:abc") {
		t.Errorf("details = %q", inst.Details)
	}
}

func TestNoInstallationNoRow(t *testing.T) {
	if rows := ProviderRows(Result{AsOf: asOf}, "detection", asOf); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestInstallationRowShowsPushError(t *testing.T) {
	inst := row(t, ProviderRows(Result{AsOf: asOf, Installation: installation(&PushResult{At: asOf, Error: "UNAUTHORIZED"})}, "detection", asOf), "installation")
	if inst.Status != fleet.SourcePartial || !strings.Contains(inst.Evidence, "; last push failed: UNAUTHORIZED") ||
		len(inst.Limitations) != 1 || inst.Limitations[0].Message != "last push failed: UNAUTHORIZED" {
		t.Errorf("installation row = %+v", inst)
	}
}

func TestInstallationRowPartialBeforeFirstPush(t *testing.T) {
	inst := row(t, ProviderRows(Result{AsOf: asOf, Installation: installation(nil)}, "detection", asOf), "installation")
	if inst.Status != fleet.SourcePartial || !strings.HasSuffix(inst.Evidence, "; push not confirmed yet") {
		t.Errorf("installation row = %+v", inst)
	}
	if stale := row(t, ProviderRows(Result{AsOf: asOf, Installation: installation(pushed)}, "detection", asOf.Add(time.Hour)), "installation"); stale.Status != fleet.SourceStale {
		t.Errorf("an old installation row reads %s", stale.Status)
	}
}

func TestInstallationRowListsEveryPushPrefix(t *testing.T) {
	inst := row(t, ProviderRows(Result{AsOf: asOf, Installation: installation(pushed)}, "detection", asOf), "installation")
	details := strings.Join(inst.Details, "\n")
	for _, want := range []string{"push prefixes ghcr.io/acme/pacto-targets, ghcr.io/acme/pacto-index, ghcr.io/acme/pacto-inferred, quay.io/stats/pacto", "target snapshot ghcr.io/acme/pacto-targets",
		"organization index ghcr.io/acme/pacto-index", "inferred contracts ghcr.io/acme/pacto-inferred", "last push ghcr.io/acme/pacto-targets:cluster-uid at 2026-11-16T09:00:00Z"} {
		if !strings.Contains(details, want) {
			t.Errorf("details lack %q:\n%s", want, details)
		}
	}
	if !strings.Contains(inst.Evidence, "tags only under the listed prefixes, referrers by digest elsewhere") {
		t.Errorf("evidence = %q", inst.Evidence)
	}
}

func TestInstallationRoundTripsAndIsBounded(t *testing.T) {
	in := Result{AsOf: asOf, Installation: installation(pushed)}
	data, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Decode(data)
	if err != nil || res.Installation == nil || res.Installation.Registry != "ghcr.io/acme" || len(res.Installation.Pushes) != 4 ||
		res.Installation.LastPush == nil || !res.Installation.LastPush.At.Equal(asOf) {
		t.Fatalf("round trip = %+v, %v", res.Installation, err)
	}
	over := installation(nil)
	over.Pushes = make([]string, MaxPushes+1)
	if _, err := Encode(Result{Installation: over}); err == nil {
		t.Error("Encode wrote more push prefixes than a reader keeps")
	}
	if _, err := Decode(mustJSON(t, Result{SchemaVersion: SchemaVersion, Installation: over})); err == nil {
		t.Error("Decode kept more push prefixes than the bound")
	}
}

func TestInstallationIsCleaned(t *testing.T) {
	long, dirty := strings.Repeat("é", MaxRunes+10), "a\u202eb"
	big := installation(&PushResult{Ref: long, Error: dirty})
	big.Registry, big.SnapshotKeyID = long, dirty
	res, err := Decode(mustJSON(t, Result{SchemaVersion: SchemaVersion, Installation: big}))
	if err != nil || len([]rune(res.Installation.Registry)) != MaxRunes || res.Installation.SnapshotKeyID != "ab" ||
		len([]rune(res.Installation.LastPush.Ref)) != MaxRunes || res.Installation.LastPush.Error != "ab" {
		t.Errorf("installation not cleaned: %+v, %v", res.Installation, err)
	}
	// Encode leaves the caller's block untouched.
	if _, err := Encode(Result{Installation: big}); err != nil || big.Registry != long || big.LastPush.Error != dirty {
		t.Errorf("Encode changed its input: %+v, %v", big, err)
	}
}

func TestDecodeRefusesNonBase64SnapshotKey(t *testing.T) {
	bad := installation(nil)
	bad.SnapshotKey = "not base64!"
	if _, err := Decode(mustJSON(t, Result{SchemaVersion: SchemaVersion, Installation: bad})); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Errorf("err = %v", err)
	}
	if _, err := Encode(Result{Installation: bad}); err == nil {
		t.Error("Encode wrote a key that is not base64")
	}
}

// The worst-case row, every push prefix and a last push, keeps the MaxDetails
// the API documents for every Provider row.
func TestInstallationRowKeepsMaxDetails(t *testing.T) {
	inst := installation(pushed)
	inst.Pushes = make([]string, MaxPushes)
	for i := range inst.Pushes {
		inst.Pushes[i] = "ghcr.io/acme/p" + strings.Repeat("x", i)
	}
	r := row(t, ProviderRows(Result{AsOf: asOf, Installation: inst}, "detection", asOf), "installation")
	if len(r.Details) > MaxDetails || !strings.Contains(strings.Join(r.Details, "\n"), "ghcr.io/acme/pxxxxxxx") {
		t.Errorf("details = %d %q", len(r.Details), r.Details)
	}
}

// An old success, or a push with no ref or time, is no confirmation.
func TestInstallationRowUnconfirmedPush(t *testing.T) {
	for name, p := range map[string]*PushResult{
		"old":    {At: asOf.Add(-StaleAfter - time.Minute), Ref: "ghcr.io/acme/pacto-targets:cluster-uid"},
		"zero":   {},
		"no ref": {At: asOf},
	} {
		r := row(t, ProviderRows(Result{AsOf: asOf, Installation: installation(p)}, "detection", asOf), "installation")
		if r.Status != fleet.SourcePartial || len(r.Limitations) != 1 || strings.Contains(strings.Join(r.Details, "\n"), "last push") {
			t.Errorf("%s: installation row = %+v", name, r)
		}
	}
}

const profileUsedFor = "installation profile and registry; set by a person with the profile and registry values"

func TestInstallationRowNamesProfile(t *testing.T) {
	inst := installation(pushed)
	inst.Profile = []string{"hardened", "idp"}
	r := row(t, ProviderRows(Result{AsOf: asOf, Installation: inst}, "detection", asOf), "installation")
	if r.Evidence != "profile hardened, idp; registry ghcr.io/acme; tags only under the listed prefixes, referrers by digest elsewhere" ||
		r.Status != fleet.SourceAvailable || len(r.Details) == 0 || r.UsedFor[0] != profileUsedFor {
		t.Errorf("installation row = %+v", r)
	}
}

func TestInstallationRowWithoutRegistryIsComplete(t *testing.T) {
	r := row(t, ProviderRows(Result{AsOf: asOf, Cluster: "uid", Installation: &Installation{Profile: []string{"default"}}}, "detection", asOf), "installation")
	if r.Evidence != "profile default; no registry value: the operator pushes nothing" || r.Status != fleet.SourceAvailable ||
		r.Details != nil || r.Limitations != nil || r.Provenance != fleet.ProvenanceDeclared || r.ClusterID != "uid" || r.UsedFor[0] != profileUsedFor {
		t.Errorf("installation row = %+v", r)
	}
}

// A block from an operator that does not advertise its profile reads exactly
// as it did before profiles.
func TestInstallationRowWithoutProfile(t *testing.T) {
	r := row(t, ProviderRows(Result{AsOf: asOf, Installation: installation(pushed)}, "detection", asOf), "installation")
	if r.Evidence != "registry ghcr.io/acme; tags only under the listed prefixes, referrers by digest elsewhere" ||
		r.UsedFor[0] != "target snapshot, organization index and inferred contracts; set by a person with the registry value" {
		t.Errorf("installation row = %+v", r)
	}
}

func TestDecodeKeepsResultWithoutProfile(t *testing.T) {
	res, err := Decode([]byte(`{"schemaVersion":"` + SchemaVersion + `","installation":{"registry":"ghcr.io/acme"}}`))
	if err != nil || res.Installation == nil || res.Installation.Profile != nil || res.Installation.Registry != "ghcr.io/acme" {
		t.Errorf("decoded = %+v, %v", res.Installation, err)
	}
}

func TestDecodeCapsProfile(t *testing.T) {
	data := []byte(`{"schemaVersion":"` + SchemaVersion + `","installation":{"registry":"","profile":["a\u202eb","b","c","d","e"]}}`)
	res, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Installation.Profile; len(got) != MaxProfiles || got[0] != "ab" {
		t.Errorf("profile = %q", got)
	}
}

func TestEncodeRefusesLongProfile(t *testing.T) {
	if _, err := Encode(Result{Installation: &Installation{Profile: []string{"a", "b", "c", "d"}}}); err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Errorf("err = %v", err)
	}
}

func TestEncodeLeavesProfileUntouched(t *testing.T) {
	profile := []string{"a\u202eb"}
	data, err := Encode(Result{Installation: &Installation{Profile: profile}})
	if err != nil || profile[0] != "a\u202eb" || !strings.Contains(string(data), `"profile":["ab"]`) {
		t.Errorf("Encode = %s, %v; caller's profile = %q", data, err, profile)
	}
}
