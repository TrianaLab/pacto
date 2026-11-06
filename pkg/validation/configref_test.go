package validation

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const pgSchema = `{"type":"object","properties":{"size":{"enum":["small","large"]},"storageGi":{"type":"integer"}},"required":["size"]}`

// pgBundle is a provider named postgresql at version with configurations
// cfgs, its schema files holding pgSchema.
func pgBundle(version string, cfgs ...contract.Configuration) *contract.Bundle {
	return &contract.Bundle{
		Contract: &contract.Contract{PactoVersion: "2.6", Service: contract.Service{Name: "postgresql", Version: version}, Configurations: cfgs},
		FS:       fstest.MapFS{"provisioning.json": {Data: []byte(pgSchema)}, "bad.json": {Data: []byte(`{`)}},
	}
}

var provisioning = contract.Configuration{Name: "provisioning", Schema: "provisioning.json"}

// refConsumer is a consumer of version referencing ref from configuration db.
func refConsumer(version, ref string, values map[string]any) *contract.Contract {
	return &contract.Contract{
		PactoVersion:   version,
		Service:        contract.Service{Name: "orders", Version: "1.0.0"},
		Configurations: []contract.Configuration{{Name: "db", Ref: ref, Values: values}},
	}
}

func pgResolver(bundles map[string]*contract.Bundle) *originResolver {
	r := &originResolver{root: "/c", bundles: map[string]*contract.Bundle{}, childOf: map[string]string{}}
	for k, b := range bundles {
		r.bundles["/c\x00"+k] = b
		r.childOf["/c\x00"+k] = "/c"
	}
	return r
}

func findingCodes(r ValidationResult) string {
	var out []string
	for _, e := range r.Errors {
		out = append(out, "E:"+e.Code+"@"+e.Path)
	}
	for _, w := range r.Warnings {
		out = append(out, "W:"+w.Code+"@"+w.Path)
	}
	return strings.Join(out, ",")
}

func TestSelectConfiguration(t *testing.T) {
	tiers := contract.Configuration{Name: "tiers", Schema: "provisioning.json"}
	chained := contract.Configuration{Name: "shared", Ref: "oci://r/base:1.0.0"}
	for _, tc := range []struct {
		name, selector, want, code, msg string
		cfgs                            []contract.Configuration
	}{
		{"selector hit", "tiers", "tiers", "", "", []contract.Configuration{provisioning, tiers}},
		{"selector miss", "provisionin", "", "CONFIG_REF_UNKNOWN_CONFIGURATION", `declares no configuration "provisionin" (did you mean "provisioning"?)`, []contract.Configuration{provisioning, tiers}},
		{"absent, one", "", "provisioning", "", "", []contract.Configuration{provisioning}},
		{"absent, zero", "", "", "CONFIG_REF_UNKNOWN_CONFIGURATION", "postgresql@16.1.0 declares no configuration", nil},
		{"absent, two", "", "", "CONFIG_REF_AMBIGUOUS", "declares 2 configurations (provisioning, tiers); set configuration: <name> to pick one", []contract.Configuration{provisioning, tiers}},
		{"selected entry is a ref", "", "shared", "", "", []contract.Configuration{chained}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, code, msg := selectConfiguration(contract.Configuration{Name: "db", Ref: "x", ProviderConfiguration: tc.selector}, pgBundle("16.1.0", tc.cfgs...).Contract)
			if got.Name != tc.want || code != tc.code || !strings.Contains(msg, tc.msg) {
				t.Fatalf("got %q %q %q", got.Name, code, msg)
			}
		})
	}
}

