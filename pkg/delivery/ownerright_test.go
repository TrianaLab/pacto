package delivery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func ownerPolicy() OwnerPolicy {
	return OwnerPolicy{
		DefaultApprovers: []SignerRule{{Name: "platform-admins", Issuer: "https://accounts.google.com", Subject: `^admin@acme\.com$`}},
		Teams:            map[string][]SignerRule{"payments": {{Name: "ana", Issuer: "https://github.com/login/oauth", Subject: `^ana@acme\.com$`}}},
	}
}

// TestOwnerPolicyCheck: an empty issuer, a subject that is not pinned and more
// than MaxOwnerPolicyRules rules are refused, in the approvers and the teams.
func TestOwnerPolicyCheck(t *testing.T) {
	if err := ownerPolicy().Check(); err != nil {
		t.Fatal(err)
	}
	if err := (OwnerPolicy{}).Check(); err != nil {
		t.Fatalf("empty policy: %v", err)
	}
	many := make([]SignerRule, MaxOwnerPolicyRules+1)
	for i := range many {
		many[i] = SignerRule{Name: fmt.Sprint(i), Issuer: "https://accounts.google.com", Subject: `^a@b\.c$`}
	}
	for name, edit := range map[string]func(*OwnerPolicy){
		"issuer":  func(p *OwnerPolicy) { p.DefaultApprovers[0].Issuer = "" },
		"subject": func(p *OwnerPolicy) { p.DefaultApprovers[0].Subject = ".*" },
		"payments": func(p *OwnerPolicy) {
			p.Teams["payments"][0].Subject = `^ana@acme\.com`
		},
		"rules":        func(p *OwnerPolicy) { p.DefaultApprovers = many },
		"emailIssuers": func(p *OwnerPolicy) { p.EmailIssuers = []string{""} },
	} {
		p := ownerPolicy()
		edit(&p)
		if err := p.Check(); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestReadOwnerPolicyStrict: the mounted policy decodes strictly and passes
// Check; an unknown key, an open subject, malformed JSON and a missing file
// give no policy and the reason.
func TestReadOwnerPolicyStrict(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The chart's rendering (tests/owner_policy_test.yaml): every key present, empty ones included.
	good := write("good.json", `{"defaultApprovers":[{"issuer":"https://accounts.google.com","name":"platform-admins","subject":"^admin@acme\\.com$"}],"emailIssuers":[],"teams":{}}`)
	if p, why := ReadOwnerPolicy(good); p == nil || why != "" || p.DefaultApprovers[0].Name != "platform-admins" {
		t.Fatalf("good policy: %+v %q", p, why)
	}
	for name, c := range map[string]struct{ path, want string }{
		"unknown key":  {write("unknown.json", `{"approvers":[]}`), "approvers"},
		"open subject": {write("open.json", `{"defaultApprovers":[{"issuer":"https://accounts.google.com","subject":".*"}]}`), "pinned"},
		"malformed":    {write("bad.json", `{`), "owner policy"},
		"missing file": {filepath.Join(dir, "absent.json"), "absent.json"},
	} {
		if p, why := ReadOwnerPolicy(c.path); p != nil || !strings.Contains(why, c.want) {
			t.Errorf("%s: %+v %q, want a reason with %q", name, p, why, c.want)
		}
	}
}

// human is a person signer as ClassifySigner places one.
func human(issuer, subject string) PublisherIdentity {
	return PublisherIdentity{Issuer: issuer, Subject: subject, Class: ClassHuman}
}

var (
	ownerNow      = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	paymentsOwner = contract.Owner{Team: "payments", DRI: "Lead@Acme.com", Contacts: []contract.OwnerContact{{Type: "email", Value: "oncall@acme.com"}, {Type: "slack", Value: "dev@acme.com"}}}
	billing       = OwnerClaim{Owner: contract.Owner{Team: "billing"}, Provenance: "declared"}
	google        = "https://accounts.google.com"
	githubWeb     = "https://github.com/login/oauth"
)

func TestOwnerRightTable(t *testing.T) {
	pol := ownerPolicy()
	declared := OwnerClaim{Owner: paymentsOwner, Provenance: "declared"}
	attested := OwnerClaim{Owner: paymentsOwner, Provenance: "attested", Source: "CODEOWNERS:7 at a1b2c3d, signed by github.com/acme/invoices/.github/workflows/ci.yml"}
	ci := PublisherIdentity{Issuer: "https://token.actions.githubusercontent.com", Subject: "Lead@Acme.com", Class: ClassCI}
	agent := PublisherIdentity{Issuer: google, Subject: "lead@acme.com", Class: ClassAgent}
	cases := []struct {
		name              string
		signer            PublisherIdentity
		current, proposed OwnerClaim
		policy            *OwnerPolicy
		allowed           bool
		code, grant       string
	}{
		{"unchanged", agent, declared, declared, nil, true, "", "unchanged"},
		{"agent DRI", agent, declared, billing, &pol, false, OwnerSignerNotPerson, ""},
		{"CI DRI", ci, declared, billing, &pol, false, OwnerSignerNotPerson, ""},
		{"workload", PublisherIdentity{Subject: "spiffe://x", Class: ClassAgent}, declared, billing, &pol, false, OwnerSignerNotPerson, ""},
		{"team rule", human(githubWeb, "ana@acme.com"), declared, billing, &pol, true, "", "declared-owner"},
		{"DRI email", human(google, "lead@acme.com"), declared, billing, nil, true, "", "declared-owner"},
		{"DRI unlisted issuer", human("https://idp.example", "lead@acme.com"), declared, billing, &pol, false, OwnerSignerNotAllowed, ""},
		{"email contact", human(google, "ONCALL@acme.com"), declared, billing, &pol, true, "", "declared-owner"},
		{"slack contact", human(google, "dev@acme.com"), declared, billing, &pol, false, OwnerSignerNotAllowed, ""},
		{"attested member", human(githubWeb, "ana@acme.com"), attested, billing, &pol, true, "", "attested-owner"},
		{"default approver", human(google, "admin@acme.com"), declared, billing, &pol, true, "", "default-approver:platform-admins"},
		{"first owner", human(githubWeb, "ana@acme.com"), OwnerClaim{}, billing, &pol, false, OwnerSignerNotAllowed, ""},
		{"no policy", human(githubWeb, "ana@acme.com"), declared, billing, nil, false, OwnerPolicyUnverified, ""},
		{"via target", human(githubWeb, "ana@acme.com"), OwnerClaim{Owner: paymentsOwner, Provenance: "declared-via-target"}, billing, &pol, false, OwnerSignerNotAllowed, ""},
		{"inferred", human(google, "lead@acme.com"), OwnerClaim{Owner: paymentsOwner, Provenance: "inferred"}, billing, &pol, false, OwnerSignerNotAllowed, ""},
		{"removal", human(githubWeb, "bob@acme.com"), declared, OwnerClaim{}, &pol, false, OwnerSignerNotAllowed, ""},
		{"email issuers listed", human(google, "lead@acme.com"), declared, billing, &OwnerPolicy{EmailIssuers: []string{"https://idp.example"}}, false, OwnerSignerNotAllowed, ""},
		{"no email subject", human(google, "lead"), OwnerClaim{Owner: contract.Owner{DRI: "lead"}, Provenance: "declared"}, billing, &pol, false, OwnerSignerNotAllowed, ""},
	}
	for _, c := range cases {
		r := DecideOwnerRight("invoices", c.signer, c.current, c.proposed, c.policy, "trust missing", ownerNow)
		if r.Allowed != c.allowed || r.Code != c.code || r.Grant != c.grant || r.Message == "" {
			t.Errorf("%s: %+v", c.name, r)
		}
		if c.code == OwnerPolicyUnverified && (r.Unverified == "" || !strings.Contains(r.Message, "trust missing")) {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}

func TestOwnerRightMessageMatchesUX(t *testing.T) {
	pol := OwnerPolicy{DefaultApprovers: []SignerRule{{Name: "platform-admins", Issuer: google, Subject: `^admin@acme\.com$`}}}
	attested := OwnerClaim{Owner: contract.Owner{Team: "payments"}, Provenance: "attested",
		Source: "CODEOWNERS:7 at a1b2c3, signed by github.com/acme/invoices/.github/workflows/ci.yml"}
	r := DecideOwnerRight("invoices", human(google, "eve@acme.com"), attested, billing, &pol, "", ownerNow)
	want := "service.owner is attested as team payments (CODEOWNERS:7 at a1b2c3, signed by github.com/acme/invoices/.github/workflows/ci.yml); " +
		"only payments members or default approvers (platform-admins) may set it"
	if r.Message != want {
		t.Fatalf("got  %q\nwant %q", r.Message, want)
	}
	for name, c := range map[string]struct {
		r    OwnerRight
		want string
	}{
		"declared": {DecideOwnerRight("invoices", human(google, "eve@acme.com"), OwnerClaim{Owner: contract.Owner{DRI: "lead@acme.com"}, Provenance: "declared"}, billing, nil, "x", ownerNow), "; lead@acme.com or the default approvers (unknown until a verified owner policy is read) may set it"},
		"firstNoPolicy": {DecideOwnerRight("invoices", human(google, "eve@acme.com"), OwnerClaim{}, billing, nil, "trust missing", ownerNow),
			"no verified owner policy was read (trust missing); the default approvers (unknown until a verified owner policy is read) may set it"},
		"declaredAs": {DecideOwnerRight("invoices", human(google, "eve@acme.com"), OwnerClaim{Owner: contract.Owner{Team: "payments"}, Provenance: "declared"}, billing, &pol, "", ownerNow), "service.owner is declared as team payments; only payments members or default approvers (platform-admins) may set it"},
		"first":      {DecideOwnerRight("invoices", human(google, "eve@acme.com"), OwnerClaim{}, billing, &OwnerPolicy{}, "", ownerNow), "no declared or attested owner yet; nobody (no default approvers are configured) may set the first one"},
		"contacts":   {DecideOwnerRight("invoices", human(google, "eve@acme.com"), OwnerClaim{Owner: contract.Owner{Contacts: []contract.OwnerContact{{Type: "slack", Value: "#x"}}}, Provenance: "declared"}, billing, &pol, "", ownerNow), "declared as its contacts"},
		"notPerson":  {DecideOwnerRight("invoices", PublisherIdentity{Class: ClassAgent}, OwnerClaim{}, billing, &pol, "", ownerNow), "only a person may set service.owner, and an unsigned run signs as agent"},
		"noClass":    {DecideOwnerRight("invoices", PublisherIdentity{Subject: "bot"}, OwnerClaim{}, billing, &pol, "", ownerNow), "bot signs as no person"},
		"allowed":    {DecideOwnerRight("invoices", human(google, "admin@acme.com"), OwnerClaim{}, billing, &pol, "", ownerNow), "allowed to set service.owner of invoices (default-approver:platform-admins)"},
	} {
		if !strings.Contains(c.r.Message, c.want) {
			t.Errorf("%s: %q lacks %q", name, c.r.Message, c.want)
		}
	}
}

func TestOwnerRightMayAcceptBounded(t *testing.T) {
	pol := OwnerPolicy{}
	for i := range 15 {
		pol.DefaultApprovers = append(pol.DefaultApprovers, SignerRule{Name: fmt.Sprint("r", i%12), Issuer: google, Subject: fmt.Sprintf(`^a%d@b\.c$`, i)})
	}
	pol.DefaultApprovers = append(pol.DefaultApprovers, SignerRule{Issuer: google, Subject: `^x@b\.c$`})
	r := DecideOwnerRight("invoices", human(google, "eve@acme.com"), OwnerClaim{Owner: paymentsOwner, Provenance: "attested"}, billing, &pol, "", ownerNow)
	if len(r.MayAccept) != maxMayAccept || r.MayAccept[0] != "payments members" || r.MayAccept[1] != "Lead@Acme.com" || r.MayAccept[2] != "r0" {
		t.Fatalf("%v", r.MayAccept)
	}
	if got := mayAccept(OwnerClaim{Owner: paymentsOwner, Provenance: "inferred"}, nil); len(got) != 0 {
		t.Fatalf("an inferred owner named %v", got)
	}
	if r := DecideOwnerRight("invoices", human(google, "x@b.c"), OwnerClaim{}, billing, &pol, "", ownerNow); r.Grant != `default-approver:^x@b\.c$` {
		t.Fatalf("%+v", r)
	}
}

func TestOwnerRightSourceIsRegistryPolicy(t *testing.T) {
	r := DecideOwnerRight("invoices", human(google, "eve@acme.com"), billing, billing, nil, "", ownerNow)
	want := WorkloadAccess{Resource: "services", Name: "invoices", Allowed: true, Source: AccessByRegistryPolicy, At: "2026-11-20T10:00:00Z"}
	if r.WorkloadAccess != want {
		t.Fatalf("%+v", r.WorkloadAccess)
	}
}

// TestJudgeOwnerSigners: the first allowed signer wins, else the most
// decisive refusal; no signer is unverified with why.
func TestJudgeOwnerSigners(t *testing.T) {
	pol := ownerPolicy()
	current := OwnerClaim{Owner: contract.Owner{Team: "payments"}, Provenance: "declared"}
	proposed := OwnerClaim{Owner: contract.Owner{Team: "billing"}}
	agent, eve, admin := PublisherIdentity{Subject: "bot", Class: ClassAgent}, human(google, "eve@acme.com"), human(google, "admin@acme.com")
	for name, c := range map[string]struct {
		signers []PublisherIdentity
		policy  *OwnerPolicy
		code    string
	}{
		"allowed wins":         {[]PublisherIdentity{agent, eve, admin}, &pol, ""},
		"person over agent":    {[]PublisherIdentity{agent, eve}, &pol, OwnerSignerNotAllowed},
		"agent over no policy": {[]PublisherIdentity{eve, agent}, nil, OwnerSignerNotPerson},
	} {
		if r := JudgeOwnerSigners("invoices", c.signers, "", current, proposed, c.policy, "trust missing", ownerNow); r.Code != c.code || r.Allowed != (c.code == "") {
			t.Errorf("%s: %+v", name, r)
		}
	}
	r := JudgeOwnerSigners("invoices", nil, "", current, proposed, &pol, "", ownerNow)
	if r.Allowed || r.Code != OwnerSignerUnverified || r.Unverified != "no signature of it verified" || r.Source != AccessByRegistryPolicy ||
		!strings.Contains(r.Message, "invoices sets is unverified") || len(r.MayAccept) == 0 {
		t.Errorf("no signer: %+v", r)
	}
	if r := JudgeOwnerSigners("invoices", nil, "registry down", current, proposed, &pol, "", ownerNow); !strings.HasSuffix(r.Message, "registry down") {
		t.Errorf("why: %+v", r)
	}
}

// TestDecideFirstOwner: a created service may name only an owner its creator
// belongs to, or any owner when the creator is a default approver; nobody
// else, and nobody at all without a verified policy.
func TestDecideFirstOwner(t *testing.T) {
	pol := ownerPolicy()
	payments := OwnerClaim{Owner: contract.Owner{Team: "payments"}, Provenance: "declared"}
	for _, c := range []struct {
		name        string
		signer      PublisherIdentity
		proposed    OwnerClaim
		policy      *OwnerPolicy
		code, grant string
		message     string
	}{
		{"member", human(githubWeb, "ana@acme.com"), payments, &pol, "", "first-owner", "allowed to set service.owner of invoices (first-owner)"},
		{"approver", human(google, "admin@acme.com"), billing, &pol, "", "default-approver:platform-admins", ""},
		{"non member", human(githubWeb, "ana@acme.com"), billing, &pol, OwnerSignerNotAllowed, "",
			"a new service names an owner its creator belongs to: only billing members or default approvers (platform-admins) may create one owned by team billing"},
		{"no policy", human(githubWeb, "ana@acme.com"), payments, nil, OwnerPolicyUnverified, "", "trust missing"},
		{"agent", PublisherIdentity{Subject: "ana@acme.com", Issuer: githubWeb, Class: ClassAgent}, payments, &pol, OwnerSignerNotPerson, "", "only a person"},
	} {
		r := DecideFirstOwner("invoices", c.signer, c.proposed, c.policy, "trust missing", ownerNow)
		if r.Allowed != (c.code == "") || r.Code != c.code || r.Grant != c.grant || !strings.Contains(r.Message, c.message) || len(r.MayAccept) == 0 {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}
