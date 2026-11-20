package delivery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
