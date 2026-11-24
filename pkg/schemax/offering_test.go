package schemax

import (
	"encoding/json"
	"testing"
)

const tieredSchema = `{"type":"object","additionalProperties":false,"properties":{
"class":{"enum":["small","large"],"x-pacto-tier":"tunable"},
"storageGB":{"type":"integer","minimum":10,"maximum":500},
"backup":{"type":"object","x-pacto-tier":"tunable","properties":{"window":{"type":"string"},"keep":{"type":"integer","x-pacto-tier":"contract"}}},
"labels":{"type":"object","x-pacto-tier":"tunable","additionalProperties":{"type":"string"}},
"shared":{"$ref":"#/$defs/s"},
"mixed":{"type":"object","x-pacto-tier":"tunable","properties":{"a":{"oneOf":[{"type":"string"}]}}},
"odd":{"type":"string","x-pacto-tier":"Tunable"}}}`

func decode(t *testing.T, s string) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestTierAtUntieredIsContract(t *testing.T) {
	doc := decode(t, tieredSchema)
	for _, p := range [][]string{{"storageGB"}, {}, {"nope"}} {
		if tier, declared := TierAt(doc, p); tier != TierContract || declared {
			t.Fatalf("%v: got %s %v", p, tier, declared)
		}
	}
	if tier, _ := TierAt("not a schema", []string{"x"}); tier != TierContract {
		t.Fatalf("got %s", tier)
	}
}

func TestTierAtNearestAncestorDecides(t *testing.T) {
	doc := decode(t, tieredSchema)
	for _, tc := range []struct {
		path []string
		want string
	}{
		{[]string{"class"}, TierTunable},
		{[]string{"backup"}, TierTunable},
		{[]string{"backup", "window"}, TierTunable},
		{[]string{"backup", "keep"}, TierContract},
		{[]string{"labels", "team"}, TierTunable},
		{[]string{"labels", "team", "deeper"}, TierTunable},
	} {
		if tier, declared := TierAt(doc, tc.path); tier != tc.want || !declared {
			t.Fatalf("%v: got %s %v", tc.path, tier, declared)
		}
	}
}

func TestTierAtBehindRefIsContract(t *testing.T) {
	doc := decode(t, tieredSchema)
	for _, p := range [][]string{{"shared"}, {"shared", "x"}, {"mixed", "a"}} {
		if tier, declared := TierAt(doc, p); tier != TierContract || declared {
			t.Fatalf("%v: got %s %v", p, tier, declared)
		}
	}
}

func TestTierUnknownValueIsContract(t *testing.T) {
	if tier, declared := TierAt(decode(t, tieredSchema), []string{"odd"}); tier != TierContract || !declared {
		t.Fatalf("got %s %v", tier, declared)
	}
	if Tier(map[string]any{TierKeyword: TierTunable}) != TierTunable || Tier(nil) != TierContract {
		t.Fatal("Tier")
	}
}

func TestTieredDetectsKeyword(t *testing.T) {
	if !Tiered(decode(t, tieredSchema)) {
		t.Fatal("tiered schema not detected")
	}
	if !Tiered(decode(t, `{"allOf":[{"properties":{"a":{"x-pacto-tier":"contract"}}}]}`)) {
		t.Fatal("tier inside an array not detected")
	}
	if Tiered(decode(t, `{"type":"object","properties":{"a":{"enum":["x"]}},"required":["a"]}`)) || Tiered(nil) {
		t.Fatal("untiered schema read as tiered")
	}
}
