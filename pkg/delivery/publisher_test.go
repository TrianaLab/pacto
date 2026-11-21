package delivery

import (
	"strings"
	"testing"
)

var (
	payments = ServiceRef{Name: "payments"}
	ciAcme   = PublisherIdentity{Issuer: issuerGitHubActions,
		Subject:    "https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/main",
		Repository: "https://github.com/acme/payments", Ref: "refs/heads/main", CI: true}
	person = PublisherIdentity{Issuer: "https://accounts.google.com", Subject: "ana@acme.example"}
)

func acmeRule(t *testing.T) PublisherRule {
	t.Helper()
	r, why := DeriveRule("ghcr.io/acme", "main")
	if why != "" {
		t.Fatal(why)
	}
	return r
}

func TestPinnedPattern(t *testing.T) {
	const gh = `^https://token\.actions\.githubusercontent\.com$`
	if !PinnedPattern(gh) || !PinnedPattern(`^https://github\.com/acme/app/\.github/workflows/release\.yml@refs/heads/main$`) {
		t.Fatal("anchored patterns pin an identity")
	}
	for name, p := range map[string]string{
		"open subject": "^https://github.com/acme/", "any subject": "^.*$", "any issuer": ".*",
		"bad pattern": "^($", "empty": "",
	} {
		if PinnedPattern(p) {
			t.Errorf("%s: %q pins nobody", name, p)
		}
	}
}

func TestDeriveRuleGitHub(t *testing.T) {
	r := acmeRule(t)
	if r.Issuer != issuerGitHubActions || r.Ref != "refs/heads/main" || r.Name != "derived from ghcr.io/acme" {
		t.Fatalf("rule %+v", r)
	}
	if v := JudgePublisher(payments, ciAcme, "", []PublisherRule{r}, PublisherSourceRule, nil, nil); v.Verdict != VerdictAccepted {
		t.Fatalf("the org's workflow on main: %+v", v)
	}
	other := ciAcme
	other.Subject, other.Repository = strings.ReplaceAll(other.Subject, "acme", "evil"), "https://github.com/evil/payments"
	if anchoredMatch(r.Subject, other.Subject) || anchoredMatch(r.Repository, other.Repository) {
		t.Fatal("another org matched")
	}
	// The org matches in any case: ghcr.io lowercases a GitHub org.
	mixed := ciAcme
	mixed.Subject, mixed.Repository = strings.Replace(mixed.Subject, "acme", "AcMe", 1), strings.Replace(mixed.Repository, "acme", "AcMe", 1)
	if !anchoredMatch(r.Subject, mixed.Subject) || !anchoredMatch(r.Repository, mixed.Repository) {
		t.Fatalf("a mixed-case org did not match: %+v", r)
	}
	// A deeper path keeps its first segment as the org, and the default branch defaults to main.
	if d, _ := DeriveRule("oci://GHCR.io/acme/team", ""); d.Subject != r.Subject {
		t.Fatalf("deep path %+v", d)
	}
}

func TestDeriveRuleGitLab(t *testing.T) {
	r, why := DeriveRule("registry.gitlab.com/acme", "trunk")
	if why != "" || r.Issuer != issuerGitLab || r.Ref != "refs/heads/trunk" {
		t.Fatalf("rule %+v %q", r, why)
	}
	if !anchoredMatch(r.Subject, "https://gitlab.com/acme/payments//.gitlab-ci.yml@refs/heads/trunk") ||
		!anchoredMatch(r.Repository, "https://gitlab.com/acme/payments") {
		t.Fatalf("a GitLab pipeline of acme on trunk: %+v", r)
	}
}

func TestDeriveRuleNoProvider(t *testing.T) {
	for _, reg := range []string{"123.dkr.ecr.eu-west-1.amazonaws.com/acme", "europe-docker.pkg.dev/acme", "zot.local/acme", "docker.io/acme"} {
		r, why := DeriveRule(reg, "main")
		if r.Subject != "" || !strings.Contains(why, "names no CI provider") {
			t.Errorf("%s: %+v %q", reg, r, why)
		}
	}
	if _, why := DeriveRule("ghcr.io", "main"); !strings.Contains(why, "names no organization") {
		t.Fatalf("no org: %q", why)
	}
}

