package validation

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// refDefaultsDoc is a 2.3 contract whose required ref configuration db gets
// no values in prod. Before 2.4 nothing checks it; from 2.4 its defaults must
// pass the provider's schema. Its meaning trap is historical, so it names 2.3.
const refDefaultsDoc = `pactoVersion: "2.3" # kept as written
service: {name: orders, version: 1.0.0}
configurations:
  - {name: db, ref: "oci://r/pg:16.1.0", required: true}
environments:
  - {name: prod, file: prod.yaml}
`

var envOnly = fstest.MapFS{"prod.yaml": {Data: []byte("{}\n")}}

func upgradeRefDefaults(t *testing.T, doc string, fsys fstest.MapFS, resolver BundleResolver) (contract.Upgraded, error) {
	t.Helper()
	return contract.Upgrade([]byte(doc), contract.Current(), UpgradeCheck(t.Context(), fsys, resolver, time.Now()))
}

func assertKeptWith(t *testing.T, up contract.Upgraded, err error, code string) {
	t.Helper()
	if err != nil || up.Kept == nil || up.Kept.Code != code || up.To != "2.3" || string(up.Raw) != refDefaultsDoc {
		t.Fatalf("got %+v kept %+v err %v", up, up.Kept, err)
	}
}

func TestUpgradeKeepsRequiredRefDefaultsThatFail(t *testing.T) {
	up, err := upgradeRefDefaults(t, refDefaultsDoc, envOnly, pgResolver(map[string]*contract.Bundle{pgRef: pgBundle("16.1.0", provisioning)}))
	assertKeptWith(t, up, err, "ENVIRONMENT_VALUES_MISSING")
	if !strings.HasPrefix(up.Kept.Path, "environments") {
		t.Fatalf("path %q", up.Kept.Path)
	}
}

func TestUpgradeRaisesRequiredRefDefaultsThatPass(t *testing.T) {
	open := &contract.Bundle{
		Contract: &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "postgresql", Version: "16.1.0"}, Configurations: []contract.Configuration{{Name: "provisioning", Schema: "open.json"}}},
		FS:       fstest.MapFS{"open.json": {Data: []byte(`{"type":"object","properties":{"size":{"type":"string"}}}`)}},
	}
	up, err := upgradeRefDefaults(t, refDefaultsDoc, envOnly, pgResolver(map[string]*contract.Bundle{pgRef: open}))
	want := strings.Replace(refDefaultsDoc, `"2.3"`, `"`+contract.Current()+`"`, 1)
	if err != nil || up.Kept != nil || up.To != contract.Current() || string(up.Raw) != want {
		t.Fatalf("got %+v %v", up, err)
	}
}

func TestUpgradeKeepsUnresolvableRef(t *testing.T) {
	up, err := upgradeRefDefaults(t, refDefaultsDoc, envOnly, nil)
	assertKeptWith(t, up, err, "CONFIG_REF_UNRESOLVED")
}

func TestUpgradeKeepsAmbiguousRef(t *testing.T) {
	tiers := contract.Configuration{Name: "tiers", Schema: "provisioning.json"}
	up, err := upgradeRefDefaults(t, refDefaultsDoc, envOnly, pgResolver(map[string]*contract.Bundle{pgRef: pgBundle("16.1.0", provisioning, tiers)}))
	assertKeptWith(t, up, err, "CONFIG_REF_AMBIGUOUS")
}

func TestUpgradeCheckSkippedStageIsUnknown(t *testing.T) {
	// A schema file missing from the bundle fails cross-field, which skips the
	// stages that hold every version-dependent rule.
	doc := strings.Replace(refDefaultsDoc, "required: true}\n", "required: true}\n  - {name: extra, schema: missing.json, required: false}\n", 1)
	up, err := upgradeRefDefaults(t, doc, envOnly, pgResolver(map[string]*contract.Bundle{pgRef: pgBundle("16.1.0", provisioning)}))
	if !errors.Is(err, ErrUnverified) || up.Kept != nil || up.To != "2.3" || string(up.Raw) != doc {
		t.Fatalf("got %+v %v", up, err)
	}
}

