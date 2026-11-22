/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package member

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/v4/pkg/memberlink"
)

var now = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

// jwt is an unsigned JWT-shaped token expiring at exp, holding marker so a
// leak is findable.
func jwt(exp time.Time) string {
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix(), "iss": "https://login.acme.dev", "marker": "SENTINEL-TOKEN"})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".c2ln"
}

var token = jwt(now.Add(10 * time.Minute))

// fakeCluster records what the reviewer asked: the token review's config and
// body, and every SubjectAccessReview.
type fakeCluster struct {
	whoami     authenticationv1.UserInfo
	ssrErr     error
	newErr     error
	sarErr     error
	allowed    bool
	evalErr    string
	ssrCfg     *rest.Config
	ssrBody    []byte
	sars       []authorizationv1.SubjectAccessReview
	deadlineIn time.Duration
}

func (f *fakeCluster) reviewer() *Reviewer {
	own := &rest.Config{Host: "https://member.example:6443", BearerToken: "OPERATOR-TOKEN",
		TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca"), CertData: []byte("cert"), KeyData: []byte("key")},
		Username:        "u", Password: "p"}
	sar := fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			if dl, ok := ctx.Deadline(); ok {
				f.deadlineIn = time.Until(dl)
			}
			if f.sarErr != nil {
				return f.sarErr
			}
			s := obj.(*authorizationv1.SubjectAccessReview)
			f.sars = append(f.sars, *s)
			s.Status.Allowed, s.Status.EvaluationError = f.allowed, f.evalErr
			return nil
		}}).Build()
	return &Reviewer{Own: own, Client: sar, Now: func() time.Time { return now },
		NewClient: func(cfg *rest.Config) (client.Client, error) {
			f.ssrCfg = cfg
			if f.newErr != nil {
				return nil, f.newErr
			}
			return fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
					f.ssrBody, _ = json.Marshal(obj)
					if f.ssrErr != nil {
						return f.ssrErr
					}
					obj.(*authenticationv1.SelfSubjectReview).Status.UserInfo = f.whoami
					return nil
				}}).Build(), nil
		}}
}

func pactos(id memberlink.Review) memberlink.Review {
	id.Namespace, id.Group, id.Resource = "payments", "pacto.trianalab.io", "pactos"
	return id
}

var alice = authenticationv1.UserInfo{Username: "https://login.acme.dev#alice", UID: "u1",
	Groups: []string{"payments"}, Extra: map[string]authenticationv1.ExtraValue{"k": {"v"}}}

func TestMemberReviewsTokenWithTokenAsOnlyCredential(t *testing.T) {
	f := &fakeCluster{whoami: alice, allowed: true}
	f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Token: token}))
	c := f.ssrCfg
	if c.BearerToken != token || c.Host != "https://member.example:6443" || string(c.CAData) != "ca" {
		t.Fatalf("token review config = %+v; want the token, the server and its CA", c)
	}
	if len(c.CertData) != 0 || len(c.KeyData) != 0 || c.Username != "" || c.Password != "" ||
		c.ExecProvider != nil || c.AuthProvider != nil || c.BearerTokenFile != "" {
		t.Fatalf("token review config carries another credential: %+v", c)
	}
	if bytes.Contains(f.ssrBody, []byte("SENTINEL")) || bytes.Contains(f.ssrBody, []byte(token)) {
		t.Fatalf("the token travelled in the request body: %s", f.ssrBody)
	}
}

func TestMemberTokenAllowedSharedIssuer(t *testing.T) {
	f := &fakeCluster{whoami: alice, allowed: true}
	a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Token: token}))
	if !a.Allowed || a.Model != memberlink.ModelSharedIssuer || a.Refused != "" {
		t.Fatalf("answer = %+v; want allowed, shared issuer", a)
	}
	s := f.sars[0].Spec
	if s.User != alice.Username || s.UID != "u1" || s.Groups[0] != "payments" || s.Extra["k"][0] != "v" {
		t.Fatalf("SAR spec = %+v; want the person the member's apiserver named", s)
	}
}

