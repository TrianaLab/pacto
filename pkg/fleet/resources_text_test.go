package fleet

import "testing"

func TestRequestedText(t *testing.T) {
	r := RequestedConfiguration{Provenance: ProvenanceDeclared, Configuration: "db", Revision: &EntityRef{ParentService: "postgresql", Version: "16.1.0", Label: "postgresql 16.1.0"},
		Values: RuntimePreview{Items: []RuntimeFact{{Key: "size", Value: "medium"}}}}
	if got := r.Text(); got != "requested (declared): size=medium, provider postgresql@16.1.0, configuration db" {
		t.Errorf("versioned = %q", got)
	}
	r = RequestedConfiguration{Provenance: ProvenanceDeclared, Revision: &EntityRef{Label: "postgresql sha256:ab"}}
	if got := r.Text(); got != "requested (declared): none, provider postgresql sha256:ab" {
		t.Errorf("unversioned = %q", got)
	}
}
