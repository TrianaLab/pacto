package app

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore/pkg/oauthflow"
	"golang.org/x/oauth2"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// The Sigstore login `pacto declare` runs by default: public-good Sigstore's
// issuer and the client whose tokens Fulcio accepts.
const (
	sigstoreIssuer = "https://oauth2.sigstore.dev/auth"
	sigstoreClient = "sigstore"
)

// Where a declare run's signer comes from: a key file, the ambient OIDC
// token, the person's interactive login, or none (an unsigned draft).
const (
	signerKey     = "key"
	signerAmbient = "ambient"
	signerLogin   = "login"
	signerNone    = "none"
)

// declareSigning is how a declare run may sign: a key file, else the ambient
// OIDC token, else the person's interactive Sigstore login.
type declareSigning struct {
	Key, OIDCIssuer, OIDCClient, FulcioURL, TimestampURL, RekorURL, TrustedRoot string
	// Device logs in with a device code instead of a browser redirect.
	Device bool
	// Prompt shows the person where to log in.
	Prompt func(string)
	// FreshLogin is the browser login; nil is authn.FreshIDToken.
	FreshLogin func(context.Context, authn.FreshConfig) (string, error)
	// Rules are the verified installation policy's signer class rules; nil
	// is the default rule.
	Rules []delivery.SignerClassRule
	// Policy is the installation signer policy readSignerPolicy read, nil
	// when it could not be read; PolicyNote then says why.
	Policy     *delivery.ClassPolicy
	PolicyNote string
}

// declareLogin is the interactive Sigstore login a person declares with.
type declareLogin struct {
	Issuer, ClientID string
	Device           bool
	Prompt           func(string)
	// Fresh is the browser login; nil is authn.FreshIDToken.
	Fresh func(context.Context, authn.FreshConfig) (string, error)
}

// signerChoice is who signs a declare run and what that signature may write:
// only a person's interactive login (Source login, class human) may write a
// declared record; every other signer writes a draft.
type signerChoice struct {
	Signer       signing.Signer
	Identity     delivery.PublisherIdentity
	Class        delivery.ActorClass
	ClassifiedBy string
	Source       string
	PolicyNote   string
	Why          string
	MayDeclare   bool
}

// declareSigner picks the signer of a declare run: the key, else the ambient
// token, else (for a person's CLI run, never for an agent) the interactive
// login. An ambient token is never the person: whoever runs in the shell can
// set the ambient token, and a certificate cannot show who held it.
func (s *Service) declareSigner(ctx context.Context, o declareSigning, agent bool) (signerChoice, error) {
	switch {
	case o.Key != "":
		return keyDeclareSigner(o)
	case signing.HasIDToken() == nil:
		return ambientDeclareSigner(ctx, o, agent)
	case agent:
		return unsignedChoice("unsigned draft: an agent has no signing identity of its own"), nil
	}
	return s.loginDeclareSigner(ctx, o)
}

// keyDeclareSigner signs with the key file: a key names no identity, so it is
// never a person.
func keyDeclareSigner(o declareSigning) (signerChoice, error) {
	kp, err := signing.LoadKey(o.Key)
	if err != nil {
		return signerChoice{}, fmt.Errorf("the signing key %s: %w", o.Key, err)
	}
	by := "default:a key names no identity"
	return signerChoice{Signer: signing.Keyed(kp, o.RekorURL), Source: signerKey, Class: delivery.ClassAgent, ClassifiedBy: by,
		Identity: delivery.PublisherIdentity{Class: delivery.ClassAgent, ClassifiedBy: by},
		Why:      "a key names no person: this run writes a draft"}, nil
}

// ambientDeclareSigner signs a draft with the ambient OIDC token. A token
// whose identity Pacto cannot read is an unknown non-person; an agent never
// signs with a person's token.
func ambientDeclareSigner(ctx context.Context, o declareSigning, agent bool) (signerChoice, error) {
	c, tok, err := ambientChoice(ctx, o, agent)
	if err != nil || c.Source == signerNone {
		return c, err
	}
	c.Signer, err = declareKeyless(o, tok)
	return c, err
}

// ambientChoice is who the ambient OIDC token names, and the token, with no
// signer built: an unsigned draft when an agent holds a person's token.
func ambientChoice(ctx context.Context, o declareSigning, agent bool) (signerChoice, string, error) {
	tok, err := signing.IDToken("")(ctx)
	if err != nil {
		return signerChoice{}, "", fmt.Errorf("the ambient OIDC token: %w", err)
	}
	c := signerChoice{Source: signerAmbient}
	if sub, iss, err := signing.TokenIdentity(tok); err != nil {
		c.Identity = delivery.PublisherIdentity{Class: delivery.ClassAgent, ClassifiedBy: "default:the token names no identity Pacto can read"}
		c.Why = "the ambient OIDC token names no person (" + err.Error() + "): this run writes a draft signed by it"
	} else {
		c.Identity = delivery.Classified(delivery.PublisherIdentity{Subject: sub, Issuer: iss}, o.Rules)
	}
	c.Class, c.ClassifiedBy = c.Identity.Class, c.Identity.ClassifiedBy
	if agent && c.Class == delivery.ClassHuman {
		return unsignedChoice("unsigned draft: an ambient token of a person is never used by an agent"), "", nil
	}
	return c, tok, nil
}