func TestMemberIssuerNotAcceptedOn401(t *testing.T) {
	f := &fakeCluster{ssrErr: apierrors.NewUnauthorized("no")}
	if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Token: token})); a.Refused != memberlink.RefusedIssuerNotAccepted {
		t.Fatalf("answer = %+v; want issuer-not-accepted", a)
	}
	for _, f := range []*fakeCluster{{ssrErr: errors.New("boom")}, {newErr: errors.New("boom")}} {
		if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Token: token})); a.Refused != memberlink.RefusedReviewFailed {
			t.Fatalf("answer = %+v; want review-failed for a token review that errored", a)
		}
	}
}

func TestMemberRefusesExpiredToken(t *testing.T) {
	for _, tok := range []string{jwt(now.Add(-time.Minute)), jwt(now.Add(2 * time.Hour)), "opaque"} {
		f := &fakeCluster{whoami: alice, allowed: true}
		if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Token: tok})); a.Refused != memberlink.RefusedTokenExpired || f.ssrCfg != nil {
			t.Fatalf("answer = %+v; want token-expired with no review", a)
		}
	}
}

func TestMemberRefusesClusterLocalIdentity(t *testing.T) {
	for _, ui := range []authenticationv1.UserInfo{
		{Username: "system:serviceaccount:pacto:pacto"},
		{Username: "system:node:n1"},
		{Username: "x", Groups: []string{"system:serviceaccounts"}},
		{Username: "x", Groups: []string{"system:serviceaccounts:pacto"}},
		{Username: "x", Groups: []string{"system:nodes"}},
	} {
		f := &fakeCluster{whoami: ui, allowed: true}
		if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Token: token})); a.Refused != memberlink.RefusedClusterLocal || len(f.sars) != 0 {
			t.Fatalf("%+v: answer = %+v; want cluster-local-identity and no SAR", ui, a)
		}
	}
}

func mapped(user string, groups ...string) *memberlink.Mapped {
	return &memberlink.Mapped{User: user, Groups: groups, Record: "sha256:abc"}
}

const mappedAlice = "pacto-mapped:0123456789ab:alice"

func TestMemberMappedAllowed(t *testing.T) {
	f := &fakeCluster{allowed: true}
	a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Mapped: mapped(mappedAlice, "payments")}))
	if !a.Allowed || a.Model != memberlink.ModelGroupMapping {
		t.Fatalf("answer = %+v; want allowed, group mapping", a)
	}
	if s := f.sars[0].Spec; s.User != mappedAlice || s.Groups[0] != "payments" || f.ssrCfg != nil {
		t.Fatalf("SAR spec = %+v; want the mapped identity and no token review", s)
	}
	f = &fakeCluster{}
	if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Mapped: mapped(mappedAlice)})); a.Allowed || a.Refused != "" {
		t.Fatalf("answer = %+v; want a plain no", a)
	}
}

func TestMemberRefusesSystemGroupsInMapping(t *testing.T) {
	many := make([]string, 65)
	for i := range many {
		many[i] = fmt.Sprint("g", i)
	}
	for _, m := range []*memberlink.Mapped{
		mapped(mappedAlice, "system:masters"),
		mapped(mappedAlice, "payments", "system:authenticated"),
		mapped(mappedAlice, many...),
		{User: mappedAlice, Record: "md5:abc"},
	} {
		f := &fakeCluster{allowed: true}
		if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Mapped: m})); a.Refused != memberlink.RefusedMappingInvalid || len(f.sars) != 0 {
			t.Fatalf("%+v: answer = %+v; want mapping-invalid and no SAR", m, a)
		}
	}
}

func TestMemberRefusesMappedUserWithoutPrefix(t *testing.T) {
	for _, u := range []string{"alice", "pacto-mapped:alice", "pacto-mapped:0123456789AB:alice",
		"pacto-mapped:0123456789a:alice", "pacto-mapped:0123456789ab:"} {
		f := &fakeCluster{allowed: true}
		if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Mapped: mapped(u)})); a.Refused != memberlink.RefusedMappingInvalid {
			t.Fatalf("%q: answer = %+v; want mapping-invalid", u, a)
		}
	}
}

