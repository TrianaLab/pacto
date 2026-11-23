package contract

import "testing"

func TestContributionRefusal(t *testing.T) {
	raw := func(s string) *Bundle { return &Bundle{RawYAML: []byte(s)} }
	const ok = "pactoVersion: \"2.5\"\nservice:\n  name: a\n  version: 1.0.0\n"
	for _, tc := range []struct {
		name string
		b    *Bundle
		rec  *InferredRecord
		want string
	}{
		{"name and version only", raw(ok), nil, ""},
		{"record without declaration", raw(ok), &InferredRecord{}, ""},
		{"record with declaration", raw(ok), &InferredRecord{Declaration: &InferredDeclaration{}}, "a contribution carries no declaration"},
		{"unreadable", &Bundle{}, nil, "it declares pacto.yaml; a contribution declares only service.name and service.version: move the rest to pacto.inferred.yaml"},
		{"invalid yaml", raw(":\n- ["), nil, "it declares pacto.yaml; a contribution declares only service.name and service.version: move the rest to pacto.inferred.yaml"},
		{"top-level field", raw(ok + "owner: x\n"), nil, "it declares owner; a contribution declares only service.name and service.version: move the rest to pacto.inferred.yaml"},
		{"service not a map", raw("pactoVersion: \"2.5\"\nservice: x\n"), nil, "it declares service; a contribution declares only service.name and service.version: move the rest to pacto.inferred.yaml"},
		{"service field", raw(ok + "  owner: x\n"), nil, "it declares service.owner; a contribution declares only service.name and service.version: move the rest to pacto.inferred.yaml"},
	} {
		if got := ContributionRefusal(tc.b, tc.rec); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