func TestSelectConfiguration_SeverityFollowsTheConsumerVersion(t *testing.T) {
	r := pgResolver(map[string]*contract.Bundle{"oci://r/pg:16.1.0": pgBundle("16.1.0", provisioning, contract.Configuration{Name: "tiers", Schema: "provisioning.json"})})
	for version, want := range map[string]string{"2.6": "E:CONFIG_REF_AMBIGUOUS@configurations[0].ref", "2.5": "W:CONFIG_REF_AMBIGUOUS@configurations[0].ref"} {
		refs, res := ResolveConfigRefs(t.Context(), refConsumer(version, "oci://r/pg:16.1.0", nil), nil, ConfigRefOptions{Resolver: r, Strict: true})
		if findingCodes(res) != want || refs["db"].Schema != nil || refs["db"].Err == nil {
			t.Fatalf("%s: got %s %+v", version, findingCodes(res), refs["db"])
		}
	}
	c := refConsumer("2.6", "oci://r/pg:16.1.0", nil)
	c.Configurations[0].ProviderConfiguration = "nope"
	if _, res := ResolveConfigRefs(t.Context(), c, nil, ConfigRefOptions{Resolver: r}); findingCodes(res) != "E:CONFIG_REF_UNKNOWN_CONFIGURATION@configurations[0].configuration" {
		t.Fatalf("got %s", findingCodes(res))
	}
}

func TestResolveConfigRefs(t *testing.T) {
	const ref = "oci://r/pg:16.1.0"
	good := map[string]*contract.Bundle{ref: pgBundle("16.1.0", provisioning)}
	noSchema := map[string]*contract.Bundle{ref: pgBundle("16.1.0", contract.Configuration{Name: "provisioning", Schema: "missing.json"})}
	badSchema := map[string]*contract.Bundle{ref: pgBundle("16.1.0", contract.Configuration{Name: "provisioning", Schema: "bad.json"})}
	empty := map[string]*contract.Bundle{ref: {Contract: pgBundle("16.1.0").Contract}}
	envFiles := fstest.MapFS{"prod.yaml": {Data: []byte("db: {size: small}\n")}}
	withEnv := func(c *contract.Contract) *contract.Contract {
		c.Environments = []contract.Environment{{Name: "prod", File: "prod.yaml"}}
		return c
	}
	withDep := func(c *contract.Contract) *contract.Contract {
		c.Dependencies = []contract.Dependency{{Name: "pg", Ref: "oci://r/pg@sha256:abc", Compatibility: "^16.0.0"}}
		return c
	}
	for _, tc := range []struct {
		name    string
		c       *contract.Contract
		fs      fstest.MapFS
		bundles map[string]*contract.Bundle
		nilRes  bool
		strict  bool
		want    string
	}{
		{"resolved", refConsumer("2.6", ref, nil), nil, good, false, true, ""},
		{"schema missing, strict", refConsumer("2.6", ref, nil), nil, noSchema, false, true, "E:CONFIG_REF_UNRESOLVED@configurations[0].ref"},
		{"schema missing, lenient", refConsumer("2.6", ref, nil), nil, noSchema, false, false, "W:CONFIG_REF_UNVERIFIED@configurations[0].ref"},
		{"schema does not compile", refConsumer("2.6", ref, nil), nil, badSchema, false, true, "E:CONFIG_REF_UNRESOLVED@configurations[0].ref"},
		{"empty bundle", refConsumer("2.6", ref, nil), nil, empty, false, true, "E:CONFIG_REF_UNRESOLVED@configurations[0].ref"},
		{"resolver error", refConsumer("2.6", ref, nil), nil, nil, false, true, "E:CONFIG_REF_UNRESOLVED@configurations[0].ref"},
		{"nil resolver, strict", refConsumer("2.6", ref, nil), nil, nil, true, true, "E:CONFIG_REF_UNRESOLVED@configurations[0].ref"},
		{"nil resolver, lenient", refConsumer("2.6", ref, nil), nil, nil, true, false, "W:CONFIG_REF_UNVERIFIED@configurations[0].ref"},
		{"2.5, nothing depends on it", refConsumer("2.5", ref, nil), nil, nil, false, true, "W:CONFIG_REF_UNVERIFIED@configurations[0].ref"},
		{"2.5, environment values", withEnv(refConsumer("2.5", ref, nil)), envFiles, nil, false, true, "E:CONFIG_REF_UNRESOLVED@configurations[0].ref"},
		{"2.5, ranged dependency", withDep(refConsumer("2.5", ref, nil)), nil, nil, false, true, "E:CONFIG_REF_UNRESOLVED@configurations[0].ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var resolver BundleResolver = pgResolver(tc.bundles)
			if tc.nilRes {
				resolver = nil
			}
			refs, res := ResolveConfigRefs(t.Context(), tc.c, tc.fs, ConfigRefOptions{Resolver: resolver, Strict: tc.strict})
			if got := findingCodes(res); got != tc.want {
				t.Fatalf("got %q, want %q (%+v)", got, tc.want, res)
			}
			r := refs["db"]
			if (tc.want == "") != (r.Schema != nil) || (tc.want == "") == (r.Err != nil) {
				t.Fatalf("ref: %+v", r)
			}
		})
	}
	refs, _ := ResolveConfigRefs(t.Context(), refConsumer("2.6", ref, nil), nil, ConfigRefOptions{Resolver: pgResolver(good)})
	if r := refs["db"]; r.Selected != "provisioning" || r.ProviderName != "postgresql" || r.ProviderVersion != "16.1.0" || r.SchemaPath != "provisioning.json" || r.ProviderFS == nil {
		t.Fatalf("got %+v", r)
	}
}

