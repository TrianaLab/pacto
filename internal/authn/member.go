package authn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	pdelivery "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/memberlink"
)

// MemberLink is one member cluster's live link, as the hub holds it.
type MemberLink interface {
	// UID is the member cluster's UID.
	UID() string
	// Review asks the member; it fails when the link is down, the cluster is
	// no longer approved or the member did not answer in time.
	Review(ctx context.Context, r memberlink.Review) (memberlink.Answer, error)
	// Note records the model a review used for a token from issuer, for
	// person.
	Note(issuer, model, person string)
	// IssuerRefused says whether the member's apiserver is marked as
	// refusing tokens from issuer.
	IssuerRefused(issuer string) bool
}

// MemberExchange trades a person's session token for one only the member
// cluster uid accepts.
type MemberExchange func(ctx context.Context, token, uid string) (string, error)

// MemberMapping is the group mapping in force for a member and issuer, nil
// when a person approved none.
type MemberMapping func(issuer string) *pdelivery.GroupMappingState

// DefaultMemberAudience is the audience a person's token is exchanged for,
// {uid} replaced by the member's UID: one each member's apiserver accepts
// and the hub's does not.
const DefaultMemberAudience = "pacto-member-{uid}"

var (
	// errIssuerNotAccepted: the member's apiserver refuses the person's
	// issuer and no group mapping stands in for it.
	errIssuerNotAccepted = errors.New("the member cluster does not accept your identity provider and no person approved a group mapping for it")
	// errNoForwardableIdentity: the person signed in some way the member
	// cannot check (a sign-in link, an opaque, expired or long-lived token).
	errNoForwardableIdentity = errors.New("your sign-in cannot be checked by a member cluster")
	// errMemberListOnly: a member answers list over every object of a
	// reviewable resource only.
	errMemberListOnly = errors.New("a member cluster answers only whether a person may list the resources Pacto reads")
	// errForeignIssuer: the person's token comes from another identity
	// provider than the one the hub exchanges tokens at, so no member token
	// can be made for it.
	errForeignIssuer = errors.New("the token comes from another identity provider than the one the hub exchanges tokens at")
)

// MemberReview reviews people in the member cluster behind link. With
// exchange set, and the member's apiserver not marked as refusing the
// person's issuer, it forwards a token exchanged for that member, never the
// session token. Otherwise, or once the member refuses the issuer, it sends
// the person under the group mapping in force for the issuer. Any failure or
// refusal is an error, so the cluster is hidden and the verdict not cached.
func MemberReview(link MemberLink, exchange MemberExchange, mapping MemberMapping) Review {
	r := &memberReviewer{link: link, exchange: exchange, mapping: mapping, now: time.Now}
	return r.review
}

type memberReviewer struct {
	link     MemberLink
	exchange MemberExchange
	mapping  MemberMapping
	now      func() time.Time
	// logged holds the warnings already logged, once each.
	logged sync.Map
}

func (m *memberReviewer) review(ctx context.Context, p Person, namespace string, res ResourceAttributes) (bool, error) {
	if (res.Verb != "" && res.Verb != "list") || res.Name != "" || !memberlink.Reviewable(res.Group, res.Resource) {
		return false, errMemberListOnly
	}
	token, issuer, err := m.forwardable(ctx)
	if err != nil {
		return false, err
	}
	q := memberlink.Review{Namespace: namespace, Group: res.Group, Resource: res.Resource}
	if m.exchange != nil && !m.link.IssuerRefused(issuer) {
		allowed, refused, err := m.withToken(ctx, p, issuer, token, q)
		if !refused {
			return allowed, err
		}
	}
	return m.withMapping(ctx, p, issuer, q)
}

// forwardable is the signed-in person's session token and its issuer, when
// a member could check it: a fresh JWT. A sign-in link session holds no
// token, and is a ServiceAccount besides.
func (m *memberReviewer) forwardable(ctx context.Context) (token, issuer string, err error) {
	st := StateFrom(ctx)
	if !st.SignedIn || st.Token == "" {
		return "", "", errNoForwardableIdentity
	}
	issuer = memberlink.TokenIssuer(st.Token)
	if err := memberlink.TokenFresh(st.Token, m.now()); err != nil {
		if errors.Is(err, memberlink.ErrTokenLifetime) {
			m.once(ctx, "lifetime\x00"+issuer, "a member cluster reviews nobody signed in with tokens valid for over an hour", "issuer", issuer, "reason", err.Error())
		}
		return "", "", errNoForwardableIdentity
	}
	return st.Token, issuer, nil
}

