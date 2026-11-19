package orgindex

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestNewestReleasePerService(t *testing.T) {
	a, b := Prefix("x/a"), Prefix("x/b")
	got, ignored := Newest([]string{a + "-1.0.0", a + "-1.10.0", a + "-2.0.0-rc.1", a + "-1.9.0", b + "-0.1.0", "latest", a + "-notsemver"})
	if got[a] != a+"-1.10.0" || got[b] != b+"-0.1.0" || len(got) != 2 || ignored != 2 {
		t.Fatalf("Newest = %v ignored %d", got, ignored)
	}
}

func TestNewestFallsBackToPrerelease(t *testing.T) {
	a := Prefix("x/a")
	got, _ := Newest([]string{a + "-1.0.0-rc.1", a + "-1.0.0-rc.2", a + "-0.9.0-beta_build.1"})
	if got[a] != a+"-1.0.0-rc.2" {
		t.Fatalf("Newest = %v", got)
	}
	// Build metadata is not a prerelease.
	got, _ = Newest([]string{a + "-1.0.0-rc.2", a + "-0.9.0_build.1"})
	if got[a] != a+"-0.9.0_build.1" {
		t.Fatalf("Newest = %v", got)
	}
}

var ledger = Provider{Repo: "ghcr.io/acme/contracts/ledger", Key: fleet.NewServiceKeyDomain("ghcr.io/acme/contracts", "ledger")}

func TestNamesByRefRepository(t *testing.T) {
	e := Entry{Service: string(fleet.NewServiceKeyDomain("ghcr.io/other", "c")),
		Dependencies: []Dependency{{Name: "books", Ref: "oci://ghcr.io/acme/contracts/ledger:2.0.0"}}}
	if !e.Names(ledger) {
		t.Fatal("a ref naming the repository selects")
	}
	e.Dependencies[0].Ref = "ghcr.io/acme/contracts/ledger"
	if e.Names(ledger) {
		t.Fatal("a ref with no oci scheme names no repository")
	}
}

func TestNamesByNameInSameDomain(t *testing.T) {
	e := Entry{Service: string(fleet.NewServiceKeyDomain("ghcr.io/acme/contracts", "c")), Dependencies: []Dependency{{Name: "auth"}, {Name: "ledger"}}}
	if !e.Names(ledger) {
		t.Fatal("a name in the same domain selects")
	}
}

func TestNamesIgnoresOtherDomain(t *testing.T) {
	e := Entry{Service: string(fleet.NewServiceKeyDomain("ghcr.io/other", "c")), Dependencies: []Dependency{{Name: "ledger", Ref: "oci://ghcr.io/other/ledger:1.0.0"}}}
	if e.Names(ledger) {
		t.Fatal("a name in another domain does not select")
	}
}

func TestDecodeRoundTrips(t *testing.T) {
	e, _ := entry(t)
	b, _ := Encode(e)
	got, err := Decode(b)
	if err != nil || got.Service != e.Service || len(got.Dependencies) != 2 {
		t.Fatalf("Decode = %+v %v", got, err)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	if _, err := Decode([]byte(`{"schemaVersion":"` + SchemaVersion + `","extra":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Decode([]byte(`{"schemaVersion":"v0"}`)); err == nil || !strings.Contains(err.Error(), "schemaVersion is v0") {
		t.Fatalf("wrong schema: %v", err)
	}
}

func TestDecodeRejectsOversizedEntry(t *testing.T) {
	if _, err := Decode(make([]byte, MaxEntryBytes+1)); err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("oversized: %v", err)
	}
}
