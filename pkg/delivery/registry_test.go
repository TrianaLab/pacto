package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// mapRegistry serves r/consumer@c1 as the contract of every artifact and
// pulls the bundles it holds by ref.
type mapRegistry map[string]*contract.Bundle

func (mapRegistry) Annotations(context.Context, string) (map[string]string, error) {
	return map[string]string{ContractAnnotation: "oci://r/consumer@" + c1}, nil
}

func (m mapRegistry) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	if b := m[ref]; b != nil {
		return b, nil
	}
	return nil, errors.New("not found: " + ref)
}

// platformBundle declares configuration app, whose schema needs an integer
// replicas.
func platformBundle() *contract.Bundle {
	b := bundle("platform", "1.0.0")
	b.Contract.Configurations = []contract.Configuration{{Name: "app", Schema: "app.json"}}
	b.FS = fstest.MapFS{"app.json": {Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"}}}`)}}
	return b
}

func TestResolveRevisionConfigRefs(t *testing.T) {
	consumer := bundle("consumer", "1.0.0")
	consumer.Contract.Configurations = []contract.Configuration{{Name: "local", Ref: "../platform"}, {Name: "app", Ref: "oci://r/platform:1.0.0"}}
	r := ResolveRevision(t.Context(), mapRegistry{"r/consumer@" + c1: consumer, "r/platform:1.0.0": platformBundle()}, "r/a", dA)
	if r.ConfigRefs["app"].Schema == nil || r.ConfigRefs["app"].ProviderName != "platform" {
		t.Errorf("oci ref = %+v", r.ConfigRefs["app"])
	}
	if l := r.ConfigRefs["local"]; l.Schema != nil || l.Err == nil || !strings.Contains(l.Err.Error(), "oci:// refs only") {
		t.Errorf("local ref = %+v", l)
	}
}