// TestDeriveRuleAlwaysPinned: every derived pattern is anchored, and an org or
// branch carrying regular expression syntax is quoted, never an open rule.
func TestDeriveRuleAlwaysPinned(t *testing.T) {
	for _, reg := range []string{"ghcr.io/acme", "ghcr.io/a.b", "ghcr.io/.*", "registry.gitlab.com/x+y"} {
		for _, branch := range []string{"main", ".*", "release/*"} {
			r, why := DeriveRule(reg, branch)
			if why != "" || !PinnedPattern(r.Subject) || !PinnedPattern(r.Repository) {
				t.Errorf("%s %s: %+v %q", reg, branch, r, why)
			}
		}
	}
	r, _ := DeriveRule("ghcr.io/.*", "main")
	if anchoredMatch(r.Repository, "https://github.com/acme/payments") {
		t.Fatal("an org named .* matched every org")
	}
}

func TestNormalizeRepo(t *testing.T) {
	for in, want := range map[string]string{
		"https://GitHub.com/Acme/payments.git/": "github.com/Acme/payments",
		"http://github.com/acme/payments":       "github.com/acme/payments",
		"github.com":                            "github.com",
		"GITHUB.COM":                            "github.com",
	} {
		if got := NormalizeRepo(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestJudgeRuleAccepts(t *testing.T) {
	v := JudgePublisher(payments, ciAcme, "https://github.com/acme/payments.git", []PublisherRule{acmeRule(t)}, PublisherSourcePolicy, nil, nil)
	if v.Verdict != VerdictAccepted || v.Source != PublisherSourcePolicy || v.Identity == nil || v.Identity.Subject != ciAcme.Subject ||
		!strings.Contains(v.Reason, "derived from ghcr.io/acme") {
		t.Fatalf("verdict %+v", v)
	}
}

func TestJudgeRuleNeedsDefaultBranchRef(t *testing.T) {
	branch := ciAcme
	branch.Ref = "refs/heads/feature"
	if v := JudgePublisher(payments, branch, "", []PublisherRule{acmeRule(t)}, PublisherSourceRule, nil, nil); v.Verdict != VerdictNewSigner {
		t.Fatalf("a feature branch: %+v", v)
	}
}

func TestJudgeSourceMismatchRefused(t *testing.T) {
	pin := IdentityPin{Service: payments, Issuer: ciAcme.Issuer, Subject: ciAcme.Subject, Repository: ciAcme.Repository}
	v := JudgePublisher(payments, ciAcme, "https://github.com/acme/other", []PublisherRule{acmeRule(t)}, PublisherSourceRule, []IdentityPin{pin}, nil)
	if v.Verdict != VerdictRefused || v.Source != PublisherSourceNone || !strings.Contains(v.Reason, "acme/other") {
		t.Fatalf("verdict %+v", v)
	}
}

func TestJudgePinAccepts(t *testing.T) {
	pin := IdentityPin{Service: payments, Issuer: ciAcme.Issuer, Subject: ciAcme.Subject, Repository: ciAcme.Repository + ".git", Approver: "ana", At: "2026-11-17T10:00:00Z"}
	v := JudgePublisher(payments, ciAcme, "", nil, PublisherSourceRule, []IdentityPin{pin}, nil)
	if v.Verdict != VerdictAccepted || v.Source != PublisherSourcePin || !strings.Contains(v.Reason, "ana pinned for payments") {
		t.Fatalf("verdict %+v", v)
	}
}

func TestJudgePinOtherServiceIgnored(t *testing.T) {
	pin := IdentityPin{Service: ServiceRef{Name: "orders"}, Issuer: ciAcme.Issuer, Subject: ciAcme.Subject, Repository: ciAcme.Repository}
	if v := JudgePublisher(payments, ciAcme, "", nil, PublisherSourceRule, []IdentityPin{pin}, nil); v.Verdict != VerdictNewSigner {
		t.Fatalf("verdict %+v", v)
	}
}

func TestJudgeCIOutsideRuleIsNewSigner(t *testing.T) {
	other := PublisherIdentity{Issuer: issuerGitHubActions, Subject: "https://github.com/contractor/payments/.github/workflows/x.yml@refs/heads/main",
		Repository: "https://github.com/contractor/payments", CI: true}
	v := JudgePublisher(payments, other, "", []PublisherRule{acmeRule(t)}, PublisherSourceRule, nil, nil)
	if v.Verdict != VerdictNewSigner || v.Source != PublisherSourceNone || !strings.Contains(v.Reason, "a person confirms it") {
		t.Fatalf("verdict %+v", v)
	}
}

// TestJudgePersonNeverTOFU: a person identity is refused with the
// registry-declared wording, even when a pin names it.
func TestJudgePersonNeverTOFU(t *testing.T) {
	pin := IdentityPin{Service: payments, Issuer: person.Issuer, Subject: person.Subject}
	v := JudgePublisher(payments, person, "https://github.com/acme/payments", []PublisherRule{acmeRule(t)}, PublisherSourceRule, []IdentityPin{pin}, nil)
	if v.Verdict != VerdictRefused || !strings.Contains(v.Reason, "a person identity") || !strings.Contains(v.Reason, "must reuse its CI-signed baseline") {
		t.Fatalf("verdict %+v", v)
	}
}

func TestJudgePolicyServicesScope(t *testing.T) {
	r := acmeRule(t)
	r.Services = []string{"pay*"}
	if v := JudgePublisher(payments, ciAcme, "", []PublisherRule{r}, PublisherSourcePolicy, nil, nil); v.Verdict != VerdictAccepted {
		t.Fatalf("in scope: %+v", v)
	}
	if v := JudgePublisher(ServiceRef{Name: "orders"}, ciAcme, "", []PublisherRule{r}, PublisherSourcePolicy, nil, nil); v.Verdict != VerdictNewSigner {
		t.Fatalf("out of scope: %+v", v)
	}
	open := acmeRule(t)
	open.Subject = "^.*$"
	if v := JudgePublisher(payments, ciAcme, "", []PublisherRule{open}, PublisherSourcePolicy, nil, nil); v.Verdict == VerdictAccepted {
		t.Fatalf("an open rule accepted: %+v", v)
	}
}

func TestJudgeSignersFirstAcceptedWins(t *testing.T) {
	rules := []PublisherRule{acmeRule(t)}
	stranger := PublisherIdentity{Issuer: issuerGitHubActions, Subject: "https://github.com/x/y/.github/workflows/a@refs/heads/main", Repository: "https://github.com/x/y", CI: true}
	if v := JudgeSigners(payments, []PublisherIdentity{person, ciAcme}, "", rules, PublisherSourceRule, nil, nil); v.Verdict != VerdictAccepted || v.Identity.Subject != ciAcme.Subject {
		t.Fatalf("accepted second: %+v", v)
	}
	if v := JudgeSigners(payments, []PublisherIdentity{stranger, person}, "", rules, PublisherSourceRule, nil, nil); v.Verdict != VerdictRefused {
		t.Fatalf("refused wins over new-signer: %+v", v)
	}
	if v := JudgeSigners(payments, []PublisherIdentity{person, stranger}, "", rules, PublisherSourceRule, nil, nil); v.Verdict != VerdictRefused {
		t.Fatalf("refused stays: %+v", v)
	}
	if v := JudgeSigners(payments, nil, "", rules, PublisherSourceRule, nil, nil); v.Verdict != VerdictUnknown {
		t.Fatalf("no signer: %+v", v)
	}
}
