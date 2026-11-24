package contract_test

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
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

func TestTrimOCIScheme(t *testing.T) {
	for in, want := range map[string]string{
		"oci://ghcr.io/a/b": "ghcr.io/a/b",
		"ghcr.io/a/b":       "ghcr.io/a/b",
		"oci://":            "",
		"":                  "",
		"./oci://x":         "./oci://x",
	} {
		if got := contract.TrimOCIScheme(in); got != want {
			t.Errorf("TrimOCIScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitOCIRef(t *testing.T) {
	const d = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		in                string
		repo, tag, digest string
		tagged            bool // SplitOCITag on the digest-free part
	}{
		{"ghcr.io/acme/svc", "ghcr.io/acme/svc", "", "", false},
		{"ghcr.io/acme/svc:1.0.0", "ghcr.io/acme/svc", "1.0.0", "", true},
		{"ghcr.io/acme/svc@" + d, "ghcr.io/acme/svc", "", d, false},
		{"ghcr.io/acme/svc:1.0.0@" + d, "ghcr.io/acme/svc", "1.0.0", d, true},
		{"localhost:5000/svc", "localhost:5000/svc", "", "", false},
		{"localhost:5000/svc:2", "localhost:5000/svc", "2", "", true},
		{"localhost:5000", "localhost", "5000", "", true},
		{"svc", "svc", "", "", false},
		{"svc:", "svc", "", "", true},
		{"oci://ghcr.io/acme/svc:1", "oci://ghcr.io/acme/svc", "1", "", true},
		{"oci://svc", "oci://svc", "", "", false},
		{"a@b@c", "a", "", "b@c", false},
		{"repo@", "repo", "", "", false},
		{"", "", "", "", false},
	}
	for _, c := range cases {
		repo, tag, digest := contract.SplitOCIRef(c.in)
		if repo != c.repo || tag != c.tag || digest != c.digest {
			t.Errorf("SplitOCIRef(%q) = (%q, %q, %q), want (%q, %q, %q)", c.in, repo, tag, digest, c.repo, c.tag, c.digest)
		}
		rest, _ := contract.SplitOCIDigest(c.in)
		if _, _, tagged := contract.SplitOCITag(rest); tagged != c.tagged {
			t.Errorf("SplitOCITag(%q) tagged = %v, want %v", rest, tagged, c.tagged)
		}
	}
}

func TestOCIDomain(t *testing.T) {
	for in, want := range map[string]string{
		"oci://ghcr.io/acme/payments:1.0":       "ghcr.io/acme",
		"ghcr.io/acme/payments":                 "ghcr.io/acme",
		"localhost:5000/acme/payments@sha256:x": "localhost:5000/acme",
		"localhost:5000/payments:1":             "localhost:5000",
		"ghcr.io/acme/team/payments:1@sha256:x": "ghcr.io/acme/team",
		"payments:1.0":                          "",
		"payments":                              "",
		"oci://payments":                        "",
		"./svc":                                 "",
		"../a/b":                                "",
		"/abs/path/svc":                         "",
		"~/svc":                                 "",
		"":                                      "",
	} {
		if got := contract.OCIDomain(in); got != want {
			t.Errorf("OCIDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInferredRef(t *testing.T) {
	for _, tc := range []struct {
		ref, want string
		ok        bool
	}{
		{"oci://ghcr.io/acme/pacto-inferred/stripe:0.0.2", "stripe", true},
		{"ghcr.io/acme/pacto-inferred/aws/rds/1/eu-west-1/db@sha256:" + hex64, "aws/rds/1/eu-west-1/db", true},
		{"zot:5000/demo/pacto-inferred/host/corp-legacy.example", "host/corp-legacy.example", true},
		{"ghcr.io/pacto-inferred/x", "", false},
		{"ghcr.io/acme/payments", "", false},
		{"ghcr.io/acme/pacto-inferred", "", false},
		{"ghcr.io/acme/pacto-inferred/Stripe", "Stripe", false},
		{"ghcr.io/acme/pacto-inferred/a/b/c/d/e/f", "a/b/c/d/e/f", false},
	} {
		got, ok := contract.InferredRef(tc.ref)
		if got != tc.want || ok != tc.ok {
			t.Errorf("InferredRef(%q) = %q, %v; want %q, %v", tc.ref, got, ok, tc.want, tc.ok)
		}
	}
}

func TestContributionRepositoryJoins(t *testing.T) {
	if got := contract.ContributionRepository("ghcr.io/acme", "my-idp"); got != "ghcr.io/acme/pacto-inferred/my-idp/services" {
		t.Fatalf("ContributionRepository = %q", got)
	}
}

func TestContributionRefShapes(t *testing.T) {
	for _, tc := range []struct {
		ref, producer string
		ok            bool
	}{
		{"oci://ghcr.io/acme/pacto-inferred/my-idp/services:checkout_1.0.0", "my-idp", true},
		{"ghcr.io/acme/pacto-inferred/my-idp/services@sha256:" + hex64, "my-idp", true},
		{"ghcr.io/acme/team/pacto-inferred/my-idp/services", "my-idp", true},
		{"ghcr.io/acme/pacto-inferred/stripe", "", false},
		{"ghcr.io/acme/pacto-inferred/my-idp/services/x", "", false},
		{"ghcr.io/acme/pacto-inferred/my-idp/other", "", false},
		{"ghcr.io/acme/pacto-inferred/host/services", "", false},
		{"ghcr.io/pacto-inferred/my-idp/services", "", false},
		{"ghcr.io/acme/pacto-inferred/My_IdP/services", "", false},
		{"ghcr.io/acme/checkout:1.0.0", "", false},
	} {
		p, ok := contract.ContributionRef(tc.ref)
		if p != tc.producer || ok != tc.ok {
			t.Errorf("ContributionRef(%q) = %q %v, want %q %v", tc.ref, p, ok, tc.producer, tc.ok)
		}
	}
}

func TestInferredRefStillCoversContributions(t *testing.T) {
	if id, ok := contract.InferredRef("oci://ghcr.io/acme/pacto-inferred/my-idp/services:checkout_1.0.0"); !ok || id != "my-idp/services" {
		t.Fatalf("InferredRef = %q %v", id, ok)
	}
}

func TestContributionGrant(t *testing.T) {
	for _, tc := range []struct {
		prefix, producer, repo string
		grant, err             bool
	}{
		{"ghcr.io/acme/pacto-inferred/my-idp", "my-idp", "ghcr.io/acme/pacto-inferred/my-idp/services", true, false},
		{"ghcr.io/acme", "my-idp", "", false, false},
		{"ghcr.io/pacto-inferred/my-idp", "my-idp", "", false, false},
		{"ghcr.io/acme/pacto-inferred/other-idp", "my-idp", "", false, true},
		{"ghcr.io/acme/pacto-inferred", "my-idp", "", false, true},
		{"ghcr.io/acme/pacto-inferred/my-idp/services", "my-idp", "", false, true},
		{"ghcr.io/acme/pacto-inferred/my-idp/", "my-idp", "", false, true},
		{"ghcr.io/acme/pacto-inferred/host", "host", "", false, true},
		{"ghcr.io/acme/pacto-inferred/My-IdP", "My-IdP", "", false, true},
	} {
		repo, grant, err := contract.ContributionGrant(tc.prefix, tc.producer)
		if repo != tc.repo || grant != tc.grant || (err != nil) != tc.err {
			t.Errorf("ContributionGrant(%q, %q) = %q %v %v", tc.prefix, tc.producer, repo, grant, err)
		}
	}
}
