package fleet

import "testing"

func TestNewServiceKey(t *testing.T) {
	if got := NewServiceKey("payments"); got != ServiceKey("payments") {
		t.Errorf("NewServiceKey = %q", got)
	}
}

func TestObservedNameResolver(t *testing.T) {
	snap := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{
		NewServiceKey("solo"):                 {Name: "solo"},
		NewServiceKeyDomain("eu", "payments"): {Name: "payments"},
		NewServiceKeyDomain("us", "payments"): {Name: "payments"},
	}}
	resolve := snap.ObservedNameResolver()

	if k, res := resolve("solo"); res != ObservedResolved || k != NewServiceKey("solo") {
		t.Errorf("solo = %q,%v; want unique", k, res)
	}
	if k, res := resolve("payments"); res != ObservedAmbiguous || k != "" {
		t.Errorf("payments = %q,%v; want ambiguous", k, res)
	}
	if k, res := resolve("ghost"); res != ObservedUnknown || k != "" {
		t.Errorf("ghost = %q,%v; want unknown", k, res)
	}
}

func TestNewServiceKeyDomain(t *testing.T) {
	if got := NewServiceKeyDomain("", "payments"); got != ServiceKey("payments") {
		t.Errorf("empty domain = %q, want payments", got)
	}
	if got := NewServiceKeyDomain("east", "payments"); got != ServiceKey("east/payments") {
		t.Errorf("non-empty domain = %q, want east/payments", got)
	}
	// The ergonomic NewServiceKey is exactly the empty-domain form.
	if NewServiceKey("payments") != NewServiceKeyDomain("", "payments") {
		t.Error("NewServiceKey must equal NewServiceKeyDomain with an empty domain")
	}
}

// TestParseServiceKey_RoundTrip asserts NewServiceKeyDomain and ParseServiceKey
// are inverse — including a domain and a name that each carry "/" and "%", the
// characters that could otherwise forge or blur the domain separator. It also
// exercises the no-domain path of indexUnescapedSlash.
func TestParseServiceKey_RoundTrip(t *testing.T) {
	cases := []struct{ domain, name string }{
		{"", "plain"},
		{"east", "payments"},
		{"a/b%c", "x/y%z"}, // both components carry "/" and "%"
		{"", "n/m%k"},      // default domain, name carrying "/" and "%"
	}
	for _, c := range cases {
		key := NewServiceKeyDomain(c.domain, c.name)
		d, n := ParseServiceKey(key)
		if d != c.domain || n != c.name {
			t.Errorf("round-trip %q → (%q,%q), want (%q,%q)", key, d, n, c.domain, c.name)
		}
		if NewServiceKey(c.name) != NewServiceKeyDomain("", c.name) {
			t.Errorf("NewServiceKey(%q) must equal NewServiceKeyDomain(\"\", …)", c.name)
		}
	}
}

func TestNewRevisionKey(t *testing.T) {
	tests := []struct {
		name      string
		svc       ServiceKey
		contentID string
		want      RevisionKey
	}{
		{"digest identity", NewServiceKey("svc"), "sha256:abc", "svc@sha256:abc"},
		{"domain-qualified key", NewServiceKeyDomain("east", "svc"), "sha256:abc", "east/svc@sha256:abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewRevisionKey(tt.svc, tt.contentID); got != tt.want {
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
	bad := []TargetKey{
		"", "one", "one/two", "a/b/c/d",
		// Non-canonical / invalid percent encodings must NOT decode ok.
		"a/b/c%",   // dangling percent
		"a/b/c%2G", // invalid hex
		"a/b/c%2f", // lowercase (non-canonical; NewTargetKey emits %2F)
		"a%/b/c",   // bare percent in a component
	}
	for _, k := range bad {
		if _, _, _, ok := ParseTargetKey(k); ok {
			t.Errorf("ParseTargetKey(%q) = ok, want not ok", k)
		}
	}
	// A canonically-encoded key round-trips.
	k := NewTargetKey("prod/x", "k8s", "a%b/c")
	scope, kind, name, ok := ParseTargetKey(k)
	if !ok || scope != "prod/x" || kind != "k8s" || name != "a%b/c" {
		t.Errorf("canonical key must decode: %q -> %q/%q/%q ok=%v", k, scope, kind, name, ok)
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

func TestValidStatus(t *testing.T) {
	for _, s := range []string{
		StatusInvalid, StatusNonCompliant, StatusUnknown, StatusWarning,
		StatusCompliant, StatusReference, StatusNotEvaluated,
	} {
		if !ValidStatus(s) {
			t.Errorf("%q should be a canonical status", s)
		}
	}
	if ValidStatus("Nonsense") {
		t.Error("a non-canonical value must not validate")
	}
	if ValidStatus("") {
		t.Error("empty string is not a canonical status")
	}
}

func TestCloneHelpers_NilSafe(t *testing.T) {
	if cloneContract(nil) != nil || cloneLock(nil) != nil ||
		cloneRevision(nil) != nil || cloneTarget(nil) != nil || cloneService(nil) != nil {
		t.Error("clone helpers must return nil for nil input")
	}
	if cloneStringMap(nil) != nil || cloneCoverage(nil) != nil ||
		cloneReadiness(nil) != nil {
		t.Error("map/coverage/readiness clone helpers must return nil for nil input")
	}
	if copyTime(nil) != nil {
		t.Error("copyTime(nil) must be nil")
	}
}
