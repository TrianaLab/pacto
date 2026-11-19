package delivery

import (
	"encoding/json"
	"strings"
	"testing"
)

var acme = PersonSigner{Issuer: "https://idp.acme.example", ClientID: "sigstore", UsernamePrefix: "oidc:", Subjects: `^[^@]+@acme\.example$`}

func TestApproverIdentity(t *testing.T) {
	p := ClassPolicy{AgentGroups: []string{"pacto:agents"}, Signers: []PersonSigner{acme}}
	for _, c := range []struct {
		name, issuer, san string
		policy            ClassPolicy
		want              ClassSource
	}{
		{"person", acme.Issuer, "alice@acme.example", p, ClassByUser},
		{"uri", acme.Issuer, "https://github.com/acme/app/.github/workflows/ci.yml@refs/heads/main", p, ClassBySignerURI},
		{"outside subjects", acme.Issuer, "mallory@evil.example", p, ClassBySignerURI},
		{"gcp service account", acme.Issuer, "ci@proj.iam.gserviceaccount.com", ClassPolicy{Signers: []PersonSigner{{Issuer: acme.Issuer, Subjects: ".+"}}}, ClassBySignerURI},
		{"workload identity", acme.Issuer, "proj.svc.id.goog[ns/sa]", ClassPolicy{Signers: []PersonSigner{{Issuer: acme.Issuer, Subjects: ".+"}}}, ClassBySignerURI},
		{"unanchored subjects match the whole address", acme.Issuer, "eve@acme.example.evil.io",
			ClassPolicy{Signers: []PersonSigner{{Issuer: acme.Issuer, UsernamePrefix: "oidc:", Subjects: `@acme\.example`}}}, ClassBySignerURI},
		{"unanchored subjects", acme.Issuer, "alice@acme.example",
			ClassPolicy{Signers: []PersonSigner{{Issuer: acme.Issuer, UsernamePrefix: "oidc:", Subjects: `[^@]+@acme\.example`}}}, ClassByUser},
		{"classified agent", acme.Issuer, "alice@acme.example", ClassPolicy{Signers: []PersonSigner{acme}, HumanGroups: []string{"people"}}, ClassBySignerURI},
	} {
		id, source, err := ApproverIdentity(c.issuer, c.san, c.policy)
		if err != nil || source != c.want {
			t.Errorf("%s: %v %q, want %q", c.name, err, source, c.want)
		}
		if id.User != "oidc:"+c.san && id.User != c.san || len(id.Groups) != 0 {
			t.Errorf("%s: identity %+v", c.name, id)
		}
	}
	for name, c := range map[string]struct {
		issuer, san string
		policy      ClassPolicy
	}{
		"unknown issuer": {"https://other.example", "alice@acme.example", p},
		"no subjects":    {acme.Issuer, "alice@acme.example", ClassPolicy{Signers: []PersonSigner{{Issuer: acme.Issuer}}}},
		"bad subjects":   {acme.Issuer, "alice@acme.example", ClassPolicy{Signers: []PersonSigner{{Issuer: acme.Issuer, Subjects: "("}}}},
		"empty san":      {acme.Issuer, "", p},
	} {
		if _, _, err := ApproverIdentity(c.issuer, c.san, c.policy); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestKeylessRefusalNamesAgents(t *testing.T) {
	id := Identity{User: "oidc:https://ci.example"}
	code, why := KeylessRefusal(id, ClassBySignerURI)
	if code != "AgentsMayNotApprove" || why != "agents cannot approve: oidc:https://ci.example is a signer whose certificate names a workload or an address outside the approvers' domain" {
		t.Errorf("%q %q", code, why)
	}
	if code, why := KeylessRefusal(Identity{User: "oidc:alice@acme.example"}, ClassByUser); code != "" || why != "" {
		t.Errorf("a person refused: %q %q", code, why)
	}
}

func TestClassPolicySignersJSON(t *testing.T) {
	p, err := ParseClassPolicy([]byte(`{"signers":[{"issuer":"https://idp.acme.example","clientID":"sigstore","usernamePrefix":"oidc:","subjects":"^a$","fulcioURL":"https://f","timestampURL":"https://t","trustedRoot":"{}"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := p.SignerFor("https://idp.acme.example")
	if !ok || s != (PersonSigner{Issuer: "https://idp.acme.example", ClientID: "sigstore", UsernamePrefix: "oidc:", Subjects: "^a$", FulcioURL: "https://f", TimestampURL: "https://t", TrustedRoot: "{}"}) {
		t.Errorf("%+v %v", s, ok)
	}
	if _, ok := p.SignerFor("https://other.example"); ok {
		t.Error("found a signer for another issuer")
	}
	b, _ := json.Marshal(DefaultClassPolicy())
	if strings.Contains(string(b), "signers") {
		t.Errorf("an empty signer list is published: %s", b)
	}
}
