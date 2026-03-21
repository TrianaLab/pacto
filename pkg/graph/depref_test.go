package graph

import "testing"

func TestParseDependencyRef(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		scheme   Scheme
		location string
		isLocal  bool
		isOCI    bool
	}{
		{
			name:     "oci scheme",
			raw:      "oci://ghcr.io/acme/svc:1.0.0",
			scheme:   SchemeOCI,
			location: "ghcr.io/acme/svc:1.0.0",
			isLocal:  false,
			isOCI:    true,
		},
		{
			name:     "oci scheme no tag",
			raw:      "oci://registry.io/repo",
			scheme:   SchemeOCI,
			location: "registry.io/repo",
			isLocal:  false,
			isOCI:    true,
		},
		{
			name:     "file scheme absolute",
			raw:      "file:///abs/path/to/svc",
			scheme:   SchemeLocal,
			location: "/abs/path/to/svc",
			isLocal:  true,
			isOCI:    false,
		},
		{
			name:     "file scheme relative",
			raw:      "file://./relative/path",
			scheme:   SchemeLocal,
			location: "./relative/path",
			isLocal:  true,
			isOCI:    false,
		},
		{
			name:     "no scheme relative path",
			raw:      "../dep-svc",
			scheme:   SchemeLocal,
			location: "../dep-svc",
			isLocal:  true,
			isOCI:    false,
		},
		{
			name:     "no scheme dot path",
			raw:      "./dep-svc",
			scheme:   SchemeLocal,
			location: "./dep-svc",
			isLocal:  true,
			isOCI:    false,
		},
		{
			name:     "no scheme bare name",
			raw:      "my-service",
			scheme:   SchemeLocal,
			location: "my-service",
			isLocal:  true,
			isOCI:    false,
		},
		{
			name:     "no scheme absolute path",
			raw:      "/abs/path/to/svc",
			scheme:   SchemeLocal,
			location: "/abs/path/to/svc",
			isLocal:  true,
			isOCI:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := ParseDependencyRef(tt.raw)
			if ref.Scheme != tt.scheme {
				t.Errorf("Scheme = %v, want %v", ref.Scheme, tt.scheme)
			}
			if ref.Location != tt.location {
				t.Errorf("Location = %q, want %q", ref.Location, tt.location)
			}
			if ref.Original != tt.raw {
				t.Errorf("Original = %q, want %q", ref.Original, tt.raw)
			}
			if ref.IsLocal() != tt.isLocal {
				t.Errorf("IsLocal() = %v, want %v", ref.IsLocal(), tt.isLocal)
			}
			if ref.IsOCI() != tt.isOCI {
				t.Errorf("IsOCI() = %v, want %v", ref.IsOCI(), tt.isOCI)
			}
		})
	}
}

func TestScheme_String(t *testing.T) {
	tests := []struct {
		scheme Scheme
		want   string
	}{
		{SchemeLocal, "local"},
		{SchemeOCI, "oci"},
		{Scheme(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.scheme.String(); got != tt.want {
			t.Errorf("Scheme(%d).String() = %q, want %q", tt.scheme, got, tt.want)
		}
	}
}
