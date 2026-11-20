package delivery

import (
	"fmt"
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