func TestMemberRefusesUnlistedResource(t *testing.T) {
	for _, gr := range []schema.GroupResource{{Resource: "secrets"}, {Resource: "configmaps"}, {Group: "acme.io", Resource: "widgets"}} {
		f := &fakeCluster{whoami: alice, allowed: true}
		a := f.reviewer().Answer(context.Background(), memberlink.Review{Group: gr.Group, Resource: gr.Resource, Token: token})
		if a.Refused != memberlink.RefusedReviewFailed || f.ssrCfg != nil || len(f.sars) != 0 {
			t.Fatalf("%v: answer = %+v; want review-failed with no review", gr, a)
		}
	}
}

func TestMemberRefusesBothOrNeither(t *testing.T) {
	for _, r := range []memberlink.Review{{}, {Token: token, Mapped: mapped(mappedAlice)}} {
		f := &fakeCluster{allowed: true}
		if a := f.reviewer().Answer(context.Background(), pactos(r)); a.Refused != memberlink.RefusedNoIdentity {
			t.Fatalf("answer = %+v; want no-identity", a)
		}
	}
}

func TestMemberEvaluationErrorIsReviewFailed(t *testing.T) {
	for _, f := range []*fakeCluster{{evalErr: "webhook down"}, {sarErr: errors.New("boom")}} {
		if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Mapped: mapped(mappedAlice)})); a.Refused != memberlink.RefusedReviewFailed || a.Allowed {
			t.Fatalf("answer = %+v; want review-failed", a)
		}
	}
	f := &fakeCluster{allowed: true, evalErr: "partial"}
	if a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Mapped: mapped(mappedAlice)})); !a.Allowed {
		t.Fatalf("answer = %+v; an allow with an evaluation error still allows", a)
	}
}

func TestMemberAlwaysAsksList(t *testing.T) {
	f := &fakeCluster{whoami: alice, allowed: true}
	rv := f.reviewer()
	rv.Answer(context.Background(), pactos(memberlink.Review{Token: token}))
	rv.Answer(context.Background(), memberlink.Review{Group: "apps", Resource: "deployments", Mapped: mapped(mappedAlice)})
	for _, s := range f.sars {
		ra := s.Spec.ResourceAttributes
		if ra.Verb != "list" || ra.Name != "" || ra.Subresource != "" {
			t.Fatalf("SAR attributes = %+v; want list with no name or subresource", ra)
		}
	}
	if ra := f.sars[0].Spec.ResourceAttributes; ra.Namespace != "payments" || ra.Group != "pacto.trianalab.io" || ra.Resource != "pactos" {
		t.Fatalf("SAR attributes = %+v; want the review's namespace and resource", ra)
	}
}

func TestMemberAnswerBoundedAt5s(t *testing.T) {
	f := &fakeCluster{allowed: true}
	f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Mapped: mapped(mappedAlice)}))
	if f.deadlineIn <= 0 || f.deadlineIn > memberlink.ReviewTimeout {
		t.Fatalf("SAR deadline in %v; want at most %v", f.deadlineIn, memberlink.ReviewTimeout)
	}
}

func TestMemberNeverLogsToken(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	for _, f := range []*fakeCluster{
		{whoami: alice, allowed: true}, {ssrErr: apierrors.NewUnauthorized(token)}, {ssrErr: errors.New(token)},
		{newErr: errors.New(token)}, {whoami: alice, sarErr: errors.New(token)}, {whoami: alice, evalErr: token},
	} {
		a := f.reviewer().Answer(context.Background(), pactos(memberlink.Review{Token: token}))
		if strings.Contains(fmt.Sprintf("%+v", a), "SENTINEL") || strings.Contains(fmt.Sprintf("%+v", a), token) {
			t.Fatalf("answer %+v carries the token", a)
		}
	}
	if strings.Contains(buf.String(), "SENTINEL") {
		t.Fatalf("the token was logged: %s", buf.String())
	}
}
