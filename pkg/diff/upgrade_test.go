package diff

import (
	"bytes"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// A raise-only change made by contract.Upgrade is one pactoVersion change,
// classified NON_BREAKING.
func TestUpgradeOnlyDiffIsNonBreaking(t *testing.T) {
	prev := contract.SupportedVersions[len(contract.SupportedVersions)-2]
	raw := []byte("pactoVersion: '" + prev + "'\nservice: {name: s, version: 1.0.0}\nworkload: service\n")
	up, err := contract.Upgrade(raw, contract.Current(), func([]byte) ([]contract.ValidationError, []contract.ValidationWarning, error) { return nil, nil, nil })
	if err != nil || up.To != contract.Current() {
		t.Fatalf("Upgrade: %+v %v", up, err)
	}
	old, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	raised, err := contract.Parse(bytes.NewReader(up.Raw))
	if err != nil {
		t.Fatal(err)
	}
	r := Compare(t.Context(), old, raised, nil, nil)
	if len(r.Changes) != 1 || r.Changes[0].Path != "pactoVersion" || r.Changes[0].Type != Modified || r.Classification != NonBreaking {
		t.Fatalf("got %+v", r)
	}
}
