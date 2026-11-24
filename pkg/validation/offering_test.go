package validation

import (
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// offeringSchema is a tiered offering's configuration schema: class, replicas,
// backup and password are tunable; storageGB declares no tier, so it is
// contract.
const offeringSchema = `{"type":"object","additionalProperties":false,"properties":{
"class":{"enum":["small","large"],"x-pacto-tier":"tunable"},
"storageGB":{"type":"integer","minimum":10,"maximum":500},
"replicas":{"type":"integer","minimum":1,"maximum":5,"x-pacto-tier":"tunable"},
"zone":{"enum":["a","b"],"x-pacto-tier":"tunable"},
"backup":{"type":"object","x-pacto-tier":"tunable","properties":{"window":{"type":"string","pattern":"^[0-2][0-9]:00$"}},"additionalProperties":false},
"password":{"type":"string","writeOnly":true,"pattern":"^[A-Za-z0-9]{16,}$","x-pacto-tier":"tunable"}}}`

const offeringRef = "oci://r/postgresql:16.1.0"

// offeringBundle is an offering named name (service.offering name, owner
// team-data) whose one configuration has schema.
func offeringBundle(name, offering, schema string, extra fstest.MapFS) *contract.Bundle {
	fsys := fstest.MapFS{"configuration/schema.json": {Data: []byte(schema)}}
	for k, v := range extra {
		fsys[k] = v
	}
	return &contract.Bundle{
		Contract: &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name, Version: "16.1.0", Offering: offering, Owner: contract.Owner{Team: "team-data"}},
			Configurations: []contract.Configuration{{Name: "default", Schema: "configuration/schema.json"}}},
		FS: fsys,
	}
}

// offeringConsumer takes database values from offeringRef, with base values base,
// in staging and production, whose files hold staging and production.
func offeringConsumer(base map[string]any, staging, production string) (*contract.Contract, fstest.MapFS) {
	c := &contract.Contract{
		PactoVersion: "2.5",
		Service:      contract.Service{Name: "orders", Version: "1.0.0"},
		Configurations: []contract.Configuration{{Name: "database", Ref: offeringRef, ProviderConfiguration: "default", Required: true,
			Values: base}},
		Environments: []contract.Environment{{Name: "staging", File: "environments/staging.yaml"}, {Name: "production", File: "environments/production.yaml"}},
	}
	return c, fstest.MapFS{
		"environments/staging.yaml":    {Data: []byte(staging)},
		"environments/production.yaml": {Data: []byte(production)},
	}
}

func baseValues() map[string]any { return map[string]any{"class": "small", "storageGB": 20} }

// runOffering resolves c's refs against bundle and returns the environments
// and configuration-refs stages.
func runOffering(t *testing.T, c *contract.Contract, fsys fstest.MapFS, bundle *contract.Bundle) (Stage, Stage) {
	t.Helper()
	refs, res := ResolveConfigRefs(t.Context(), c, fsys, ConfigRefOptions{Resolver: pgResolver(map[string]*contract.Bundle{offeringRef: bundle}), Strict: true})
	return EnvironmentStageWith(c, fsys, refs), ConfigRefStage(c, refs, res, ConfigRefOptions{})
}

func pg() *contract.Bundle { return offeringBundle("postgresql", "postgresql", offeringSchema, nil) }

// envProduction runs the stages with production setting prod and staging
// setting nothing.
func envProduction(t *testing.T, prod string) Stage {
	t.Helper()
	c, fsys := offeringConsumer(baseValues(), "{}\n", prod)
	st, _ := runOffering(t, c, fsys, pg())
	return st
}

func TestEnvironmentTierNotTunable(t *testing.T) {
	st := envProduction(t, "database:\n  replicas: 3\n  storageGB: 100\n")
	if codes(st) != string(finding.CodeTierNotTunable) {
		t.Fatalf("got %+v", st.Errors)
	}
	e := st.Errors[0]
	want := "environment production: database.storageGB is contract-tier in offering postgresql 16.1.0, so it cannot change per environment. Next: set it once in configurations[0].values, or ask team-data to mark it x-pacto-tier: tunable"
	if e.Path != "environments[1].file" || e.File != "environments/production.yaml" || e.Line != 3 || e.Message != want {
		t.Fatalf("got %+v", e)
	}
}

