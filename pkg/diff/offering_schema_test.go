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
