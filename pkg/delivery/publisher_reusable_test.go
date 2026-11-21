package delivery

import "testing"

const pactoActionsSubject = "https://github.com/" + PactoActionsWorkflow

// v2Acme is a publish of acme/payments through pacto-actions at sha on ref.
func v2Acme(sha, ref string) PublisherIdentity {
	return PublisherIdentity{Issuer: issuerGitHubActions, Subject: pactoActionsSubject + "@" + sha,
		Repository: "https://github.com/acme/payments", Ref: ref, CI: true}
}

func TestDeriveRuleAcceptsPactoActionsCaller(t *testing.T) {
	v := JudgePublisher(payments, v2Acme("1111", "refs/heads/main"), "", []PublisherRule{acmeRule(t)}, PublisherSourceRule, nil)
	if v.Verdict != VerdictAccepted {
		t.Fatalf("verdict %+v", v)
	}
}

func TestDeriveRuleRefusesPactoActionsFromOtherOrg(t *testing.T) {
	id := v2Acme("1111", "refs/heads/main")
	id.Repository = "https://github.com/evil/payments"
	if v := JudgePublisher(payments, id, "", []PublisherRule{acmeRule(t)}, PublisherSourceRule, nil); v.Verdict == VerdictAccepted {
		t.Fatalf("verdict %+v", v)
	}
	r := acmeRule(t)
	if anchoredMatch(r.Subject, "https://github.com/evil/pacto-actions/.github/workflows/ci.yml@1111") {
		t.Fatal("a fork of pacto-actions matched")
	}
}

func TestDeriveRuleRefusesPactoActionsOnOtherBranch(t *testing.T) {
	if v := JudgePublisher(payments, v2Acme("1111", "refs/heads/feature"), "", []PublisherRule{acmeRule(t)}, PublisherSourceRule, nil); v.Verdict == VerdictAccepted {
		t.Fatalf("verdict %+v", v)
	}
}

func TestDeriveRulePatternStillPinned(t *testing.T) {
	if r := acmeRule(t); !PinnedPattern(r.Subject) || anchoredMatch(r.Subject, pactoActionsSubject) {
		t.Fatalf("rule %+v", r)
	}
}

// ordersRule is a rule of GitHub's issuer on main for acme's repositories
// that accepts only their own release.yml, so only a pin can accept a
// pacto-actions signer of payments; its Ref still names payments' default
// branch.
func ordersRule(t *testing.T) PublisherRule {
	r := acmeRule(t)
	r.Subject = `^https://github\.com/acme/[^/]+/\.github/workflows/release\.yml@refs/heads/main$`
	return r
}

func v2Pin(sha string) IdentityPin {
	id := v2Acme(sha, "refs/heads/main")
	return IdentityPin{Service: payments, Issuer: id.Issuer, Subject: id.Subject, Repository: id.Repository, Approver: "ana"}
}

func TestPinSurvivesReusableWorkflowBump(t *testing.T) {
	v := JudgePublisher(payments, v2Acme("2222", "refs/heads/main"), "", []PublisherRule{ordersRule(t)}, PublisherSourceRule, []IdentityPin{v2Pin("1111")})
	if v.Verdict != VerdictAccepted || v.Source != PublisherSourcePin {
		t.Fatalf("verdict %+v", v)
	}
}

func TestPinStoredFullSubjectMatches(t *testing.T) {
	pin := v2Pin("1111")
	if pin.Subject != pactoActionsSubject+"@1111" {
		t.Fatalf("pin %+v", pin)
	}
	if PinFor(payments, v2Acme("2222", "refs/heads/main"), []IdentityPin{pin}, []PublisherRule{ordersRule(t)}) == nil {
		t.Fatal("the full stored subject did not match a bumped pin")
	}
}

// TestPinReusableWorkflowOtherBranchRefused: a pin stored for main never
// accepts the reusable workflow at any commit from another branch, nor from
// any branch when no rule names a ref.
func TestPinReusableWorkflowOtherBranchRefused(t *testing.T) {
	pins := []IdentityPin{v2Pin("1111")}
	for _, sha := range []string{"1111", "3333"} {
		if v := JudgePublisher(payments, v2Acme(sha, "refs/heads/feature"), "", []PublisherRule{ordersRule(t)}, PublisherSourceRule, pins); v.Verdict == VerdictAccepted {
			t.Errorf("%s from feature: %+v", sha, v)
		}
	}
	noRef := ordersRule(t)
	noRef.Ref = ""
	if v := JudgePublisher(payments, v2Acme("3333", "refs/heads/main"), "", []PublisherRule{noRef}, PublisherSourceRule, pins); v.Verdict == VerdictAccepted {
		t.Fatalf("no rule names a ref: %+v", v)
	}
}

func TestPinOwnWorkflowStillExact(t *testing.T) {
	pin := IdentityPin{Service: payments, Issuer: ciAcme.Issuer, Subject: ciAcme.Subject, Repository: ciAcme.Repository}
	other := ciAcme
	other.Subject = "https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/feature"
	if v := JudgePublisher(payments, other, "", []PublisherRule{ordersRule(t)}, PublisherSourceRule, []IdentityPin{pin}); v.Verdict != VerdictNewSigner {
		t.Fatalf("verdict %+v", v)
	}
	if got := stableSubject(ciAcme.Subject, ""); got != ciAcme.Subject {
		t.Fatalf("no repository: %q", got)
	}
}

// TestPinUnrelatedRuleRefDoesNotAnchor: a stripped pin subject is tied to a
// branch only by a rule that applies to this service and repository; a rule
// for another service, or another repository, naming the caller's branch
// never makes it match.
func TestPinUnrelatedRuleRefDoesNotAnchor(t *testing.T) {
	pins := []IdentityPin{v2Pin("1111")}
	otherService := ordersRule(t)
	otherService.Services, otherService.Ref = []string{"legacy-*"}, "refs/heads/release"
	otherRepo := ordersRule(t)
	otherRepo.Repository, otherRepo.Ref = `^https://github\.com/beta/[^/]+$`, "refs/heads/release"
	for name, r := range map[string]PublisherRule{"other service": otherService, "other repository": otherRepo} {
		if v := JudgePublisher(payments, v2Acme("3333", "refs/heads/release"), "", []PublisherRule{r}, PublisherSourceRule, pins); v.Verdict == VerdictAccepted {
			t.Errorf("%s: %+v", name, v)
		}
	}
}
