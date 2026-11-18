package delivery

import (
	"strings"
	"testing"
)

var (
	ciSignerID = PublisherIdentity{Issuer: issuerGitHubActions, CI: true,
		Subject:    "https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/main",
		Repository: "https://github.com/acme/payments", Ref: "refs/heads/main"}
	workloadSignerID = PublisherIdentity{Issuer: "https://kubernetes.default.svc",
		Subject: "https://kubernetes.io/namespaces/agents/serviceaccounts/reviewer"}
)

func TestClassifySignerDefaults(t *testing.T) {
	for name, tc := range map[string]struct {
		id   PublisherIdentity
		want ActorClass
	}{
		"github actions":          {ciSignerID, ClassCI},
		"buildkite issuer":        {PublisherIdentity{Issuer: "https://agent.buildkite.com", Subject: "x"}, ClassCI},
		"circleci issuer":         {PublisherIdentity{Issuer: "https://oidc.circleci.com/org/abc", Subject: "x"}, ClassCI},
		"spiffe":                  {PublisherIdentity{Issuer: "https://spire.acme", Subject: "spiffe://acme/ns/agents/sa/bot"}, ClassAgent},
		"kubernetes sa":           {workloadSignerID, ClassAgent},
		"gcp service account":     {PublisherIdentity{Issuer: "https://accounts.google.com", Subject: "bot@acme.iam.gserviceaccount.com"}, ClassAgent},
		"interactive email":       {PublisherIdentity{Issuer: "https://login.acme.example", Subject: "alice@acme.com"}, ClassHuman},
		"unknown uri":             {PublisherIdentity{Issuer: "https://login.acme.example", Subject: "https://acme.example/bot"}, ClassAgent},
		"no subject":              {PublisherIdentity{Issuer: "https://login.acme.example"}, ClassAgent},
		"email in a uri is a uri": {PublisherIdentity{Issuer: "https://login.acme.example", Subject: "https://a@acme.example/x"}, ClassAgent},
	} {
		class, by := ClassifySigner(tc.id, nil)
		if class != tc.want || !strings.HasPrefix(by, "default:") {
			t.Errorf("%s: %s %q, want %s", name, class, by, tc.want)
		}
	}
}

func TestClassifySignerPolicyRuleWins(t *testing.T) {
	rules := []SignerClassRule{
		{Name: "other issuer", Issuer: "https://other.example", Subject: `^.+@acme\.com$`, Class: ClassAgent},
		{Name: "open", Issuer: "https://login.acme.example", Subject: ".*", Class: ClassAgent},
		{Name: "release bots", Issuer: "https://login.acme.example", Subject: `^release-bot@acme\.com$`, Class: ClassAgent},
	}
	id := PublisherIdentity{Issuer: "https://login.acme.example", Subject: "release-bot@acme.com"}
	if class, by := ClassifySigner(id, rules); class != ClassAgent || by != "policy:release bots" {
		t.Fatalf("%s %q", class, by)
	}
	id.Subject = "alice@acme.com"
	if got := Classified(id, rules); got.Class != ClassHuman || !strings.HasPrefix(got.ClassifiedBy, "default:") {
		t.Fatalf("no rule matches: %+v", got)
	}
}

func TestPolicyRejectsOpenSignerRule(t *testing.T) {
	ok := SignerClassRule{Name: "bots", Issuer: "https://login.acme.example", Subject: `^bot@acme\.com$`, Class: ClassAgent}
	got, err := OpenPolicy(signPolicy(t, PublisherPolicy{Signers: []SignerClassRule{ok}, At: "2026-11-18T10:00:00Z"}), func([]byte) error { return nil })
	if err != nil || len(got.Signers) != 1 || got.Signers[0] != ok {
		t.Fatalf("a valid signer rule: %+v %v", got, err)
	}
	for name, r := range map[string]SignerClassRule{
		"open subject":  {Issuer: ok.Issuer, Subject: ".*", Class: ClassAgent},
		"no issuer":     {Subject: ok.Subject, Class: ClassAgent},
		"another class": {Issuer: ok.Issuer, Subject: ok.Subject, Class: ClassBreakGlass},
		"no class":      {Issuer: ok.Issuer, Subject: ok.Subject},
	} {
		if _, err := OpenPolicy(signPolicy(t, PublisherPolicy{Signers: []SignerClassRule{r}, At: "2026-11-18T10:00:00Z"}),
			func([]byte) error { return nil }); err == nil || !strings.Contains(err.Error(), "signer rule 0") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadsAsDecision(t *testing.T) {
	for class, want := range map[ActorClass]bool{ClassHuman: true, ClassAgent: false, ClassCI: false, ClassController: false, ClassBreakGlass: false, "": false} {
		if ReadsAsDecision(class) != want {
			t.Errorf("%q: want %v", class, want)
		}
	}
}

// An acceptance (508-S7) signed by a GitHub Actions OIDC identity is a
// proposal: the reader classifies the verified signer and asks
// ReadsAsDecision.
func TestCISignedAcceptanceReadsAsProposal(t *testing.T) {
	if class, _ := ClassifySigner(ciSignerID, nil); class != ClassCI || ReadsAsDecision(class) {
		t.Fatalf("a CI-signed acceptance decides: %s", class)
	}
}

func TestWorkloadIdentitySignedAcceptanceReadsAsProposal(t *testing.T) {
	if class, _ := ClassifySigner(workloadSignerID, nil); class != ClassAgent || ReadsAsDecision(class) {
		t.Fatalf("a workload-signed acceptance decides: %s", class)
	}
}
