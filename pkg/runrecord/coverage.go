package runrecord

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

const (
	// KindCoverage is a snapshot of the fleet's contract and observation
	// coverage, written by pacto fleet status.
	KindCoverage = "coverage"
	// CoverageService is the service every coverage record names: coverage is
	// fleet-wide. The contract schema allows only lowercase letters, digits
	// and inner hyphens in a service name, so no real service can carry it.
	CoverageService = "_fleet"
)

// Coverage is the body of a coverage record: the fleet's coverage counters
// as one snapshot reported them. It is a level, never summed.
type Coverage struct {
	// Services counts the services that are no golden path.
	Services int `json:"services"`
	// Uncontracted counts the callers the fleet observed with no contract.
	Uncontracted int `json:"uncontracted"`
	// Knowledge is the snapshot's knowledge tally, copied field by field. Nil
	// means the snapshot carried none: unknown, never 0.
	Knowledge *CoverageKnowledge `json:"knowledge,omitempty"`
	// Match is the snapshot's declared and observed match tally. Nil means
	// the snapshot carried none: unknown, never 0.
	Match *CoverageMatch `json:"match,omitempty"`
	// Contracts is the snapshot's contract completeness figure. Nil means
	// the snapshot carried none: unknown, never 0.
	Contracts *CoverageContracts `json:"contracts,omitempty"`
	// Links counts the operational targets by how they link to a contract.
	Links CoverageLinks `json:"links"`
	// SnapshotAsOf is the time of the snapshot the counters came from.
	SnapshotAsOf time.Time `json:"snapshotAsOf"`
	// Completeness is complete or partial: how complete that snapshot was.
	Completeness string `json:"completeness"`
}

// CoverageKnowledge is the fleet's knowledge tally: Described + WithGaps +
// Unknown == Services. The observed services are Services - Unknown -
// NotDeployed, drawn beside Unknown; Unknown is never subtracted into a
// positive count.
type CoverageKnowledge struct {
	Services    int `json:"services"`
	Described   int `json:"described"`
	WithGaps    int `json:"withGaps"`
	Unknown     int `json:"unknown"`
	NotDeployed int `json:"notDeployed"`
}

// CoverageMatch counts the services and the dependencies by how what is
// declared compares with what is observed. The match percentage is matched
// over matched, expected-only and observed-only; insufficient never counts
// as matched.
type CoverageMatch struct {
	ServicesMatched          int `json:"servicesMatched"`
	ServicesExpectedOnly     int `json:"servicesExpectedOnly"`
	ServicesObservedOnly     int `json:"servicesObservedOnly"`
	ServicesInsufficient     int `json:"servicesInsufficient"`
	DependenciesMatched      int `json:"dependenciesMatched"`
	DependenciesExpectedOnly int `json:"dependenciesExpectedOnly"`
	DependenciesObservedOnly int `json:"dependenciesObservedOnly"`
	DependenciesInsufficient int `json:"dependenciesInsufficient"`
}

// CoverageContracts is the fleet's contract completeness: Judged + Unknown
// == Services, and PercentComplete is the mean over the judged services, nil
// when none is judged.
type CoverageContracts struct {
	Services        int  `json:"services"`
	Judged          int  `json:"judged"`
	Unknown         int  `json:"unknown"`
	FullyDescribed  int  `json:"fullyDescribed"`
	PercentComplete *int `json:"percentComplete,omitempty"`
}

// CoverageLinks counts the operational targets by how they link to a contract.
type CoverageLinks struct {
	Exact      int `json:"exact"`
	Inferred   int `json:"inferred"`
	Ambiguous  int `json:"ambiguous"`
	Unresolved int `json:"unresolved"`
}

// coverageCompleteness is what a coverage snapshot may say it was. An empty
// snapshot is recorded as partial.
var coverageCompleteness = []string{"complete", "partial"}

// CoverageRecord is the coverage record of c. Its id is a function of the
// snapshot, so recording one snapshot twice counts once.
func CoverageRecord(c Coverage, pactoVersion string, now time.Time) Record {
	r := Record{
		Version: Version, Kind: KindCoverage, Service: CoverageService, Coverage: &c,
		PactoVersion: pactoVersion, RecordedAt: now.UTC().Truncate(time.Second),
	}
	r.ID = ExpectedID(r)
	return r
}

// ExpectedID is the id r must carry: a coverage record's is a function of its
// snapshot, every other kind's is ID(kind, service, old, new). An operation
// record's id is a Kubernetes UID, which this never matches.
func ExpectedID(r Record) string {
	if r.Kind == KindCoverage && r.Coverage != nil {
		return ID(KindCoverage, CoverageService, "", r.Coverage.SnapshotAsOf.UTC().Format(time.RFC3339))
	}
	return ID(r.Kind, r.Service, r.OldRevision, r.NewRevision)
}

// validateCoverage checks a coverage record's body and that it carries
// nothing a run would.
func validateCoverage(r Record) error {
	c := r.Coverage
	switch {
	case r.Service != CoverageService:
		return fmt.Errorf("a coverage record names service %q, not %s", r.Service, CoverageService)
	case c == nil:
		return errors.New("coverage is missing")
	case !slices.Contains(coverageCompleteness, c.Completeness):
		return fmt.Errorf("unknown coverage completeness %q", c.Completeness)
	case c.SnapshotAsOf.IsZero():
		return errors.New("coverage.snapshotAsOf is missing")
	case c.SnapshotAsOf.After(r.RecordedAt):
		return errors.New("coverage.snapshotAsOf is after recordedAt")
	case coverageNegative(c):
		return errors.New("a coverage count is negative")
	case c.Contracts != nil && c.Contracts.PercentComplete != nil && (*c.Contracts.PercentComplete < 0 || *c.Contracts.PercentComplete > 100):
		return errors.New("coverage.contracts.percentComplete is outside 0 to 100")
	}
	return present([]field{{"outcome", r.Outcome != ""}, {"environment", r.Environment != ""}})
}

func coverageNegative(c *Coverage) bool {
	counts := []int{c.Services, c.Uncontracted, c.Links.Exact, c.Links.Inferred, c.Links.Ambiguous, c.Links.Unresolved}
	if k := c.Knowledge; k != nil {
		counts = append(counts, k.Services, k.Described, k.WithGaps, k.Unknown, k.NotDeployed)
	}
	if m := c.Match; m != nil {
		counts = append(counts, m.ServicesMatched, m.ServicesExpectedOnly, m.ServicesObservedOnly, m.ServicesInsufficient,
			m.DependenciesMatched, m.DependenciesExpectedOnly, m.DependenciesObservedOnly, m.DependenciesInsufficient)
	}
	if k := c.Contracts; k != nil {
		counts = append(counts, k.Services, k.Judged, k.Unknown, k.FullyDescribed)
	}
	return slices.Min(counts) < 0
}