func TestResolveConfigRefs_FollowsAChainAndCatchesACycle(t *testing.T) {
	// orders -> shared (a ref) -> postgresql's provisioning schema.
	shared := &contract.Bundle{
		Contract: &contract.Contract{PactoVersion: "2.6", Service: contract.Service{Name: "shared", Version: "1.0.0"},
			Configurations: []contract.Configuration{{Name: "shared", Ref: "./pg", ProviderConfiguration: "provisioning"}}},
		FS: fstest.MapFS{},
	}
	r := &originResolver{root: "/c", bundles: map[string]*contract.Bundle{
		"/c\x00./shared":  shared,
		"/shared\x00./pg": pgBundle("16.1.0", provisioning, contract.Configuration{Name: "other", Schema: "bad.json"}),
	}, childOf: map[string]string{"/c\x00./shared": "/shared", "/shared\x00./pg": "/pg"}}
	refs, res := ResolveConfigRefs(t.Context(), refConsumer("2.6", "./shared", nil), nil, ConfigRefOptions{Resolver: r, Strict: true})
	if got := refs["db"]; findingCodes(res) != "" || got.Schema == nil || got.ProviderName != "shared" || got.Selected != "shared" || got.SchemaPath != "provisioning.json" {
		t.Fatalf("chain: %s %+v", findingCodes(res), got)
	}

	// A -> B -> A: the consumer's own directory comes round again.
	a := &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "a", Version: "1.0.0"},
		Configurations: []contract.Configuration{{Name: "db", Ref: "./b"}}}, FS: fstest.MapFS{}}
	b := &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Name: "b", Version: "1.0.0"},
		Configurations: []contract.Configuration{{Name: "x", Ref: "./a"}}}, FS: fstest.MapFS{}}
	r = &originResolver{root: "/c", bundles: map[string]*contract.Bundle{"/c\x00./b": b, "/b\x00./a": a},
		childOf: map[string]string{"/c\x00./b": "/b", "/b\x00./a": "/c"}}
	_, res = ResolveConfigRefs(t.Context(), refConsumer("2.6", "./b", nil), nil, ConfigRefOptions{Resolver: r})
	if findingCodes(res) != "E:CONFIG_REF_CYCLE@configurations[0].ref" || !strings.Contains(res.Errors[0].Message, "[./b ./a ./b]") {
		t.Fatalf("cycle: %+v", res)
	}

	// A diamond, two refs to one provider, resolves twice without a cycle.
	c := refConsumer("2.6", "oci://r/pg:16.1.0", nil)
	c.Configurations = append(c.Configurations, contract.Configuration{Name: "replica", Ref: "oci://r/pg:16.1.0"})
	refs, res = ResolveConfigRefs(t.Context(), c, nil, ConfigRefOptions{Resolver: pgResolver(map[string]*contract.Bundle{"oci://r/pg:16.1.0": pgBundle("16.1.0", provisioning)})})
	if findingCodes(res) != "" || refs["db"].Schema == nil || refs["replica"].Schema == nil {
		t.Fatalf("diamond: %+v", res)
	}
}

