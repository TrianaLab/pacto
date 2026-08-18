package evidenceoci

import (
	"errors"
	"strings"
	"testing"
)

const (
	digestA = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	digestB = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	refA    = "oci://registry.example/team/contracts/orders@" + digestA
	refB    = "oci://registry.example/team/contracts/checkout@" + digestB
)

func TestParseSubject_Exact(t *testing.T) {
	s, err := ParseSubject(refA)
	if err != nil {
		t.Fatalf("ParseSubject: %v", err)
	}
	if s.Registry != "registry.example" {
		t.Errorf("registry = %q, want registry.example", s.Registry)
	}
	if s.Repository != "team/contracts/orders" {
		t.Errorf("repository = %q, want team/contracts/orders", s.Repository)
	}
	if s.Digest != digestA {
		t.Errorf("digest = %q, want %q", s.Digest, digestA)
	}
	if s.Ref() != refA {
		t.Errorf("Ref() = %q, want %q", s.Ref(), refA)
	}
	if got, want := s.Domain(), "registry.example/team/contracts"; got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

// A single-segment repository still has a domain: the registry itself. This
// mirrors the domain the accepted record carries, which is everything before the
// final path segment of the immutable contract ref.
func TestSubject_DomainOfSingleSegmentRepository(t *testing.T) {
	s, err := ParseSubject("oci://registry:5000/orders@" + digestA)
	if err != nil {
		t.Fatalf("ParseSubject: %v", err)
	}
	if got, want := s.Domain(), "registry:5000"; got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

// Every rejected shape is a way a configured subject could stop being an exact,
// immutable, remote contract revision.
func TestParseSubject_Rejects(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"local path":         "/var/lib/contracts/orders",
		"relative path":      "./orders",
		"no scheme":          "registry.example/team/orders@" + digestA,
		"wrong scheme":       "https://registry.example/team/orders@" + digestA,
		"mutable tag":        "oci://registry.example/team/orders:1.2.3",
		"no reference":       "oci://registry.example/team/orders",
		"tag and digest":     "oci://registry.example/team/orders:1.2.3@" + digestA,
		"short digest":       "oci://registry.example/team/orders@sha256:1111",
		"uppercase digest":   "oci://registry.example/team/orders@sha256:" + strings.Repeat("A", 64),
		"non hex digest":     "oci://registry.example/team/orders@sha256:" + strings.Repeat("g", 64),
		"unsupported algo":   "oci://registry.example/team/orders@sha512:" + strings.Repeat("a", 128),
		"no repository":      "oci://registry.example@" + digestA,
		"empty repository":   "oci://registry.example/@" + digestA,
		"trailing slash":     "oci://registry.example/team/orders/@" + digestA,
		"whitespace":         " oci://registry.example/team/orders@" + digestA,
		"uppercase repo":     "oci://registry.example/Team/orders@" + digestA,
		"double at":          "oci://registry.example/team/orders@" + digestA + "@" + digestB,
		"digest only":        "oci://@" + digestA,
		"no registry":        "oci:///team/orders@" + digestA,
		"scheme only":        "oci://",
		"missing digest sep": "oci://registry.example/team/orders" + digestA,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := ParseSubject(raw)
			if err == nil {
				t.Fatalf("ParseSubject(%q) = %+v, want error", raw, s)
			}
			if !errors.Is(err, ErrInvalidSubject) {
				t.Errorf("error = %v, want ErrInvalidSubject", err)
			}
		})
	}
}

func TestParseSubjects_DeduplicatesPreservingOrder(t *testing.T) {
	subs, err := ParseSubjects([]string{refB, refA, refB})
	if err != nil {
		t.Fatalf("ParseSubjects: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("len = %d, want 2 (deduplicated)", len(subs))
	}
	if subs[0].Ref() != refB || subs[1].Ref() != refA {
		t.Errorf("order = %q, %q; want input order preserved", subs[0].Ref(), subs[1].Ref())
	}
}

// A server with no configured subject has no durable store at all: there is no
// implicit catalog-wide discovery to fall back to.
func TestParseSubjects_RequiresNonEmpty(t *testing.T) {
	for name, in := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSubjects(in); !errors.Is(err, ErrNoSubjects) {
				t.Fatalf("error = %v, want ErrNoSubjects", err)
			}
		})
	}
}

func TestParseSubjects_RejectsOneBadEntry(t *testing.T) {
	_, err := ParseSubjects([]string{refA, "oci://registry.example/team/orders:latest"})
	if !errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("error = %v, want ErrInvalidSubject", err)
	}
}

func TestSubjects_MatchIsExact(t *testing.T) {
	subs, err := ParseSubjects([]string{refA})
	if err != nil {
		t.Fatalf("ParseSubjects: %v", err)
	}
	set := SubjectList(subs)
	if _, ok := set.Lookup(refA); !ok {
		t.Error("the configured subject is not looked up by its own reference")
	}
	// Same repository, different revision: a subject is a contract REVISION, not a
	// repository, so a sibling digest is not configured.
	if _, ok := set.Lookup("oci://registry.example/team/contracts/orders@" + digestB); ok {
		t.Error("a different digest in the same repository must not match a configured subject")
	}
	// Same digest, different repository.
	if _, ok := set.Lookup("oci://other.example/team/contracts/orders@" + digestA); ok {
		t.Error("the same digest in another repository must not match a configured subject")
	}
	if _, ok := set.Lookup("oci://registry.example/team/contracts/orders:1.0.0"); ok {
		t.Error("a mutable tag must never match a configured subject")
	}
}
