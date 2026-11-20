package delivery

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// MaxOwnerPolicyRules bounds the signer rules of one owner policy, default
// approvers and team rules together.
const MaxOwnerPolicyRules = 200

// SignerRule matches one signer by its OIDC issuer, compared exactly, and its
// subject, matched by a pinned pattern.
type SignerRule struct {
	Name    string `json:"name,omitempty" doc:"A name that refusals and the who-may-set list print"`
	Issuer  string `json:"issuer" doc:"The OIDC issuer, compared exactly"`
	Subject string `json:"subject" doc:"A pattern anchored with ^ and $ that the signer subject must match"`
}

// OwnerPolicy is who may set a service owner beside the owner's own members:
// the default approvers, the signers that count as members of a team and the
// issuers whose email subjects count as people.
type OwnerPolicy struct {
	DefaultApprovers []SignerRule            `json:"defaultApprovers,omitempty" doc:"Signers who may set any service owner"`
	Teams            map[string][]SignerRule `json:"teams,omitempty" doc:"Per team, the signers who count as its members"`
	EmailIssuers     []string                `json:"emailIssuers,omitempty" doc:"Issuers whose email subjects match an owner's DRI or email contacts"`
}

// Check refuses a rule with no issuer or a subject pattern that is not pinned,
// an empty email issuer and more than MaxOwnerPolicyRules rules.
func (p OwnerPolicy) Check() error {
	n := len(p.DefaultApprovers)
	if err := checkSignerRules("default approver", p.DefaultApprovers); err != nil {
		return err
	}
	teams := make([]string, 0, len(p.Teams))
	for team := range p.Teams {
		teams = append(teams, team)
	}
	slices.Sort(teams)
	for _, team := range teams {
		n += len(p.Teams[team])
		if err := checkSignerRules("team "+team, p.Teams[team]); err != nil {
			return err
		}
	}
	if n > MaxOwnerPolicyRules {
		return fmt.Errorf("%d rules, over the %d rule bound", n, MaxOwnerPolicyRules)
	}
	if slices.Contains(p.EmailIssuers, "") {
		return errors.New("emailIssuers holds an empty issuer")
	}
	return nil
}

// checkSignerRules refuses a rule in rules with no issuer or an unpinned
// subject; where names the list in the error.
func checkSignerRules(where string, rules []SignerRule) error {
	for i, r := range rules {
		switch {
		case r.Issuer == "":
			return fmt.Errorf("%s rule %d: issuer is empty", where, i)
		case !PinnedPattern(r.Subject):
			return fmt.Errorf("%s rule %d: subject %q is not a pinned pattern (^...$ matching one identity)", where, i, r.Subject)
		}
	}
	return nil
}

// ReadOwnerPolicy reads the owner policy at path, decoded strictly and checked.
// On any failure it returns nil and the reason: a policy that cannot be read is
// no policy, never an empty one. The file is a mounted ConfigMap, which
// Kubernetes bounds at 1 MiB.
func ReadOwnerPolicy(path string) (*OwnerPolicy, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err.Error()
	}
	var p OwnerPolicy
	if err := strictjson.Unmarshal(b, &p); err != nil {
		return nil, "the owner policy " + path + ": " + err.Error()
	}
	if err := p.Check(); err != nil {
		return nil, "the owner policy " + path + ": " + err.Error()
	}
	return &p, ""
}
