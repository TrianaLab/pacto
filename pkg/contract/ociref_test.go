package contract_test

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// hex64 is a syntactically valid 64-char lowercase hex sha256 digest body.
const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParseOCIReference(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantReg    string
		wantRepo   string
		wantTag    string
		wantDigest string
	}{
		{
			name:     "tag only",
			input:    "ghcr.io/acme/service-pacto:1.0.0",
			wantReg:  "ghcr.io",
			wantRepo: "acme/service-pacto",
			wantTag:  "1.0.0",
		},
		{
			name:       "digest only",
			input:      "ghcr.io/acme/service-pacto@sha256:" + hex64,
			wantReg:    "ghcr.io",
			wantRepo:   "acme/service-pacto",
			wantDigest: "sha256:" + hex64,
		},
		{
			name:       "tag and digest",
			input:      "ghcr.io/acme/service-pacto:1.0.0@sha256:" + hex64,
			wantReg:    "ghcr.io",
			wantRepo:   "acme/service-pacto",
			wantTag:    "1.0.0",
			wantDigest: "sha256:" + hex64,
		},
		{
			name:    "malformed digest (too short)",
			input:   "ghcr.io/acme/service-pacto@sha256:abc123",
			wantErr: true,
		},
		{
			name:    "malformed tag (space)",
			input:   "ghcr.io/acme/service-pacto:bad tag",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no repo",
			input:   "ghcr.io",
			wantErr: true,
		},
		{
			name:     "no tag or digest",
			input:    "ghcr.io/acme/service",
			wantReg:  "ghcr.io",
			wantRepo: "acme/service",
		},
		{
			name:    "empty registry",
			input:   "/repo:1.0.0",
			wantErr: true,
		},
		{
			name:    "empty repository",
			input:   "ghcr.io/:1.0.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := contract.ParseOCIReference(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseOCIReference(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if ref.Registry != tt.wantReg {
				t.Errorf("registry = %q, want %q", ref.Registry, tt.wantReg)
			}
			if ref.Repository != tt.wantRepo {
				t.Errorf("repository = %q, want %q", ref.Repository, tt.wantRepo)
			}
			if ref.Tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", ref.Tag, tt.wantTag)
			}
			if ref.Digest != tt.wantDigest {
				t.Errorf("digest = %q, want %q", ref.Digest, tt.wantDigest)
			}
		})
	}
}

func TestOCIReferenceString(t *testing.T) {
	tests := []struct {
		ref  contract.OCIReference
		want string
	}{
		{
			ref:  contract.OCIReference{Registry: "ghcr.io", Repository: "acme/svc", Tag: "1.0.0"},
			want: "ghcr.io/acme/svc:1.0.0",
		},
		{
			ref:  contract.OCIReference{Registry: "ghcr.io", Repository: "acme/svc", Digest: "sha256:abc"},
			want: "ghcr.io/acme/svc@sha256:abc",
		},
		{
			ref:  contract.OCIReference{Registry: "ghcr.io", Repository: "acme/svc"},
			want: "ghcr.io/acme/svc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.ref.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
