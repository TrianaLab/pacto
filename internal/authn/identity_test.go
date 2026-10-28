package authn

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// An OIDC session names the person as the API server does, at sign-in and on
// every refresh; only when the cluster cannot be asked does the claim mapping.
func TestOIDCSessionNamesThePersonAsTheClusterDoes(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	who, reviewErr := "oidc:alice", error(nil)
	var asked []string
	o.cfg.Review = func(_ context.Context, token string) (Person, error) {
		asked = append(asked, token)
		return Person{User: who, Groups: []string{"team-a"}}, reviewErr
	}
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	s := o.Session(withCookie(c))
	expect(t, s.Person().User == "oidc:alice" && s.Person().Groups[0] == "team-a" && len(asked) == 1 && asked[0] == s.IDToken(),
		"the session names the person the API server answered for its ID token: %+v %v", s.Person(), asked)
	secs, err := strconv.ParseInt(s.AuthTime(), 10, 64)
	expect(t, err == nil && time.Since(time.Unix(secs, 0)) < time.Hour, "the session keeps the ID token's auth_time: %q", s.AuthTime())

	// A refresh asks again with the new ID token.
	who = "oidc:alice2"
	o.sessions[c.Value].expiry = time.Now().Add(-time.Second)
	s = o.Session(withCookie(c))
	expect(t, s.Person().User == "oidc:alice2" && len(asked) == 2 && asked[1] == s.IDToken(), "a refresh names the person again: %+v", s.Person())

	// A cluster that does not answer leaves the claim mapping.
	reviewErr = errors.New("Unauthorized")
	c = cookieNamed(t, login(t, o, f, ""), SessionCookie)
	s = o.Session(withCookie(c))
	expect(t, s.Person().User == f.issuer()+"#u-1", "the claim mapping names a person the cluster does not: %+v", s.Person())

	// No cluster at all: the claim mapping.
	o.cfg.Review = nil
	c = cookieNamed(t, login(t, o, f, ""), SessionCookie)
	expect(t, o.Session(withCookie(c)).Person().User == f.issuer()+"#u-1", "no cluster, the claim mapping")
}

func TestReviewSelfReadsTheExtra(t *testing.T) {
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("create", "selfsubjectreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		obj.Object["status"] = map[string]any{"userInfo": map[string]any{"username": "alice", "groups": []any{"g"},
			"extra": map[string]any{AuthTimeExtra: []any{"1800000000"}}}}
		return true, obj, nil
	})
	p, extra, err := ReviewSelf(context.Background(), c)
	expect(t, err == nil && p.User == "alice" && p.Groups[0] == "g" && extra[AuthTimeExtra][0] == "1800000000", "review %+v %v %v", p, extra, err)
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("Unauthorized")
	})
	_, _, err = ReviewSelf(context.Background(), c)
	expect(t, err != nil, "a refused review is an error")
}
