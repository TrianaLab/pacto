package authn

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	pdelivery "github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/memberlink"
)

const (
	issuerA = "https://login.acme.dev"
	issuerB = "https://other.acme.dev"
)

// jwtOf is an unsigned JWT naming iss and exp: what a member link reads.
func jwtOf(iss string, exp time.Time, extra ...string) string {
	c := map[string]any{"iss": iss, "exp": exp.Unix(), "sub": strings.Join(extra, "")}
	b, _ := json.Marshal(c)
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func signedIn(token string) context.Context {
	return context.WithValue(context.Background(), stateKey{}, State{SignedIn: true, Token: token, Method: "oidc"})
}

// fakeLink is a member link answering with answer and remembering every
// frame and note.
type fakeLink struct {
	uid     string
	answer  func(memberlink.Review) (memberlink.Answer, error)
	refused map[string]bool

	mu    sync.Mutex
	sent  []memberlink.Review
	notes []string
}

func (l *fakeLink) UID() string { return l.uid }
func (l *fakeLink) Review(_ context.Context, r memberlink.Review) (memberlink.Answer, error) {
	l.mu.Lock()
	l.sent = append(l.sent, r)
	l.mu.Unlock()
	return l.answer(r)
}
func (l *fakeLink) Note(issuer, model, person string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.notes = append(l.notes, issuer+" "+model+" "+person)
}
func (l *fakeLink) IssuerRefused(issuer string) bool { return l.refused[issuer] }

func allow(model string) func(memberlink.Review) (memberlink.Answer, error) {
	return func(memberlink.Review) (memberlink.Answer, error) {
		return memberlink.Answer{Allowed: true, Model: model}, nil
	}
}

// exchanges is a member exchange returning a fresh token per call and
// remembering what it was asked.
type exchanges struct {
	mu    sync.Mutex
	asked []string
	outs  []string
	err   error
	out   string
}

func (x *exchanges) exchange(_ context.Context, token, uid string) (string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.asked = append(x.asked, token+"@"+uid)
	if x.out != "" {
		return x.out, x.err
	}
	out := jwtOf(issuerA, time.Now().Add(10*time.Minute), "exchanged-for-"+uid)
	x.outs = append(x.outs, out)
	return out, x.err
}

var aliceP = Person{User: "alice@acme.dev", Groups: []string{"team-pay", "devs"}}

func mappingOf(rules ...pdelivery.GroupRule) MemberMapping {
	return func(issuer string) *pdelivery.GroupMappingState {
		if issuer != issuerA {
			return nil
		}
		return &pdelivery.GroupMappingState{Issuer: issuer, Rules: rules, Record: "sha256:abc"}
	}
}

var pactos = PactoResource

func TestMemberReviewRefusesNonListVerb(t *testing.T) {
	l := &fakeLink{uid: "u1", answer: allow(memberlink.ModelSharedIssuer)}
	r := MemberReview(l, (&exchanges{}).exchange, nil)
	ctx := signedIn(jwtOf(issuerA, time.Now().Add(time.Hour-time.Minute)))
	for _, res := range []ResourceAttributes{ClusterJoinResource, {Group: "apps", Resource: "deployments", Verb: "update"}, {Group: pactos.Group, Resource: pactos.Resource, Name: "one"}} {
		if ok, err := r(ctx, aliceP, "prod", res); ok || !errors.Is(err, errMemberListOnly) {
			t.Errorf("%+v: %v %v", res, ok, err)
		}
	}
	expect(t, len(l.sent) == 0, "no frame for a non-list question: %d", len(l.sent))
	ok, err := r(ctx, aliceP, "prod", ResourceAttributes{Group: pactos.Group, Resource: pactos.Resource, Verb: "list"})
	expect(t, ok && err == nil, "an explicit list is asked: %v %v", ok, err)
}

func TestMemberReviewSharedIssuerAllowed(t *testing.T) {
	l := &fakeLink{uid: "u1", answer: allow(memberlink.ModelSharedIssuer)}
	x := &exchanges{}
	ok, err := MemberReview(l, x.exchange, nil)(signedIn(jwtOf(issuerA, time.Now().Add(30*time.Minute))), aliceP, "prod", pactos)
	expect(t, ok && err == nil, "allowed: %v %v", ok, err)
	expect(t, len(l.sent) == 1 && l.sent[0].Mapped == nil && l.sent[0].Namespace == "prod" && l.sent[0].Resource == pactos.Resource, "one token frame: %+v", l.sent)
	expect(t, len(l.notes) == 1 && l.notes[0] == issuerA+" shared-issuer alice@acme.dev", "the model is noted: %v", l.notes)
	l.answer = func(memberlink.Review) (memberlink.Answer, error) {
		return memberlink.Answer{Model: memberlink.ModelSharedIssuer}, nil
	}
	ok, err = MemberReview(l, x.exchange, nil)(signedIn(jwtOf(issuerA, time.Now().Add(30*time.Minute))), aliceP, "prod", pactos)
	expect(t, !ok && err == nil, "a denial is a verdict: %v %v", ok, err)
}

func TestRawSessionTokenNeverForwarded(t *testing.T) {
	session := jwtOf(issuerA, time.Now().Add(30*time.Minute), "session")
	l := &fakeLink{uid: "u1", answer: allow(memberlink.ModelSharedIssuer)}
	x := &exchanges{}
	_, _ = MemberReview(l, x.exchange, nil)(signedIn(session), aliceP, "prod", pactos)
	expect(t, len(l.sent) == 1 && l.sent[0].Token != "" && l.sent[0].Token != session, "the exchanged token is forwarded: %+v", l.sent)
	expect(t, len(x.outs) == 1 && l.sent[0].Token == x.outs[0], "the frame holds the exchange result")
	// Without an exchange no token is ever sent: the mapping answers.
	l2 := &fakeLink{uid: "u1", answer: allow(memberlink.ModelGroupMapping)}
	ok, err := MemberReview(l2, nil, mappingOf(pdelivery.GroupRule{From: "devs", To: "viewers"}))(signedIn(session), aliceP, "prod", pactos)
	expect(t, ok && err == nil && len(l2.sent) == 1 && l2.sent[0].Token == "" && l2.sent[0].Mapped != nil, "mapping path: %v %v %+v", ok, err, l2.sent)
}

func TestForwardedTokenAudienceIsMember(t *testing.T) {
	f := newIdP(t)
	exp := time.Now().Add(30 * time.Minute)
	f.exchangeOut = map[string]any{"access_token": jwtOf(issuerA, exp), "expires_in": 600}
	x := NewMemberExchange(MemberExchangeConfig{IssuerURL: f.srv.URL, ClientID: "dash", ClientSecret: "s"})
	session := jwtOf(issuerA, exp, "s")
	for _, uid := range []string{"uid-a", "uid-b", "uid-a"} {
		if _, err := x(context.Background(), session, uid); err != nil {
			t.Fatal(err)
		}
	}
	expect(t, len(f.exchanges) == 2, "one exchange per member, cached by token and uid: %d", len(f.exchanges))
	expect(t, f.exchanges[0].Get("audience") == "pacto-member-uid-a" && f.exchanges[1].Get("audience") == "pacto-member-uid-b", "audiences: %v", f.exchanges)
	expect(t, f.exchanges[0].Get("subject_token") == session, "the session token is the exchange's subject only")
	custom := NewMemberExchange(MemberExchangeConfig{IssuerURL: f.srv.URL, ClientID: "dash", Audience: "https://{uid}.k8s"})
	_, _ = custom(context.Background(), session, "uid-c")
	expect(t, f.exchanges[2].Get("audience") == "https://uid-c.k8s", "a configured audience: %v", f.exchanges[2])
	down := NewMemberExchange(MemberExchangeConfig{IssuerURL: "http://127.0.0.1:1"})
	_, err := down(context.Background(), session, "uid-a")
	expect(t, err != nil, "an unreachable issuer fails the exchange")
}

func TestMemberReviewIssuerNotAcceptedUsesMapping(t *testing.T) {
	l := &fakeLink{uid: "u1", refused: map[string]bool{}}
	l.answer = func(r memberlink.Review) (memberlink.Answer, error) {
		if r.Token != "" {
			l.refused[issuerA] = true // the hub marks it: a second person was refused
			return memberlink.Answer{Refused: memberlink.RefusedIssuerNotAccepted}, nil
		}
		return memberlink.Answer{Allowed: true, Model: memberlink.ModelGroupMapping}, nil
	}
	ok, err := MemberReview(l, (&exchanges{}).exchange, mappingOf(pdelivery.GroupRule{From: "devs", To: "acme:viewers"}))(signedIn(jwtOf(issuerA, time.Now().Add(time.Minute))), aliceP, "prod", pactos)
	expect(t, ok && err == nil, "the mapping answers once the issuer is refused: %v %v", ok, err)
	expect(t, len(l.sent) == 2 && l.sent[1].Mapped.User == mappedUser(issuerA, aliceP.User) && strings.Join(l.sent[1].Mapped.Groups, ",") == "acme:viewers" && l.sent[1].Mapped.Record == "sha256:abc",
		"the second frame is the mapped person: %+v", l.sent)
	expect(t, len(l.notes) == 2 && strings.HasSuffix(l.notes[0], "issuer-not-accepted alice@acme.dev") && strings.HasSuffix(l.notes[1], "group-mapping alice@acme.dev"), "notes: %v", l.notes)
}

func TestMemberReviewIssuerNotAcceptedNoMappingHides(t *testing.T) {
	l := &fakeLink{uid: "u1", refused: map[string]bool{issuerA: true}, answer: allow(memberlink.ModelGroupMapping)}
	ctx := signedIn(jwtOf(issuerA, time.Now().Add(time.Minute)))
	for _, m := range []MemberMapping{nil, mappingOf()} {
		ok, err := MemberReview(l, (&exchanges{}).exchange, m)(ctx, Person{User: "bob", Groups: []string{"x"}}, "prod", pactos)
		if m == nil {
			expect(t, !ok && errors.Is(err, errIssuerNotAccepted) && len(l.sent) == 0, "no mapping hides: %v %v", ok, err)
		} else {
			// A mapping no group of bob's matches still asks, with no group.
			expect(t, ok && err == nil && len(l.sent) == 1 && len(l.sent[0].Mapped.Groups) == 0, "empty apply is sent: %+v", l.sent)
		}
	}
	other := MemberReview(l, (&exchanges{}).exchange, mappingOf())
	_, err := other(signedIn(jwtOf(issuerB, time.Now().Add(time.Minute))), aliceP, "prod", pactos)
	expect(t, err == nil, "another issuer still tries its token: %v", err)
}

func TestMemberReviewRemembersRefusedIssuerPerCluster(t *testing.T) {
	l := &fakeLink{uid: "u1", refused: map[string]bool{issuerA: true}, answer: allow(memberlink.ModelGroupMapping)}
	x := &exchanges{}
	r := MemberReview(l, x.exchange, mappingOf(pdelivery.GroupRule{From: "devs", To: "viewers"}))
	_, _ = r(signedIn(jwtOf(issuerA, time.Now().Add(time.Minute))), aliceP, "prod", pactos)
	expect(t, len(x.asked) == 0 && l.sent[0].Mapped != nil, "a marked issuer goes straight to the mapping: %v", x.asked)
	_, _ = r(signedIn(jwtOf(issuerB, time.Now().Add(time.Minute))), aliceP, "prod", pactos)
	expect(t, len(x.asked) == 1 && l.sent[1].Token != "", "another issuer is exchanged: %v", x.asked)
	l2 := &fakeLink{uid: "u2", answer: allow(memberlink.ModelSharedIssuer)}
	_, _ = MemberReview(l2, x.exchange, nil)(signedIn(jwtOf(issuerA, time.Now().Add(time.Minute))), aliceP, "prod", pactos)
	expect(t, len(x.asked) == 2 && strings.HasSuffix(x.asked[1], "@u2"), "the mark is per cluster: %v", x.asked)
}

func TestMappedUserNamesIssuer(t *testing.T) {
	sum := sha256.Sum256([]byte(issuerA))
	expect(t, mappedUser(issuerA, "alice@acme.dev") == "pacto-mapped:"+hex.EncodeToString(sum[:])[:12]+":alice@acme.dev", "shape: %s", mappedUser(issuerA, "alice@acme.dev"))
	expect(t, mappedUser(issuerA, "alice") != mappedUser(issuerB, "alice"), "two issuers, two names")
}

func TestLinkSessionNeverForwarded(t *testing.T) {
	l := &fakeLink{uid: "u1", answer: allow(memberlink.ModelSharedIssuer)}
	r := MemberReview(l, (&exchanges{}).exchange, mappingOf(pdelivery.GroupRule{From: "devs", To: "v"}))
	for name, ctx := range map[string]context.Context{
		"link session": context.WithValue(context.Background(), stateKey{}, State{SignedIn: true, Method: "token", Person: Person{User: "system:serviceaccount:p:v"}}),
		"signed out":   context.Background(),
		"opaque token": signedIn("not-a-jwt"),
		"expired":      signedIn(jwtOf(issuerA, time.Now().Add(-time.Minute))),
	} {
		if _, err := r(ctx, aliceP, "prod", pactos); !errors.Is(err, errNoForwardableIdentity) {
			t.Errorf("%s: %v", name, err)
		}
	}
	expect(t, len(l.sent) == 0, "nothing sent: %+v", l.sent)
}

// captureLogs sends slog's default output to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestLongLivedTokenNeverForwarded(t *testing.T) {
	logs := captureLogs(t)
	l := &fakeLink{uid: "u1", answer: allow(memberlink.ModelSharedIssuer)}
	x := &exchanges{}
	r := MemberReview(l, x.exchange, mappingOf(pdelivery.GroupRule{From: "devs", To: "v"}))
	long := jwtOf(issuerA, time.Now().Add(2*time.Hour), "SENTINEL")
	for range 3 {
		if _, err := r(signedIn(long), aliceP, "prod", pactos); !errors.Is(err, errNoForwardableIdentity) {
			t.Fatal(err)
		}
	}
	expect(t, len(l.sent) == 0 && len(x.asked) == 0, "nothing forwarded or exchanged")
	expect(t, strings.Count(logs.String(), "token lifetime over 1h") == 1 && strings.Contains(logs.String(), issuerA), "logged once per issuer with the reason: %s", logs)
	expect(t, !strings.Contains(logs.String(), long), "never the token")
	// An exchange that returns a long-lived token is not forwarded either.
	x.out = jwtOf(issuerA, time.Now().Add(3*time.Hour))
	_, err := r(signedIn(jwtOf(issuerA, time.Now().Add(time.Minute))), aliceP, "prod", pactos)
	expect(t, err != nil && len(l.sent) == 0 && !strings.Contains(err.Error(), x.out), "a long exchanged token is refused: %v", err)
}

