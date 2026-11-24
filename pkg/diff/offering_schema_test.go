package diff

import (
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// storageSchema is a closed provisioning schema: storageGB up to maximum, a
// class from classes and the required list. extra adds properties.
func storageSchema(maximum int, classes string, required string, extra string) fstest.MapFS {
	return fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object","additionalProperties":false,` +
		`"required":[` + required + `],"properties":{` +
		`"storageGB":{"type":"integer","maximum":` + strconv.Itoa(maximum) + `},` +
		`"class":{"type":"string","enum":[` + classes + `]}` + extra + `}}`)}}
}

// configContract is a contract with one provisioning configuration; offering
// sets service.offering.
func configContract(offering string) *contract.Contract {
	c := minimalContract()
	c.Service.Offering = offering
	c.Configurations = []contract.Configuration{{Name: "provisioning", Schema: "schema.json"}}
	return c
}

// worstConfig is the worst classification diffConfiguration gives the change
// from oldFS to newFS between contracts declaring oldOffering and newOffering.
func worstConfig(t *testing.T, oldOffering, newOffering string, oldFS, newFS fstest.MapFS) Classification {
	t.Helper()
	changes := diffConfiguration(configContract(oldOffering), configContract(newOffering), oldFS, newFS)
	if len(changes) == 0 {
		t.Fatal("no changes")
	}
	worst := NonBreaking
	for _, c := range changes {
		worst = max(worst, c.Classification)
	}
	return worst
}

const twoClasses = `"small","large"`

// assertOfferingGrade checks the worst grade of the change from the base
// offering schema to next, both sides declaring service.offering.
func assertOfferingGrade(t *testing.T, next fstest.MapFS, want Classification) {
	t.Helper()
	if got := worstConfig(t, "postgresql", "postgresql", storageSchema(500, twoClasses, `"class"`, ""), next); got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOfferingSchemaRemovedFieldBreaks(t *testing.T) {
	assertOfferingGrade(t, fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object","additionalProperties":false,"required":["class"],` +
		`"properties":{"class":{"type":"string","enum":["small","large"]}}}`)}}, Breaking)
}

func TestOfferingSchemaNarrowedBoundBreaks(t *testing.T) {
	assertOfferingGrade(t, storageSchema(100, twoClasses, `"class"`, ""), Breaking)
}

func TestOfferingSchemaRemovedClassBreaks(t *testing.T) {
	assertOfferingGrade(t, storageSchema(500, `"small"`, `"class"`, ""), Breaking)
}

func TestOfferingSchemaNewRequiredFieldBreaks(t *testing.T) {
	assertOfferingGrade(t, storageSchema(500, twoClasses, `"class","zone"`, `,"zone":{"type":"string"}`), Breaking)
}

func TestOfferingSchemaNewOptionalFieldCompatible(t *testing.T) {
	assertOfferingGrade(t, storageSchema(500, twoClasses, `"class"`, `,"zone":{"type":"string"}`), NonBreaking)
}

func TestOfferingSchemaWidenedBoundCompatible(t *testing.T) {
	assertOfferingGrade(t, storageSchema(1000, twoClasses, `"class"`, ""), NonBreaking)
}

func TestOfferingSchemaAddedClassCompatible(t *testing.T) {
	assertOfferingGrade(t, storageSchema(500, `"small","large","xl"`, `"class"`, ""), NonBreaking)
}

