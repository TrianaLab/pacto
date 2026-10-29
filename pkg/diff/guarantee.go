package diff

import (
	"encoding/json"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffGuarantees compares what one interface guarantees before and after. A
// guarantee the new side no longer meets is weakened or, when it is gone,
// removed; one the old side did not meet is strengthened or, when it is new,
// added. A guarantee that moves both ways (at-most-once to at-least-once) is
// reported once, as weakened, because a consumer relying on the old value
// breaks.
func diffGuarantees(name string, old, new *contract.Guarantees) []Change {
	weaker := offeredBy(name, old, new)
	stronger := offeredBy(name, new, old)
	var changes []Change
	moved := map[string]bool{}
	for _, m := range weaker {
		moved[m.Guarantee] = true
		path, ct := "interfaces.guarantees.weakened", Modified
		if m.Offered == "" {
			path, ct = "interfaces.guarantees", Removed
		}
		changes = append(changes, guaranteeChange(path, ct, name, m.Guarantee, m.Required, m.Offered))
	}
	for _, m := range stronger {
		if moved[m.Guarantee] {
			continue
		}
		path, ct := "interfaces.guarantees.strengthened", Modified
		if m.Offered == "" {
			path, ct = "interfaces.guarantees", Added
		}
		changes = append(changes, guaranteeChange(path, ct, name, m.Guarantee, m.Offered, m.Required))
	}
	return changes
}

// offeredBy lists what want guarantees that have does not meet, by asking
// contract.Unmet whether have satisfies a consumer requiring exactly want.
func offeredBy(name string, want, have *contract.Guarantees) []contract.Mismatch {
	if want == nil {
		return nil
	}
	provider := &contract.Contract{Interfaces: []contract.Interface{{Name: name, Guarantees: have}}}
	dep := contract.Dependency{Requires: []contract.Requirement{{Interface: name, Guarantees: *want}}}
	return contract.Unmet("", dep, provider)
}

// guaranteeChange renders one guarantee change, leaving an absent side nil.
func guaranteeChange(path string, ct ChangeType, name, guarantee, oldVal, newVal string) Change {
	side := func(v string) any {
		if v == "" {
			return nil
		}
		return name + ": " + guarantee + " " + v
	}
	return newChange(path, ct, side(oldVal), side(newVal))
}

// diffRequires reports a change to the guarantees a dependency requires as one
// change. A new or changed requirement takes the PotentialBreaking default,
// because it can refuse a provider the service accepted before.
func diffRequires(name string, old, new []contract.Requirement) []Change {
	side := func(r []contract.Requirement) any {
		if len(r) == 0 {
			return nil
		}
		b, _ := json.Marshal(r) // plain strings and bools always marshal
		return name + ": " + string(b)
	}
	if side(old) == side(new) {
		return nil
	}
	ct := Modified
	switch {
	case len(old) == 0:
		ct = Added
	case len(new) == 0:
		ct = Removed
	}
	return []Change{newChange("dependencies.requires", ct, side(old), side(new))}
}