// plannedSigner is who would sign a declare run, as declareSigner picks it,
// without logging in or building a signer: a plan never fetches a login.
// With no key, no ambient token and no agent it is the person, after a
// Sigstore login.
func plannedSigner(ctx context.Context, o declareSigning, agent bool) (signerChoice, error) {
	switch {
	case o.Key != "":
		return keyDeclareSigner(o)
	case signing.HasIDToken() == nil:
		c, _, err := ambientChoice(ctx, o, agent)
		return c, err
	case agent:
		return unsignedChoice("unsigned draft: an agent has no signing identity of its own"), nil
	}
	id := delivery.PublisherIdentity{Issuer: cmp.Or(o.OIDCIssuer, sigstoreIssuer), Class: delivery.ClassHuman, ClassifiedBy: "default:a person's Sigstore login"}
	return signerChoice{Source: signerLogin, Identity: id, Class: id.Class, ClassifiedBy: id.ClassifiedBy, MayDeclare: true,
		Why: "you sign after a Sigstore login"}, nil
}

// loginDeclareSigner logs the person in and signs with that login alone.
// Only a login classified human may declare, and only when the installation
// signer policy, when read, lists its issuer and subject.
func (s *Service) loginDeclareSigner(ctx context.Context, o declareSigning) (signerChoice, error) {
	tok, err := s.env().login(ctx, declareLogin{Issuer: cmp.Or(o.OIDCIssuer, sigstoreIssuer),
		ClientID: cmp.Or(o.OIDCClient, sigstoreClient), Device: o.Device, Prompt: o.Prompt, Fresh: o.FreshLogin})
	if err != nil {
		return signerChoice{}, fmt.Errorf("the Sigstore login: %w", err)
	}
	sub, iss, err := signing.TokenIdentity(tok)
	if err != nil {
		return signerChoice{}, fmt.Errorf("the Sigstore login's token: %w", err)
	}
	id := delivery.Classified(delivery.PublisherIdentity{Subject: sub, Issuer: iss}, o.Rules)
	c := signerChoice{Source: signerLogin, Identity: id, Class: id.Class, ClassifiedBy: id.ClassifiedBy,
		MayDeclare: id.Class == delivery.ClassHuman, PolicyNote: o.PolicyNote}
	if !c.MayDeclare {
		c.Why = "the login names no person (" + id.ClassifiedBy + "): this run writes a draft"
	} else if err := checkInstallationSigner(id, o.Policy); err != nil {
		return signerChoice{}, &DeclareError{Code: codeSignerNotAllowed, Message: err.Error()}
	}
	c.Signer, err = declareKeyless(o, tok)
	return c, err
}

// unsignedChoice is an unsigned draft: its asker is nobody, why says so.
func unsignedChoice(why string) signerChoice {
	by := "default:an unsigned draft names no signer"
	return signerChoice{Source: signerNone, Class: delivery.ClassAgent, ClassifiedBy: by, Why: why,
		Identity: delivery.PublisherIdentity{Issuer: signerNone, Class: delivery.ClassAgent, ClassifiedBy: by}}
}

// declareKeyless is a keyless signer bound to tok alone: the token is used
// for this run and never stored.
func declareKeyless(o declareSigning, tok string) (signing.Signer, error) {
	tm, err := declareRoot(o.TrustedRoot)
	if err != nil {
		return signing.Signer{}, err
	}
	return signing.Keyless(cmp.Or(o.FulcioURL, fulcioURL), o.TimestampURL, o.RekorURL,
		func(context.Context) (string, error) { return tok, nil }, tm)
}

// declareRoot is the trusted root at path, or public-good Sigstore's.
func declareRoot(path string) (root.TrustedMaterial, error) {
	if path == "" {
		tm, err := publicGoodRoot(signing.PublicTUF)
		if err != nil {
			return nil, fmt.Errorf("public-good Sigstore's trusted root: %w", err)
		}
		return tm, nil
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		var tr *root.TrustedRoot
		if tr, err = root.NewTrustedRootFromJSON(raw); err == nil {
			return tr, nil
		}
	}
	return nil, fmt.Errorf("the trusted root %s: %w", path, err)
}