func TestHubNeverLogsToken(t *testing.T) {
	logs := captureLogs(t)
	session := jwtOf(issuerA, time.Now().Add(time.Minute), "SENTINEL-SESSION")
	x := &exchanges{out: jwtOf(issuerA, time.Now().Add(time.Minute), "SENTINEL-EXCHANGED")}
	var errs []error
	answers := []func(memberlink.Review) (memberlink.Answer, error){
		func(memberlink.Review) (memberlink.Answer, error) { return memberlink.Answer{}, memberlink.ErrLinkDown },
		func(memberlink.Review) (memberlink.Answer, error) {
			return memberlink.Answer{}, memberlink.ErrReviewTimeout
		},
		func(memberlink.Review) (memberlink.Answer, error) {
			return memberlink.Answer{Refused: memberlink.RefusedTokenExpired}, nil
		},
		func(memberlink.Review) (memberlink.Answer, error) {
			return memberlink.Answer{Refused: memberlink.RefusedIssuerNotAccepted}, nil
		},
	}
	for _, a := range answers {
		_, err := MemberReview(&fakeLink{uid: "u1", answer: a}, x.exchange, nil)(signedIn(session), aliceP, "prod", pactos)
		errs = append(errs, err)
	}
	x.err = errors.New("idp down")
	_, err := MemberReview(&fakeLink{uid: "u1", answer: allow("")}, x.exchange, nil)(signedIn(session), aliceP, "prod", pactos)
	errs = append(errs, err)
	for _, err := range errs {
		expect(t, err != nil, "every failure is an error")
		expect(t, !strings.Contains(err.Error(), "SENTINEL") && !strings.Contains(err.Error(), session), "no token in %v", err)
	}
	expect(t, !strings.Contains(logs.String(), session) && !strings.Contains(logs.String(), x.out), "no token in the logs: %s", logs)
}

