package story

import "testing"

func TestMayUpdate(t *testing.T) {
	for _, p := range []Persona{Alice, Carol} {
		for _, ns := range []string{"test", "prod", "prod-eu", "prod-us"} {
			if !MayUpdate(p, ns) {
				t.Errorf("%s may update in %s", p.Name, ns)
			}
		}
	}
	if !MayUpdate(Dana, "test") || MayUpdate(Dana, "prod") || MayUpdate(Dana, "prod-eu") {
		t.Error("dana updates in test only")
	}
	if MayUpdate(PayAgent, "test") || MayUpdate(Promoter, "prod") {
		t.Error("agents and the promoter update nothing")
	}
}
