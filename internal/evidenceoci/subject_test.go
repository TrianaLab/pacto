package evidenceoci

import (
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
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

// An exact subject may name an inferred repository: only a prefix refuses
// Pacto's own and inferred repositories, as ParseSubject documents.
func TestParseSubject_ExactInferredRepository(t *testing.T) {
	if _, err := ParseSubject("oci://registry.example/acme/pacto-inferred/svc@" + digestA); err != nil {
		t.Fatalf("ParseSubject: %v", err)
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

// No subject is an empty list: a server may run on prefixes alone, and the
// store refuses a configuration with neither (TestNewStoreNeedsSubjectOrPrefix).
func TestParseSubjects_EmptyIsEmpty(t *testing.T) {
	for name, in := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseSubjects(in); err != nil || len(got) != 0 {
				t.Fatalf("ParseSubjects(%v) = %v, %v, want an empty list", in, got, err)
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

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes([]string{"oci://zot.acme.internal/acme/", "oci://ghcr.io/", "oci://zot.acme.internal/acme/", "oci://reg:5000/a/b/"})
	if err != nil {
		t.Fatalf("ParsePrefixes: %v", err)
	}
	want := PrefixList{{Registry: "zot.acme.internal", Path: "acme"}, {Registry: "ghcr.io"}, {Registry: "reg:5000", Path: "a/b"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v (deduplicated, in order)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("prefix %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if s := got[0].String(); s != "oci://zot.acme.internal/acme/" {
		t.Errorf("String() = %q", s)
	}
	if s := got[1].String(); s != "oci://ghcr.io/" {
		t.Errorf("String() = %q", s)
	}
	if l := got[0].Ledger(); l != "zot.acme.internal/acme/pacto-evidence" {
		t.Errorf("Ledger() = %q", l)
	}
	if l := got[1].Ledger(); l != "ghcr.io/pacto-evidence" {
		t.Errorf("Ledger() = %q", l)
	}
	if empty, err := ParsePrefixes(nil); err != nil || len(empty) != 0 {
		t.Errorf("ParsePrefixes(nil) = %v, %v, want empty", empty, err)
	}

	for name, raw := range map[string][]string{
		"no scheme":        {"zot.acme.internal/acme/"},
		"no trailing":      {"oci://zot.acme.internal/acme"},
		"digest":           {"oci://zot.acme.internal/acme@" + digestA + "/"},
		"tag":              {"oci://zot.acme.internal/acme:1.0/"},
		"uppercase":        {"oci://zot.acme.internal/Acme/"},
		"empty segment":    {"oci://zot.acme.internal/acme//"},
		"no registry":      {"oci:///"},
		"bad registry":     {"oci://bad host/"},
		"reserved segment": {"oci://zot.acme.internal/acme/pacto-inferred/"},
		"overlap":          {"oci://zot.acme.internal/acme/", "oci://zot.acme.internal/acme/team/"},
		"overlap reversed": {"oci://zot.acme.internal/acme/team/", "oci://zot.acme.internal/"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePrefixes(raw); !errors.Is(err, ErrInvalidPrefix) {
				t.Fatalf("ParsePrefixes(%q) = %v, want ErrInvalidPrefix", raw, err)
			}
		})
	}
	// Siblings that share a string prefix but not a path segment do not overlap.
	if _, err := ParsePrefixes([]string{"oci://reg/acme/", "oci://reg/acme-two/"}); err != nil {
		t.Errorf("sibling prefixes: %v", err)
	}
}

func TestParseSubject_RejectsWhatORASCannotOpen(t *testing.T) {
	if _, err := ParseSubject("oci://registry.example/team/a._b@" + digestA); !errors.Is(err, ErrInvalidSubject) {
		t.Fatalf("err = %v, want ErrInvalidSubject", err)
	}
}

func TestPrefixCovers(t *testing.T) {
	p := Prefix{Registry: "reg", Path: "acme"}
	sub := func(repo string) Subject { return Subject{Registry: "reg", Repository: repo, Digest: digestA} }
	for repo, want := range map[string]bool{
		"acme/invoices":                true,
		"acme/team/deep/invoices":      true,
		"acme":                         false,
		"acme-two/invoices":            false,
		"other/invoices":               false,
		"acme/pacto-inferred/x":        false,
		"acme/pacto-targets":           false,
		"acme/pacto-index":             false,
		"acme/pacto-evidence":          false,
		"acme/pacto-evidence/sub":      false,
		"acme/team/pacto-inferred/svc": false,
	} {
		if got := p.Covers(sub(repo)); got != want {
			t.Errorf("Covers(%s) = %v, want %v", repo, got, want)
		}
	}
	root := Prefix{Registry: "reg"}
	if !root.Covers(sub("invoices")) || root.Covers(Subject{Registry: "reg2", Repository: "invoices", Digest: digestA}) {
		t.Error("a registry-wide prefix covers its own registry only")
	}
	if _, ok := (PrefixList{p}).Covering(sub("other/x")); ok {
		t.Error("Covering found a prefix for an uncovered subject")
	}
}

func TestAuthorizeSubjectTable(t *testing.T) {
	const reg = "zot.acme.internal"
	exact := "oci://" + reg + "/other/exact@" + digestA
	covered := "oci://" + reg + "/acme/exact@" + digestB
	subjects, _ := ParseSubjects([]string{exact, covered})
	prefixes, _ := ParsePrefixes([]string{"oci://" + reg + "/acme/"})
	s, err := NewStore(subjects, prefixes, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		ref string
		ok  bool
	}{
		"prefix":           {"oci://" + reg + "/acme/invoices@" + digestA, true},
		"exact":            {exact, true},
		"exact-and-prefix": {covered, true},
		"nested-depth":     {"oci://" + reg + "/acme/team/billing/invoices@" + digestA, true},
		"tag":              {"oci://" + reg + "/acme/invoices:0.2.0", false},
		"tag+digest":       {"oci://" + reg + "/acme/invoices:0.2.0@" + digestA, false},
		"sha512":           {"oci://" + reg + "/acme/invoices@sha512:" + strings.Repeat("a", 128), false},
		"outside-prefix":   {"oci://" + reg + "/other/x@" + digestA, false},
		"sibling-registry": {"oci://zot.other.internal/acme/invoices@" + digestA, false},
		"ledger-repo":      {"oci://" + reg + "/acme/pacto-evidence@" + digestA, false},
		"ledger-subrepo":   {"oci://" + reg + "/acme/pacto-evidence/x@" + digestA, false},
		"inferred-repo":    {"oci://" + reg + "/acme/pacto-inferred/svc@" + digestA, false},
		"targets-repo":     {"oci://" + reg + "/acme/pacto-targets@" + digestA, false},
		"index-repo":       {"oci://" + reg + "/acme/pacto-index@" + digestA, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := s.AuthorizeSubject(tc.ref)
			if tc.ok && err != nil {
				t.Fatalf("AuthorizeSubject(%q) = %v, want accepted", tc.ref, err)
			}
			if !tc.ok && !errors.Is(err, evidenceingest.ErrContractRefPolicy) {
				t.Fatalf("AuthorizeSubject(%q) = %v, want ErrContractRefPolicy", tc.ref, err)
			}
		})
	}
}