// withToken asks the member about the person's token exchanged for it. It
// reports refused when the member does not accept the issuer and the hub
// has marked it so, for the mapping to answer instead.
func (m *memberReviewer) withToken(ctx context.Context, p Person, issuer, token string, q memberlink.Review) (allowed, refused bool, err error) {
	uid := m.link.UID()
	forward, err := m.exchange(ctx, token, uid)
	if errors.Is(err, errForeignIssuer) {
		return m.notAccepted(p, issuer)
	}
	if err != nil {
		return false, false, err
	}
	if err := memberlink.TokenFresh(forward, m.now()); err != nil {
		return false, false, errors.New("the token exchanged for the member cluster cannot be forwarded: " + err.Error())
	}
	q.Token = forward
	a, err := m.link.Review(ctx, q)
	switch {
	case err != nil:
		return false, false, err
	case a.Refused == memberlink.RefusedIssuerNotAccepted:
		return m.notAccepted(p, issuer)
	case a.Refused != "":
		return false, false, errors.New("the member cluster refused the review: " + a.Refused)
	}
	m.link.Note(issuer, memberlink.ModelSharedIssuer, p.User)
	return a.Allowed, false, nil
}

// notAccepted notes that the member cannot accept p's token from issuer,
// and reports refused once the hub marks the issuer so.
func (m *memberReviewer) notAccepted(p Person, issuer string) (allowed, refused bool, err error) {
	m.link.Note(issuer, memberlink.ModelIssuerNotAccepted, p.User)
	if m.link.IssuerRefused(issuer) {
		return false, true, nil
	}
	return false, false, errors.New("the member cluster did not accept your token")
}

// withMapping asks the member about the person as the group mapping in
// force for issuer names them.
func (m *memberReviewer) withMapping(ctx context.Context, p Person, issuer string, q memberlink.Review) (bool, error) {
	var g *pdelivery.GroupMappingState
	if m.mapping != nil {
		g = m.mapping(issuer)
	}
	if g == nil {
		m.once(ctx, "mapping\x00"+issuer, "a member cluster hides from signed-in readers: it does not accept their issuer and no group mapping is approved",
			"cluster", m.link.UID(), "issuer", issuer)
		return false, errIssuerNotAccepted
	}
	groups := pdelivery.ClusterGroupMapping{Rules: g.Rules}.Apply(p.Groups)
	if slices.ContainsFunc(groups, func(s string) bool { return strings.HasPrefix(s, "system:") }) {
		return false, errors.New("the group mapping names a system group")
	}
	q.Mapped = &memberlink.Mapped{User: mappedUser(issuer, p.User), Groups: groups, Record: g.Record}
	a, err := m.link.Review(ctx, q)
	switch {
	case err != nil:
		return false, err
	case a.Refused != "":
		return false, errors.New("the member cluster refused the review: " + a.Refused)
	}
	m.link.Note(issuer, memberlink.ModelGroupMapping, p.User)
	return a.Allowed, nil
}

// once logs msg the first time key is seen.
func (m *memberReviewer) once(ctx context.Context, key, msg string, args ...any) {
	if _, seen := m.logged.LoadOrStore(key, true); !seen {
		slog.WarnContext(ctx, msg, args...)
	}
}

// mappedUser is user as a member knows them under a group mapping: bound to
// the issuer, so two issuers' people with one username never share a member
// RoleBinding.
func mappedUser(issuer, user string) string {
	sum := sha256.Sum256([]byte(issuer))
	return "pacto-mapped:" + hex.EncodeToString(sum[:])[:12] + ":" + user
}

// MemberExchangeConfig is the OAuth client a hub exchanges people's tokens
// as, one audience per member.
type MemberExchangeConfig struct {
	IssuerURL, ClientID, ClientSecret string
	// Audience is asked for with {uid} replaced by the member's UID;
	// DefaultMemberAudience when empty.
	Audience   string
	HTTPClient *http.Client
}

// NewMemberExchange exchanges a person's token for one member (RFC 8693),
// as MCP.Exchange does for the apiserver. A token from another issuer is
// refused with no request, and counts as one the member does not accept,
// so people of a second identity provider reach the group mapping. The issuer is read at the first
// exchange, and again while that fails. Answers are cached per token hash
// and member.
func NewMemberExchange(cfg MemberExchangeConfig) MemberExchange {
	if cfg.Audience == "" {
		cfg.Audience = DefaultMemberAudience
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	x := &tokenExchange{client: client, clientID: cfg.ClientID, clientSecret: cfg.ClientSecret, now: time.Now, cache: map[[32]byte]exchanged{}}
	var mu sync.Mutex
	return func(ctx context.Context, token, uid string) (string, error) {
		if memberlink.TokenIssuer(token) != cfg.IssuerURL {
			return "", errForeignIssuer
		}
		mu.Lock()
		if x.tokenURL == "" {
			p, _, err := discover(client, cfg.IssuerURL)
			if err != nil {
				mu.Unlock()
				return "", err
			}
			x.tokenURL = p.Endpoint().TokenURL
		}
		mu.Unlock()
		sum := sha256.Sum256([]byte(token))
		key := sha256.Sum256(append(sum[:], uid...))
		return x.exchange(ctx, token, strings.ReplaceAll(cfg.Audience, "{uid}", uid), key, tokenExpiry(token))
	}
}