func TestResolveConfigRefs_PlainResolver(t *testing.T) {
	m := &mockBundleResolver{bundles: map[string]*contract.Bundle{"oci://r/pg:16.1.0": pgBundle("16.1.0", provisioning)}}
	if refs, res := ResolveConfigRefs(t.Context(), refConsumer("2.6", "oci://r/pg:16.1.0", nil), nil, ConfigRefOptions{Resolver: m}); findingCodes(res) != "" || refs["db"].Schema == nil {
		t.Fatalf("got %+v", res)
	}
}

const pgRef = "oci://r/pg:16.1.0"

// refStage runs the configuration-refs stage of c with b served under pgRef,
// or with no resolver when b is nil.
func refStage(t *testing.T, c *contract.Contract, b *contract.Bundle, strict bool, now time.Time) Stage {
	var resolver BundleResolver = pgResolver(map[string]*contract.Bundle{pgRef: b})
	if b == nil {
		resolver = nil
	}
	opts := ConfigRefOptions{Resolver: resolver, Strict: strict, Now: now}
	refs, res := ResolveConfigRefs(t.Context(), c, nil, opts)
	return ConfigRefStage(c, refs, res, opts)
}

func TestConfigRefStage(t *testing.T) {
	const ref = pgRef
	stage := func(c *contract.Contract, b *contract.Bundle, strict bool, now time.Time) Stage {
		return refStage(t, c, b, strict, now)
	}
	pg := pgBundle("16.1.0", provisioning)

	if st := stage(refConsumer("2.6", ref, map[string]any{"size": "small"}), pg, true, time.Time{}); st.Status != StagePass || codes(st) != "" {
		t.Fatalf("pass: %+v", st)
	}
	st := stage(refConsumer("2.6", ref, map[string]any{"size": "huge"}), pg, false, time.Time{})
	if st.Status != StageFail || codes(st) != "CONFIG_VALUES_VALIDATION_FAILED" || st.Errors[0].Path != "configurations[0].values" ||
		!strings.Contains(st.Errors[0].Message, "configuration db at /size:") || !strings.Contains(st.Errors[0].Message, "small") {
		t.Fatalf("invalid: %+v", st)
	}
	if st := stage(refConsumer("2.6", ref, map[string]any{"storageGi": 2}), pg, false, time.Time{}); !strings.Contains(st.Errors[0].Message, "configuration db at /: missing property 'size'") {
		t.Fatalf("root violation: %+v", st)
	}
	if st := stage(refConsumer("2.6", ref, map[string]any{"size": "small"}), nil, false, time.Time{}); st.Status != StageSkip || st.Reason != "could not verify: db" || codes(st) != "CONFIG_REF_UNVERIFIED" {
		t.Fatalf("lenient unresolved: %+v", st)
	}
	if st := stage(&contract.Contract{PactoVersion: "2.6"}, nil, true, time.Time{}); st.Status != StageSkip || st.Reason != "no configuration references" {
		t.Fatalf("no refs: %+v", st)
	}
}

func TestConfigRefStage_VersionOutsideRange(t *testing.T) {
	c := refConsumer("2.6", pgRef, nil)
	c.Dependencies = []contract.Dependency{
		{Name: "pg", Ref: "oci://r/pg@sha256:abc", Compatibility: "^16.0.0"},
		{Name: "other", Ref: "oci://r/other:1.0.0", Compatibility: "^1.0.0"},
		{Name: "loose", Ref: "oci://r/pg:16.1.0", Compatibility: "not a range"},
	}
	st := refStage(t, c, pgBundle("17.0.0", provisioning), true, time.Time{})
	if codes(st) != "CONFIG_REF_VERSION_OUTSIDE_RANGE" || st.Errors[0].Path != "configurations[0].ref" ||
		st.Errors[0].Message != `configuration db: ref "oci://r/pg:16.1.0" resolves postgresql@17.0.0, outside the compatibility ^16.0.0 of dependency pg` {
		t.Fatalf("range: %+v", st)
	}
	if st := refStage(t, c, pgBundle("16.1.0", provisioning), true, time.Time{}); st.Status != StagePass {
		t.Fatalf("in range: %+v", st)
	}
}