func TestMemberReviewMappingFailures(t *testing.T) {
	ctx := signedIn(jwtOf(issuerA, time.Now().Add(time.Minute)))
	system := MemberReview(&fakeLink{uid: "u1", answer: allow("")}, nil, mappingOf(pdelivery.GroupRule{From: "devs", To: "system:masters"}))
	_, err := system(ctx, aliceP, "prod", pactos)
	expect(t, err != nil && strings.Contains(err.Error(), "system group"), "a system group never travels: %v", err)
	for _, a := range []func(memberlink.Review) (memberlink.Answer, error){
		func(memberlink.Review) (memberlink.Answer, error) { return memberlink.Answer{}, memberlink.ErrLinkDown },
		func(memberlink.Review) (memberlink.Answer, error) {
			return memberlink.Answer{Refused: memberlink.RefusedMappingInvalid}, nil
		},
	} {
		l := &fakeLink{uid: "u1", answer: a}
		ok, err := MemberReview(l, nil, mappingOf(pdelivery.GroupRule{From: "devs", To: "v"}))(ctx, aliceP, "prod", pactos)
		expect(t, !ok && err != nil && len(l.notes) == 0, "a failed mapped review is an error: %v %v", ok, err)
	}
}

func TestMCPExchangeUnchanged(t *testing.T) {
	TestMCPExchange(t)
	TestMCPExchangeFailures(t)
}
