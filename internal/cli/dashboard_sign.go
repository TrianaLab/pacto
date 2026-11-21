package cli

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// fulcioTrustTTL is how long an answer about Fulcio's trust is kept.
const fulcioTrustTTL = 10 * time.Minute

// signFlags are the signing-login settings of pacto dashboard, bound to
// PACTO_DASHBOARD_* like the sign-in flags.
var signFlags = []string{"sign-client-id", "sign-redirect-url", "fulcio-url", "rekor-url", "timestamp-url", "trusted-root"}

// addSignFlags registers the flags a shared dashboard signs declarations
// with: a second OIDC client Fulcio trusts and the Sigstore services, named
// as pacto declare names them.
func addSignFlags(cmd *cobra.Command, v *viper.Viper) {
	f := cmd.Flags()
	f.String("sign-client-id", "", "a second OpenID Connect client at the sign-in issuer whose ID tokens Fulcio accepts: a signed-in person logs in again with it to sign a declaration as themselves (also PACTO_DASHBOARD_SIGN_CLIENT_ID; the client secret comes from PACTO_DASHBOARD_SIGN_CLIENT_SECRET)")
	f.String("sign-redirect-url", "", "the dashboard's /auth/sign/callback as browsers reach it (https)")
	f.String("fulcio-url", signing.PublicFulcio, "Fulcio to get the signing certificate from")
	f.String("rekor-url", signing.PublicRekor, "Rekor transparency log the signature is recorded in")
	f.String("timestamp-url", "", "timestamp authority to date the signature with")
	f.String("trusted-root", "", "Sigstore trusted root JSON to verify with (default: public-good Sigstore)")
	for _, name := range signFlags {
		_ = v.BindPFlag("dashboard."+name, f.Lookup(name))
	}
	// A secret never goes on a command line, where any process can read it.
	_ = v.BindEnv("dashboard.sign-client-secret", "PACTO_DASHBOARD_SIGN_CLIENT_SECRET")
}

// errSignNeedsOIDC: a signing login is a second login at the sign-in issuer.
var errSignNeedsOIDC = errors.New("--sign-client-id needs OIDC sign-in (--oidc-issuer-url)")

// dashboardSigning is the shared dashboard's signing login and whether the
// installation's Fulcio trusts its client.
type dashboardSigning struct {
	signer   *authn.Signer
	fulcio   string
	issuer   string
	clientID string
	client   *http.Client
	now      func() time.Time

	mu     sync.Mutex
	at     time.Time
	ok     bool
	reason string
}

// newDashboardSigning builds the signing login when --sign-client-id is set;
// nil without it. o is the sign-in client, nil without OIDC sign-in.
func newDashboardSigning(v *viper.Viper, o *authn.OIDC) (*dashboardSigning, error) {
	id := v.GetString("dashboard.sign-client-id")
	if id == "" {
		return nil, nil
	}
	if o == nil {
		return nil, errSignNeedsOIDC
	}
	issuer := v.GetString("dashboard.oidc-issuer-url")
	s, err := authn.NewSigner(authn.SignConfig{IssuerURL: issuer, ClientID: id,
		ClientSecret: v.GetString("dashboard.sign-client-secret"), RedirectURL: v.GetString("dashboard.sign-redirect-url")}, o)
	if err != nil {
		return nil, err
	}
	return &dashboardSigning{signer: s, fulcio: v.GetString("dashboard.fulcio-url"), issuer: issuer, clientID: id,
		client: &http.Client{Timeout: 30 * time.Second}, now: time.Now}, nil
}

// login is the signing login to serve; nil without one.
func (s *dashboardSigning) login() *authn.Signer {
	if s == nil {
		return nil
	}
	return s.signer
}

// trusted reports whether Fulcio issues certificates for the sign client's
// tokens, kept fulcioTrustTTL. An unreadable configuration is untrusted, with
// the reason.
func (s *dashboardSigning) trusted(ctx context.Context) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && s.now().Sub(s.at) < fulcioTrustTTL {
		return s.ok, s.reason
	}
	ok, reason, err := signing.FulcioTrusts(ctx, s.client, s.fulcio, s.issuer, s.clientID)
	if err != nil {
		reason = err.Error()
	}
	s.at, s.ok, s.reason = s.now(), ok, reason
	return ok, reason
}

// report says at start whether people can sign in the dashboard, so an
// installation whose Fulcio does not trust the sign client learns it before
// anyone presses Sign. Without a signing login it says nothing.
func (s *dashboardSigning) report(ctx context.Context) {
	if s == nil {
		return
	}
	log := logging.LoggerFromContext(ctx)
	if ok, reason := s.trusted(ctx); !ok {
		log.Warn("people cannot sign declarations in the dashboard; they save drafts instead", "reason", reason)
		return
	}
	log.Info("people sign declarations in the dashboard with a fresh login", "fulcio", s.fulcio, "client", s.clientID)
}

// The signing modes a declaration plan answers.
const (
	signModeFreshLogin = "fresh-login"
	signModeNone       = "none"
)

// signingMode is how a person signs a declaration here (Decision 6): with a
// fresh login at the identity provider, only on a shared dashboard whose
// session came from OIDC and whose Fulcio trusts the sign client; otherwise
// not at all, and why. A local dashboard signs in the person's terminal, and
// a token sign-in never signs.
func (s *dashboardSigning) signingMode(ctx context.Context, local bool, method, user string) dashboard.DeclareSigning {
	none := func(reason string) dashboard.DeclareSigning {
		return dashboard.DeclareSigning{Mode: signModeNone, Reason: reason}
	}
	switch {
	case local:
		return none("run the command in your terminal to sign")
	case s == nil:
		return none("this dashboard has no signing login: save a draft or run the command in your terminal")
	case method != "oidc":
		return none("a token sign-in never signs: save a draft or run the command in your terminal")
	}
	if ok, reason := s.trusted(ctx); !ok {
		return none("Fulcio does not issue certificates for the dashboard's signing login (" + reason + "): save a draft or run the command in your terminal")
	}
	return dashboard.DeclareSigning{Mode: signModeFreshLogin, Identity: user}
}
