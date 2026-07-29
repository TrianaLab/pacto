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
		{"empty components stay empty", "", "", "", "//"},
		{"partial empty", "prod", "", "web", "prod//web"},
		{"embedded slash is escaped", "prod/eu", "k8s", "a/b", "prod%2Feu/k8s/a%2Fb"},
		{"embedded percent is escaped", "a%b", "k8s", "n", "a%25b/k8s/n"},
		{"literal escape sequence round-trips", "a%2Fb", "k8s", "n", "a%252Fb/k8s/n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewTargetKey(tt.scope, tt.kind, tt.tgtn); got != tt.want {
				t.Errorf("NewTargetKey = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTargetKeyRoundTrip asserts NewTargetKey and ParseTargetKey are inverse for
// components carrying the characters ("/", "%") that could otherwise forge or
// blur a component boundary.
func TestTargetKeyRoundTrip(t *testing.T) {
	triples := [][3]string{
		{"prod", "k8s", "web"},
		{"", "", ""},
		{"prod/eu", "kubernetes-workload", "payments/payments-service"},
		{"a%b", "k/8/s", "n%2Fm"},
		{"%25", "/", "%"},
	}
	for _, tr := range triples {
		key := NewTargetKey(tr[0], tr[1], tr[2])
		scope, kind, name, ok := ParseTargetKey(key)
		if !ok {
			t.Fatalf("ParseTargetKey(%q) not ok", key)
		}
		if scope != tr[0] || kind != tr[1] || name != tr[2] {
			t.Errorf("round-trip %q → (%q,%q,%q), want (%q,%q,%q)", key, scope, kind, name, tr[0], tr[1], tr[2])
		}
	}
	// Two distinct triples must never collide.
	if NewTargetKey("a/b", "k", "n") == NewTargetKey("a", "b/k", "n") {
		t.Error("distinct triples collided into the same key")
	}
}

func TestParseTargetKey_Malformed(t *testing.T) {
	for _, bad := range []TargetKey{"", "one", "one/two", "a/b/c/d"} {
		if _, _, _, ok := ParseTargetKey(bad); ok {
			t.Errorf("ParseTargetKey(%q) = ok, want not ok", bad)
		}
	}
}

func TestTargetRecordDisplayName(t *testing.T) {
	tr := &TargetRecord{Scope: "prod", Kind: "k8s", Name: "a/b"}
	if got := tr.DisplayName(); got != "prod/k8s/a/b" {
		t.Errorf("DisplayName = %q, want prod/k8s/a/b", got)
	}
	empty := &TargetRecord{}
	if got := empty.DisplayName(); got != "//" {
		t.Errorf("empty DisplayName = %q, want //", got)
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
