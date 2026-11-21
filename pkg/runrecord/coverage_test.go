package runrecord

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

var coverageAsOf = time.Date(2026, 11, 13, 8, 59, 0, 0, time.UTC)

func coverageFixture() Record {
	return CoverageRecord(Coverage{
		Services: 12, Uncontracted: 2,
		Knowledge:    &CoverageKnowledge{Services: 12, Described: 5, WithGaps: 4, Unknown: 3, NotDeployed: 1},
		Links:        CoverageLinks{Exact: 20, Inferred: 3, Ambiguous: 1, Unresolved: 2},
		SnapshotAsOf: coverageAsOf, Completeness: "partial",
	}, "4.6.0", time.Date(2026, 11, 13, 9, 0, 0, 500, time.UTC))
}

func TestCoverageRecordGoldenJSON(t *testing.T) {
	out, err := Encode(coverageFixture())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/coverage.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, bytes.TrimSuffix(data, []byte("\n"))) {
		t.Fatalf("CoverageRecord does not reproduce the golden:\n%s", out)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("the golden does not round-trip: %v", err)
	}
	if strings.Contains(string(out), `"outcome"`) {
		t.Fatal("a coverage record carries an outcome")
	}
}

func coverageGolden(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/coverage.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestParseCoverageRequiresFleetService(t *testing.T) {
	diff := strings.Replace(golden(t), `"service":"payments"`, `"service":"_fleet"`, 1)
	if _, err := Parse([]byte(diff)); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("an impact record on _fleet parsed: %v", err)
	}
	cov := strings.Replace(coverageGolden(t), `"service":"_fleet"`, `"service":"payments"`, 1)
	if _, err := Parse([]byte(cov)); err == nil {
		t.Fatal("a coverage record on payments parsed")
	}
}

