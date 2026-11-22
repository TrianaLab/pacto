package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/memberlink"
)

// memberHub is the hub of a dashboard that reads joined clusters: the
// endpoint members link to, the approved clusters it accepts, and the
// reviewers that ask each live member about a person.
type memberHub struct {
	hub       *memberlink.Hub
	approvals *approvals
	exchange  authn.MemberExchange
	logger    *slog.Logger

	mu        sync.Mutex
	reviewers map[string]authn.Review
	// deduped holds the members already logged as served by a context.
	deduped sync.Map
}

// newMemberHub is the hub of host, the dashboard's external host the members
// dial and sign, accepting the clusters approvals approves. exchange is nil
// when no token exchange is configured: every person is then reviewed under
// a group mapping.
func newMemberHub(host string, exchange authn.MemberExchange, logger *slog.Logger) (*memberHub, error) {
	if host == "" {
		return nil, errors.New("member clusters link to the dashboard's external host: set --oidc-redirect-url or --members-host")
	}
	a := &approvals{now: time.Now, interval: fleetRefreshInterval}
	hub, _ := memberlink.NewHub(memberlink.HubOptions{Host: host, Approved: a.lookup, Logger: logger}) // both set: no error
	return &memberHub{hub: hub, approvals: a, exchange: exchange, logger: logger, reviewers: map[string]authn.Review{}}, nil
}

// memberHost is the host members dial: that of the dashboard's sign-in
// redirect URL, else --members-host.
func memberHost(v *viper.Viper) string {
	if u, err := url.Parse(v.GetString("dashboard.oidc-redirect-url")); err == nil && u.Host != "" {
		return u.Host
	}
	return v.GetString("dashboard.members-host")
}

// dashboardMembers is the member hub of a dashboard that reads joined
// clusters through list; nil without them, and nil with a warning when no
// external host is configured, so its members stay unreviewable. It
// exchanges tokens when the dashboard has an issuer and client credentials.
func dashboardMembers(ctx context.Context, v *viper.Viper, list func(context.Context) delivery.ClusterJoinList, logger *slog.Logger) *memberHub {
	if list == nil {
		return nil
	}
	var exchange authn.MemberExchange
	issuer, id, secret := v.GetString("dashboard.oidc-issuer-url"), v.GetString("dashboard.oidc-client-id"), v.GetString("dashboard.oidc-client-secret")
	if issuer != "" && id != "" && secret != "" {
		// newDashboardIdentity refuses the same issuer settings when they fail.
		client, _ := authn.IssuerClient(issuer, v.GetString("dashboard.oidc-internal-issuer-url"), v.GetString("dashboard.oidc-ca-file"))
		exchange = authn.NewMemberExchange(authn.MemberExchangeConfig{IssuerURL: issuer, ClientID: id, ClientSecret: secret,
			Audience: v.GetString("dashboard.members-token-audience"), HTTPClient: client})
	}
	m, err := newMemberHub(memberHost(v), exchange, logger)
	if err != nil {
		// ponytail: a warning, not a refusal, so a dashboard with token
		// sign-in only keeps starting; its members show no live channel.
		logger.Warn("member clusters cannot link to this dashboard", "reason", err.Error())
		return nil
	}
	go m.approvals.run(ctx, list)
	return m
}

// add is reviews with a reviewer for each live member that no context
// already reviews. The person's own context is local and needs no hub
// trust, so it wins. reviews is never changed.
func (m *memberHub) add(reviews map[string]authn.Review) map[string]authn.Review {
	if m == nil {
		return reviews
	}
	live := m.hub.Live()
	if len(live) == 0 {
		return reviews
	}
	out := maps.Clone(reviews)
	if out == nil {
		out = map[string]authn.Review{}
	}
	for _, uid := range live {
		if _, ok := out[uid]; ok {
			if _, seen := m.deduped.LoadOrStore(uid, true); !seen {
				m.logger.Info("a member cluster is reviewed through its kubeconfig context, not its link", "cluster", uid)
			}
			continue
		}
		out[uid] = m.reviewer(uid)
	}
	return out
}

// reviewer is the one reviewer of member uid, kept so it logs each warning
// once.
func (m *memberHub) reviewer(uid string) authn.Review {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reviewers[uid]
	if !ok {
		r = authn.MemberReview(hubLink{hub: m.hub, uid: uid}, m.exchange, func(issuer string) *delivery.GroupMappingState {
			return m.approvals.mapping(uid, issuer)
		})
		m.reviewers[uid] = r
	}
	return r
}

// linksFunc is the fleet's member link facts over the hub, nil without one.
func (m *memberHub) linksFunc() func() map[string]fleet.ClusterLink {
	if m == nil {
		return nil
	}
	return func() map[string]fleet.ClusterLink { return m.links(time.Now()) }
}