// interactiveLogin is the person's Sigstore login: a fresh browser login
// (prompt=login, max_age=0, auth_time checked) or a device code.
func interactiveLogin(ctx context.Context, l declareLogin) (string, error) {
	if l.Device {
		return deviceLogin(ctx, l)
	}
	fresh := l.Fresh
	if fresh == nil {
		fresh = authn.FreshIDToken
	}
	return fresh(ctx, authn.FreshConfig{IssuerURL: l.Issuer, ClientID: l.ClientID,
		ExtraScopes: []string{"email"}, Prompt: l.Prompt})
}

// deviceLogin logs the person in with sigstore's device code flow.
func deviceLogin(ctx context.Context, l declareLogin) (string, error) {
	p, err := oidc.NewProvider(ctx, l.Issuer)
	if err != nil {
		return "", fmt.Errorf("the identity provider %s: %w", l.Issuer, err)
	}
	g := oauthflow.NewDeviceFlowTokenGetterForIssuer(l.Issuer)
	g.MessagePrinter = l.Prompt
	tok, err := g.GetIDToken(p, oauth2.Config{ClientID: l.ClientID})
	if err != nil {
		return "", fmt.Errorf("the device login: %w", err)
	}
	return tok.RawString, nil
}

// readSignerPolicy reads the installation signer policy with one get of the
// delivery identity ConfigMap, and the publisher rule its publisher trust
// config derives (nil when none). When it cannot, the note says so: readers
// then judge the signature with their own policy.
func readSignerPolicy(ctx context.Context, dyn dynamic.Interface) (*delivery.ClassPolicy, []delivery.PublisherRule, string) {
	var cm struct {
		Data map[string]string `json:"data"`
	}
	err := getInto(ctx, dyn.Resource(gvrConfigMaps).Namespace(delivery.PolicyNamespace), delivery.PolicyConfigMap, &cm)
	if err == nil {
		var p delivery.ClassPolicy
		if p, err = delivery.ParseClassPolicy([]byte(cm.Data[delivery.PolicyKey])); err == nil {
			return &p, publisherRules(cm.Data[delivery.PublishersKey]), ""
		}
	}
	return nil, nil, fmt.Sprintf("no installation signer policy was read (%v); readers judge this signature with their own", err)
}

// publisherRules is the default publisher rule of the publisher trust config
// raw, nil when it is absent, unreadable or derives none.
// ponytail: a signed delivery policy's rules are not verified here, so a
// baseline only its policy accepts is skipped; the trust loader is the
// upgrade path.
func publisherRules(raw string) []delivery.PublisherRule {
	cfg, err := delivery.ParsePublishers([]byte(raw))
	if err != nil || raw == "" {
		return nil
	}
	rule, why := deriveRule(cfg.Registry, cfg.DefaultBranch)
	if why != "" {
		return nil
	}
	return []delivery.PublisherRule{rule}
}

// checkInstallationSigner refuses a person the installation signer policy p
// does not allow: an issuer it does not list, or a subject matching none of
// that issuer's subjects (anchored). A nil p was not read and refuses nobody.
func checkInstallationSigner(id delivery.PublisherIdentity, p *delivery.ClassPolicy) error {
	if p == nil {
		return nil
	}
	sg, ok := p.SignerFor(id.Issuer)
	if !ok {
		return fmt.Errorf("the installation signer policy lists no issuer %s, so %s may not declare a service", id.Issuer, id.Subject)
	}
	if re, err := regexp.Compile("^(?:" + sg.Subjects + ")$"); err != nil || !re.MatchString(id.Subject) {
		return fmt.Errorf("%s matches none of the subjects the installation signer policy allows for %s", id.Subject, id.Issuer)
	}
	return nil
}

// sameSigner checks the pushed bundle carries exactly one verified signature,
// by the planned identity, before any tag is written.
func sameSigner(planned delivery.PublisherIdentity, got []delivery.PublisherIdentity) error {
	if len(got) != 1 {
		return fmt.Errorf("the pushed bundle carries %d verified signatures; expected exactly one by %s (%s)", len(got), planned.Subject, planned.Issuer)
	}
	if got[0].Subject != planned.Subject || got[0].Issuer != planned.Issuer {
		return fmt.Errorf("the pushed bundle is signed by %s (%s); expected %s (%s)", got[0].Subject, got[0].Issuer, planned.Subject, planned.Issuer)
	}
	return nil
}

// rekorNotice tells a person signing through the public Rekor log that their
// email is recorded there.
func rekorNotice(id delivery.PublisherIdentity, rekorURL string) string {
	if id.Class != delivery.ClassHuman || rekorURL != signing.PublicRekor {
		return ""
	}
	return strings.Join(strings.Fields("your email "+id.Subject+" is recorded in the public Rekor transparency log"), " ")
}