// A contract without service.offering keeps the directionless grading: every
// structural edit is potentially breaking, a widened bound included.
func TestConfigurationDiffUnchangedForServices(t *testing.T) {
	base := storageSchema(500, twoClasses, `"class"`, "")
	for _, tt := range []struct {
		name string
		new  fstest.MapFS
		want Classification
	}{
		{"widened bound", storageSchema(1000, twoClasses, `"class"`, ""), PotentialBreaking},
		{"narrowed bound", storageSchema(100, twoClasses, `"class"`, ""), PotentialBreaking},
		{"new optional field", storageSchema(500, twoClasses, `"class"`, `,"zone":{"type":"string"}`), PotentialBreaking},
		{"new required field", storageSchema(500, twoClasses, `"class","zone"`, `,"zone":{"type":"string"}`), Breaking},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := worstConfig(t, "", "", base, tt.new); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Only one side declaring service.offering is not enough to read the schema
// as consumer input.
func TestOfferingSchemaOneSideOfferingUsesUnknown(t *testing.T) {
	base, widened := storageSchema(500, twoClasses, `"class"`, ""), storageSchema(1000, twoClasses, `"class"`, "")
	for _, sides := range [][2]string{{"", "postgresql"}, {"postgresql", ""}} {
		if got := worstConfig(t, sides[0], sides[1], base, widened); got != PotentialBreaking {
			t.Errorf("%q -> %q: got %v, want PotentialBreaking", sides[0], sides[1], got)
		}
	}
}

// Policies keep the directionless grading even on an offering.
func TestPolicyDiffUnchanged(t *testing.T) {
	old, new := configContract("postgresql"), configContract("postgresql")
	old.Policies = []contract.Policy{{Name: "p", Schema: "schema.json"}}
	new.Policies = []contract.Policy{{Name: "p", Schema: "schema.json"}}
	changes := diffPolicy(old, new, storageSchema(500, twoClasses, `"class"`, ""), storageSchema(1000, twoClasses, `"class"`, ""))
	if len(changes) != 1 || changes[0].Classification != PotentialBreaking {
		t.Errorf("changes = %+v", changes)
	}
}

// tierSchema is a closed schema whose storageGB declares tier, or no tier
// when tier is empty.
func tierSchema(tier string) fstest.MapFS {
	kw := ""
	if tier != "" {
		kw = `,"x-pacto-tier":"` + tier + `"`
	}
	return fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object","additionalProperties":false,` +
		`"properties":{"storageGB":{"type":"integer"` + kw + `}}}`)}}
}

// tierChange is the one change diffConfiguration gives an offering whose
// storageGB tier moves from old to new.
func tierChange(t *testing.T, old, new string) Change {
	t.Helper()
	changes := diffConfiguration(configContract("postgresql"), configContract("postgresql"), tierSchema(old), tierSchema(new))
	if len(changes) != 1 {
		t.Fatalf("changes = %+v", changes)
	}
	return changes[0]
}

func TestOfferingSchemaTierTunableToContractBreaks(t *testing.T) {
	c := tierChange(t, "tunable", "contract")
	if c.Classification != Breaking {
		t.Errorf("got %v, want Breaking", c.Classification)
	}
	if want := "storageGB moved from tunable to contract: a consumer that set it per environment breaks"; c.Reason != want {
		t.Errorf("reason = %q, want %q", c.Reason, want)
	}
}

// An untiered field is contract (#452 D2), so dropping tunable breaks.
func TestOfferingSchemaTunableRemovedBreaks(t *testing.T) {
	if c := tierChange(t, "tunable", ""); c.Classification != Breaking {
		t.Errorf("got %v, want Breaking", c.Classification)
	}
}

func TestOfferingSchemaContractToTunableCompatible(t *testing.T) {
	for _, old := range []string{"contract", "", "bogus"} {
		c := tierChange(t, old, "tunable")
		if c.Classification != NonBreaking {
			t.Errorf("%q -> tunable: got %v, want NonBreaking", old, c.Classification)
		}
		if want := "storageGB moved from contract to tunable"; c.Reason != want {
			t.Errorf("reason = %q, want %q", c.Reason, want)
		}
	}
}

// An unknown tier value reads as contract on both sides: no consumer moves.
func TestOfferingSchemaTierSpellingChangeCompatible(t *testing.T) {
	if c := tierChange(t, "contract", "bogus"); c.Classification != NonBreaking {
		t.Errorf("got %v, want NonBreaking", c.Classification)
	}
}

// A root tier names the schema root, and a property literally named
// x-pacto-tier is a field, graded like any other.
func TestOfferingSchemaTierPathShapes(t *testing.T) {
	root := func(tier string) fstest.MapFS {
		return fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object","x-pacto-tier":"` + tier + `"}`)}}
	}
	changes := diffConfiguration(configContract("postgresql"), configContract("postgresql"), root("tunable"), root("contract"))
	if len(changes) != 1 || changes[0].Classification != Breaking ||
		changes[0].Reason != "the schema root moved from tunable to contract: a consumer that set it per environment breaks" {
		t.Errorf("root: %+v", changes)
	}
	prop := fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object","additionalProperties":false,` +
		`"properties":{"x-pacto-tier":{"type":"string"}}}`)}}
	empty := fstest.MapFS{"schema.json": {Data: []byte(`{"type":"object","additionalProperties":false,"properties":{}}`)}}
	if got := worstConfig(t, "postgresql", "postgresql", prop, empty); got != Breaking {
		t.Errorf("removed property named x-pacto-tier: got %v, want Breaking", got)
	}
}