func TestEnvironmentParameterOutOfRange(t *testing.T) {
	for _, tc := range []struct{ values, path, next string }{
		{"replicas: 9", "database.replicas", "use a value of type integer, >= 1, <= 5; pacto fleet get postgresql lists the parameters"},
		{`replicas: "2"`, "database.replicas", "use a value of type integer"},
		{"zone: north", "database.zone", "use a value in a, b; pacto fleet get postgresql"},
		{`backup: {window: "25:30"}`, "database.backup.window", "use a value of type string, matching ^[0-2][0-9]:00$"},
	} {
		st := envProduction(t, "database: {"+tc.values+"}\n")
		if codes(st) != string(finding.CodeParameterOutOfRange) || !strings.Contains(st.Errors[0].Message, tc.path+" is outside the parameters of offering postgresql 16.1.0. Next: "+tc.next) {
			t.Fatalf("%s: got %+v", tc.values, st.Errors)
		}
	}
}

func TestEnvironmentParameterNotDeclared(t *testing.T) {
	st := envProduction(t, "database: {replica: 2}\n")
	if codes(st) != string(finding.CodeParameterNotDeclared) || !strings.HasSuffix(st.Errors[0].Message,
		`database.replica is not a parameter of offering postgresql 16.1.0. Next: remove it (did you mean "replicas"?); pacto fleet get postgresql lists the parameters`) {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestEnvironmentClassNotOffered(t *testing.T) {
	st := envProduction(t, "database: {class: medim}\n")
	if codes(st) != string(finding.CodeClassNotOffered) || !strings.HasSuffix(st.Errors[0].Message, "names a class offering postgresql 16.1.0 does not offer. Next: use one of small, large") {
		t.Fatalf("got %+v", st.Errors)
	}
	st = envProduction(t, "database: {class: smal}\n")
	if codes(st) != string(finding.CodeClassNotOffered) || !strings.HasSuffix(st.Errors[0].Message, `Next: use one of small, large (did you mean "small"?)`) {
		t.Fatalf("got %+v", st.Errors)
	}
	// A contract-tier class is reported once, as the class, never also as a tier.
	tight := offeringBundle("postgresql", "postgresql", strings.Replace(offeringSchema, `"enum":["small","large"],"x-pacto-tier":"tunable"`, `"enum":["small","large"]`, 1), nil)
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {class: medim}\n")
	if st, _ := runOffering(t, c, fsys, tight); codes(st) != string(finding.CodeClassNotOffered) {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestContractTierInBaseValuesAppliesEverywhere(t *testing.T) {
	c, fsys := offeringConsumer(baseValues(), "database: {replicas: 1}\n", "database: {replicas: 3, class: large}\n")
	env, refs := runOffering(t, c, fsys, pg())
	if env.Status != StagePass || refs.Status != StagePass {
		t.Fatalf("got %+v %+v", env, refs)
	}
}

func TestBaseValuesOnOfferingGetParameterCode(t *testing.T) {
	c, fsys := offeringConsumer(map[string]any{"class": "small", "storageGB": 5}, "{}\n", "{}\n")
	_, refs := runOffering(t, c, fsys, pg())
	if codes(refs) != string(finding.CodeParameterOutOfRange) || refs.Errors[0].Path != "configurations[0].values" ||
		!strings.HasPrefix(refs.Errors[0].Message, "configuration database: storageGB is outside the parameters") {
		t.Fatalf("got %+v", refs.Errors)
	}
}

func TestFilelessServiceValidatesBaseValuesOnly(t *testing.T) {
	c, fsys := offeringConsumer(baseValues(), "", "")
	c.PactoVersion = "2.6"
	c.Environments = []contract.Environment{{Name: "production"}}
	if env, refs := runOffering(t, c, fsys, pg()); env.Status != StagePass || refs.Status != StagePass {
		t.Fatalf("got %+v %+v", env, refs)
	}
	c.Configurations[0].Values = map[string]any{"class": "small", "storageGB": 5}
	env, refs := runOffering(t, c, fsys, pg())
	if findingCodes(ValidationResult{Errors: refs.Errors, Warnings: refs.Warnings})+"|"+findingCodes(ValidationResult{Errors: env.Errors, Warnings: env.Warnings}) != "E:ParameterOutOfRange@configurations[0].values|E:ParameterOutOfRange@environments[0]" {
		t.Fatalf("got %+v %+v", refs.Errors, env.Errors)
	}
}

func TestEnvironmentNullOnContractFieldIsTierNotTunable(t *testing.T) {
	if st := envProduction(t, "database: {storageGB: null}\n"); codes(st) != string(finding.CodeTierNotTunable) {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestEnvironmentNullOnTunableObjectPasses(t *testing.T) {
	if st := envProduction(t, "database: {backup: null}\n"); st.Status != StagePass {
		t.Fatalf("got %+v", st)
	}
}

func TestEnvironmentScalarReplacesRootIsTierNotTunable(t *testing.T) {
	st := envProduction(t, "database: 5\n")
	if codes(st) != string(finding.CodeTierNotTunable) || !strings.HasPrefix(st.Errors[0].Message, "environment production: database is contract-tier") {
		t.Fatalf("got %+v", st.Errors)
	}
	// A tunable root lets the schema judge the values: a scalar is out of range.
	open := offeringBundle("postgresql", "postgresql", strings.Replace(offeringSchema, `{"type":"object",`, `{"type":"object","x-pacto-tier":"tunable",`, 1), nil)
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: 5\n")
	if st, _ := runOffering(t, c, fsys, open); codes(st) != string(finding.CodeParameterOutOfRange) {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestChainedRefToOfferingIsTierChecked(t *testing.T) {
	wrapper := &contract.Bundle{
		Contract: &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "db-wrapper", Version: "1.0.0", Owner: contract.Owner{Team: "team-app"}},
			Configurations: []contract.Configuration{{Name: "db", Ref: "./pg", ProviderConfiguration: "default"}}},
		FS: fstest.MapFS{},
	}
	r := &originResolver{root: "/c", bundles: map[string]*contract.Bundle{"/c\x00./wrapper": wrapper, "/wrapper\x00./pg": pg()},
		childOf: map[string]string{"/c\x00./wrapper": "/wrapper", "/wrapper\x00./pg": "/pg"}}
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {storageGB: 100}\n")
	c.Configurations[0].Ref, c.Configurations[0].ProviderConfiguration = "./wrapper", ""
	refs, _ := ResolveConfigRefs(t.Context(), c, fsys, ConfigRefOptions{Resolver: r})
	if refs["database"].Provider.Service.Name != "db-wrapper" || refs["database"].SchemaProvider.Service.Name != "postgresql" {
		t.Fatalf("got %+v", refs["database"])
	}
	st := EnvironmentStageWith(c, fsys, refs)
	if codes(st) != string(finding.CodeTierNotTunable) || !strings.Contains(st.Errors[0].Message, "ask team-data to mark it") {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestInferredOfferingValuesStayUnverified(t *testing.T) {
	for name, record := range map[string]string{"record": "format: pacto.inferred/v1\n", "unreadable record": "format: nope\n"} {
		inferred := offeringBundle("postgresql", "postgresql", offeringSchema, fstest.MapFS{contract.InferredRecordFile: {Data: []byte(record)}})
		c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {storageGB: 100, replica: 2}\n")
		st, _ := runOffering(t, c, fsys, inferred)
		if st.Status != StageSkip || codes(st) != "ENVIRONMENT_VALUES_UNVERIFIED,ENVIRONMENT_VALUES_UNVERIFIED" ||
			!strings.Contains(st.Warnings[0].Message, "the offering postgresql is inferred, so its parameters are not accepted") {
			t.Fatalf("%s: got %+v", name, st)
		}
	}
}

func TestUntieredOfferingKeepsEnvironmentValues(t *testing.T) {
	const shared = `{"type":"object","properties":{"platform":{"type":"object","properties":{"region":{"enum":["eu-west-1","us-east-1"]}}}}}`
	b := offeringBundle("platform-app-config", "shared-config", shared, nil)
	c, fsys := offeringConsumer(nil, "database: {platform: {region: eu-west-1}}\n", "database: {platform: {region: us-east-1}}\n")
	c.Configurations[0].Required = false
	if st, _ := runOffering(t, c, fsys, b); st.Status != StagePass {
		t.Fatalf("got %+v", st)
	}
	c, fsys = offeringConsumer(nil, "{}\n", "database: {platform: {region: mars}}\n")
	if st, _ := runOffering(t, c, fsys, b); codes(st) != "ENVIRONMENT_VALUES_INVALID" {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestNonOfferingRefKeepsEnvironmentCodes(t *testing.T) {
	required := strings.Replace(offeringSchema, `"additionalProperties":false,"properties"`, `"additionalProperties":false,"required":["class"],"properties"`, 1)
	plain := offeringBundle("postgresql", "", required, nil)
	tiered := offeringBundle("postgresql", "postgresql", required, nil)
	for name, b := range map[string]*contract.Bundle{"plain": plain, "tiered": tiered} {
		// A required value the environment leaves out keeps today's codes.
		c, fsys := offeringConsumer(nil, "database: {replicas: 2}\n", "{}\n")
		if st, _ := runOffering(t, c, fsys, b); codes(st) != "ENVIRONMENT_VALUES_INVALID,ENVIRONMENT_VALUES_MISSING" {
			t.Fatalf("%s: got %+v", name, st.Errors)
		}
	}
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {storageGB: 1000}\n")
	if st, _ := runOffering(t, c, fsys, plain); codes(st) != "ENVIRONMENT_VALUES_INVALID" {
		t.Fatalf("got %+v", st.Errors)
	}
	c.Configurations[0].Values = map[string]any{"class": "small", "storageGB": 5}
	if _, refs := runOffering(t, c, fsys, plain); codes(refs) != "CONFIG_VALUES_VALIDATION_FAILED" {
		t.Fatalf("got %+v", refs.Errors)
	}
}

func TestUnresolvedOfferingRefStaysUnverified(t *testing.T) {
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {storageGB: 100}\n")
	refs, _ := ResolveConfigRefs(t.Context(), c, fsys, ConfigRefOptions{Resolver: pgResolver(nil)})
	if st := EnvironmentStageWith(c, fsys, refs); st.Status != StageSkip || strings.Contains(codes(st), string(finding.CodeTierNotTunable)) {
		t.Fatalf("got %+v", st)
	}
}

func TestUnreadableOfferingSchemaIsUnverified(t *testing.T) {
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {storageGB: 100}\n")
	refs, _ := ResolveConfigRefs(t.Context(), c, fsys, ConfigRefOptions{Resolver: pgResolver(map[string]*contract.Bundle{offeringRef: pg()})})
	r := refs["database"]
	r.ProviderFS = fstest.MapFS{} // the schema compiled, then cannot be read again
	refs["database"] = r
	st := EnvironmentStageWith(c, fsys, refs)
	if st.Status != StageSkip || strings.Contains(codes(st), string(finding.CodeTierNotTunable)) || !strings.Contains(st.Warnings[0].Message, "cannot read the schema configuration/schema.json of offering postgresql") {
		t.Fatalf("got %+v", st)
	}
	// The inline values then keep the generic check.
	c.Configurations[0].Values = map[string]any{"class": "small", "storageGB": 5}
	if st := ConfigRefStage(c, refs, ValidationResult{}, ConfigRefOptions{}); codes(st) != "CONFIG_VALUES_VALIDATION_FAILED" {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestParameterErrorNeverEchoesWriteOnlyValue(t *testing.T) {
	st := envProduction(t, `database: {password: "short-secret"}`+"\n")
	if codes(st) != string(finding.CodeParameterOutOfRange) {
		t.Fatalf("got %+v", st.Errors)
	}
	for _, e := range st.Errors {
		if strings.Contains(e.Message, "short-secret") {
			t.Fatalf("message echoes the value: %s", e.Message)
		}
	}
	// An enum under writeOnly names no closest value either.
	secretEnum := strings.Replace(offeringSchema, `"class":{"enum":["small","large"],`, `"class":{"enum":["small","large"],"writeOnly":true,`, 1)
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {class: smal}\n")
	if st, _ := runOffering(t, c, fsys, offeringBundle("postgresql", "postgresql", secretEnum, nil)); strings.Contains(st.Errors[0].Message, "did you mean") {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestEveryParameterErrorHasNext(t *testing.T) {
	re := regexp.MustCompile(`^(environment|configuration) .+\. Next: .+$`)
	c, fsys := offeringConsumer(map[string]any{"class": "small", "storageGB": 5, "extra": 1},
		"database: {class: medim, replicas: 9}\n", "database: {storageGB: 100, replica: 2, backup: {window: x}}\n")
	env, refs := runOffering(t, c, fsys, pg())
	seen := map[string]bool{}
	for _, e := range append(env.Errors, refs.Errors...) {
		seen[e.Code] = true
		if !re.MatchString(e.Message) {
			t.Fatalf("%s: %q", e.Code, e.Message)
		}
	}
	for _, code := range []string{string(finding.CodeTierNotTunable), string(finding.CodeParameterOutOfRange), string(finding.CodeParameterNotDeclared), string(finding.CodeClassNotOffered)} {
		if !seen[code] {
			t.Fatalf("no %s in %+v %+v", code, env.Errors, refs.Errors)
		}
	}
}

func TestSchemaBoundAndOwnerFallbacks(t *testing.T) {
	doc := map[string]any{"properties": map[string]any{"a": map[string]any{"const": "x", "exclusiveMinimum": 1.0, "exclusiveMaximum": 9.0, "multipleOf": 2.0, "minLength": 1.0, "maxLength": 3.0}}}
	if got := schemaBound(doc, []string{"a"}); got != "equal to x, > 1, < 9, a multiple of 2, at least 1 characters long, at most 3 characters long" {
		t.Fatalf("got %q", got)
	}
	r := ResolvedConfigRef{SchemaProvider: &contract.Contract{Service: contract.Service{Name: "pg"}}}
	if got := nextStep(string(finding.CodeTierNotTunable), r, 0, doc, nil, "", nil); !strings.Contains(got, "ask the offering owner") {
		t.Fatalf("got %q", got)
	}
	if got := nextStep(string(finding.CodeParameterOutOfRange), r, 0, doc, []string{"zz"}, "", nil); got != "use a value the schema allows; pacto fleet get pg lists the parameters" {
		t.Fatalf("got %q", got)
	}
}

func TestTierNotTunableBehindRefSaysSo(t *testing.T) {
	refSchema := strings.Replace(offeringSchema, `"properties":{`, `"$defs":{"pool":{"type":"object","x-pacto-tier":"tunable"}},"properties":{"pool":{"$ref":"#/$defs/pool"},`, 1)
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {pool: {size: 3}, storageGB: 100}\n")
	st, _ := runOffering(t, c, fsys, offeringBundle("postgresql", "postgresql", refSchema, nil))
	if len(st.Errors) != 2 || !strings.Contains(st.Errors[0].Message, "database.pool.size is contract-tier in offering postgresql 16.1.0, so it cannot change per environment; the tier sits behind a $ref Pacto does not follow. Next: ") ||
		strings.Contains(st.Errors[1].Message, "behind") {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestFormatViolationNeverEchoesValue(t *testing.T) {
	draft7 := strings.Replace(offeringSchema, `{"type":"object",`, `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object",`, 1)
	draft7 = strings.Replace(draft7, `"properties":{`, `"properties":{"admin":{"type":"string","format":"email","writeOnly":true,"x-pacto-tier":"tunable"},`, 1)
	c, fsys := offeringConsumer(baseValues(), "{}\n", "database: {admin: secret-not-an-address}\n")
	st, _ := runOffering(t, c, fsys, offeringBundle("postgresql", "postgresql", draft7, nil))
	if codes(st) != string(finding.CodeParameterOutOfRange) || strings.Contains(st.Errors[0].Message, "secret-not-an-address") ||
		!strings.Contains(st.Errors[0].Message, "Next: use a value of type string, in format email;") {
		t.Fatalf("got %+v", st.Errors)
	}
}

func TestTierNotTunableField(t *testing.T) {
	st := envProduction(t, "database:\n  storageGB: 100\n")
	if got := TierNotTunableField(st.Errors[0].Message); got != "environment production: database.storageGB" {
		t.Fatalf("got %q from %q", got, st.Errors[0].Message)
	}
	if got := TierNotTunableField("environment production: database.replicas is outside the parameters"); got != "" {
		t.Fatalf("got %q", got)
	}
}
