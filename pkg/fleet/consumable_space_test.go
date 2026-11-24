package fleet

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// csmPGSchema is a closed postgresql configuration schema with a class, two
// tunable bounded fields and an untiered one.
const csmPGSchema = `{"type":"object","additionalProperties":false,"required":["engine","class","storageGB"],"properties":{
"engine":{"type":"string","enum":["postgres"],"x-pacto-tier":"contract"},
"class":{"type":"string","enum":["small","large"]},
"storageGB":{"type":"integer","minimum":10,"maximum":500,"default":20,"x-pacto-tier":"tunable"},
"backupRetentionDays":{"type":"integer","minimum":1,"maximum":35,"x-pacto-tier":"tunable"},
"name":{"type":"string","pattern":"^[a-z]+$"}}}`

// csmSpaceRev is pg 17.0.0, accepted under a rule, whose required
// configuration db has schema as its schema file.
func csmSpaceRev(schema string) RawRevision {
	r := csmRev("pg", "17.0.0", "postgresql", csmRule())
	r.Bundle.Contract.Configurations = []contract.Configuration{{Name: "db", Schema: "configuration/schema.json", Required: true}, {Name: "extra", Schema: "missing.json"}}
	r.Bundle.FS = fstest.MapFS{"configuration/schema.json": {Data: []byte(schema)}}
	return r
}

// spaceOf is the one consumable provider of a fleet of revs.
func spaceOf(t *testing.T, revs ...RawRevision) ConsumableProvider {
	t.Helper()
	return wantProvider(t, consumableList(t, csmFleet(t, nil, revs...)), AcceptancePublisher, csmRule().Signature.Signer)
}

// paramAt is p's parameter at path.
func paramAt(t *testing.T, p ConsumableProvider, path string) OfferingParameter {
	t.Helper()
	for _, x := range p.Parameters {
		if x.Path == path {
			return x
		}
	}
	t.Fatalf("no parameter %s in %+v", path, p.Parameters)
	return OfferingParameter{}
}

func fl(v float64) *float64 { return &v }

func TestConsumableTiersAndBounds(t *testing.T) {
	p := spaceOf(t, csmSpaceRev(csmPGSchema))
	want := map[string]OfferingParameter{
		"engine":              {Configuration: "db", Path: "engine", Type: "string", Tier: "contract", Required: true, Enum: []any{"postgres"}, EnumTotal: 1},
		"storageGB":           {Configuration: "db", Path: "storageGB", Type: "integer", Tier: "tunable", Required: true, Enum: []any{}, Minimum: fl(10), Maximum: fl(500), Default: float64(20)},
		"backupRetentionDays": {Configuration: "db", Path: "backupRetentionDays", Type: "integer", Tier: "tunable", Enum: []any{}, Minimum: fl(1), Maximum: fl(35)},
		"name":                {Configuration: "db", Path: "name", Type: "string", Tier: "contract", Enum: []any{}, Pattern: "^[a-z]+$"},
	}
	for path, w := range want {
		if got := paramAt(t, p, path); !reflect.DeepEqual(got, w) {
			t.Errorf("%s = %+v, want %+v", path, got, w)
		}
	}
	if p.ParametersTotal != 5 || p.ParametersTruncated || !reflect.DeepEqual(p.SchemaPaths, []string{"configuration/schema.json"}) {
		t.Errorf("provider = %+v", p)
	}
}

func TestConsumableUntieredFieldIsContract(t *testing.T) {
	p := spaceOf(t, csmSpaceRev(csmPGSchema))
	for _, path := range []string{"class", "name"} {
		if tier := paramAt(t, p, path).Tier; tier != "contract" {
			t.Errorf("%s tier = %s, want contract", path, tier)
		}
	}
}

func TestConsumableClassesFromClassEnum(t *testing.T) {
	if p := spaceOf(t, csmSpaceRev(csmPGSchema)); !reflect.DeepEqual(p.Classes, []string{"small", "large"}) {
		t.Errorf("classes = %v", p.Classes)
	}
	noClass := `{"type":"object","additionalProperties":false,"properties":{"size":{"type":"integer"}}}`
	if p := spaceOf(t, csmSpaceRev(noClass)); p.Classes == nil || len(p.Classes) != 0 {
		t.Errorf("classes = %#v, want empty", p.Classes)
	}
}

func TestConsumableOpenSchemaExcluded(t *testing.T) {
	open := `{"type":"object","additionalProperties":false,"properties":{
"backups":{"type":"object","properties":{"days":{"type":"integer"}}},
"anything":{}}}`
	l := consumableList(t, csmFleet(t, nil, csmSpaceRev(open)))
	wantExcluded(t, l, LimitationOfferingNotConsumable, "postgresql: db: schema.properties.anything, schema.properties.backups", "an open parameter schema", CompletenessComplete)
	if got := someNamesOf([]string{"a", "b", "c", "d"}, ", "); got != "a, b, c, 1 more" {
		t.Errorf("paths = %q", got)
	}
}

