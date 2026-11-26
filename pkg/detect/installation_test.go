package detect

import (
	"encoding/json"
	"os"
	"slices"
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
		r.Status != fleet.SourceAvailable || len(r.Details) == 0 ||
		r.UsedFor[0] != "installation profile, target snapshot, organization index and inferred contracts; set by a person with the profile and registry values" {
		t.Errorf("installation row = %+v", r)
	}
}

// The dashboard layout gate's Installation fixture (a profile and a registry)
// must carry the longest "Used for" this row emits, or the gate measures a
// shorter string than production sends.
func TestLayoutGateInstallationFixtureMatchesUsedFor(t *testing.T) {
	inst := installation(pushed)
	inst.Profile = []string{"hardened", "idp"}
	r := row(t, ProviderRows(Result{AsOf: asOf, Installation: inst}, "detection", asOf), "installation")
	ts, err := os.ReadFile("../dashboard/frontend/e2e/layoutGate.ts")
	if err != nil {
		t.Fatal(err)
	}
	if want := "usedFor: ['" + r.UsedFor[0] + "']"; !strings.Contains(string(ts), want) {
		t.Errorf("layoutGate.ts Installation fixture lacks %s", want)
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

func TestResultDeclared(t *testing.T) {
	if d := (Result{}).Declared("x"); d != nil {
		t.Errorf("declared without a block = %+v", d)
	}
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	res := Result{AsOf: at, Cluster: "uid-1", Installation: &Installation{Profile: []string{"hardened", "idp"}, Registry: "ghcr.io/acme"}}
	d := res.Declared("k8s:pacto/pacto-detected")
	if !slices.Equal(d.Profile, []string{"hardened", "idp"}) || d.Registry != "ghcr.io/acme" || d.ClusterID != "uid-1" || !d.AsOf.Equal(at) ||
		d.Source != "the operator's installation block in k8s:pacto/pacto-detected" {
		t.Errorf("declared = %+v", d)
	}
	d.Profile[0] = "changed"
	if res.Installation.Profile[0] != "hardened" {
		t.Error("Declared shares the block's profile")
	}
	if d := (Result{Installation: &Installation{}}).Declared("x"); d.Profile != nil {
		t.Errorf("a block with no profile declared %v", d.Profile)
	}
}

// TestInstallationChartVersionRoundTrips: the chart version an installation
// hands out survives Encode and Decode, cleaned; anything longer than 64 bytes
// or outside the semver characters is dropped.
func TestInstallationChartVersionRoundTrips(t *testing.T) {
	for in, want := range map[string]string{
		"6.4.0": "6.4.0", "6.4.0-rc.1+build.7": "6.4.0-rc.1+build.7", "6.4.0\u202e": "6.4.0",
		"6.4.0; rm -rf /": "", "6.4.0 ": "", strings.Repeat("1", 65): "", strings.Repeat("1", 64): strings.Repeat("1", 64),
	} {
		inst := installation(pushed)
		inst.ChartVersion = in
		data, err := Encode(Result{AsOf: asOf, Installation: inst})
		if err != nil {
			t.Fatal(err)
		}
		res, err := Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Installation.ChartVersion; got != want {
			t.Errorf("chart version %q = %q, want %q", in, got, want)
		}
	}
}

// offeringsAd is an installation's advertised inferred offerings.
func offeringsAd() *OfferingsAd {
	at := asOf
	return &OfferingsAd{Producer: "crossplane-6f1c", Repository: "ghcr.io/acme/pacto-inferred/crossplane-6f1c/services",
		Observed: []string{"xbucket", "xdatabase"}, AsOf: &at, Partial: "stopped at the item bound (200)"}
}

func TestInstallationOfferingsRoundTrip(t *testing.T) {
	inst := installation(pushed)
	inst.Offerings = offeringsAd()
	inst.Offerings.Partial = "a\u202eb"
	data, err := Encode(Result{AsOf: asOf, Installation: inst})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Offerings.Partial != "a\u202eb" {
		t.Error("Encode changed its input")
	}
	res, err := Decode(data)
	o := res.Installation.Offerings
	if err != nil || o == nil || o.Producer != "crossplane-6f1c" || o.Repository != "ghcr.io/acme/pacto-inferred/crossplane-6f1c/services" ||
		!slices.Equal(o.Observed, []string{"xbucket", "xdatabase"}) || o.AsOf == nil || !o.AsOf.Equal(asOf) || o.Partial != "ab" {
		t.Fatalf("round trip = %+v, %v", o, err)
	}
	// Encode refuses more slugs than a reader keeps; Decode caps them.
	over := installation(nil)
	over.Offerings = &OfferingsAd{Observed: make([]string, MaxObservedOfferings+1)}
	if _, err := Encode(Result{Installation: over}); err == nil {
		t.Error("Encode wrote more offerings than a reader keeps")
	}
	res, err = Decode(mustJSON(t, Result{SchemaVersion: SchemaVersion, Installation: over}))
	if err != nil || len(res.Installation.Offerings.Observed) != MaxObservedOfferings {
		t.Errorf("Decode kept %d offerings, %v", len(res.Installation.Offerings.Observed), err)
	}
	// Without an advertisement nothing is written.
	if data, _ := Encode(Result{Installation: installation(nil)}); strings.Contains(string(data), "offerings") {
		t.Error("an installation with no offerings wrote the key")
	}
}

// olderInstallation is the Installation an older reader knows, without
// Offerings.
type olderInstallation struct {
	Registry string `json:"registry"`
	Inferred string `json:"inferred,omitempty"`
}

func TestOlderDetectionReaderIgnoresOfferings(t *testing.T) {
	inst := installation(nil)
	inst.Offerings = offeringsAd()
	var older struct {
		Installation *olderInstallation `json:"installation"`
	}
	if err := json.Unmarshal(mustJSON(t, Result{SchemaVersion: SchemaVersion, Installation: inst}), &older); err != nil ||
		older.Installation == nil || older.Installation.Registry != "ghcr.io/acme" || older.Installation.Inferred != "ghcr.io/acme/pacto-inferred" {
		t.Fatalf("older reader = %+v, %v", older.Installation, err)
	}
}

func TestInstallationOfferingsRepositoryMustBeOwn(t *testing.T) {
	own := func() Result {
		return Result{Cluster: "c1", Installation: &Installation{Registry: "ghcr.io/acme",
			Offerings: &OfferingsAd{Producer: "idp-c1", Repository: "ghcr.io/acme/pacto-inferred/idp-c1/services"}}}
	}
	if repo, ok := own().OfferingsRepository(); !ok || repo != "ghcr.io/acme/pacto-inferred/idp-c1/services" {
		t.Fatalf("own repository = %q %v", repo, ok)
	}
	for name, edit := range map[string]func(*Result){
		"no installation":    func(r *Result) { r.Installation = nil },
		"no offerings":       func(r *Result) { r.Installation.Offerings = nil },
		"no registry":        func(r *Result) { r.Installation.Registry = "" },
		"no cluster":         func(r *Result) { r.Cluster = "" },
		"other cluster":      func(r *Result) { r.Cluster = "c2" },
		"other registry":     func(r *Result) { r.Installation.Registry = "ghcr.io/other" },
		"other producer":     func(r *Result) { r.Installation.Offerings.Producer = "idp-c1x" },
		"producer mismatch":  func(r *Result) { r.Installation.Offerings.Repository = "ghcr.io/acme/pacto-inferred/x-c1/services" },
		"no contribution":    func(r *Result) { r.Installation.Offerings.Repository = "ghcr.io/acme/idp-c1" },
		"nested under other": func(r *Result) { r.Installation.Offerings.Repository = "ghcr.io/acme/x/pacto-inferred/idp-c1/services" },
	} {
		r := own()
		edit(&r)
		if repo, ok := r.OfferingsRepository(); ok {
			t.Errorf("%s: read %q", name, repo)
		}
	}
}
