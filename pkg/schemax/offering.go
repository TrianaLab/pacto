package schemax

// TierKeyword marks how far a consumer may change a field of an offering's
// configuration schema per environment. Its value is TierTunable or
// TierContract; JSON Schema validation ignores it.
const TierKeyword = "x-pacto-tier"

// The tiers of an offering's configuration field.
const (
	TierContract = "contract" // set once for every environment
	TierTunable  = "tunable"  // may change per environment, within the schema's bounds
)

// Tier is node's own tier: TierTunable only when node declares exactly
// "tunable", TierContract otherwise (absent or unknown values close).
func Tier(node map[string]any) string {
	if node[TierKeyword] == TierTunable {
		return TierTunable
	}
	return TierContract
}

// TierAt is the tier of the field at path in the configuration schema doc,
// following properties.<segment> from the root. The nearest node on the way
// that declares TierKeyword decides, the field itself included; declared is
// false when none does, and the tier is TierContract then. A segment the
// schema does not name in properties (an array item or an
// additionalProperties value) is part of the deepest node reached. A node
// that uses $ref, oneOf, anyOf or allOf is TierContract and undeclared: the
// tier sits behind a reference Pacto does not follow.
//
// ponytail: no $ref resolution, so a ref'd tunable field reads contract (the
// safe direction); follow local refs if an offering needs them.
func TierAt(doc any, path []string) (tier string, declared bool) {
	tier = TierContract
	node, _ := doc.(map[string]any)
	for i := 0; node != nil; i++ {
		for _, k := range []string{"$ref", "oneOf", "anyOf", "allOf"} {
			if _, ok := node[k]; ok {
				return TierContract, false
			}
		}
		if _, ok := node[TierKeyword]; ok {
			tier, declared = Tier(node), true
		}
		if i == len(path) {
			break
		}
		props, _ := node["properties"].(map[string]any)
		node, _ = props[path[i]].(map[string]any)
	}
	return tier, declared
}

// Tiered reports whether any node of the configuration schema doc declares
// TierKeyword.
func Tiered(doc any) bool {
	switch v := doc.(type) {
	case map[string]any:
		if _, ok := v[TierKeyword]; ok {
			return true
		}
		for _, child := range v {
			if Tiered(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if Tiered(child) {
				return true
			}
		}
	}
	return false
}