func TestParseCoverageBodyOnlyOnCoverage(t *testing.T) {
	g := golden(t)
	withBody := strings.Replace(g, `"recordedAt"`, `"coverage":{"services":1,"uncontracted":0,"links":{"exact":0,"inferred":0,"ambiguous":0,"unresolved":0},"snapshotAsOf":"2026-11-13T08:00:00Z","completeness":"complete"},"recordedAt"`, 1)
	if _, err := Parse([]byte(withBody)); err == nil {
		t.Fatal("an impact record with a coverage body parsed")
	}
	op := goldenInput()
	r, _ := mustBuild(t, op)
	r.Coverage = coverageFixture().Coverage
	if _, err := Encode(r); err == nil {
		t.Fatal("an operation record with a coverage body encoded")
	}
	c := coverageGolden(t)
	cases := map[string]string{
		"no body":          c[:strings.Index(c, `,"coverage"`)] + `}`,
		"completeness":     strings.Replace(c, `"completeness":"partial"`, `"completeness":"empty"`, 1),
		"no snapshotAsOf":  strings.Replace(c, `"snapshotAsOf":"2026-11-13T08:59:00Z"`, `"snapshotAsOf":"0001-01-01T00:00:00Z"`, 1),
		"negative count":   strings.Replace(c, `"unresolved":2`, `"unresolved":-2`, 1),
		"negative tally":   strings.Replace(c, `"notDeployed":1`, `"notDeployed":-1`, 1),
		"outcome":          strings.Replace(c, `"pactoVersion"`, `"outcome":"passed","pactoVersion"`, 1),
		"environment":      strings.Replace(c, `"pactoVersion"`, `"environment":"prod","pactoVersion"`, 1),
		"ci field":         strings.Replace(c, `"pactoVersion"`, `"classification":"BREAKING","pactoVersion"`, 1),
		"operation field":  strings.Replace(c, `"pactoVersion"`, `"type":"promote","pactoVersion"`, 1),
		"top completeness": strings.Replace(c, `"pactoVersion"`, `"completeness":"complete","pactoVersion"`, 1),
	}
	for name, data := range cases {
		if data == c {
			t.Fatalf("%s: the case did not change the fixture", name)
		}
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestParseCoverageRejectsFutureSnapshot(t *testing.T) {
	future := strings.Replace(coverageGolden(t), `"snapshotAsOf":"2026-11-13T08:59:00Z"`, `"snapshotAsOf":"2026-11-13T09:00:01Z"`, 1)
	if _, err := Parse([]byte(future)); err == nil || !strings.Contains(err.Error(), "after recordedAt") {
		t.Fatalf("a snapshot after recordedAt parsed: %v", err)
	}
}

func TestCoverageRecordIDStablePerSnapshot(t *testing.T) {
	a := coverageFixture()
	c := *a.Coverage
	c.Services = 99
	b := CoverageRecord(c, "4.7.0", time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC))
	if a.ID != b.ID || a.ID != ID(KindCoverage, CoverageService, "", "2026-11-13T08:59:00Z") {
		t.Fatalf("one snapshot gave ids %s and %s", a.ID, b.ID)
	}
	c.SnapshotAsOf = coverageAsOf.Add(time.Minute)
	if CoverageRecord(c, "4.6.0", time.Now()).ID == a.ID {
		t.Fatal("another snapshot kept the id")
	}
}

func TestCoverageNilKnowledgeIsUnknown(t *testing.T) {
	r := coverageFixture()
	r.Coverage.Knowledge = nil
	out, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"knowledge"`) {
		t.Fatalf("a missing tally is encoded: %s", out)
	}
	back, err := Parse(out)
	if err != nil || back.Coverage.Knowledge != nil {
		t.Fatalf("a missing tally came back as %+v (%v)", back.Coverage, err)
	}
}

func TestExpectedID(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	cov := CoverageRecord(Coverage{SnapshotAsOf: asOf, Completeness: "complete"}, "dev", asOf)
	if cov.ID != ID(KindCoverage, CoverageService, "", asOf.Format(time.RFC3339)) || ExpectedID(cov) != cov.ID {
		t.Fatalf("coverage id %s", cov.ID)
	}
	diff := Record{Kind: KindDiff, Service: "svc", OldRevision: "a", NewRevision: "b"}
	if ExpectedID(diff) != ID(KindDiff, "svc", "a", "b") {
		t.Fatal("a run's id is ID(kind, service, old, new)")
	}
	if bare := (Record{Kind: KindCoverage, Service: CoverageService}); ExpectedID(bare) != ID(KindCoverage, CoverageService, "", "") {
		t.Fatal("a coverage record with no body falls back to the run id")
	}
}

// coverageWithMatch is the fixture with a match tally and a contract
// completeness figure.
func coverageWithMatch() Record {
	r := coverageFixture()
	r.Coverage.Match = &CoverageMatch{ServicesMatched: 7, ServicesExpectedOnly: 1, ServicesObservedOnly: 2, ServicesInsufficient: 2,
		DependenciesMatched: 9, DependenciesExpectedOnly: 3, DependenciesObservedOnly: 1, DependenciesInsufficient: 4}
	r.Coverage.Contracts = &CoverageContracts{Services: 12, Judged: 10, Unknown: 2, FullyDescribed: 4, PercentComplete: new(72)}
	return r
}

func TestCoverageRecordCarriesMatchAndContracts(t *testing.T) {
	r := coverageWithMatch()
	out, err := Encode(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"servicesMatched":7`, `"dependenciesInsufficient":4`, `"fullyDescribed":4`, `"percentComplete":72`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("record misses %s: %s", key, out)
		}
	}
	back, err := Parse(out)
	if err != nil || *back.Coverage.Match != *r.Coverage.Match || *back.Coverage.Contracts.PercentComplete != 72 || back.Coverage.Contracts.Judged != 10 {
		t.Fatalf("round trip %+v (%v)", back.Coverage, err)
	}
}

func TestCoverageRecordRejectsNegativeCounter(t *testing.T) {
	for name, mutate := range map[string]func(*Coverage){
		"match":           func(c *Coverage) { c.Match.DependenciesObservedOnly = -1 },
		"contracts":       func(c *Coverage) { c.Contracts.FullyDescribed = -1 },
		"percent above":   func(c *Coverage) { c.Contracts.PercentComplete = new(101) },
		"percent below":   func(c *Coverage) { c.Contracts.PercentComplete = new(-1) },
		"contracts total": func(c *Coverage) { c.Contracts.Services = -3 },
	} {
		r := coverageWithMatch()
		mutate(r.Coverage)
		if _, err := Encode(r); err == nil {
			t.Errorf("%s: encoded", name)
		}
	}
	r := coverageWithMatch()
	r.Coverage.Contracts.PercentComplete = nil
	if _, err := Encode(r); err != nil {
		t.Errorf("no judged service: %v", err)
	}
}

func TestCoverageNilMatchIsUnknown(t *testing.T) {
	out, err := Encode(coverageFixture())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"match"`) || strings.Contains(string(out), `"contracts"`) {
		t.Fatalf("a missing tally is encoded: %s", out)
	}
	back, err := Parse(out)
	if err != nil || back.Coverage.Match != nil || back.Coverage.Contracts != nil {
		t.Fatalf("a missing tally came back as %+v (%v)", back.Coverage, err)
	}
}
