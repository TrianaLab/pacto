package fleet

import (
	"encoding/json"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// This file makes snapshot immutability real rather than advisory: Build owns
// deep copies of the source-provided contract and lock, and every query accessor
// returns deep copies of snapshot-owned records. Mutating a source after Build,
// or mutating a value a query returned, therefore cannot affect the snapshot or
// any other query. The copies use JSON round-trips because every field is
// JSON-serializable; unexported build-only fields (the bundle) are intentionally
// dropped so they are never exposed.

// jsonClone deep-copies a JSON-serializable value. The fleet domain types are
// all JSON-safe; a marshal error would simply leave the zero value (never a
// shared alias).
func jsonClone[T any](src T) T {
	var dst T
	data, _ := json.Marshal(src)
	_ = json.Unmarshal(data, &dst)
	return dst
}

func cloneContract(c *contract.Contract) *contract.Contract {
	if c == nil {
		return nil
	}
	return jsonClone(c)
}

func cloneLock(l *lock.Lock) *lock.Lock {
	if l == nil {
		return nil
	}
	return jsonClone(l)
}

func cloneRevision(r *ContractRevision) *ContractRevision {
	if r == nil {
		return nil
	}
	return jsonClone(r)
}

func cloneTarget(t *TargetRecord) *TargetRecord {
	if t == nil {
		return nil
	}
	return jsonClone(t)
}

func cloneService(s *ServiceRecord) *ServiceRecord {
	if s == nil {
		return nil
	}
	return jsonClone(s)
}

func cloneCoverage(c *Coverage) *Coverage {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

func cloneReadiness(r *readiness.Result) *readiness.Result {
	if r == nil {
		return nil
	}
	return jsonClone(r)
}

// cloneAnyMap deep-copies an arbitrary observed-runtime map via a JSON round-trip
// (its values may be nested maps/slices).
func cloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return jsonClone(m)
}