// links is each member's link at now, with the group mapping in force for
// each issuer it reviews under a mapping. A link down for longer than a
// snapshot stays fresh is left out.
func (m *memberHub) links(now time.Time) map[string]fleet.ClusterLink {
	out := map[string]fleet.ClusterLink{}
	for uid, s := range m.hub.Links() {
		if s.Channel == fleet.LinkDown && now.Sub(s.Since) > delivery.TargetSnapshotFreshness {
			continue
		}
		l := fleet.ClusterLink{Channel: s.Channel, Since: &s.Since}
		for _, iss := range slices.Sorted(maps.Keys(s.Issuers)) {
			l.Issuers = append(l.Issuers, issuerModel(iss, s.Issuers[iss], m.approvals.mapping(uid, iss)))
		}
		out[uid] = l
	}
	return out
}

// issuerModel is how a member reviews people from issuer under model, with
// g, the group mapping approved for it, when reads go through a mapping.
func issuerModel(issuer, model string, g *delivery.GroupMappingState) fleet.IssuerModel {
	im := fleet.IssuerModel{Issuer: issuer, Model: model}
	if g == nil || model == fleet.IssuerShared {
		return im
	}
	rules := make([]string, len(g.Rules))
	for i, r := range g.Rules {
		rules[i] = r.From + "=" + r.To
	}
	im.MappedBy, im.Mapping = g.ApprovedBy, strings.Join(rules, ", ")
	if at, err := time.Parse(time.RFC3339, g.ApprovedAt); err == nil {
		im.MappedAt = &at
	}
	return im
}

// hubLink is the hub's link to one member.
type hubLink struct {
	hub *memberlink.Hub
	uid string
}

func (l hubLink) UID() string { return l.uid }
func (l hubLink) Review(ctx context.Context, r memberlink.Review) (memberlink.Answer, error) {
	return l.hub.Review(ctx, l.uid, r)
}
func (l hubLink) Note(issuer, model, person string) { l.hub.NoteIdentity(l.uid, issuer, model, person) }
func (l hubLink) IssuerRefused(issuer string) bool  { return l.hub.IssuerRefused(l.uid, issuer) }

// approvals is the newest verified list of approved members, read only from
// memory: an anonymous member handshake never drives a registry read.
type approvals struct {
	now      func() time.Time
	interval time.Duration
	cur      atomic.Pointer[map[string]approvedMember]
	// complete is when the last complete list was read; run's alone.
	complete time.Time
}

// approvedMember is one approved member's key and group mappings.
type approvedMember struct {
	pub      ed25519.PublicKey
	keyID    string
	mappings []delivery.GroupMappingState
}

// run reads list now and once per interval until ctx ends.
func (a *approvals) run(ctx context.Context, list func(context.Context) delivery.ClusterJoinList) {
	t := time.NewTicker(a.interval)
	defer t.Stop()
	for {
		a.update(list(ctx))
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// update takes l as the approved members. A list read only partly keeps the
// previous one for at most two intervals, then counts only what it
// verified, which for a registry that cannot be read at all is nobody.
// ponytail: a revoked member keeps its link up to two intervals while the
// registry fails; shorten the interval if that matters.
func (a *approvals) update(l delivery.ClusterJoinList) {
	now := a.now()
	if l.Completeness == fleet.CompletenessComplete {
		a.complete = now
	} else if a.cur.Load() != nil && now.Sub(a.complete) < 2*a.interval {
		return
	}
	set := map[string]approvedMember{}
	for _, c := range l.Clusters {
		der, _ := base64.StdEncoding.DecodeString(c.ApprovedPublicKey) // checked when it was approved
		pub, _ := x509.ParsePKIXPublicKey(der)
		if k, ok := pub.(ed25519.PublicKey); ok && c.State == delivery.JoinApproved {
			set[c.UID] = approvedMember{pub: k, keyID: c.ApprovedKeyID, mappings: c.Mappings}
		}
	}
	a.cur.Store(&set)
}

// lookup is memberlink.HubOptions.Approved over the list in memory.
func (a *approvals) lookup(_ context.Context, uid string) (ed25519.PublicKey, string, bool) {
	m, ok := a.member(uid)
	return m.pub, m.keyID, ok
}

// mapping is the group mapping in force for member uid and issuer.
func (a *approvals) mapping(uid, issuer string) *delivery.GroupMappingState {
	m, _ := a.member(uid)
	for i := range m.mappings {
		if m.mappings[i].Issuer == issuer {
			return &m.mappings[i]
		}
	}
	return nil
}

func (a *approvals) member(uid string) (approvedMember, bool) {
	set := a.cur.Load()
	if set == nil {
		return approvedMember{}, false
	}
	m, ok := (*set)[uid]
	return m, ok
}