func TestConsumableSchemaUnreadablePartial(t *testing.T) {
	r := csmSpaceRev(csmPGSchema)
	r.Bundle.FS = fstest.MapFS{}
	l := consumableList(t, csmFleet(t, nil, r))
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql: db: the file could not be read", "Parameter schema unreadable", CompletenessPartial)
	// An unparsable schema reads the same.
	l = consumableList(t, csmFleet(t, nil, csmSpaceRev("{")))
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "could not be parsed", "Parameter schema unreadable", CompletenessPartial)
	// So does a configuration reference that does not resolve.
	r = csmSpaceRev(csmPGSchema)
	r.Bundle.Contract.Configurations = []contract.Configuration{{Name: "db", Ref: "oci://" + csmDomain + "/nowhere:1.0.0", Required: true}}
	l = consumableList(t, csmFleet(t, nil, r))
	wantExcluded(t, l, LimitationOfferingAcceptanceUnknown, "postgresql: db: its configuration reference does not resolve", "Parameter schema unreadable", CompletenessPartial)
}

func TestConsumableRefSchemaParameters(t *testing.T) {
	cfg := svc24("pg-config", "1.0.0", "")
	cfg.Configurations = []contract.Configuration{{Name: "db", Schema: "config/schema.json"}}
	cfgRev := ociRev(csmDomain, cfg, fstest.MapFS{"config/schema.json": {Data: []byte(csmPGSchema)}})
	r := csmRev("pg", "17.0.0", "postgresql", csmRule())
	r.Bundle.Contract.Configurations = []contract.Configuration{{Name: "db", Ref: "oci://" + csmDomain + "/pg-config@" + testDigest(csmDomain, "pg-config", "1.0.0"), Required: true}}
	p := spaceOf(t, r, cfgRev)
	if p.ParametersTotal != 5 || paramAt(t, p, "storageGB").Tier != "tunable" || !reflect.DeepEqual(p.SchemaPaths, []string{"config/schema.json"}) {
		t.Errorf("provider = %+v", p)
	}
}

func TestConsumableSensitiveFieldHasNoValues(t *testing.T) {
	schema := `{"type":"object","additionalProperties":false,"properties":{
"password":{"type":"string","writeOnly":true,"default":"x","enum":["x","y"],"examples":["x"]},
"auth":{"type":"object","writeOnly":true,"additionalProperties":false,"properties":{"token":{"type":"string","const":"t","default":"t"}}}}}`
	p := spaceOf(t, csmSpaceRev(schema))
	for _, path := range []string{"password", "auth.token"} {
		x := paramAt(t, p, path)
		if !x.Sensitive || x.Default != nil || len(x.Enum) != 0 || x.EnumTotal != 0 || x.Type != "string" {
			t.Errorf("%s = %+v, want sensitive with no values", path, x)
		}
	}
}

func TestConsumableGuaranteesFromInterfaces(t *testing.T) {
	r := csmRev("pg", "17.0.0", "postgresql", csmRule())
	r.Bundle.Contract.Interfaces = []contract.Interface{
		{Name: "sql", Type: "http", Guarantees: &contract.Guarantees{Timeout: "5s", Idempotent: true}},
		{Name: "admin", Type: "http"},
	}
	p := spaceOf(t, r)
	want := []ConsumableGuarantee{{Interface: "sql", Guarantees: contract.Guarantees{Timeout: "5s", Idempotent: true}, Text: "timeout 5s, idempotent"}}
	if !reflect.DeepEqual(p.Guarantees, want) {
		t.Errorf("guarantees = %+v", p.Guarantees)
	}
}

func TestConsumableParametersBounded(t *testing.T) {
	var props []string
	for i := range 60 {
		props = append(props, fmt.Sprintf(`"f%02d":{"type":"integer"}`, i))
	}
	p := spaceOf(t, csmSpaceRev(`{"type":"object","additionalProperties":false,"properties":{`+strings.Join(props, ",")+`}}`))
	if len(p.Parameters) != MaxConsumableParameters || p.ParametersTotal != 60 || !p.ParametersTruncated {
		t.Errorf("parameters = %d, total %d, truncated %v", len(p.Parameters), p.ParametersTotal, p.ParametersTruncated)
	}
}

func TestConsumableEnumBounded(t *testing.T) {
	var values []string
	for i := range 80 {
		values = append(values, fmt.Sprintf(`"v%02d"`, i))
	}
	p := spaceOf(t, csmSpaceRev(`{"type":"object","additionalProperties":false,"properties":{"class":{"type":"string","enum":[`+strings.Join(values, ",")+`]}}}`))
	if x := paramAt(t, p, "class"); len(x.Enum) != MaxParameterEnum || x.EnumTotal != 80 {
		t.Errorf("enum = %d, total %d", len(x.Enum), x.EnumTotal)
	}
}

func TestConsumableNoConfigurationEmptySpace(t *testing.T) {
	p := spaceOf(t, csmRev("pg", "17.0.0", "postgresql", csmRule()))
	if p.Parameters == nil || len(p.Parameters) != 0 || p.ParametersTotal != 0 || len(p.Classes) != 0 || len(p.SchemaPaths) != 0 || p.Guarantees == nil {
		t.Errorf("provider = %+v", p)
	}
}
