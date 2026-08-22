package fleetsrc

import "testing"

func TestOciDomain(t *testing.T) {
	cases := []struct {
		ref  string
		want string
	}{
		{"oci://ghcr.io/acme/payments:1.0", "ghcr.io/acme"},                // oci:// + tag strip + registry/org
		{"localhost:5000/acme/payments@sha256:abc", "localhost:5000/acme"}, // digest strip, port not mistaken for tag
		{"ghcr.io/acme/payments", "ghcr.io/acme"},                          // no tag, no digest
		{"payments:1.0", ""}, // single segment + tag -> default domain
		{"payments", ""},     // bare name -> default domain
		{"./svc-v2", ""},     // local relative path -> no domain
		{"/abs/bundle", ""},  // local absolute path -> no domain
	}
	for _, c := range cases {
		if got := OciDomain(c.ref); got != c.want {
			t.Errorf("OciDomain(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}