func TestConfigRefStage_ProviderLifecycle(t *testing.T) {

	old := pgBundle("16.1.0", provisioning)
	old.Contract.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2027-01-01", Successor: "oci://r/pg2"}
	st := refStage(t, refConsumer("2.6", pgRef, nil), old, true, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if st.Status != StagePass || codes(st) != "DEPENDENCY_SERVICE_DEPRECATED" || st.Warnings[0].Path != "configurations[0]" ||
		st.Warnings[0].Message != "postgresql@16.1.0 is deprecated since 2026-01-01, sunset 2027-01-01; successor oci://r/pg2" {
		t.Fatalf("deprecated: %+v", st)
	}
	st = refStage(t, refConsumer("2.6", pgRef, nil), old, true, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if codes(st) != "DEPENDENCY_SERVICE_SUNSET" || !strings.Contains(st.Warnings[0].Message, "reached its sunset 2027-01-01: it may be retired in any release") {
		t.Fatalf("sunset: %+v", st)
	}
}

func TestDependencyServiceWarnings_Bare(t *testing.T) {
	p := &contract.Contract{Service: contract.Service{Name: "pg", Version: "1.0.0", Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}}}
	if w := DependencyServiceWarnings("dependencies[0]", p, time.Now()); len(w) != 1 || w[0].Message != "pg@1.0.0 is deprecated since 2026-01-01" {
		t.Fatalf("got %+v", w)
	}
	p.Service.Lifecycle = nil
	if w := DependencyServiceWarnings("dependencies[0]", p, time.Now()); w != nil {
		t.Fatalf("got %+v", w)
	}
}

func TestConfigRefsNotEnforced(t *testing.T) {
	c := refConsumer("2.6", "oci://r/pg:16.1.0", map[string]any{"size": "small"})
	c.Configurations = append(c.Configurations, contract.Configuration{Name: "quiet", Ref: "oci://r/pg:16.1.0"}, contract.Configuration{Name: "envd", Ref: "oci://r/pg:16.1.0"})
	c.Environments = []contract.Environment{{Name: "prod", File: "prod.yaml"}, {Name: "gone", File: "gone.yaml"}}
	fsys := fstest.MapFS{"prod.yaml": {Data: []byte("envd: {size: small}\n")}}
	if _, res := configRefsNotEnforced(c, fsys); findingCodes(res) != "W:CONFIG_REF_NOT_ENFORCED@configurations[0].ref,W:CONFIG_REF_NOT_ENFORCED@configurations[2].ref" {
		t.Fatalf("got %s", findingCodes(res))
	}
	if _, res := configRefsNotEnforced(c, nil); findingCodes(res) != "" {
		t.Fatalf("nil FS: %s", findingCodes(res))
	}
}

// envRefStage runs the environments stage of a consumer of version whose
// ref configuration db has values and required, and whose prod environment
// file holds env, with the provider resolvable or not.
func envRefStage(t *testing.T, version string, values map[string]any, required bool, env string, resolvable bool) Stage {
	c := refConsumer(version, pgRef, values)
	c.Configurations[0].Required = required
	c.Environments = []contract.Environment{{Name: "prod", File: "prod.yaml"}}
	fsys := fstest.MapFS{"prod.yaml": {Data: []byte(env)}}
	bundles := map[string]*contract.Bundle{}
	if resolvable {
		bundles[pgRef] = pgBundle("16.1.0", provisioning)
	}
	refs, _ := ResolveConfigRefs(t.Context(), c, fsys, ConfigRefOptions{Resolver: pgResolver(bundles), Strict: true})
	return EnvironmentStageWith(c, fsys, refs)
}

func TestEnvironmentStageConfigRef(t *testing.T) {
	run := func(version string, values map[string]any, required bool, env string, resolvable bool) Stage {
		return envRefStage(t, version, values, required, env, resolvable)
	}
	if st := run("2.5", nil, false, "db: {size: small}\n", true); st.Status != StagePass {
		t.Fatalf("accepts: %+v", st)
	}
	st := run("2.5", nil, false, "db:\n  size: huge\n", true)
	if st.Status != StageFail || codes(st) != "ENVIRONMENT_VALUES_INVALID" || st.Errors[0].File != "prod.yaml" || st.Errors[0].Line != 2 {
		t.Fatalf("rejects: %+v", st)
	}
	st = run("2.5", nil, false, "db: {size: small}\n", false)
	if st.Status != StageSkip || codes(st) != "ENVIRONMENT_VALUES_UNVERIFIED" || !strings.Contains(st.Warnings[0].Message, "which could not be resolved: configuration db: failed to resolve ref") {
		t.Fatalf("unresolved: %+v", st)
	}
}

// From 2.6 a ref configuration has defaults the environment merges over, and a
// required one must pass with them alone, exactly like a local schema.
func TestEnvironmentStageConfigRef_Defaults(t *testing.T) {
	run := func(version string, values map[string]any, required bool, env string, resolvable bool) Stage {
		return envRefStage(t, version, values, required, env, resolvable)
	}
	if st := run("2.6", map[string]any{"size": "small"}, false, "db: {size: null, storageGi: 20}\n", true); st.Status != StageFail || !strings.Contains(st.Errors[0].Message, "missing property 'size'") {
		t.Fatalf("null deletes: %+v", st)
	}
	if st := run("2.6", map[string]any{"size": "small"}, false, "db: {storageGi: 20}\n", true); st.Status != StagePass {
		t.Fatalf("merged: %+v", st)
	}
	if st := run("2.5", nil, true, "{}\n", true); st.Status != StagePass || codes(st) != "" {
		t.Fatalf("2.5 required, no values: %+v", st)
	}
	if st := run("2.6", nil, true, "{}\n", true); codes(st) != "ENVIRONMENT_VALUES_MISSING" {
		t.Fatalf("2.6 required, no values: %+v", st)
	}
}

func TestValidateStages_ConfigRefs(t *testing.T) {
	raw := []byte(`pactoVersion: "2.6"
service: {name: orders, version: 1.0.0}
configurations:
  - {name: db, ref: "oci://r/pg:16.1.0", required: true, values: {size: huge}}
environments:
  - {name: prod, file: prod.yaml}
`)
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{"prod.yaml": {Data: []byte("db: {size: tiny}\n")}}
	names := func(stages []Stage) string {
		var out []string
		for _, st := range stages {
			out = append(out, st.Name+"="+st.Status)
		}
		return strings.Join(out, ",")
	}
	// A failing configuration-refs stage leaves environments running.
	r := pgResolver(map[string]*contract.Bundle{"oci://r/pg:16.1.0": pgBundle("16.1.0", provisioning)})
	stages := ValidateStagesWithResolver(t.Context(), c, raw, fsys, r)
	if names(stages) != "structural=pass,cross-field=pass,policy=pass,configuration-refs=fail,environments=fail" || codes(stages[4]) != "ENVIRONMENT_VALUES_INVALID" {
		t.Fatalf("resolver: %+v", stages)
	}
	// Local-only: not enforced, skipped.
	stages = ValidateDocumentStages(c, contract.DecodeDocument(raw), fsys)
	if names(stages) != "structural=pass,cross-field=pass,policy=pass,configuration-refs=skip,environments=skip" || codes(stages[3]) != "CONFIG_REF_NOT_ENFORCED" {
		t.Fatalf("local: %+v", stages)
	}
	// A failing policy stage skips both.
	c.Policies = []contract.Policy{{Ref: "oci://r/policy:1.0.0"}}
	stages = ValidateStagesWithResolver(t.Context(), c, raw, fsys, nil)
	if names(stages) != "structural=pass,cross-field=pass,policy=fail,configuration-refs=skip,environments=skip" || stages[3].Reason != "policy failed" {
		t.Fatalf("policy: %+v", stages)
	}
	// Unresolved with a nil resolver fails closed.
	c.Policies = nil
	if st := ValidateStagesWithResolverAt(t.Context(), c, raw, fsys, nil, time.Now())[3]; codes(st) != "CONFIG_REF_UNRESOLVED" {
		t.Fatalf("nil resolver: %+v", st)
	}
}