func TestUpgradeFindingsRule(t *testing.T) {
	w := []contract.ValidationWarning{{Code: "W"}}
	pass := Stage{Name: "structural", Status: StagePass, Warnings: w}
	for name, tc := range map[string]struct {
		stages []Stage
		err    error
	}{
		"clean":            {[]Stage{pass, SkippedStages(skipNoConfigRefs, "configuration-refs")[0], SkippedStages(skipNoEnvironments, "environments")[0]}, nil},
		"skip":             {[]Stage{pass, {Name: "environments", Status: StageSkip, Reason: "could not verify: prod/db"}}, ErrUnverified},
		"error":            {[]Stage{pass, {Name: "policy", Status: StageFail, Errors: []contract.ValidationError{{Code: "E"}}}}, ErrUnverified},
		"skipped by error": {append([]Stage{pass}, SkippedStages("structural failed", "cross-field")...), ErrUnverified},
		"run's own stages": {[]Stage{pass, {Name: "readiness", Status: StageSkip, Reason: "not requested (--readiness)"}}, nil},
	} {
		e, ws, err := UpgradeFindings(tc.stages)
		if !errors.Is(err, tc.err) || len(ws) != 1 || len(e) != len(Merged(tc.stages).Errors) {
			t.Errorf("%s: got %v %v %v", name, e, ws, err)
		}
		// An MCP client gets no report, so the error names what blocks the judgement.
		if want := map[string]string{"skip": "the environments stage skipped: could not verify: prod/db", "error": "first: E: "}[name]; !strings.Contains(fmt.Sprint(err), want) {
			t.Errorf("%s: %v does not name %q", name, err, want)
		}
	}
}

// TestUpgradeFindingsIgnoresBuildDeprecation: the raise adds the build's
// deprecation to every declared build, so it never counts as a new finding.
func TestUpgradeFindingsIgnoresBuildDeprecation(t *testing.T) {
	ws := []contract.ValidationWarning{{Code: CodeBuildDeprecated}, {Code: "W"}}
	_, got, err := UpgradeFindings([]Stage{{Name: "cross-field", Status: StagePass, Warnings: ws}})
	if err != nil || len(got) != 1 || got[0].Code != "W" || len(ws) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestUpgradeCheckDeterministic(t *testing.T) {
	open := map[string]*contract.Bundle{pgRef: pgBundle("16.1.0", provisioning)}
	a, _ := upgradeRefDefaults(t, refDefaultsDoc, envOnly, pgResolver(open))
	b, _ := upgradeRefDefaults(t, refDefaultsDoc, envOnly, pgResolver(open))
	if *a.Kept != *b.Kept || !bytes.Equal(a.Raw, b.Raw) {
		t.Fatalf("kept %+v and %+v", a.Kept, b.Kept)
	}
	plain := "pactoVersion: '2.3'\nservice: {name: s, version: 1.0.0}\n"
	c, _ := upgradeRefDefaults(t, plain, nil, nil)
	d, _ := upgradeRefDefaults(t, plain, nil, nil)
	if !bytes.Equal(c.Raw, d.Raw) || c.To != contract.Current() {
		t.Fatalf("got %q and %q", c.Raw, d.Raw)
	}
}

func TestUpgradeCheckParseErrorKept(t *testing.T) {
	check := UpgradeCheck(t.Context(), nil, nil, time.Now())
	e, _, err := check([]byte("pactoVersion: '2.3'\nunknownField: 1\n"))
	if !errors.Is(err, ErrUnverified) || len(e) != 1 || e[0].Code != "PARSE_ERROR" {
		t.Fatalf("got %v %v", e, err)
	}
}

// A policy that fails at the declared version skips the environments stage;
// at the raised version it passes and environments reports values that were
// already invalid. The original never ran that stage, so the raise cannot be
// blamed for the finding: Pacto cannot tell.
func TestUpgradeSkippedStageNeverBacksANewFinding(t *testing.T) {
	doc := `pactoVersion: "2.3"
service: {name: s, version: 1.0.0}
configurations:
  - {name: chart, schema: chart.json, required: true}
environments:
  - {name: prod, file: prod.yaml}
policies:
  - {name: newest, schema: policy.json}
`
	fsys := fstest.MapFS{
		"chart.json":  {Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"}}}`)},
		"prod.yaml":   {Data: []byte("chart: {replicas: three}\n")},
		"policy.json": {Data: []byte(`{"properties":{"pactoVersion":{"enum":["` + contract.Current() + `"]}}}`)},
	}
	up, err := upgradeRefDefaults(t, doc, fsys, nil)
	if !errors.Is(err, ErrUnverified) || up.Kept != nil || up.To != "2.3" {
		t.Fatalf("got %+v kept %+v err %v", up, up.Kept, err)
	}
}
