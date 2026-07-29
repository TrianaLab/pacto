package fleet

import "testing"

func TestNewServiceKey(t *testing.T) {
	if got := NewServiceKey("payments"); got != ServiceKey("payments") {
		t.Errorf("NewServiceKey = %q", got)
	}
}

func TestNewRevisionKey(t *testing.T) {
	tests := []struct {
		name                             string
		service, digest, resolved, versn string
		want                             RevisionKey
	}{
		{"digest wins", "svc", "sha256:abc", "ref", "1.0.0", "svc@sha256:abc"},
		{"resolvedRef when no digest", "svc", "", "reg/svc:1.0", "1.0.0", "svc@reg/svc:1.0"},
		{"version when no digest/ref", "svc", "", "", "1.0.0", "svc@1.0.0"},
		{"unknown fallback", "svc", "", "", "", "svc@unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewRevisionKey(tt.service, tt.digest, tt.resolved, tt.versn); got != tt.want {
				t.Errorf("NewRevisionKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewTargetKey(t *testing.T) {
	tests := []struct {
		name              string
		scope, kind, tgtn string
		want              TargetKey
	}{
		{"all set", "prod", "k8s", "web", "prod/k8s/web"},
		{"empty components become dash", "", "", "", "-/-/-"},
		{"partial empty", "prod", "", "web", "prod/-/web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewTargetKey(tt.scope, tt.kind, tt.tgtn); got != tt.want {
				t.Errorf("NewTargetKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSnapshotService(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{
		NewServiceKey("a"): {Key: NewServiceKey("a"), Name: "a"},
	}}
	if snap.Service("a") == nil {
		t.Error("Service(a) should be found")
	}
	if snap.Service("missing") != nil {
		t.Error("Service(missing) should be nil")
	}
}

func TestContractRevisionBundle(t *testing.T) {
	var r ContractRevision
	if r.Bundle() != nil {
		t.Error("nil bundle expected")
	}
	b := validLeafBundle(t)
	r.bundle = b
	if r.Bundle() != b {
		t.Error("Bundle() should return the wired bundle")
	}
}
